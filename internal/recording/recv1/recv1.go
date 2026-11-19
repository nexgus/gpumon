// Package recv1 依 docs/recording-format-v1.md 寫入版本 1 的記錄檔, 只供測試建立版本 1 的檔案,
// 以驗證程式仍能讀取既有的版本 1 記錄檔. 正式程式不得使用本套件.
package recv1

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"gpumon/internal/recording"
)

const (
	recordHeaderSize  = 11
	gpuBlockSize      = 20
	maxMessageBytes   = 1024
	maxProcessesBytes = 65535

	kindSample     = 1
	kindError      = 2
	kindContinue   = 3
	kindProcesses  = 4
	kindDisconnect = 5

	noValue8   uint8  = 255
	noValueI16 int16  = 32767
	noValue16  uint16 = 65535
	noValue64  uint64 = math.MaxUint64
)

var le = binary.LittleEndian

// Writer 建立版本 1 的記錄檔並附加記錄, 行為與改版前的寫入程式相同.
type Writer struct {
	mu       sync.Mutex
	f        *os.File
	path     string
	n        int
	r        int
	ids      []int
	tBase    int64
	tsBase   float64
	lastTime int64
	hasLast  bool
	buf      []byte
}

// Create 於 `<dataDir>/history/<主機目錄>/` 建立版本 1 的記錄檔.
func Create(dataDir string, h recording.Header) (*Writer, error) {
	var d struct {
		GPUs []struct {
			ID int `json:"id"`
		} `json:"gpus"`
	}
	if err := json.Unmarshal(h.Devices, &d); err != nil {
		return nil, err
	}
	if len(d.GPUs) == 0 {
		return nil, recording.ErrNoGPU
	}
	ids := make([]int, len(d.GPUs))
	for i, g := range d.GPUs {
		ids[i] = g.ID
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"host": h.Host, "t_wall_0": h.TWall0, "ts_0": h.TS0, "devices": h.Devices}); err != nil {
		return nil, err
	}
	meta := bytes.TrimRight(b.Bytes(), "\n")
	dir := recording.HostDir(dataDir, h.Host)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stem := time.UnixMicro(h.TWall0).In(time.Local).Format("20060102-150405")
	var f *os.File
	var path string
	var err error
	for i := 1; ; i++ {
		name := stem + ".gpurec"
		if i > 1 {
			name = fmt.Sprintf("%s-%d.gpurec", stem, i)
		}
		path = filepath.Join(dir, name)
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	fixed := make([]byte, 16)
	copy(fixed, "GPUMONRC")
	le.PutUint16(fixed[8:], 1)
	le.PutUint16(fixed[10:], uint16(len(ids)))
	le.PutUint32(fixed[12:], uint32(len(meta)))
	if _, err := f.Write(append(fixed, meta...)); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, path: path, n: len(ids), r: recordHeaderSize + gpuBlockSize*len(ids), ids: ids, tBase: h.TWall0, tsBase: h.TS0}, nil
}

// Path 回傳記錄檔的路徑.
func (w *Writer) Path() string { return w.path }

// BeginConnection 設定新一次連線的時間基準.
func (w *Writer) BeginConnection(tBase int64, tsBase float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tBase, w.tsBase = tBase, tsBase
}

func (w *Writer) clampTime(t int64) int64 {
	if w.hasLast && t < w.lastTime {
		t = w.lastTime
	}
	w.lastTime, w.hasLast = t, true
	return t
}

func (w *Writer) timeOf(ts float64) int64 {
	return w.clampTime(w.tBase + int64(math.Round((ts-w.tsBase)*1000000)))
}

func (w *Writer) newRecord(kind byte, t int64, v uint16) []byte {
	start := len(w.buf)
	w.buf = append(w.buf, make([]byte, w.r)...)
	rec := w.buf[start:]
	rec[0] = kind
	le.PutUint64(rec[1:], uint64(t))
	le.PutUint16(rec[9:], v)
	return rec
}

func clamp(v *int64, lo, hi, none int64) int64 {
	if v == nil {
		return none
	}
	return min(max(*v, lo), hi)
}

