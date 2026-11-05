package alert

import (
	"testing"
	"time"

	"github.com/Zslocal/go_local_host/internal/monitor"
)

func snap(t time.Time, cpu, mem float64, mounts ...float64) *monitor.Snapshot {
	s := &monitor.Snapshot{Timestamp: t, CPU: monitor.CPUInfo{Usage: cpu}, Memory: monitor.MemoryInfo{Usage: mem}}
	for _, p := range mounts {
		s.Mounts = append(s.Mounts, monitor.MountInfo{MountPoint: "/data", Usage: p, InodesUsage: 1})
	}
	return s
}

func TestTriggerHysteresisRecoverAndRepeat(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CPU, cfg.Memory, cfg.Disk, cfg.DiskUtil, cfg.LoadFactor = 80, 0, 0, 0, 0
	cfg.Hysteresis, cfg.Repeat = 5, time.Minute
	e := NewEvaluator(cfg)
	t0 := time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

	steps := []struct {
		at   time.Duration
		cpu  float64
		want int // 事件数
		kind string
	}{
		{0, 90, 1, "WARN"},                 // 首次触发
		{10 * time.Second, 88, 0, ""},      // 仍超阈值但没到重复间隔
		{70 * time.Second, 88, 1, "WARN"},  // 到重复间隔
		{80 * time.Second, 78, 0, ""},      // 落在滞回区间(75~80)，不恢复
		{90 * time.Second, 70, 1, "OK"},    // 低于 75，恢复
		{100 * time.Second, 70, 0, ""},     // 保持正常，不再打扰
		{110 * time.Second, 95, 1, "WARN"}, // 再次触发
	}
	for i, st := range steps {
		events := e.Evaluate(snap(t0.Add(st.at), st.cpu, 10))
		if len(events) != st.want {
			t.Fatalf("step %d: got %d events %+v, want %d", i, len(events), events, st.want)
		}
		if st.want == 1 && events[0].Level != st.kind {
			t.Fatalf("step %d: level = %s, want %s", i, events[0].Level, st.kind)
		}
	}
	if got := e.ActiveCount(); got != 1 {
		t.Fatalf("active = %d, want 1", got)
	}
}

func TestMountThresholdAndDisappearingMetric(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CPU, cfg.Memory, cfg.DiskUtil, cfg.LoadFactor = 0, 0, 0, 0
	cfg.Disk, cfg.Hysteresis = 85, 3
	e := NewEvaluator(cfg)
	t0 := time.Now()

	if evs := e.Evaluate(snap(t0, 10, 10, 92)); len(evs) != 1 || evs[0].Key != "mount:/data" {
		t.Fatalf("mount alert = %+v", evs)
	}
	// 挂载点被卸载后，状态必须清理，不能永远挂着 active。
	if evs := e.Evaluate(snap(t0.Add(time.Second), 10, 10)); len(evs) != 0 {
		t.Fatalf("unexpected events after unmount: %+v", evs)
	}
	if got := e.ActiveCount(); got != 0 {
		t.Fatalf("active = %d after unmount, want 0", got)
	}
}

func TestZeroThresholdDisablesCheck(t *testing.T) {
	e := NewEvaluator(Config{Hysteresis: 3, Repeat: time.Hour})
	if evs := e.Evaluate(snap(time.Now(), 100, 100, 100)); len(evs) != 0 {
		t.Fatalf("all thresholds disabled but got %+v", evs)
	}
}
