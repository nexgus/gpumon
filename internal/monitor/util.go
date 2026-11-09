package monitor

import (
	"math"

	"gpumon/internal/protocol"
)

// procKey 以 GPU 的 id 與 PID 識別一個程序.
type procKey struct {
	gpu, pid int
}

// counters 為一個程序上一筆取樣的各引擎累計值.
type counters struct {
	engines     map[string]uint64
	capacity    map[string]uint64
	cycles      map[string]uint64
	totalCycles map[string]uint64
}

// utilTracker 以 AMD 與 Intel 的各程序累計值換算使用率 (5.4). 每次連線使用新的
// utilTracker, 不跨連線保留上一筆.
//
// 算法如下. 對每個程序的每個引擎 e, 與上一筆取樣相減:
//
//   - 有 cycles 與 total_cycles (xe 驅動) 時, 使用率為 Δcycles / (Δtotal_cycles * capacity).
//   - 否則以 engines 的忙碌時間計, 使用率為 Δbusy / (Δts * capacity), Δts 為兩筆
//     sample 訊息 ts 的差值, 換算為奈秒.
//
// capacity 為該類引擎的數量, 未列出時為 1. 程序的使用率為其各引擎使用率的最大值.
// 整張 GPU 某引擎的使用率為所有程序於該引擎的使用率總和, 整張 GPU 的使用率為各
// 引擎中的最大值, 即最忙碌的引擎. 結果皆四捨五入並限制於 0 至 100. 上一筆沒有的
// 程序, 以及累計值減少 (計數器重設) 的引擎, 不計入.
type utilTracker struct {
	hasPrev bool
	prevTS  float64
	prev    map[procKey]counters
}

func newUtilTracker() *utilTracker {
	return &utilTracker{prev: map[procKey]counters{}}
}

// utilResult 為一筆取樣換算出的使用率.
type utilResult struct {
	// gpu 為整張 GPU 的使用率, 以 GPU 的 id 為鍵; 含所有帶程序列表的 GPU, 沒有
	// 帶累計值的程序時為 0. 呼叫端只對 sample 訊息不含 util 的 GPU 採用.
	gpu map[int]int
	// proc 為各程序的使用率.
	proc map[procKey]int
}

// update 以一筆 sample 訊息更新上一筆, 回傳換算出的使用率. 第一筆取樣沒有可相減
// 的上一筆, 回傳空結果.
func (u *utilTracker) update(ts float64, gpus []protocol.GPUSample) utilResult {
	res := utilResult{gpu: map[int]int{}, proc: map[procKey]int{}}
	next := make(map[procKey]counters)
	dtNS := (ts - u.prevTS) * 1e9
	for _, g := range gpus {
		if g.Procs == nil {
			continue
		}
		engineSum := map[string]float64{}
		for _, p := range g.Procs {
			if len(p.Engines) == 0 && len(p.Cycles) == 0 {
				continue
			}
			k := procKey{g.ID, p.PID}
			cur := counters{engines: p.Engines, capacity: p.EngineCapacity, cycles: p.Cycles, totalCycles: p.TotalCycles}
			next[k] = cur
			prev, ok := u.prev[k]
			if !u.hasPrev || !ok {
				continue
			}
			procMax, procOK := 0.0, false
			for e, f := range engineFractions(prev, cur, dtNS) {
				engineSum[e] += f
				procMax, procOK = math.Max(procMax, f), true
			}
			if procOK {
				res.proc[k] = percent(procMax)
			}
		}
		if !u.hasPrev {
			continue
		}
		busiest := 0.0
		for _, f := range engineSum {
			busiest = math.Max(busiest, f)
		}
		res.gpu[g.ID] = percent(busiest)
	}
	u.prev = next
	u.prevTS = ts
	u.hasPrev = true
	return res
}

// engineFractions 回傳一個程序各引擎的使用率, 以 0 至 1 的比例表示.
func engineFractions(prev, cur counters, dtNS float64) map[string]float64 {
	out := map[string]float64{}
	names := map[string]bool{}
	for e := range cur.engines {
		names[e] = true
	}
	for e := range cur.cycles {
		names[e] = true
	}
	for e := range names {
		capacity := float64(1)
		if c, ok := cur.capacity[e]; ok && c > 0 {
			capacity = float64(c)
		}
		c1, okC1 := cur.cycles[e]
		t1, okT1 := cur.totalCycles[e]
		c0, okC0 := prev.cycles[e]
		t0, okT0 := prev.totalCycles[e]
		if okC1 && okT1 && okC0 && okT0 {
			if c1 < c0 || t1 <= t0 {
				continue
			}
			out[e] = float64(c1-c0) / (float64(t1-t0) * capacity)
			continue
		}
		b1, ok1 := cur.engines[e]
		b0, ok0 := prev.engines[e]
		if !ok1 || !ok0 || b1 < b0 || dtNS <= 0 {
			continue
		}
		out[e] = float64(b1-b0) / (dtNS * capacity)
	}
	return out
}

// percent 把 0 至 1 的比例換算為四捨五入的百分比, 限制於 0 至 100.
func percent(f float64) int {
	v := math.Round(f * 100)
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(v)
}
