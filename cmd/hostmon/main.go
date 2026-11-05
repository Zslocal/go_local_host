// hostmon — 面向 Linux 的零依赖服务器监控采集器。
// CPU / 内存 / 磁盘 / 网络 / 运行时间全部读取 /proc，编译为单个静态二进制。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Zslocal/go_local_host/internal/alert"
	"github.com/Zslocal/go_local_host/internal/monitor"
	"github.com/Zslocal/go_local_host/internal/output"
	"github.com/Zslocal/go_local_host/internal/web"
)

var version = "dev" // 可用 -ldflags "-X main.version=0.1.0" 注入

type options struct {
	interval  time.Duration
	warmup    time.Duration
	once      bool
	format    string
	pretty    bool
	webAddr   string
	webToken  string
	logPath   string
	netAll    bool
	allLines  bool
	showCores bool
	top       bool
	quiet     bool
	noAlert   bool
	procRoot  string
	hostRoot  string
	th        alert.Config
}

// envOr 让容器可以用环境变量代替命令行参数（令牌等敏感值不必出现在 docker inspect 里）。
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseFlags() options {
	o := options{}
	def := alert.DefaultConfig()
	flag.DurationVar(&o.interval, "interval", 2*time.Second, "采样间隔，例如 1s / 5s / 30s")
	flag.DurationVar(&o.warmup, "warmup", 700*time.Millisecond, "首次采样的差分窗口（决定首个快照的速率精度）")
	flag.StringVar(&o.procRoot, "proc-root", envOr("HOSTMON_PROC_ROOT", "/proc"), "宿主机 /proc 的挂载路径；容器内用 /host/proc（也可用 HOSTMON_PROC_ROOT）")
	flag.StringVar(&o.hostRoot, "host-root", envOr("HOSTMON_HOST_ROOT", ""), "宿主机根文件系统的挂载路径；容器内用 /host，用于 statfs 与 /etc/os-release（也可用 HOSTMON_HOST_ROOT）")
	flag.BoolVar(&o.once, "once", false, "采集一次后退出（适合脚本 / crontab）")
	flag.StringVar(&o.format, "format", "text", "输出格式: text | json | prom")
	flag.BoolVar(&o.pretty, "pretty", false, "-format json 时缩进输出")
	flag.StringVar(&o.webAddr, "web", "", "启用 HTTP 服务，如 127.0.0.1:9100 或 :9100")
	flag.StringVar(&o.webToken, "web-token", envOr("HOSTMON_WEB_TOKEN", ""), "HTTP 访问令牌（Bearer），留空则不鉴权；也可用 HOSTMON_WEB_TOKEN")
	flag.StringVar(&o.logPath, "log", "", "把每次快照以 JSONL 追加写入该文件")
	flag.BoolVar(&o.netAll, "net-all", false, "统计 lo / veth / cni 等虚拟网卡（默认排除）")
	flag.BoolVar(&o.allLines, "all-lines", false, "文本输出不限制挂载点/磁盘/网卡行数")
	flag.BoolVar(&o.showCores, "cores", false, "显示每个 CPU 核心的使用率")
	flag.BoolVar(&o.top, "top", false, "持续模式清屏重绘（类似 top）")
	flag.BoolVar(&o.quiet, "quiet", false, "启用 -web 时不在终端打印快照")
	flag.BoolVar(&o.noAlert, "no-alert", false, "关闭阈值告警")
	flag.BoolVar(&o.th.NetDrop, "net-drop", def.NetDrop, "对新增网卡错包/丢包告警")
	flag.Float64Var(&o.th.CPU, "cpu", def.CPU, "CPU 告警阈值 %（0 关闭）")
	flag.Float64Var(&o.th.Memory, "mem", def.Memory, "内存告警阈值 %（0 关闭）")
	flag.Float64Var(&o.th.Disk, "disk", def.Disk, "挂载点/inode 告警阈值 %（0 关闭）")
	flag.Float64Var(&o.th.DiskUtil, "disk-util", def.DiskUtil, "块设备 busy 告警阈值 %（0 关闭）")
	flag.Float64Var(&o.th.LoadFactor, "load", def.LoadFactor, "load1/核数 告警倍数，如 2（0 关闭）")
	flag.DurationVar(&o.th.Repeat, "alert-repeat", def.Repeat, "持续告警的重复间隔")
	ver := flag.Bool("version", false, "显示版本并退出")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `hostmon %s — Linux 服务器监控（CPU / 内存 / 磁盘 / 网络 / 运行时间）

用法:
  hostmon [参数]

常用示例:
  hostmon -once                              采集一次，打印文本报告
  hostmon -interval 5s -cores               每 5 秒刷新，显示每核使用率
  hostmon -web 127.0.0.1:9100 -web-token xxx  启动仪表盘 + Prometheus 端点
  hostmon -once -format json | jq .         脚本集成
  hostmon -log /var/log/hostmon.jsonl -web :9100 -quiet  后台常驻
  hostmon -proc-root /host/proc -host-root /host -once     容器内采集宿主机指标

参数:
`, version)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *ver {
		fmt.Printf("hostmon %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}
	o.th.Hysteresis = def.Hysteresis
	o.format = strings.ToLower(o.format)
	switch o.format {
	case "text", "json", "prom":
	default:
		log.Fatalf("不支持的 -format %q，可选 text | json | prom", o.format)
	}
	if o.interval < 100*time.Millisecond {
		log.Fatal("-interval 不能小于 100ms（计数器分辨率不足）")
	}
	return o
}

func main() {
	log.SetFlags(log.LstdFlags)
	o := parseFlags()

	if runtime.GOOS != "linux" {
		log.Fatalf("当前是 %s/%s，指标采集依赖 Linux /proc；请用 GOOS=linux GOARCH=arm64 go build 交叉编译后部署到服务器运行。",
			runtime.GOOS, runtime.GOARCH)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sampler := monitor.NewSampler(monitor.Config{
		Warmup: o.warmup, NetAll: o.netAll,
		ProcRoot: o.procRoot, HostRoot: o.hostRoot,
	})
	evaluator := alert.NewEvaluator(o.th)
	textOpt := output.DefaultTextOptions()
	if o.allLines {
		textOpt = output.TextOptions{ShowCores: o.showCores}
	} else {
		textOpt.ShowCores = o.showCores
	}

	var jsonl *output.JSONL
	if o.logPath != "" {
		f, err := output.OpenJSONL(o.logPath)
		if err != nil {
			log.Fatalf("打开日志文件 %s 失败: %v", o.logPath, err)
		}
		defer f.Close()
		jsonl = f
		log.Printf("JSONL 日志: %s", o.logPath)
	}

	var cache *web.Cache
	var srv *http.Server
	if o.webAddr != "" && !o.once {
		stale := o.interval * 3
		if stale < 15*time.Second {
			stale = 15 * time.Second
		}
		cache = web.NewCache(stale)
		var err error
		srv, err = web.Start(o.webAddr, cache, o.webToken)
		if err != nil {
			log.Fatalf("启动 HTTP 服务失败: %v", err)
		}
		if o.webToken == "" && !strings.HasPrefix(o.webAddr, "127.0.0.1:") && !strings.HasPrefix(o.webAddr, "localhost:") {
			log.Printf("警告: HTTP 无鉴权且监听在 %s，建议只绑定回环地址并用 Nginx 反代，或设置 -web-token", o.webAddr)
		}
	}

	log.Printf("hostmon %s 启动 | 间隔 %s | 格式 %s | %s", version, o.interval, o.format, time.Now().Format("2006-01-02 15:04:05"))
	if o.once {
		if !runOnce(o, sampler, evaluator, jsonl, textOpt) {
			os.Exit(1)
		}
		return
	}

	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()
	// 立即采集首个快照（内部会额外做一次 warmup 采样），随后重置计时器以保持固定节奏。
	tick(o, sampler, evaluator, jsonl, cache, textOpt)
	ticker.Reset(o.interval)
	for {
		select {
		case <-ctx.Done():
			log.Printf("收到退出信号，正在关闭…")
			if srv != nil {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = srv.Shutdown(shutdownCtx)
				cancel()
			}
			log.Printf("已退出")
			return
		case <-ticker.C:
			tick(o, sampler, evaluator, jsonl, cache, textOpt)
		}
	}
}

func runOnce(o options, s *monitor.Sampler, e *alert.Evaluator,
	j *output.JSONL, t output.TextOptions) bool {
	return tick(o, s, e, j, nil, t)
}

func tick(o options, s *monitor.Sampler, e *alert.Evaluator,
	j *output.JSONL, cache *web.Cache, t output.TextOptions) bool {
	snap, err := s.Sample()
	if err != nil {
		log.Printf("采集失败: %v", err)
		return false
	}

	printSnapshot(os.Stdout, o, snap, t)

	if !o.noAlert {
		for _, ev := range e.Evaluate(snap) {
			fmt.Fprintln(os.Stderr, ev.String())
			if cache != nil {
				cache.AddEvents([]alert.Event{ev})
			}
		}
	}
	if j != nil {
		if err := j.Write(snap); err != nil {
			log.Printf("写日志失败: %v", err)
		}
	}
	if cache != nil {
		cache.Set(snap)
	}
	return true
}

func printSnapshot(w io.Writer, o options, snap *monitor.Snapshot, t output.TextOptions) {
	if o.quiet && o.webAddr != "" {
		return
	}
	if o.top {
		_, _ = io.WriteString(w, "\033[H\033[2J")
	}
	var err error
	switch o.format {
	case "json":
		err = output.JSON(w, snap, o.pretty)
	case "prom":
		err = output.Prometheus(w, snap)
		_, _ = io.WriteString(w, "\n")
	default:
		err = output.Text(w, snap, t)
		_, _ = io.WriteString(w, "\n")
	}
	if err != nil {
		log.Printf("输出失败: %v", err)
	}
}
