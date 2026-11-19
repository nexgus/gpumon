package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"sync"

	"gpumon/internal/history"
	"gpumon/internal/protocol"
	"gpumon/internal/recording"
)

// recorder 管理一台主機自使用者按下連線至轉為未連線期間的記錄檔. 自動重新連線
// 沿用同一個 recorder, 使用者每次按下連線使用新的 recorder.
//
// 取樣先寫入 recording.Writer 的緩衝, 並於 pending 保留一份緊湊的副本, 供曲線
// 查詢合併尚未存檔的取樣; 每次 flush 後清空 pending. flushMu 使查詢期間不進行
// flush, 確保查詢所讀的檔案內容與 pending 不重疊也不缺漏.
//
// peaks 累計寫入中記錄檔自建立以來的全部取樣, 供曲線查詢附上整個記錄檔的最大值. 自動
// 重新連線而接續原記錄檔時沿用, 因此涵蓋重新連線之前的取樣; 建立新檔時重新累計.
type recorder struct {
	dataDir string
	log     *slog.Logger

	flushMu sync.RWMutex

	mu      sync.Mutex
	w       *recording.Writer
	pending []recording.Record
	peaks   *history.MaxTracker
}

func newRecorder(dataDir string, log *slog.Logger) *recorder {
	return &recorder{dataDir: dataDir, log: log}
}

// connStart 為一次連線第一筆取樣時的資訊.
type connStart struct {
	host       string
	devicesRaw json.RawMessage
	gpuIDs     []int
	// tBase 與 tsBase 為該次連線的時間基準.
	tBase  int64
	tsBase float64
	// reconnected 表示此次連線為自動重新連線, discTime 與 discReason 為判定斷線
	// 的時刻與原因.
	reconnected bool
	discTime    int64
	discReason  string
}

// begin 於一次連線收到第一筆取樣時呼叫, 決定接續原記錄檔或建立新檔. GPU 張數為
// 0 時不建立記錄檔.
func (r *recorder) begin(cs connStart) {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w != nil {
		last, hasLast := r.w.LastTime()
		if cs.reconnected && slices.Equal(r.w.GPUIDs(), cs.gpuIDs) && (!hasLast || cs.tBase >= last) {
			t := r.w.AddDisconnect(cs.discTime, cs.discReason)
			r.pending = append(r.pending, recording.Record{Kind: recording.KindDisconnect, Time: t, Text: cs.discReason})
			r.w.BeginConnection(cs.tBase, cs.tsBase)
			return
		}
		r.closeLocked()
	}
	if len(cs.gpuIDs) == 0 {
		return
	}
	w, err := recording.Create(r.dataDir, recording.Header{
		Host: cs.host, TWall0: cs.tBase, TS0: cs.tsBase, Devices: cs.devicesRaw,
	})
	if err != nil {
		if !errors.Is(err, recording.ErrNoGPU) {
			r.log.Error("cannot create recording file", "host", cs.host, "err", err)
		}
		return
	}
	r.w = w
	r.peaks = history.NewMaxTracker(w.GPUIDs())
}

// addSample 加入一筆取樣, 程序列表有變動時另加一筆程序變動記錄, 回傳記錄檔所用的時刻;
// 沒有記錄檔時 ok 為假. procs 為各 GPU 的程序原文.
func (r *recorder) addSample(ts float64, intervalMS int, gpus []protocol.GPUSample, procs []recording.GPUProcs) (t int64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil {
		return 0, false
	}
	rs := make([]recording.GPUSample, len(gpus))
	for i, g := range gpus {
		rs[i] = recording.GPUSample{
			ID: g.ID, Util: i64(g.Util), Fan: i64(g.Fan), Enc: i64(g.Enc), Dec: i64(g.Dec),
			Temp: i64(g.Temp), PowerW: i64(g.PowerW), ClockGPU: i64(g.ClockGPU),
			ClockMem: i64(g.ClockMem), MemUsed: g.MemUsed,
		}
	}
	t, err := r.w.AddSample(ts, intervalMS, rs, procs)
	if err != nil {
		r.log.Warn("cannot record processes", "err", err)
	}
	r.pending = append(r.pending, recording.Record{
		Kind: recording.KindSample, Time: t, IntervalMS: uint32(min(max(intervalMS, 0), math.MaxUint32)),
		GPUs: recording.SampleValues(r.w.GPUIDs(), rs),
	})
	r.peaks.Add(&r.pending[len(r.pending)-1])
	return t, true
}

// addError 加入一則遠端送出的 error 訊息.
func (r *recorder) addError(ts float64, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil {
		return
	}
	t := r.w.AddError(ts, msg)
	r.pending = append(r.pending, recording.Record{Kind: recording.KindError, Time: t, Text: msg})
}

// flush 把緩衝中的記錄附加到記錄檔. 失敗時保留緩衝與 pending, 待下次重試.
func (r *recorder) flush() {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil {
		return
	}
	if err := r.w.Flush(); err != nil {
		r.log.Error("cannot write recording file", "path", r.w.Path(), "err", err)
		return
	}
	r.pending = nil
}

// close 把緩衝寫入記錄檔後關閉; 之後不再修改該檔.
func (r *recorder) close() {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
}

func (r *recorder) closeLocked() {
	if r.w == nil {
		return
	}
	if err := r.w.Close(); err != nil {
		r.log.Error("cannot write recording file", "path", r.w.Path(), "err", err)
	}
	r.w = nil
	r.pending = nil
	r.peaks = nil
}

// path 回傳寫入中的記錄檔路徑; 沒有時為空字串.
func (r *recorder) path() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil {
		return ""
	}
	return r.w.Path()
}

// query 查詢寫入中的記錄檔並合併尚未存檔的取樣, 並附上整個記錄檔的最大值; 沒有寫入中
// 的記錄檔時回傳空的結果. 查詢期間暫停 flush. r 為 nil 時視為沒有記錄檔.
func (r *recorder) query(ctx context.Context, q history.Query) (*history.Result, error) {
	if r == nil {
		return history.QueryRange(ctx, nil, q)
	}
	r.flushMu.RLock()
	defer r.flushMu.RUnlock()
	r.mu.Lock()
	var paths []string
	var pending []history.Pending
	var peaks []history.GPUFileMax
	if r.w != nil {
		cur := r.w.Path()
		paths = []string{cur}
		pending = []history.Pending{{Path: cur, Records: slices.Clone(r.pending)}}
		peaks = r.peaks.Result()
	}
	r.mu.Unlock()
	res, err := history.QueryRangePending(ctx, paths, pending, q)
	if err != nil {
		return nil, err
	}
	res.FileMax = peaks
	return res, nil
}

func i64(v *int) *int64 {
	if v == nil {
		return nil
	}
	x := int64(*v)
	return &x
}
