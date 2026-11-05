package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Zslocal/go_local_host/internal/monitor"
)

type TextOptions struct {
	MaxMounts int // 终端最多展示的挂载点行数，0 表示全部
	MaxDisks  int
	MaxNets   int
	ShowCores bool
}

func DefaultTextOptions() TextOptions {
	return TextOptions{MaxMounts: 8, MaxDisks: 6, MaxNets: 6}
}

// Text 把快照渲染成一份可直接阅读、也可重定向到日志的纯文本报告。
func Text(w io.Writer, s *monitor.Snapshot, opt TextOptions) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	h := s.Host

	fmt.Fprintf(tw, "== %s  %s  %s/%s  %d CPU  %s\n",
		s.Timestamp.Format("2006-01-02 15:04:05"),
		empty(h.Hostname, "unknown-host"), empty(h.OS, h.Arch), h.Arch, h.CPUs,
		empty(h.CPUModel, "unknown cpu"))
	fmt.Fprintf(tw, "   kernel %s | uptime %s (%s) | boot %s | window %.2fs\n",
		empty(h.Kernel, "-"), s.Uptime.Human, humanSeconds(s.Uptime.Seconds),
		s.Uptime.BootTime.Format("2006-01-02 15:04:05"), s.Interval)
	fmt.Fprintf(tw, "   load %.2f / %.2f / %.2f (per-core %.2f) | procs %d running / %d total\n\n",
		s.Load.Load1, s.Load.Load5, s.Load.Load15, s.Load.PerCore1,
		s.Processes.Running, s.Processes.Total)

	fmt.Fprintf(tw, "CPU\t%6.2f%%\tusr %.2f sys %.2f iowait %.2f irq %.2f softirq %.2f steal %.2f nice %.2f idle %.2f\n",
		s.CPU.Usage, s.CPU.User, s.CPU.System, s.CPU.IOWait, s.CPU.IRQ,
		s.CPU.SoftIRQ, s.CPU.Steal, s.CPU.Nice, s.CPU.Idle)
	if opt.ShowCores && len(s.CPU.Cores) > 0 {
		var b strings.Builder
		for i, c := range s.CPU.Cores {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%s:%.0f%%", c.ID, c.Usage)
			if i >= 15 {
				fmt.Fprintf(&b, " ...(+%d)", len(s.CPU.Cores)-16)
				break
			}
		}
		fmt.Fprintf(tw, "  cores\t%s\n", b.String())
	}
	fmt.Fprintf(tw, "Memory\t%6.2f%%\t%s used / %s total | avail %s | free %s | buf/cache %s\n",
		s.Memory.Usage, humanBytes(s.Memory.Used), humanBytes(s.Memory.Total),
		humanBytes(s.Memory.Available), humanBytes(s.Memory.Free),
		humanBytes(s.Memory.Buffers+s.Memory.Cached))
	fmt.Fprintf(tw, "Swap\t%6.2f%%\t%s used / %s total\n\n",
		s.Swap.Usage, humanBytes(s.Swap.Used), humanBytes(s.Swap.Total))

	// 挂载点容量
	if len(s.Mounts) > 0 {
		fmt.Fprint(tw, "MOUNT\tFS\tTOTAL\tUSED\tAVAIL\tUSE%\tINODE%\n")
		for i, m := range s.Mounts {
			if opt.MaxMounts > 0 && i >= opt.MaxMounts {
				break
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%.2f\t%.2f\n",
				m.MountPoint, m.FSType, humanBytes(m.Total), humanBytes(m.Used),
				humanBytes(m.Available), m.Usage, m.InodesUsage)
		}
		if opt.MaxMounts > 0 && len(s.Mounts) > opt.MaxMounts {
			fmt.Fprintf(tw, "  ... %d more mounts (use -all-lines)\n", len(s.Mounts)-opt.MaxMounts)
		}
		fmt.Fprint(tw, "\n")
	}

	// 块设备 IO
	if len(s.DiskIO) > 0 {
		fmt.Fprint(tw, "DISK\tr/s\tw/s\tREAD/s\tWRITE/s\tIOPS\tUTIL%\tLAT(ms)\n")
		shown := 0
		for _, d := range s.DiskIO {
			if isIgnoredDisk(d.Device) {
				continue
			}
			if opt.MaxDisks > 0 && shown >= opt.MaxDisks {
				break
			}
			fmt.Fprintf(tw, "  %s\t%.1f\t%.1f\t%s/s\t%s/s\t%.1f\t%.1f\t%.2f\n",
				d.Device, d.ReadIOPS, d.WriteIOPS, humanBytes(uint64(d.ReadRate)),
				humanBytes(uint64(d.WriteRate)), d.IOPS, d.Util, d.AvgLatencyMS)
			shown++
		}
		fmt.Fprint(tw, "\n")
	}

	// 网卡速率
	if len(s.Network) > 0 {
		fmt.Fprint(tw, "NET\tRX/s\tTX/s\tRXpkt/s\tTXpkt/s\tRX ERR/DROP (total)\n")
		for i, n := range s.Network {
			if opt.MaxNets > 0 && i >= opt.MaxNets {
				fmt.Fprintf(tw, "  ... %d more interfaces (use -all-lines)\n", len(s.Network)-i)
				break
			}
			fmt.Fprintf(tw, "  %s\t%s/s\t%s/s\t%.0f\t%.0f\t%d / %d\n",
				n.Interface, humanBytes(uint64(n.RxRate)), humanBytes(uint64(n.TxRate)),
				n.RxPackets, n.TxPackets, n.RxErrors, n.RxDropped)
		}
		fmt.Fprint(tw, "\n")
	}

	fmt.Fprintf(tw, "TCP\t estab %d | listen %d | time_wait %d | syn_sent %d | syn_recv %d | close_wait %d | total %d\n",
		s.TCP.Established, s.TCP.Listen, s.TCP.TimeWait, s.TCP.SynSent,
		s.TCP.SynRecv, s.TCP.CloseWait, s.TCP.Total)
	return tw.Flush()
}

func isIgnoredDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "fd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func empty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func humanSeconds(v float64) string {
	d := int64(v)
	return fmt.Sprintf("%dd%02dh%02dm", d/86400, (d%86400)/3600, (d%3600)/60)
}

// humanBytes 用 1024 进制，保留一位小数，适合终端阅读。
func humanBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%dB", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(v)/float64(div), "KMGTP"[exp])
}
