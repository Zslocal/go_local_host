package alert

// 阈值告警：带状态跟踪与滞回，只在「触发 / 恢复 / 按间隔重复」时产出事件，
// 避免持续模式下每个采样周期都刷同一条告警。

import (
	"fmt"
	"sync"
	"time"

	"github.com/Zslocal/go_local_host/internal/monitor"
)

type Config struct {
	CPU        float64       // CPU 使用率 %，0 表示禁用
	Memory     float64       // 内存使用率 %
	Disk       float64       // 任一挂载点使用率 %
	DiskUtil   float64       // 块设备 busy %
	LoadFactor float64       // load1 / 核数 的倍数，如 2.0
	NetDrop    bool          // 是否对新增网卡丢包/错包告警
	Hysteresis float64       // 恢复所需的回落幅度（百分点），默认 3
	Repeat     time.Duration // 持续告警的重复间隔，默认 5min；0 表示不重复
}

func DefaultConfig() Config {
	return Config{
		CPU: 85, Memory: 90, Disk: 90, DiskUtil: 95, LoadFactor: 0,
		Hysteresis: 3, Repeat: 5 * time.Minute,
	}
}

type Event struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"` // WARN | OK
	Key       string    `json:"key"`
	Message   string    `json:"message"`
	Value     float64   `json:"value"`
	Threshold float64   `json:"threshold"`
}

func (e Event) String() string {
	tag := "[WARN]"
	if e.Level == "OK" {
		tag = "[ OK ]"
	}
	return fmt.Sprintf("%s %s  %s", tag, e.Time.Format("15:04:05"), e.Message)
}

type check struct {
	key       string
	label     string
	unit      string
	value     float64
	threshold float64
}

type state struct {
	active   bool
	lastSent time.Time
}

type Evaluator struct {
	mu     sync.Mutex
	cfg    Config
	states map[string]*state
}

func NewEvaluator(cfg Config) *Evaluator {
	if cfg.Hysteresis <= 0 {
		cfg.Hysteresis = 3
	}
	if cfg.Repeat <= 0 {
		cfg.Repeat = 5 * time.Minute
	}
	return &Evaluator{cfg: cfg, states: make(map[string]*state)}
}

func (e *Evaluator) Evaluate(s *monitor.Snapshot) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	var checks []check
	add := func(key, label, unit string, value, threshold float64) {
		if threshold > 0 {
			checks = append(checks, check{key, label, unit, value, threshold})
		}
	}

	add("cpu", "CPU usage", "%", s.CPU.Usage, e.cfg.CPU)
	add("memory", "memory usage", "%", s.Memory.Usage, e.cfg.Memory)
	add("load", "load1 per core", "x", s.Load.PerCore1, e.cfg.LoadFactor)
	for _, m := range s.Mounts {
		add("mount:"+m.MountPoint, "mount "+m.MountPoint+" usage", "%", m.Usage, e.cfg.Disk)
		add("inode:"+m.MountPoint, "mount "+m.MountPoint+" inode usage", "%", m.InodesUsage, e.cfg.Disk)
	}
	for _, d := range s.DiskIO {
		add("diskutil:"+d.Device, "disk "+d.Device+" busy", "%", d.Util, e.cfg.DiskUtil)
	}
	if e.cfg.NetDrop {
		for _, n := range s.Network {
			add("netdrop:"+n.Interface, "iface "+n.Interface+" rx err+drop/s", "/s",
				n.RxErrRate+n.RxDropRate, 0.5)
		}
	}

	var events []Event
	current := make(map[string]bool, len(checks))
	for _, c := range checks {
		st := e.states[c.key]
		if st == nil {
			st = &state{}
			e.states[c.key] = st
		}
		triggered := c.value >= c.threshold
		// 记录本周期出现过的检查项；只有彻底消失的指标才清理状态，
		// 否则滞回区间内的项会被误清除并在下一周期重复告警。
		current[c.key] = true
		// 滞回：已告警时，必须回落到 threshold-hysteresis 以下才算恢复。
		recovered := st.active && c.value < c.threshold-e.cfg.Hysteresis

		switch {
		case triggered && !st.active:
			st.active, st.lastSent = true, s.Timestamp
			events = append(events, Event{s.Timestamp, "WARN", c.key,
				fmt.Sprintf("%s %.2f%s >= threshold %.2f%s", c.label, c.value, c.unit, c.threshold, c.unit),
				c.value, c.threshold})
		case triggered && s.Timestamp.Sub(st.lastSent) >= e.cfg.Repeat:
			st.lastSent = s.Timestamp
			events = append(events, Event{s.Timestamp, "WARN", c.key,
				fmt.Sprintf("%s still high: %.2f%s >= %.2f%s", c.label, c.value, c.unit, c.threshold, c.unit),
				c.value, c.threshold})
		case recovered:
			st.active = false
			events = append(events, Event{s.Timestamp, "OK", c.key,
				fmt.Sprintf("%s recovered: %.2f%s (threshold %.2f%s)", c.label, c.value, c.unit, c.threshold, c.unit),
				c.value, c.threshold})
		}
	}

	// 指标消失（例如挂载点被卸载）时清理状态，避免残留。
	for key, st := range e.states {
		if st.active && !current[key] {
			st.active = false
		}
	}
	return events
}

// ActiveCount 返回当前处于告警状态的检查项数量。
func (e *Evaluator) ActiveCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, st := range e.states {
		if st.active {
			n++
		}
	}
	return n
}
