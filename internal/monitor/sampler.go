package monitor

// 平台无关的差分引擎：CPU 时间片、块设备与网卡都是单调递增计数器，
// 必须用相邻两次采样的差值除以真实时间窗口才能得到百分比 / 速率。

import (
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

const sectorSize = 512 // /proc/diskstats 的 sectors 恒为 512 字节

// clkTicks 是内核 USER_HZ：/proc/<pid>/stat 的 utime/stime 以它为 1 秒。
// Linux（含 arm64）上恒为 100，无需 sysconf。
const clkTicks = 100

type Config struct {
	Warmup   time.Duration // 首次采样的差分窗口，默认 700ms
	NetAll   bool          // 是否把 lo / veth / cni 等虚拟网卡一并统计
	TopN     int           // 进程榜单长度（CPU / 内存各 N 条），0 表示不采集
	ProcRoot string        // /proc 的挂载路径，默认 /proc；容器内为 /host/proc
	HostRoot string        // 宿主机根文件系统的挂载路径，默认空（本机）；容器内为 /host
}

type Sampler struct {
	mu   sync.Mutex
	cfg  Config
	prev *rawSnapshot
}

func NewSampler(cfg Config) *Sampler {
	if cfg.Warmup <= 0 {
		cfg.Warmup = 700 * time.Millisecond
	}
	return &Sampler{cfg: cfg}
}

// Sample 采集一次并返回完整快照。首次调用会在内部做两次采样（间隔 Warmup），
// 因此 -once 模式也能立刻得到真实的 CPU 百分比和网络/磁盘速率；
// 持续调用时差分窗口自动等于调用间隔。
func (s *Sampler) Sample() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := collectRaw(s.cfg)
	if err != nil {
		return nil, err
	}
	prev := s.prev
	if prev == nil {
		time.Sleep(s.cfg.Warmup)
		prev = cur
		cur, err = collectRaw(s.cfg)
		if err != nil {
			return nil, err
		}
	}
	s.prev = cur
	snap := buildSnapshot(prev, cur, s.cfg.TopN)
	// cmdline 只对入选榜单的进程读取，需在榜单算完之后补，且只有 Linux 有 /proc。
	enrichProcDetails(snap, newHostPaths(s.cfg.ProcRoot, s.cfg.HostRoot))
	return snap, nil
}

func buildSnapshot(prev, cur *rawSnapshot, topN int) *Snapshot {
	sec := cur.At.Sub(prev.At).Seconds()
	if sec <= 0 {
		sec = 1e-6
	}
	snap := &Snapshot{
		Timestamp: cur.At,
		Interval:  round2(sec),
		Host:      cur.Host,
		Uptime:    cur.Uptime,
		Load:      cur.Load,
		Memory:    buildMemory(cur.Memory),
		Swap:      buildSwap(cur.Memory),
		CPU:       buildCPU(prev.CPU.All, cur.CPU.All),
		Mounts:    append([]MountInfo(nil), cur.Mounts...),
		TCP:       cur.TCP,
		Processes: cur.Processes,
	}
	snap.Processes.TopCPU, snap.Processes.TopMem = buildProcTop(prev, cur, sec, topN)
	snap.Processes.Scanned = len(cur.Procs)

	// 每核使用率
	for id, pc := range cur.CPU.Cores {
		old, ok := prev.CPU.Cores[id]
		if !ok {
			old = pc
		}
		snap.CPU.Cores = append(snap.CPU.Cores, CoreInfo{
			ID: id, Usage: buildCPU(old, pc).Usage,
		})
	}
	sort.Slice(snap.CPU.Cores, func(i, j int) bool {
		a, errA := strconv.Atoi(snap.CPU.Cores[i].ID)
		b, errB := strconv.Atoi(snap.CPU.Cores[j].ID)
		if errA == nil && errB == nil {
			return a < b
		}
		return snap.CPU.Cores[i].ID < snap.CPU.Cores[j].ID
	})

	// 块设备 IO
	for name, c := range cur.DiskIO {
		p, ok := prev.DiskIO[name]
		if !ok {
			p = c // 本次新增设备不产生速率
		}
		snap.DiskIO = append(snap.DiskIO, buildDiskIO(name, p, c, sec))
	}
	sort.Slice(snap.DiskIO, func(i, j int) bool {
		return snap.DiskIO[i].ReadRate+snap.DiskIO[i].WriteRate >
			snap.DiskIO[j].ReadRate+snap.DiskIO[j].WriteRate
	})

	// 网卡流量
	for name, c := range cur.Network {
		p, ok := prev.Network[name]
		if !ok {
			p = c
		}
		snap.Network = append(snap.Network, buildNet(name, p, c, sec))
	}
	sort.Slice(snap.Network, func(i, j int) bool {
		return snap.Network[i].RxRate+snap.Network[i].TxRate >
			snap.Network[j].RxRate+snap.Network[j].TxRate
	})

	sort.Slice(snap.Mounts, func(i, j int) bool {
		return snap.Mounts[i].Usage > snap.Mounts[j].Usage
	})
	return snap
}

