package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/Zslocal/go_local_host/internal/monitor"
)

// Prometheus 输出 Prometheus text exposition format（0.0.4），
// 可直接被 Prometheus / VictoriaMetrics / Grafana Agent 抓取。
func Prometheus(w io.Writer, s *monitor.Snapshot) error {
	var b strings.Builder
	// 同一个 metric family 的 HELP/TYPE 只允许出现一次，重复会被严格的解析器拒绝。
	seen := make(map[string]bool)
	emit := func(name, help, typ string, value interface{}, labels ...string) {
		if !seen[name] {
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
			seen[name] = true
		}
		fmt.Fprintf(&b, "%s%s %v\n", name, renderLabels(labels), value)
	}
	gauge := func(name, help string, value interface{}, labels ...string) {
		emit(name, help, "gauge", value, labels...)
	}
	counter := func(name, help string, value interface{}, labels ...string) {
		emit(name, help, "counter", value, labels...)
	}

	gauge("hostmon_up", "Whether the last collection succeeded.", 1)
	gauge("hostmon_info", "Static host information (value is always 1).", 1,
		"hostname", s.Host.Hostname, "os", s.Host.OS, "kernel", s.Host.Kernel,
		"arch", s.Host.Arch, "cpu_model", s.Host.CPUModel)
	gauge("hostmon_cpu_count", "Number of logical CPUs.", s.Host.CPUs)
	gauge("hostmon_uptime_seconds", "Seconds since boot.", s.Uptime.Seconds)
	gauge("hostmon_boot_time_seconds", "Boot time as Unix timestamp.", s.Uptime.BootTime.Unix())
	gauge("hostmon_scrape_interval_seconds", "Interval between the two samples used for rate metrics.", s.Interval)

	gauge("hostmon_load1", "1-minute load average.", s.Load.Load1)
	gauge("hostmon_load5", "5-minute load average.", s.Load.Load5)
	gauge("hostmon_load15", "15-minute load average.", s.Load.Load15)
	gauge("hostmon_load1_per_cpu", "1-minute load average divided by logical CPU count.", s.Load.PerCore1)

	gauge("hostmon_cpu_usage_percent", "Total CPU usage percent.", s.CPU.Usage)
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.User, "mode", "user")
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.Nice, "mode", "nice")
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.System, "mode", "system")
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.IOWait, "mode", "iowait")
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.IRQ, "mode", "irq")
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.SoftIRQ, "mode", "softirq")
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.Steal, "mode", "steal")
	gauge("hostmon_cpu_percent", "CPU time breakdown in percent.", s.CPU.Idle, "mode", "idle")
	for _, c := range s.CPU.Cores {
		gauge("hostmon_cpu_core_usage_percent", "Per-core CPU usage percent.", c.Usage, "core", c.ID)
	}

	gauge("hostmon_memory_bytes", "Memory in bytes.", s.Memory.Total, "kind", "total")
	gauge("hostmon_memory_bytes", "Memory in bytes.", s.Memory.Used, "kind", "used")
	gauge("hostmon_memory_bytes", "Memory in bytes.", s.Memory.Available, "kind", "available")
	gauge("hostmon_memory_bytes", "Memory in bytes.", s.Memory.Free, "kind", "free")
	gauge("hostmon_memory_bytes", "Memory in bytes.", s.Memory.Buffers, "kind", "buffers")
	gauge("hostmon_memory_bytes", "Memory in bytes.", s.Memory.Cached, "kind", "cached")
	gauge("hostmon_memory_usage_percent", "Memory usage percent (based on MemAvailable).", s.Memory.Usage)
	gauge("hostmon_swap_bytes", "Swap in bytes.", s.Swap.Total, "kind", "total")
	gauge("hostmon_swap_bytes", "Swap in bytes.", s.Swap.Used, "kind", "used")
	gauge("hostmon_swap_usage_percent", "Swap usage percent.", s.Swap.Usage)

	for _, m := range s.Mounts {
		l := []string{"device", m.Device, "mountpoint", m.MountPoint, "fstype", m.FSType}
		gauge("hostmon_filesystem_bytes", "Filesystem capacity in bytes.", m.Total, append(l, "kind", "total")...)
		gauge("hostmon_filesystem_bytes", "Filesystem capacity in bytes.", m.Used, append(l, "kind", "used")...)
		gauge("hostmon_filesystem_bytes", "Filesystem capacity in bytes.", m.Available, append(l, "kind", "available")...)
		gauge("hostmon_filesystem_usage_percent", "Filesystem usage percent.", m.Usage, l...)
		gauge("hostmon_filesystem_inodes", "Inode count.", m.InodesTotal, append(l, "kind", "total")...)
		gauge("hostmon_filesystem_inodes", "Inode count.", m.InodesUsed, append(l, "kind", "used")...)
		gauge("hostmon_filesystem_inodes_usage_percent", "Inode usage percent.", m.InodesUsage, l...)
	}

	for _, d := range s.DiskIO {
		l := []string{"device", d.Device}
		gauge("hostmon_disk_reads_per_second", "Completed reads per second.", d.ReadIOPS, l...)
		gauge("hostmon_disk_writes_per_second", "Completed writes per second.", d.WriteIOPS, l...)
		gauge("hostmon_disk_read_bytes_per_second", "Bytes read per second.", d.ReadRate, l...)
		gauge("hostmon_disk_write_bytes_per_second", "Bytes written per second.", d.WriteRate, l...)
		gauge("hostmon_disk_io_in_flight_percent", "Device busy percent (io_time / elapsed).", d.Util, l...)
		gauge("hostmon_disk_avg_latency_ms", "Average IO latency in milliseconds.", d.AvgLatencyMS, l...)
		counter("hostmon_disk_reads_total", "Cumulative completed reads.", d.ReadTotal, l...)
		counter("hostmon_disk_writes_total", "Cumulative completed writes.", d.WriteTotal, l...)
		counter("hostmon_disk_read_bytes_total", "Cumulative bytes read.", d.ReadBytes, l...)
		counter("hostmon_disk_write_bytes_total", "Cumulative bytes written.", d.WriteBytes, l...)
	}

	for _, n := range s.Network {
		l := []string{"interface", n.Interface}
		gauge("hostmon_network_receive_bytes_per_second", "Receive rate in bytes/s.", n.RxRate, l...)
		gauge("hostmon_network_transmit_bytes_per_second", "Transmit rate in bytes/s.", n.TxRate, l...)
		gauge("hostmon_network_receive_packets_per_second", "Receive packets/s.", n.RxPackets, l...)
		gauge("hostmon_network_transmit_packets_per_second", "Transmit packets/s.", n.TxPackets, l...)
		counter("hostmon_network_receive_bytes_total", "Cumulative received bytes.", n.RxBytes, l...)
		counter("hostmon_network_transmit_bytes_total", "Cumulative transmitted bytes.", n.TxBytes, l...)
		counter("hostmon_network_receive_errors_total", "Cumulative receive errors.", n.RxErrors, l...)
		counter("hostmon_network_transmit_errors_total", "Cumulative transmit errors.", n.TxErrors, l...)
		counter("hostmon_network_receive_drops_total", "Cumulative receive drops.", n.RxDropped, l...)
		counter("hostmon_network_transmit_drops_total", "Cumulative transmit drops.", n.TxDropped, l...)
	}

	gauge("hostmon_tcp_connections", "TCP connections by state.", s.TCP.Established, "state", "established")
	gauge("hostmon_tcp_connections", "TCP connections by state.", s.TCP.Listen, "state", "listen")
	gauge("hostmon_tcp_connections", "TCP connections by state.", s.TCP.TimeWait, "state", "time_wait")
	gauge("hostmon_tcp_connections", "TCP connections by state.", s.TCP.SynSent, "state", "syn_sent")
	gauge("hostmon_tcp_connections", "TCP connections by state.", s.TCP.SynRecv, "state", "syn_recv")
	gauge("hostmon_tcp_connections", "TCP connections by state.", s.TCP.CloseWait, "state", "close_wait")
	gauge("hostmon_tcp_connections", "TCP connections by state.", s.TCP.Other, "state", "other")
	gauge("hostmon_processes", "Process count.", s.Processes.Total, "state", "total")
	gauge("hostmon_processes", "Process count.", s.Processes.Running, "state", "running")
	counter("hostmon_processes_created_total", "Cumulative forks since boot.", s.Processes.Created)

	_, err := io.WriteString(w, b.String())
	return err
}

// renderLabels 接收 key,value 交替的参数列表。
func renderLabels(kv []string) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=\"%s\"", kv[i], escLabel(kv[i+1]))
	}
	b.WriteByte('}')
	return b.String()
}

func escLabel(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}
