//go:build linux

package monitor

// Linux 采集实现：全部数据来自 /proc、/proc/sys 与 statfs(2)，无外部命令、无 CGO。

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// 路径片段：实际绝对路径由 hostPaths 根据 -proc-root / -host-root 拼出。
const (
	procStat      = "stat"
	procMemInfo   = "meminfo"
	procLoadAvg   = "loadavg"
	procUptime    = "uptime"
	procNetDev    = "net/dev"
	procDiskStats = "diskstats"
	procMounts    = "mounts"
	procNetTCP    = "net/tcp"
	procNetTCP6   = "net/tcp6"
	procCPUInfo   = "cpuinfo"
	procOsRelease = "sys/kernel/osrelease"
	etcOsRelease  = "os-release"
	etcHostname   = "hostname"
)

// 只统计真实的、带容量语义的文件系统，排除 procfs/sysfs/cgroup/tmpfs 等伪文件系统。
var realFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true,
	"f2fs": true, "reiserfs": true, "jfs": true, "zfs": true, "bcache": true,
	"vfat": true, "ntfs": true, "exfat": true, "nfs": true, "nfs4": true,
	"cifs": true, "smb3": true, "overlay": true, "9p": true, "virtiofs": true,
}

// 容器宿主机上 veth/cni 数量可达数十个，默认不计入网卡统计。
var virtualIfacePrefixes = []string{
	"lo", "veth", "docker", "br-", "virbr", "flannel", "cni", "tunl",
	"dummy", "sit", "gre", "ip6tnl", "kube-", "cali", "lxc",
}

func readProc(path string) (*os.File, error) {
	return os.Open(path)
}

func collectRaw(cfg Config) (*rawSnapshot, error) {
	s := &rawSnapshot{At: time.Now()}
	hp := newHostPaths(cfg.ProcRoot, cfg.HostRoot)

	// --- CPU / fork 数 / 启动时间戳 ---
	statPath := hp.proc(procStat)
	if f, err := readProc(statPath); err == nil {
		cpu, created, btime, err := parseCPUStat(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", statPath, err)
		}
		s.CPU = cpu
		s.Processes.Created = created
		if btime > 0 {
			s.Uptime.BootTime = time.Unix(int64(btime), 0).UTC()
		}
	} else {
		return nil, fmt.Errorf("open %s: %w", statPath, err)
	}

	// --- 运行时间 ---
	if f, err := readProc(hp.proc(procUptime)); err == nil {
		sec, err := parseUptime(f)
		f.Close()
		if err == nil {
			s.Uptime.Seconds = sec
		}
	}
	s.Uptime.Human = humanDuration(s.Uptime.Seconds)
	if s.Uptime.BootTime.IsZero() {
		s.Uptime.BootTime = s.At.Add(-time.Duration(s.Uptime.Seconds * float64(time.Second))).UTC()
	}

	// --- 负载与进程数 ---
	if f, err := readProc(hp.proc(procLoadAvg)); err == nil {
		load, procs, err := parseLoadAvg(f)
		f.Close()
		if err == nil {
			s.Load, s.Processes.Running, s.Processes.Total = load, procs.Running, procs.Total
		}
	}

	// --- 内存 ---
	memPath := hp.proc(procMemInfo)
	if f, err := readProc(memPath); err == nil {
		m, err := parseMemInfo(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", memPath, err)
		}
		s.Memory = m
	} else {
		return nil, fmt.Errorf("open %s: %w", memPath, err)
	}

	// --- 挂载点容量 ---
	if f, err := readProc(hp.proc(procMounts)); err == nil {
		mounts, err := parseMounts(f)
		f.Close()
		if err == nil {
			s.Mounts = statMounts(mounts, hp)
		}
	}

	// --- 块设备 IO ---
	if f, err := readProc(hp.proc(procDiskStats)); err == nil {
		s.DiskIO, _ = parseDiskStats(f)
		f.Close()
	}

	// --- 网卡流量 ---
	if f, err := readProc(hp.proc(procNetDev)); err == nil {
		all, _ := parseNetDev(f)
		f.Close()
		s.Network = make(map[string]netRaw)
		for name, v := range all {
			if cfg.NetAll || !isVirtualIface(name) {
				s.Network[name] = v
			}
		}
	}

	// --- TCP 连接状态（文件缺失属正常，例如 IPv6 被禁用） ---
	for _, name := range []string{procNetTCP, procNetTCP6} {
		if f, err := readProc(hp.proc(name)); err == nil {
			_ = parseTCP(f, &s.TCP)
			f.Close()
		}
	}

	s.Host = collectHost(hp)
	if s.Host.CPUs > 0 && s.Load.Load1 >= 0 {
		s.Load.PerCore1 = s.Load.Load1 / float64(s.Host.CPUs)
	}
	return s, nil
}

