package history

import "math"

// Counters 為一個程序某一筆取樣的各引擎累計值, 以引擎名稱為鍵. 即 sample 訊息中
// 程序元素的 engines, engine_capacity, cycles 與 total_cycles.
type Counters struct {
	Engines     map[string]uint64
	Capacity    map[string]uint64
	Cycles      map[string]uint64
	TotalCycles map[string]uint64
}

// EngineFractions 以兩筆累計值回傳一個程序各引擎的使用率, 以 0 至 1 的比例表示.
// dtNS 為兩筆取樣相隔的奈秒數, 只在以 engines 忙碌時間計算時使用. 這是監看畫面與
// 歷史畫面共用的算法 (5.4), 規則如下.
//
//   - 有 cycles 與 total_cycles (xe 驅動) 時, 使用率為 Δcycles / (Δtotal_cycles * capacity).
//   - 否則以 engines 的忙碌時間計, 使用率為 Δbusy / (Δts * capacity).
//   - capacity 為該類引擎的數量, 未列出時為 1.
//   - 累計值減少 (計數器重設) 或兩筆相隔不為正的引擎不計入.
func EngineFractions(prev, cur Counters, dtNS float64) map[string]float64 {
	out := map[string]float64{}
	names := map[string]bool{}
	for e := range cur.Engines {
		names[e] = true
	}
	for e := range cur.Cycles {
		names[e] = true
	}
	for e := range names {
		capacity := float64(1)
		if c, ok := cur.Capacity[e]; ok && c > 0 {
			capacity = float64(c)
		}
		c1, okC1 := cur.Cycles[e]
		t1, okT1 := cur.TotalCycles[e]
		c0, okC0 := prev.Cycles[e]
		t0, okT0 := prev.TotalCycles[e]
		if okC1 && okT1 && okC0 && okT0 {
			if c1 < c0 || t1 <= t0 {
				continue
			}
			out[e] = float64(c1-c0) / (float64(t1-t0) * capacity)
			continue
		}
		b1, ok1 := cur.Engines[e]
		b0, ok0 := prev.Engines[e]
		if !ok1 || !ok0 || b1 < b0 || dtNS <= 0 {
			continue
		}
		out[e] = float64(b1-b0) / (dtNS * capacity)
	}
	return out
}

// ProcUtil 回傳一個程序的使用率百分比, 為其各引擎使用率的最大值. 沒有任何引擎
// 可算出使用率時 ok 為假.
func ProcUtil(prev, cur Counters, dtNS float64) (v int, ok bool) {
	best := 0.0
	for _, f := range EngineFractions(prev, cur, dtNS) {
		best, ok = math.Max(best, f), true
	}
	return Percent(best), ok
}

// Percent 把 0 至 1 的比例換算為四捨五入的百分比, 限制於 0 至 100.
func Percent(f float64) int {
	v := math.Round(f * 100)
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(v)
}
