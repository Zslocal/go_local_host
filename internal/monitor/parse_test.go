package monitor

import (
	"strings"
	"testing"
	"time"
)

const statSample = `cpu  1000 100 500 8000 200 10 40 5 0 0
cpu0 500 50 250 4000 100 5 20 2 0 0
cpu1 500 50 250 4000 100 5 20 3 0 0
intr 123456
ctxt 99999
btime 1700000000
processes 424242
procs_running 3
procs_blocked 1
`

const memSample = `MemTotal:       16384000 kB
MemFree:         2048000 kB
MemAvailable:    8192000 kB
Buffers:          512000 kB
Cached:          4096000 kB
SReclaimable:     256000 kB
Shmem:            128000 kB
SwapTotal:       2097152 kB
SwapFree:        1048576 kB
`

const devSample = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0
  eth0: 2000 20 1 2 0 0 0 0 4000 40 3 4 0 0 0 0
veth0abc: 500 5 0 0 0 0 0 0 500 5 0 0 0 0 0 0
`

const diskSample = `   8       0 sda 100 10 2000 50 200 20 4000 80 0 300 400
 259       0 nvme0n1 10 0 100 5 20 0 200 10 0 15 25
   7       0 loop0 1 0 8 0 0 0 0 0 0 0 0
 259       1 nvme0n1p1 5 0 40 2 0 0 0 0 0 2 3 4 0 0 0 0
`

func TestParseCPUStat(t *testing.T) {
	cpu, created, btime, err := parseCPUStat(strings.NewReader(statSample))
	if err != nil {
		t.Fatal(err)
	}
	if cpu.All.total() != 9855 {
		t.Fatalf("total jiffies = %d, want 9855", cpu.All.total())
	}
	if len(cpu.Cores) != 2 {
		t.Fatalf("cores = %d, want 2", len(cpu.Cores))
	}
	if created != 424242 || btime != 1700000000 {
		t.Fatalf("created=%d btime=%d", created, btime)
	}
}

func TestBuildCPUDelta(t *testing.T) {
	a := cpuCounter{User: 1000, System: 500, Idle: 8000, IOWait: 200, IRQ: 10, SoftIRQ: 40, Steal: 5}
	// 一个 1000 jiffy 的窗口内新增：user 300 + system 100 + idle 500 + iowait 100 = 1000
	b := cpuCounter{User: 1300, System: 600, Idle: 8500, IOWait: 300, IRQ: 10, SoftIRQ: 40, Steal: 5}
	got := buildCPU(a, b)
	// 与 top / node_exporter 一致：100 - idle(50) = 50，iowait 计入非空闲。
	if got.Usage != 50 {
		t.Fatalf("usage = %v, want 50", got.Usage)
	}
	if got.User != 30 || got.System != 10 || got.IOWait != 10 {
		t.Fatalf("breakdown = %+v", got)
	}
	// 计数器回绕不应产生异常值
	if bad := buildCPU(b, a); bad.Usage != 0 {
		t.Fatalf("wraparound usage = %v, want 0", bad.Usage)
	}
}

func TestParseMemInfo(t *testing.T) {
	m, err := parseMemInfo(strings.NewReader(memSample))
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasAvailable || m.Available != 8192000 || m.SwapTotal != 2097152 {
		t.Fatalf("mem = %+v", m)
	}
	got := buildMemory(m)
	if got.Total != 16384000*1024 || got.Used != 8192000*1024 || got.Usage != 50 {
		t.Fatalf("memory = %+v", got)
	}
	swap := buildSwap(m)
	if swap.Usage != 50 {
		t.Fatalf("swap usage = %v, want 50", swap.Usage)
	}
}

func TestParseLoadAvgAndUptime(t *testing.T) {
	load, procs, err := parseLoadAvg(strings.NewReader("1.25 0.80 0.50 3/1024 999\n"))
	if err != nil {
		t.Fatal(err)
	}
	if load.Load1 != 1.25 || load.Load15 != 0.5 {
		t.Fatalf("load = %+v", load)
	}
	if procs.Running != 3 || procs.Total != 1024 {
		t.Fatalf("procs = %+v", procs)
	}
	up, err := parseUptime(strings.NewReader("93784.50 300000.00\n"))
	if err != nil || up != 93784.5 {
		t.Fatalf("uptime = %v, err = %v", up, err)
	}
	if humanDuration(93784.5) != "1d 2h 3m" {
		t.Fatalf("human = %q", humanDuration(93784.5))
	}
}

func TestParseNetDev(t *testing.T) {
	m, err := parseNetDev(strings.NewReader(devSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("interfaces = %d, want 3", len(m))
	}
	eth := m["eth0"]
	if eth.RxBytes != 2000 || eth.RxPackets != 20 || eth.RxErrors != 1 || eth.RxDropped != 2 ||
		eth.TxBytes != 4000 || eth.TxPackets != 40 || eth.TxErrors != 3 || eth.TxDropped != 4 {
		t.Fatalf("eth0 = %+v", eth)
	}
	// 2 秒窗口：rx 1000 B -> 500 B/s
	n := buildNet("eth0", netRaw{RxBytes: 1000, TxBytes: 2000}, eth, 2)
	if n.RxRate != 500 || n.TxRate != 1000 || n.RxDropped != 2 {
		t.Fatalf("net delta = %+v", n)
	}
}

func TestParseDiskStats(t *testing.T) {
	m, err := parseDiskStats(strings.NewReader(diskSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 4 {
		t.Fatalf("devices = %d, want 4 (含 5.x 内核新增列的分区)", len(m))
	}
	sda := m["sda"]
	if sda.Reads != 100 || sda.ReadSectors != 2000 || sda.Writes != 200 || sda.WriteSectors != 4000 || sda.IOTimeMS != 300 {
		t.Fatalf("sda = %+v", sda)
	}
	d := buildDiskIO("sda", diskRaw{Reads: 50, ReadSectors: 1000, Writes: 100, WriteSectors: 1000, IOTimeMS: 100},
		sda, 2)
	if d.ReadRate != 256000 || d.WriteRate != 768000 || d.IOPS != 75 || d.Util != 10 {
		t.Fatalf("disk delta = %+v", d)
	}
}

func TestParseMountsAndTCP(t *testing.T) {
	mounts, err := parseMounts(strings.NewReader(`/dev/sda1 / ext4 rw,relatime 0 0
