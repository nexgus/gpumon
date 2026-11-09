package monitor

import (
	"testing"

	"gpumon/internal/protocol"
)

func proc(pid int, engines map[string]uint64) protocol.Process {
	return protocol.Process{PID: pid, Engines: engines}
}

func TestUtilTrackerEngines(t *testing.T) {
	u := newUtilTracker()
	first := u.update(100, []protocol.GPUSample{{ID: 0, Procs: []protocol.Process{
		proc(1, map[string]uint64{"render": 0, "video": 0}),
		proc(2, map[string]uint64{"render": 1000}),
	}}})
	if len(first.gpu) != 0 || len(first.proc) != 0 {
		t.Fatalf("first sample %+v", first)
	}
	// 0.5 秒內程序 1 的 render 忙碌 0.15 秒, video 0.1 秒; 程序 2 的 render 忙碌 0.2 秒.
	r := u.update(100.5, []protocol.GPUSample{{ID: 0, Procs: []protocol.Process{
		proc(1, map[string]uint64{"render": 150_000_000, "video": 100_000_000}),
		proc(2, map[string]uint64{"render": 200_001_000}),
		proc(3, map[string]uint64{"render": 999_000_000}),
	}}})
	if r.gpu[0] != 70 {
		t.Fatalf("GPU utilization %d, want 70 (busiest engine)", r.gpu[0])
	}
	if r.proc[procKey{0, 1}] != 30 || r.proc[procKey{0, 2}] != 40 {
		t.Fatalf("process utilization %+v", r.proc)
	}
	if _, ok := r.proc[procKey{0, 3}]; ok {
		t.Fatal("a new process has no previous sample")
	}
	// 累計值減少 (計數器重設) 的引擎不計入, 上限為 100.
	r = u.update(101.5, []protocol.GPUSample{{ID: 0, Procs: []protocol.Process{
		proc(1, map[string]uint64{"render": 0}),
		proc(3, map[string]uint64{"render": 3_000_000_000}),
	}}})
	if r.gpu[0] != 100 || r.proc[procKey{0, 3}] != 100 {
		t.Fatalf("clamped %+v", r)
	}
	if _, ok := r.proc[procKey{0, 1}]; ok {
		t.Fatal("reset counter counted")
	}
}

func TestUtilTrackerCapacityAndCycles(t *testing.T) {
	u := newUtilTracker()
	cap2 := map[string]uint64{"compute": 2}
	u.update(0, []protocol.GPUSample{
		{ID: 0, Procs: []protocol.Process{{PID: 1, Engines: map[string]uint64{"compute": 0}, EngineCapacity: cap2}}},
		{ID: 1, Procs: []protocol.Process{{PID: 1, Cycles: map[string]uint64{"rcs": 100}, TotalCycles: map[string]uint64{"rcs": 1000}}}},
		{ID: 2, Procs: []protocol.Process{}},
	})
	r := u.update(1, []protocol.GPUSample{
		{ID: 0, Procs: []protocol.Process{{PID: 1, Engines: map[string]uint64{"compute": 1_000_000_000}, EngineCapacity: cap2}}},
		{ID: 1, Procs: []protocol.Process{{PID: 1, Cycles: map[string]uint64{"rcs": 400}, TotalCycles: map[string]uint64{"rcs": 2000}}}},
		{ID: 2, Procs: []protocol.Process{}},
		{ID: 3},
	})
	if r.gpu[0] != 50 {
		t.Fatalf("capacity 2: %d, want 50", r.gpu[0])
	}
	if r.gpu[1] != 30 {
		t.Fatalf("xe cycles: %d, want 30", r.gpu[1])
	}
	if v, ok := r.gpu[2]; !ok || v != 0 {
		t.Fatalf("GPU without own processes: %d %v, want 0", v, ok)
	}
	if _, ok := r.gpu[3]; ok {
		t.Fatal("GPU without a process list must stay unknown")
	}
}