// AddSample 加入一筆取樣記錄, 回傳其時刻.
func (w *Writer) AddSample(ts float64, intervalMS int, gpus []recording.GPUSample) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.timeOf(ts)
	rec := w.newRecord(kindSample, t, uint16(min(max(intervalMS, 0), math.MaxUint16)))
	byID := map[int]*recording.GPUSample{}
	for i := range gpus {
		if _, dup := byID[gpus[i].ID]; !dup {
			byID[gpus[i].ID] = &gpus[i]
		}
	}
	for i, id := range w.ids {
		blk := rec[recordHeaderSize+gpuBlockSize*i:][:gpuBlockSize]
		g := byID[id]
		if g == nil {
			g = &recording.GPUSample{}
		}
		for k, v := range []*int64{g.Util, g.Fan, g.Enc, g.Dec} {
			blk[k] = uint8(clamp(v, 0, int64(noValue8)-1, int64(noValue8)))
		}
		le.PutUint16(blk[4:], uint16(int16(clamp(g.Temp, math.MinInt16, int64(noValueI16)-1, int64(noValueI16)))))
		for k, v := range []*int64{g.PowerW, g.ClockGPU, g.ClockMem} {
			le.PutUint16(blk[6+2*k:], uint16(clamp(v, 0, int64(noValue16)-1, int64(noValue16))))
		}
		mem := noValue64
		if g.MemUsed != nil {
			mem = min(*g.MemUsed, noValue64-1)
		}
		le.PutUint64(blk[12:], mem)
	}
	return t
}

func truncateText(s string, limit int) []byte {
	if len(s) <= limit {
		return []byte(s)
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return []byte(s[:cut])
}

func (w *Writer) addChunked(kind byte, t int64, payload []byte) {
	size := gpuBlockSize * w.n
	first := true
	for rest := payload; first || len(rest) > 0; first = false {
		take := min(size, len(rest))
		var rec []byte
		if first {
			rec = w.newRecord(kind, t, uint16(len(payload)))
		} else {
			rec = w.newRecord(kindContinue, t, 0)
		}
		copy(rec[recordHeaderSize:], rest[:take])
		rest = rest[take:]
	}
}

// AddError 加入一筆錯誤記錄與其接續記錄, 文字超過 1024 位元組時截短.
func (w *Writer) AddError(ts float64, msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addChunked(kindError, w.timeOf(ts), truncateText(msg, maxMessageBytes))
}

// AddDisconnect 加入一筆斷線記錄.
func (w *Writer) AddDisconnect(t int64, reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addChunked(kindDisconnect, w.clampTime(t), truncateText(reason, maxMessageBytes))
}

// AddProcesses 加入一筆程序記錄, 各 GPU 的程序合併為一個 JSON 陣列, 每個元素另加成員 gpu.
func (w *Writer) AddProcesses(ts float64, procs []recording.GPUProcs) error {
	var elems [][]byte
	for _, gp := range procs {
		for _, raw := range gp.Procs {
			var b bytes.Buffer
			if err := json.Compact(&b, raw); err != nil {
				return err
			}
			e := b.Bytes()
			if len(e) < 2 || e[0] != '{' {
				return errors.New("recv1: process element is not a JSON object")
			}
			e = e[:len(e)-1]
			if len(e) > 1 {
				e = append(e, ',')
			}
			elems = append(elems, fmt.Appendf(e, `"gpu":%d}`, gp.GPU))
		}
	}
	payload := []byte{'['}
	for i, e := range elems {
		if len(payload)+len(e)+2 > maxProcessesBytes {
			break
		}
		if i > 0 {
			payload = append(payload, ',')
		}
		payload = append(payload, e...)
	}
	payload = append(payload, ']')
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addChunked(kindProcesses, w.timeOf(ts), payload)
	return nil
}

// Flush 把累積的記錄附加到檔尾.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	if _, err := w.f.Write(w.buf); err != nil {
		return err
	}
	w.buf = w.buf[:0]
	return nil
}

// Close 先 Flush 再關閉檔案.
func (w *Writer) Close() error {
	return errors.Join(w.Flush(), w.f.Close())
}