tmpfs /dev/shm tmpfs rw 0 0
10.0.0.1:/data\040dir /mnt/my\040disk nfs4 rw 0 0
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 3 || mounts[2].MountPoint != "/mnt/my disk" || mounts[2].Device != "10.0.0.1:/data dir" {
		t.Fatalf("mounts = %+v", mounts)
	}
	var tcp TCPInfo
	tcpSample := `  sl  local_address rem_address   st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode
   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 1 1
   1: 0100007F:0050 0100007F:1234 01 00000000:00000000 00:00000000 00000000 0 0 2 1
   2: 0100007F:0050 0100007F:1235 06 00000000:00000000 00:00000000 00000000 0 0 3 1
`
	if err := parseTCP(strings.NewReader(tcpSample), &tcp); err != nil {
		t.Fatal(err)
	}
	if tcp.Total != 3 || tcp.Listen != 1 || tcp.Established != 1 || tcp.TimeWait != 1 {
		t.Fatalf("tcp = %+v", tcp)
	}
}

func TestBuildSnapshotWindow(t *testing.T) {
	t0 := time.Now()
	prev := &rawSnapshot{At: t0, CPU: cpuRaw{Cores: map[string]cpuCounter{}}, DiskIO: map[string]diskRaw{}, Network: map[string]netRaw{}}
	cur := &rawSnapshot{At: t0.Add(2 * time.Second), CPU: cpuRaw{Cores: map[string]cpuCounter{}}, DiskIO: map[string]diskRaw{}, Network: map[string]netRaw{}}
	if s := buildSnapshot(prev, cur); s.Interval != 2 {
		t.Fatalf("interval = %v, want 2", s.Interval)
	}
}
