package monitor

import "time"

// Snapshot 是某一时刻的完整监控快照，所有速率类指标都由相邻两次采样差分得出。
type Snapshot struct {
	Timestamp time.Time    `json:"timestamp"`
	Interval  float64      `json:"interval_sec"` // 差分窗口（秒）
	Host      HostInfo     `json:"host"`
	Uptime    UptimeInfo   `json:"uptime"`
	Load      LoadInfo     `json:"load"`
	CPU       CPUInfo      `json:"cpu"`
	Memory    MemoryInfo   `json:"memory"`
	Swap      SwapInfo     `json:"swap"`
	Mounts    []MountInfo  `json:"mounts"`
	DiskIO    []DiskIOInfo `json:"disk_io"`
	Network   []NetInfo    `json:"network"`
	TCP       TCPInfo      `json:"tcp"`
	Processes ProcessInfo  `json:"processes"`
}

type HostInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	CPUs     int    `json:"cpus"`
	CPUModel string `json:"cpu_model"`
}

type UptimeInfo struct {
	Seconds  float64   `json:"seconds"`
	Human    string    `json:"human"`
	BootTime time.Time `json:"boot_time"`
}

type LoadInfo struct {
	Load1    float64 `json:"load1"`
	Load5    float64 `json:"load5"`
	Load15   float64 `json:"load15"`
	PerCore1 float64 `json:"per_core_load1"` // load1 / CPU 核数，跨机器可比值
}

type CPUInfo struct {
	Usage   float64    `json:"usage_percent"`
	User    float64    `json:"user_percent"`
	Nice    float64    `json:"nice_percent"`
	System  float64    `json:"system_percent"`
	IOWait  float64    `json:"iowait_percent"`
	IRQ     float64    `json:"irq_percent"`
	SoftIRQ float64    `json:"softirq_percent"`
	Steal   float64    `json:"steal_percent"`
	Idle    float64    `json:"idle_percent"`
	Cores   []CoreInfo `json:"cores"`
}

type CoreInfo struct {
	ID    string  `json:"id"`
	Usage float64 `json:"usage_percent"`
}

// 内存/容量类字段单位均为字节。
type MemoryInfo struct {
	Total        uint64  `json:"total_bytes"`
	Used         uint64  `json:"used_bytes"`
	Available    uint64  `json:"available_bytes"`
	Free         uint64  `json:"free_bytes"`
	Buffers      uint64  `json:"buffers_bytes"`
	Cached       uint64  `json:"cached_bytes"`
	SReclaimable uint64  `json:"sreclaimable_bytes"`
	Shmem        uint64  `json:"shmem_bytes"`
	Usage        float64 `json:"usage_percent"`
}

type SwapInfo struct {
	Total uint64  `json:"total_bytes"`
	Used  uint64  `json:"used_bytes"`
	Free  uint64  `json:"free_bytes"`
	Usage float64 `json:"usage_percent"`
}

type MountInfo struct {
	Device      string  `json:"device"`
	MountPoint  string  `json:"mount_point"`
	FSType      string  `json:"fs_type"`
	Total       uint64  `json:"total_bytes"`
	Used        uint64  `json:"used_bytes"`
	Free        uint64  `json:"free_bytes"`
	Available   uint64  `json:"available_bytes"`
	Usage       float64 `json:"usage_percent"`
	InodesTotal uint64  `json:"inodes_total"`
	InodesUsed  uint64  `json:"inodes_used"`
	InodesUsage float64 `json:"inodes_usage_percent"`
}

type DiskIOInfo struct {
	Device       string  `json:"device"`
	ReadIOPS     float64 `json:"read_iops"`
	WriteIOPS    float64 `json:"write_iops"`
	IOPS         float64 `json:"iops"`
	ReadRate     float64 `json:"read_bytes_per_sec"`
	WriteRate    float64 `json:"write_bytes_per_sec"`
	ReadTotal    uint64  `json:"reads_total"`
	WriteTotal   uint64  `json:"writes_total"`
	ReadBytes    uint64  `json:"read_bytes_total"`
	WriteBytes   uint64  `json:"write_bytes_total"`
	Util         float64 `json:"util_percent"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
}

type NetInfo struct {
	Interface  string  `json:"interface"`
	RxRate     float64 `json:"rx_bytes_per_sec"`
	TxRate     float64 `json:"tx_bytes_per_sec"`
	RxPackets  float64 `json:"rx_packets_per_sec"`
	TxPackets  float64 `json:"tx_packets_per_sec"`
	RxBytes    uint64  `json:"rx_bytes_total"`
	TxBytes    uint64  `json:"tx_bytes_total"`
	RxErrors   uint64  `json:"rx_errors_total"`
	TxErrors   uint64  `json:"tx_errors_total"`
	RxDropped  uint64  `json:"rx_dropped_total"`
	TxDropped  uint64  `json:"tx_dropped_total"`
	RxErrRate  float64 `json:"rx_errors_per_sec"`
	RxDropRate float64 `json:"rx_dropped_per_sec"`
}

type TCPInfo struct {
	Established int `json:"established"`
	Listen      int `json:"listen"`
	TimeWait    int `json:"time_wait"`
	SynSent     int `json:"syn_sent"`
	SynRecv     int `json:"syn_recv"`
	CloseWait   int `json:"close_wait"`
	Other       int `json:"other"`
	Total       int `json:"total"`
}

type ProcessInfo struct {
	Running int    `json:"running"`
	Total   int    `json:"total"`
	Created uint64 `json:"created_total"` // /proc/stat processes，累计 fork 数
}

// 以下是原始计数器（未经差分），仅在包内使用。

type cpuCounter struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

type cpuRaw struct {
	All   cpuCounter
	Cores map[string]cpuCounter
}

type memRaw struct {
	Total, Free, Available, Buffers, Cached, SReclaimable, Shmem uint64 // kB
	SwapTotal, SwapFree                                          uint64 // kB
	HasAvailable                                                 bool
}

type mountRaw struct {
	Device, MountPoint, FSType string
}

type diskRaw struct {
	Reads, ReadSectors, Writes, WriteSectors, IOTimeMS uint64
	ReadTimeMS, WriteTimeMS                            uint64
}

type netRaw struct {
	RxBytes, RxPackets, RxErrors, RxDropped uint64
	TxBytes, TxPackets, TxErrors, TxDropped uint64
}

type rawSnapshot struct {
	At        time.Time
	Host      HostInfo
	Uptime    UptimeInfo
	Load      LoadInfo
	CPU       cpuRaw
	Memory    memRaw
	Mounts    []MountInfo
	DiskIO    map[string]diskRaw
	Network   map[string]netRaw
	TCP       TCPInfo
	Processes ProcessInfo
}