func buildCPU(a, b cpuCounter) CPUInfo {
	dt := delta(a.total(), b.total())
	if dt == 0 {
		return CPUInfo{Idle: 100}
	}
	p := func(x, y uint64) float64 { return round2(float64(delta(x, y)) / float64(dt) * 100) }
	c := CPUInfo{
		User:    p(a.User, b.User),
		Nice:    p(a.Nice, b.Nice),
		System:  p(a.System, b.System),
		IOWait:  p(a.IOWait, b.IOWait),
		IRQ:     p(a.IRQ, b.IRQ),
		SoftIRQ: p(a.SoftIRQ, b.SoftIRQ),
		Steal:   p(a.Steal, b.Steal),
	}
	c.Idle = p(a.Idle, b.Idle)
	// 与 top / node_exporter 口径一致：CPU 使用率 = 100 - idle（idle 不含 iowait），
	// 即 iowait 属于「非空闲」，但也单独列分项，便于判断 IO 瓶颈。
	c.Usage = clamp(round2(100-c.Idle), 0, 100)
	return c
}

func buildMemory(m memRaw) MemoryInfo {
	kb := func(v uint64) uint64 { return v * 1024 }
	avail := m.Available
	if !m.HasAvailable {
		// 内核 < 3.14 无 MemAvailable，用经典近似值
		avail = m.Free + m.Buffers + m.Cached + m.SReclaimable
		if avail > m.Shmem {
			avail -= m.Shmem
		}
	}
	if avail > m.Total {
		avail = m.Total
	}
	used := m.Total - avail
	return MemoryInfo{
		Total: kb(m.Total), Used: kb(used), Available: kb(avail),
		Free: kb(m.Free), Buffers: kb(m.Buffers), Cached: kb(m.Cached),
		SReclaimable: kb(m.SReclaimable), Shmem: kb(m.Shmem),
		Usage: percent(used, m.Total),
	}
}

func buildSwap(m memRaw) SwapInfo {
	used := delta(m.SwapFree, m.SwapTotal)
	return SwapInfo{
		Total: m.SwapTotal * 1024, Used: used * 1024, Free: m.SwapFree * 1024,
		Usage: percent(used, m.SwapTotal),
	}
}

func buildDiskIO(name string, a, b diskRaw, sec float64) DiskIOInfo {
	reads := delta(a.Reads, b.Reads)
	writes := delta(a.Writes, b.Writes)
	rsect := delta(a.ReadSectors, b.ReadSectors)
	wsect := delta(a.WriteSectors, b.WriteSectors)
	ioMs := delta(a.IOTimeMS, b.IOTimeMS)
	ops := reads + writes
	var latency float64
	if ops > 0 {
		latency = round2(float64(delta(a.ReadTimeMS, b.ReadTimeMS)+delta(a.WriteTimeMS, b.WriteTimeMS)) / float64(ops))
	}
	return DiskIOInfo{
		Device:       name,
		ReadIOPS:     round2(float64(reads) / sec),
		WriteIOPS:    round2(float64(writes) / sec),
		IOPS:         round2(float64(ops) / sec),
		ReadRate:     round2(float64(rsect*sectorSize) / sec),
		WriteRate:    round2(float64(wsect*sectorSize) / sec),
		ReadTotal:    b.Reads,
		WriteTotal:   b.Writes,
		ReadBytes:    b.ReadSectors * sectorSize,
		WriteBytes:   b.WriteSectors * sectorSize,
		Util:         clamp(round2(float64(ioMs)/(sec*1000)*100), 0, 100),
		AvgLatencyMS: latency,
	}
}

