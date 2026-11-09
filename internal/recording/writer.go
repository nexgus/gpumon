package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// HostDirName 將 user@addr 轉為主機目錄名稱, 不屬於英文字母, 數字, `.`, `_`, `@`, `-` 的字元逐一換成 `_`.
func HostDirName(host string) string {
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '@', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// HostDir 回傳某主機的記錄檔目錄 `<dataDir>/history/<主機目錄>`.
func HostDir(dataDir, host string) string {
	return filepath.Join(dataDir, "history", HostDirName(host))
}

// Writer 負責建立記錄檔並於檔尾附加記錄. Add 系列函數只寫入記憶體緩衝,
// Flush 將累積的記錄以一次寫入附加到檔尾, 呼叫端應每 5 秒呼叫一次.
// Writer 可由多個 goroutine 同時使用.
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
	size     int64
	buf      []byte
	closed   bool
}

// Create 於 `<dataDir>/history/<主機目錄>/` 建立記錄檔並寫入固定區與說明區.
// 呼叫端應於收到第一筆 sample 訊息時才呼叫, 且 GPU 張數為 0 時回傳 ErrNoGPU, 不建立檔案.
// 檔名由 TWall0 換算為本機時區, 同名時依序加 `-2`, `-3`.
func Create(dataDir string, h Header) (*Writer, error) {
	ids, err := parseGPUIDs(h.Devices, true)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrNoGPU
	}
	if len(ids) > math.MaxUint16 {
		return nil, fmt.Errorf("recording: too many GPUs: %d", len(ids))
	}
	dirName := HostDirName(h.Host)
	if strings.Trim(dirName, ".") == "" {
		return nil, fmt.Errorf("recording: invalid host %q", h.Host)
	}
	meta, err := encodeHeader(h)
	if err != nil {
		return nil, err
	}
	dir := HostDir(dataDir, h.Host)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stem := time.UnixMicro(h.TWall0).In(time.Local).Format("20060102-150405")
	var f *os.File
	var path string
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
	fixed := make([]byte, fixedSize, fixedSize+len(meta))
	copy(fixed, magic)
	le.PutUint16(fixed[8:], formatVersion)
	le.PutUint16(fixed[10:], uint16(len(ids)))
	le.PutUint32(fixed[12:], uint32(len(meta)))
	out := append(fixed, meta...)
	if _, err := f.Write(out); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return &Writer{
		f:      f,
		path:   path,
		n:      len(ids),
		r:      recordHeaderSize + gpuBlockSize*len(ids),
		ids:    ids,
		tBase:  h.TWall0,
		tsBase: h.TS0,
		size:   int64(len(out)),
	}, nil
}

func encodeHeader(h Header) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(headerJSON{Host: h.Host, TWall0: h.TWall0, TS0: h.TS0, Devices: h.Devices}); err != nil {
		return nil, fmt.Errorf("recording: encode header: %w", err)
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Path 回傳記錄檔的路徑.
func (w *Writer) Path() string { return w.path }

// NumGPUs 回傳 GPU 張數 N.
func (w *Writer) NumGPUs() int { return w.n }

// GPUIDs 回傳說明區 devices.gpus 各元素的 id, 順序與取樣記錄的 GPU 區塊相同.
func (w *Writer) GPUIDs() []int { return append([]int(nil), w.ids...) }

// BeginConnection 設定新一次連線的時間基準, 即本機收到該次連線第一筆 sample 訊息的時刻與該筆的 ts.
// 第一次連線的基準於 Create 時即取自說明區. 基準不存入檔案.
func (w *Writer) BeginConnection(tBase int64, tsBase float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tBase, w.tsBase = tBase, tsBase
}

// Time 依目前連線的時間基準將 ts 換算為時刻.
func (w *Writer) Time(ts float64) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timeLocked(ts)
}

func (w *Writer) timeLocked(ts float64) int64 {
	return w.tBase + int64(math.Round((ts-w.tsBase)*1000000))
}

// LastTime 回傳最後一筆記錄 (含尚未 Flush 者) 的時刻; 尚無記錄時 ok 為假.
func (w *Writer) LastTime() (t int64, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastTime, w.hasLast
}

// Pending 回傳尚未 Flush 的位元組數.
func (w *Writer) Pending() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.buf)
}

// clampTime 使時刻不早於前一筆記錄, 以維持記錄區各筆時刻不遞減, 並更新最後時刻.
func (w *Writer) clampTime(t int64) int64 {
	if w.hasLast && t < w.lastTime {
		t = w.lastTime
	}
	w.lastTime, w.hasLast = t, true
	return t
}

func (w *Writer) newRecord(kind Kind, t int64, v uint16) []byte {
	start := len(w.buf)
	w.buf = append(w.buf, make([]byte, w.r)...)
	rec := w.buf[start:]
	rec[0] = byte(kind)
	le.PutUint64(rec[1:], uint64(t))
	le.PutUint16(rec[9:], v)
	return rec
}

// AddSample 加入一筆取樣記錄, 回傳其時刻. gpus 以 id 與說明區的 GPU 配對,
// 缺少的 GPU 與省略的欄位存入代表無值的數值, 超出範圍的數值存入最接近的有效值.
func (w *Writer) AddSample(ts float64, intervalMS int, gpus []GPUSample) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.clampTime(w.timeLocked(ts))
	rec := w.newRecord(KindSample, t, clampU16(intervalMS))
	byID := make(map[int]*GPUSample, len(gpus))
	for i := range gpus {
		if _, dup := byID[gpus[i].ID]; !dup {
			byID[gpus[i].ID] = &gpus[i]
		}
	}
	for i, id := range w.ids {
		blk := rec[recordHeaderSize+gpuBlockSize*i:][:gpuBlockSize]
		g := byID[id]
		if g == nil {
			g = &GPUSample{}
		}
		blk[0] = clampU8(g.Util)
		blk[1] = clampU8(g.Fan)
		blk[2] = clampU8(g.Enc)
		blk[3] = clampU8(g.Dec)
		le.PutUint16(blk[4:], uint16(clampI16(g.Temp)))
		le.PutUint16(blk[6:], clampU16p(g.PowerW))
		le.PutUint16(blk[8:], clampU16p(g.ClockGPU))
		le.PutUint16(blk[10:], clampU16p(g.ClockMem))
		le.PutUint64(blk[12:], clampU64(g.MemUsed))
	}
	return t
}

