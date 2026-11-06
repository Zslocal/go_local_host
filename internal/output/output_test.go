package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zslocal/go_local_host/internal/monitor"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0B"}, {1023, "1023B"}, {1024, "1.0K"},
		{1024 * 1024, "1.0M"}, {1024 * 1024 * 1024, "1.0G"},
		{1 << 50, "1.0P"}, {1 << 60, "1024.0P"}, // 不越界
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func sampleSnapshot() *monitor.Snapshot {
	return &monitor.Snapshot{
		Timestamp: time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC),
		Interval:  2,
		Host: monitor.HostInfo{
			Hostname: "node-01", OS: `Anolis "8.9"`, Kernel: "5.10.134", Arch: "arm64", CPUs: 8,
		},
		Uptime: monitor.UptimeInfo{BootTime: time.Unix(1700000000, 0).UTC()},
		CPU: monitor.CPUInfo{
			Usage: 42.5, User: 30, System: 10, IOWait: 2.5,
			Cores: []monitor.CoreInfo{{ID: "0", Usage: 50}, {ID: "1", Usage: 35}},
		},
		Memory:  monitor.MemoryInfo{Total: 16 << 30, Used: 8 << 30, Usage: 50},
		Mounts:  []monitor.MountInfo{{Device: "/dev/sda1", MountPoint: "/", FSType: "ext4", Total: 100 << 30, Used: 60 << 30, Usage: 60}},
		DiskIO:  []monitor.DiskIOInfo{{Device: "sda", ReadRate: 1024, WriteRate: 2048, ReadTotal: 10}},
		Network: []monitor.NetInfo{{Interface: "eth0", RxRate: 1000, TxRate: 2000, RxBytes: 5000}},
		TCP:     monitor.TCPInfo{Established: 12, Listen: 3, Total: 20},
		Processes: monitor.ProcessInfo{
			Running: 2, Total: 300, Created: 9999, Scanned: 287,
			TopCPU: []monitor.ProcInfo{
				{PID: 4242, Name: "java", User: "app", State: "R", CPU: 187.5, Mem: 12.5, RSS: 2 << 30, Threads: 168,
					Cmdline: "/usr/bin/java -Xmx2g -jar /opt/app/app.jar"},
				// 内核线程没有 cmdline，文本里应回退为 [comm]
				{PID: 7, Name: "nginx", User: "www-data", State: "S", CPU: 3.25, Mem: 0.4, RSS: 64 << 20, Threads: 4},
			},
			// 故意与 TopCPU 有重叠（PID 4242），用于验证 Prometheus 输出按 pid 去重。
			TopMem: []monitor.ProcInfo{
				{PID: 4242, Name: "java", User: "app", State: "R", CPU: 187.5, Mem: 12.5, RSS: 2 << 30, Threads: 168,
					Cmdline: "/usr/bin/java -Xmx2g -jar /opt/app/app.jar"},
				{PID: 91, Name: "postgres", User: "postgres", State: "S", CPU: 1.5, Mem: 6.25, RSS: 1 << 30, Threads: 12,
					Cmdline: "postgres -D /var/lib/pgsql/data"},
			},
		},
	}
}

func TestPrometheusFormat(t *testing.T) {
	var b bytes.Buffer
	if err := Prometheus(&b, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	s := b.String()

	// 同一 family 只能有一组 HELP/TYPE，否则严格解析器会拒绝。
	if n := strings.Count(s, "# TYPE hostmon_cpu_percent gauge\n"); n != 1 {
		t.Fatalf("hostmon_cpu_percent TYPE lines = %d, want 1", n)
	}
	if n := strings.Count(s, "# TYPE hostmon_memory_bytes gauge\n"); n != 1 {
		t.Fatalf("hostmon_memory_bytes TYPE lines = %d, want 1", n)
	}
	// label 值中的引号必须转义
	if !strings.Contains(s, `os="Anolis \"8.9\""`) {
		t.Fatalf("label escaping failed:\n%s", s[:min(len(s), 400)])
	}
	for _, want := range []string{
		"hostmon_cpu_usage_percent 42.5\n",
		`hostmon_cpu_core_usage_percent{core="0"} 50`,
		`hostmon_filesystem_usage_percent{device="/dev/sda1",mountpoint="/",fstype="ext4"} 60`,
		`# TYPE hostmon_disk_reads_total counter`,
		`hostmon_network_receive_bytes_total{interface="eth0"} 5000`,
		`hostmon_tcp_connections{state="established"} 12`,
		`hostmon_processes_scanned 287`,
		`hostmon_process_cpu_percent{pid="4242",name="java",user="app"} 187.5`,
		`hostmon_process_memory_bytes{pid="91",name="postgres",user="postgres"} 1073741824`,
		"hostmon_up 1\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	// 两个榜单重叠的进程必须只出现一次，否则同样标签重复、解析器报错。
	if n := strings.Count(s, `hostmon_process_cpu_percent{pid="4242"`); n != 1 {
		t.Fatalf("pid 4242 emitted %d times, want 1", n)
	}
}

func TestTextAndJSON(t *testing.T) {
	s := sampleSnapshot()
	var b bytes.Buffer
	if err := Text(&b, s, DefaultTextOptions()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"node-01", "CPU", "Memory", "/", "sda", "eth0", "TCP", "TOP CPU", "TOP MEM", "java", "postgres", "共扫描 287 个进程",
		// 启动命令列：有 cmdline 的原样展示，内核线程回退为 [comm]
		"CMD", "/usr/bin/java -Xmx2g -jar /opt/app/app.jar", "[nginx]"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("text output missing %q", want)
		}
	}
	b.Reset()
	if err := JSON(&b, s, false); err != nil {
		t.Fatal(err)
	}
	var got monitor.Snapshot
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("json output invalid: %v", err)
	}
	if got.Host.Hostname != "node-01" || got.CPU.Usage != 42.5 {
		t.Fatalf("json roundtrip = %+v", got)
	}
	if bytes.Count(b.Bytes(), []byte("\n")) != 1 {
		t.Fatal("compact JSON should be exactly one line")
	}
}

func TestClipCmd(t *testing.T) {
	cases := []struct {
		name, cmd, proc, want string
		max                   int
	}{
		{"原样保留", "/usr/bin/java -jar /app/app.jar", "java", "/usr/bin/java -jar /app/app.jar", 72},
		{"空白折叠成单行", "/usr/sbin/nginx\t-c\n/etc/nginx.conf", "nginx", "/usr/sbin/nginx -c /etc/nginx.conf", 72},
		{"内核线程回退", "", "kworker/0:2", "[kworker/0:2]", 72},
		{"只有空白也回退", "   \t ", "ksoftirqd/1", "[ksoftirqd/1]", 72},
		{"超长按字符截断", "aaaaaaaaaaaaaaaaaaaaa", "a", "aaaaaaaaaaaaaaaaaaa…", 20},
		// 按 rune 而不是 byte 截断，中文不会被切成半个字符
		{"中文按字符截断", "一二三四五六七八九十", "x", "一二三四五六七…", 8},
		{"max<=0 不截断", "abcdef", "x", "abcdef", 0},
	}
	for _, c := range cases {
		if got := clipCmd(c.cmd, c.proc, c.max); got != c.want {
			t.Errorf("%s: clipCmd(%q) = %q, want %q", c.name, c.cmd, got, c.want)
		}
	}
}