func buildNet(name string, a, b netRaw, sec float64) NetInfo {
	return NetInfo{
		Interface:  name,
		RxRate:     round2(float64(delta(a.RxBytes, b.RxBytes)) / sec),
		TxRate:     round2(float64(delta(a.TxBytes, b.TxBytes)) / sec),
		RxPackets:  round2(float64(delta(a.RxPackets, b.RxPackets)) / sec),
		TxPackets:  round2(float64(delta(a.TxPackets, b.TxPackets)) / sec),
		RxBytes:    b.RxBytes,
		TxBytes:    b.TxBytes,
		RxErrors:   b.RxErrors,
		TxErrors:   b.TxErrors,
		RxDropped:  b.RxDropped,
		TxDropped:  b.TxDropped,
		RxErrRate:  round2(float64(delta(a.RxErrors, b.RxErrors)) / sec),
		RxDropRate: round2(float64(delta(a.RxDropped, b.RxDropped)) / sec),
	}
}

// buildProcTop 返回两个榜单：按 CPU 使用率降序、按常驻内存降序，各截断到 topN。
// 只做一次全量遍历 + 两次排序，不额外访问文件系统。
func buildProcTop(prev, cur *rawSnapshot, sec float64, topN int) (byCPU, byMem []ProcInfo) {
	if topN <= 0 || len(cur.Procs) == 0 {
		return nil, nil
	}
	page := float64(os.Getpagesize())
	memTotal := float64(cur.Memory.Total) * 1024 // kB -> 字节
	all := make([]ProcInfo, 0, len(cur.Procs))
	for pid, p := range cur.Procs {
		pi := ProcInfo{
			PID: pid, Name: p.Name, User: p.User, State: p.State, Threads: p.Threads,
			RSS: uint64(float64(p.RSSPages) * page),
		}
		// PID 复用检测：starttime 变了说明这是新进程，不能拿它和旧进程比 CPU 时间。
		if old, ok := prev.Procs[pid]; ok && old.StartTicks == p.StartTicks {
			pi.CPU = round2(float64(delta(old.CPUTicks, p.CPUTicks)) / clkTicks / sec * 100)
		}
		if memTotal > 0 {
			pi.Mem = clamp(round2(float64(pi.RSS)/memTotal*100), 0, 100)
		}
		all = append(all, pi)
	}

	byCPU = append([]ProcInfo(nil), all...)
	sort.Slice(byCPU, func(i, j int) bool {
		if byCPU[i].CPU != byCPU[j].CPU {
			return byCPU[i].CPU > byCPU[j].CPU
		}
		if byCPU[i].RSS != byCPU[j].RSS {
			return byCPU[i].RSS > byCPU[j].RSS
		}
		return byCPU[i].PID < byCPU[j].PID
	})
	byMem = append([]ProcInfo(nil), all...)
	sort.Slice(byMem, func(i, j int) bool {
		if byMem[i].RSS != byMem[j].RSS {
			return byMem[i].RSS > byMem[j].RSS
		}
		return byMem[i].PID < byMem[j].PID
	})
	return headProcs(byCPU, topN), headProcs(byMem, topN)
}

func headProcs(list []ProcInfo, n int) []ProcInfo {
	if len(list) > n {
		return list[:n]
	}
	return list
}

// delta 防止计数器回绕 / 设备重置产生负数（或 uint64 下溢）。
func delta(a, b uint64) uint64 {
	if b <= a {
		return 0
	}
	return b - a
}

func percent(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return clamp(round2(float64(part)/float64(total)*100), 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