func clampU16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(v)
}

func clampU8(v *int64) uint8 {
	switch {
	case v == nil:
		return NoValue8
	case *v < 0:
		return 0
	case *v > int64(NoValue8)-1:
		return NoValue8 - 1
	}
	return uint8(*v)
}

func clampI16(v *int64) int16 {
	switch {
	case v == nil:
		return NoValueI16
	case *v < math.MinInt16:
		return math.MinInt16
	case *v > int64(NoValueI16)-1:
		return NoValueI16 - 1
	}
	return int16(*v)
}

func clampU16p(v *int64) uint16 {
	switch {
	case v == nil:
		return NoValue16
	case *v < 0:
		return 0
	case *v > int64(NoValue16)-1:
		return NoValue16 - 1
	}
	return uint16(*v)
}

func clampU64(v *uint64) uint64 {
	switch {
	case v == nil:
		return NoValue64
	case *v > NoValue64-1:
		return NoValue64 - 1
	}
	return *v
}

// truncateText 將文字截短至不超過 limit 位元組, 自結尾刪去整個 UTF-8 字元.
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

// addChunked 寫入一筆首記錄與其後的接續記錄. payload 長度即首記錄位移 9 的值.
func (w *Writer) addChunked(kind Kind, t int64, payload []byte) {
	size := gpuBlockSize * w.n
	first := true
	for rest := payload; first || len(rest) > 0; first = false {
		take := min(size, len(rest))
		var rec []byte
		if first {
			rec = w.newRecord(kind, t, uint16(len(payload)))
		} else {
			rec = w.newRecord(KindContinue, t, 0)
		}
		copy(rec[recordHeaderSize:], rest[:take])
		rest = rest[take:]
	}
}

// AddError 加入一則 error 訊息 (錯誤記錄與其接續記錄), 訊息文字超過 1024 位元組時截短.
func (w *Writer) AddError(ts float64, msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.clampTime(w.timeLocked(ts))
	w.addChunked(KindError, t, truncateText(msg, maxMessageBytes))
}

// AddDisconnect 加入一筆斷線記錄. t 為本機判定斷線的時刻 (微秒), 早於前一筆記錄時以前一筆記錄的時刻計.
func (w *Writer) AddDisconnect(t int64, reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t = w.clampTime(t)
	w.addChunked(KindDisconnect, t, truncateText(reason, maxMessageBytes))
}

// AddProcesses 加入一筆程序記錄, ts 為同一筆 sample 訊息的 ts. 各 GPU 的程序合併為一個 JSON 陣列,
// 每個元素另加成員 gpu; 超過 65535 位元組時自陣列結尾刪去整個元素. 任一元素不是 JSON 物件時回報錯誤且不加入任何記錄.
func (w *Writer) AddProcesses(ts float64, procs []GPUProcs) error {
	var elems [][]byte
	for _, gp := range procs {
		for _, raw := range gp.Procs {
			e, err := withGPUMember(raw, gp.GPU)
			if err != nil {
				return err
			}
			elems = append(elems, e)
		}
	}
	total := 2
	for _, e := range elems {
		total += len(e)
	}
	if len(elems) > 1 {
		total += len(elems) - 1
	}
	for total > maxProcessesBytes {
		last := elems[len(elems)-1]
		total -= len(last)
		if len(elems) > 1 {
			total--
		}
		elems = elems[:len(elems)-1]
	}
	payload := make([]byte, 0, total)
	payload = append(payload, '[')
	for i, e := range elems {
		if i > 0 {
			payload = append(payload, ',')
		}
		payload = append(payload, e...)
	}
	payload = append(payload, ']')

	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.clampTime(w.timeLocked(ts))
	w.addChunked(KindProcesses, t, payload)
	return nil
}

// withGPUMember 將 JSON 物件壓縮後在結尾加上 gpu 成員.
func withGPUMember(raw json.RawMessage, gpu int) ([]byte, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return nil, fmt.Errorf("recording: invalid process element: %w", err)
	}
	e := b.Bytes()
	if len(e) < 2 || e[0] != '{' {
		return nil, errors.New("recording: process element is not a JSON object")
	}
	e = e[:len(e)-1]
	if len(e) > 1 {
		e = append(e, ',')
	}
	e = fmt.Appendf(e, `"gpu":%d}`, gpu)
	return e, nil
}

// Flush 將累積的記錄以一次寫入附加到檔尾. 寫入失敗時嘗試截回原長度並保留緩衝, 供下次重試.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushLocked()
}

func (w *Writer) flushLocked() error {
	if w.closed {
		return os.ErrClosed
	}
	if len(w.buf) == 0 {
		return nil
	}
	n, err := w.f.Write(w.buf)
	if err != nil {
		if n > 0 {
			if terr := w.f.Truncate(w.size); terr != nil {
				return errors.Join(err, terr)
			}
		}
		return err
	}
	w.size += int64(n)
	w.buf = w.buf[:0]
	return nil
}

// Close 先 Flush 再關閉檔案.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	err := w.flushLocked()
	w.closed = true
	return errors.Join(err, w.f.Close())
}
