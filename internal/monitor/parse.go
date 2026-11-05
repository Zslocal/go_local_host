package monitor

// 本文件只做纯文本解析，不访问文件系统，因此在任意平台上都可以编译并单元测试。

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var errEmpty = errors.New("empty input")

// /proc/stat:
//
//	cpu   user nice system idle iowait irq softirq steal guest guest_nice
//	cpu0  ...
//	intr ... / ctxt ... / btime ... / processes ... / procs_running ... / procs_blocked ...
//
// guest/guest_nice 已包含在 user/nice 中，故只取前 8 个计数器。
func parseCPUStat(r io.Reader) (cpuRaw, uint64, uint64, error) {
	out := cpuRaw{Cores: make(map[string]cpuCounter)}
	var created uint64
	var btime uint64
	sc := bufio.NewScanner(r)
	seen := false
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "cpu" || strings.HasPrefix(f[0], "cpu"):
			c, err := parseCPUFields(f[1:])
			if err != nil {
				continue
			}
			if f[0] == "cpu" {
				out.All = c
				seen = true
			} else if _, err := strconv.Atoi(strings.TrimPrefix(f[0], "cpu")); err == nil {
				out.Cores[strings.TrimPrefix(f[0], "cpu")] = c
			}
		case f[0] == "processes" && len(f) > 1:
			created, _ = strconv.ParseUint(f[1], 10, 64)
		case f[0] == "btime" && len(f) > 1:
			btime, _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	if err := sc.Err(); err != nil {
		return out, 0, 0, err
	}
	if !seen {
		return out, 0, 0, errors.New("/proc/stat: missing cpu line")
	}
	return out, created, btime, nil
}

func parseCPUFields(f []string) (cpuCounter, error) {
	if len(f) < 8 {
		return cpuCounter{}, errEmpty
	}
	var v [8]uint64
	for i := 0; i < 8; i++ {
		n, err := strconv.ParseUint(f[i], 10, 64)
		if err != nil {
			return cpuCounter{}, err
		}
		v[i] = n
	}
	return cpuCounter{
		User: v[0], Nice: v[1], System: v[2], Idle: v[3],
		IOWait: v[4], IRQ: v[5], SoftIRQ: v[6], Steal: v[7],
	}, nil
}

func (c cpuCounter) total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

func (c cpuCounter) idleAll() uint64 { return c.Idle + c.IOWait }

// /proc/meminfo，单位 kB。
func parseMemInfo(r io.Reader) (memRaw, error) {
	var m memRaw
	sc := bufio.NewScanner(r)
	read := 0
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		key := line[:i]
		f := strings.Fields(line[i+1:])
		if len(f) == 0 {
			continue
		}
		val, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		read++
		switch key {
		case "MemTotal":
			m.Total = val
		case "MemFree":
			m.Free = val
		case "MemAvailable":
			m.Available, m.HasAvailable = val, true
		case "Buffers":
			m.Buffers = val
		case "Cached":
			m.Cached = val
		case "SReclaimable":
			m.SReclaimable = val
		case "Shmem":
			m.Shmem = val
		case "SwapTotal":
			m.SwapTotal = val
		case "SwapFree":
			m.SwapFree = val
		}
	}
	if err := sc.Err(); err != nil {
		return m, err
	}
	if m.Total == 0 {
		return m, errors.New("/proc/meminfo: missing MemTotal")
	}
	_ = read
	return m, nil
}

// /proc/loadavg: "0.15 0.21 0.18 2/1024 12345"
func parseLoadAvg(r io.Reader) (LoadInfo, ProcessInfo, error) {
	var l LoadInfo
	var p ProcessInfo
	b, err := io.ReadAll(r)
	if err != nil {
		return l, p, err
	}
	f := strings.Fields(string(b))
	if len(f) < 5 {
		return l, p, errors.New("/proc/loadavg: malformed")
	}
	l.Load1, _ = strconv.ParseFloat(f[0], 64)
	l.Load5, _ = strconv.ParseFloat(f[1], 64)
	l.Load15, _ = strconv.ParseFloat(f[2], 64)
	parts := strings.SplitN(f[3], "/", 2)
	if len(parts) == 2 {
		p.Running, _ = strconv.Atoi(parts[0])
		p.Total, _ = strconv.Atoi(parts[1])
	}
	return l, p, nil
}