func collectHost(hp hostPaths) HostInfo {
	h := HostInfo{
		Arch:     runtime.GOARCH,
		CPUs:     runtime.NumCPU(),
		OS:       runtime.GOOS,
		CPUModel: "unknown",
	}
	// 容器内 os.Hostname() 返回的是容器 ID；挂了宿主机根时优先读宿主机 /etc/hostname。
	if hp.hostRoot != "" {
		if b, err := os.ReadFile(hp.etc(etcHostname)); err == nil {
			h.Hostname = strings.TrimSpace(string(b))
		}
	}
	if h.Hostname == "" {
		if name, err := os.Hostname(); err == nil {
			h.Hostname = name
		}
	}
	if b, err := os.ReadFile(hp.proc(procOsRelease)); err == nil {
		h.Kernel = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(hp.etc(etcOsRelease)); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				h.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
				break
			}
		}
	}
	if f, err := readProc(hp.proc(procCPUInfo)); err == nil {
		h.CPUModel = parseCPUModel(f)
		f.Close()
	}
	return h
}

func statMounts(mounts []mountRaw, hp hostPaths) []MountInfo {
	var out []MountInfo
	seen := make(map[string]string) // device -> 已保留的最短挂载点
	skipPrefix := []string{"/proc", "/sys", "/dev", "/run", "/snap", "/var/lib/docker", "/var/lib/kubelet"}

	for _, m := range mounts {
		if !realFS[m.FSType] || m.MountPoint == "" {
			continue
		}
		// 容器里挂载表可能已带 /host 前缀，统一归一化后再过滤 / statfs / 展示。
		statPath, display := hp.normalize(m.MountPoint)
		skip := false
		for _, p := range skipPrefix {
			if display == p || strings.HasPrefix(display, p+"/") {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// 同一设备（典型如 docker overlay）只保留最有代表性的一个挂载点。
		key := m.Device
		if old, ok := seen[key]; ok {
			if len(old) <= len(display) {
				continue
			}
			for i := range out {
				if out[i].Device == key && out[i].MountPoint == old {
					out = append(out[:i], out[i+1:]...)
					break
				}
			}
		}
		seen[key] = display

		var st syscall.Statfs_t
		if err := syscall.Statfs(statPath, &st); err != nil {
			continue
		}
		bsize := uint64(st.Bsize)
		if bsize == 0 {
			continue
		}
		total := uint64(st.Blocks) * bsize
		free := uint64(st.Bfree) * bsize
		avail := uint64(st.Bavail) * bsize
		used := total - free
		mi := MountInfo{
			Device: m.Device, MountPoint: display, FSType: m.FSType,
			Total: total, Used: used, Free: free, Available: avail,
			InodesTotal: uint64(st.Files), InodesUsed: uint64(st.Files) - uint64(st.Ffree),
		}
		mi.Usage = percent(used, total)
		mi.InodesUsage = percent(mi.InodesUsed, mi.InodesTotal)
		out = append(out, mi)
	}
	return out
}

func isVirtualIface(name string) bool {
	for _, p := range virtualIfacePrefixes {
		if name == p || strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