// /proc/uptime: "12345.67 98765.43"（运行秒数 / 所有核空闲累加秒数）
func parseUptime(r io.Reader) (float64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, errors.New("/proc/uptime: empty")
	}
	return strconv.ParseFloat(f[0], 64)
}

// /proc/net/dev，前两行是表头。
func parseNetDev(r io.Reader) (map[string]netRaw, error) {
	out := make(map[string]netRaw)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue // 表头
		}
		name := strings.TrimSpace(line[:i])
		f := strings.Fields(line[i+1:])
		if len(f) < 12 {
			continue
		}
		var v [12]uint64
		ok := true
		for j := range v {
			n, err := strconv.ParseUint(f[j], 10, 64)
			if err != nil {
				ok = false
				break
			}
			v[j] = n
		}
		if !ok {
			continue
		}
		out[name] = netRaw{
			RxBytes: v[0], RxPackets: v[1], RxErrors: v[2], RxDropped: v[3],
			TxBytes: v[8], TxPackets: v[9], TxErrors: v[10], TxDropped: v[11],
		}
	}
	return out, sc.Err()
}

// /proc/diskstats，只取前 14 列，兼容 linux 4.18+ 新增的 discard/flush 列。
// 列: major minor name reads readsMerged sectorsRead msRead
//
//	writes writesMerged sectorsWritten msWrite inflight ioMs weightedMs
func parseDiskStats(r io.Reader) (map[string]diskRaw, error) {
	out := make(map[string]diskRaw)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 14 {
			continue
		}
		v := func(i int) uint64 {
			n, _ := strconv.ParseUint(f[i], 10, 64)
			return n
		}
		out[f[2]] = diskRaw{
			Reads: v(3), ReadSectors: v(5), ReadTimeMS: v(6),
			Writes: v(7), WriteSectors: v(9), WriteTimeMS: v(10),
			IOTimeMS: v(12),
		}
	}
	return out, sc.Err()
}

// /proc/mounts，字段中的空格被转义为 \040、\011、\012、\134。
func parseMounts(r io.Reader) ([]mountRaw, error) {
	var out []mountRaw
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		out = append(out, mountRaw{
			Device:     unescapeMount(f[0]),
			MountPoint: unescapeMount(f[1]),
			FSType:     f[2],
		})
	}
	return out, sc.Err()
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

// /proc/net/tcp 与 tcp6，统计第 4 列状态。
// 状态: 01 ESTABLISHED 02 SYN_SENT 03 SYN_RECV 04 FIN_WAIT1 05 FIN_WAIT2
//
//	06 TIME_WAIT 07 CLOSE 08 CLOSE_WAIT 09 LAST_ACK 0A LISTEN 0B CLOSING
func parseTCP(r io.Reader, acc *TCPInfo) error {
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // 跳过表头
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		acc.Total++
		switch strings.ToUpper(f[3]) {
		case "01":
			acc.Established++
		case "02":
			acc.SynSent++
		case "03":
			acc.SynRecv++
		case "06":
			acc.TimeWait++
		case "08":
			acc.CloseWait++
		case "0A":
			acc.Listen++
		default:
			acc.Other++
		}
	}
	return sc.Err()
}

// /proc/cpuinfo：x86 取 model name，ARM 取 Hardware/Processor 兜底。
func parseCPUModel(r io.Reader) string {
	sc := bufio.NewScanner(r)
	model, hardware, processor := "", "", ""
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		switch key {
		case "model name":
			if model == "" {
				model = val
			}
		case "Hardware":
			if hardware == "" {
				hardware = val
			}
		case "Processor":
			if processor == "" {
				processor = val
			}
		}
	}
	for _, v := range []string{model, hardware, processor} {
		if v != "" {
			return v
		}
	}
	return "unknown"
}

func humanDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	d := int64(sec)
	days := d / 86400
	d %= 86400
	h := d / 3600
	d %= 3600
	m := d / 60
	s := d % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
