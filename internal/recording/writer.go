package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HostDirName 將主機識別 ("user@addr" 或 "user@addr:port") 轉為主機目錄名稱, 不屬於英文
// 字母, 數字, `.`, `_`, `@`, `-` 的字元逐一換成 `_`. 結尾為連接埠時, 連接埠之前的 `:`
// 改為 `+`, 例如 `user@host:2222` 為 `user@host+2222`; `+` 不會由其他字元換來, 因此
// 帶連接埠的目錄名稱不與不帶連接埠者相同. 位址含 `:` (IPv6) 而帶連接埠時, 位址以方括號
// 括住, 例如 `user@[::1]:2222`; 不帶方括號的 IPv6 位址不視為帶連接埠.
func HostDirName(host string) string {
	base, port := host, ""
	if b, p, ok := splitPortSuffix(host); ok {
		base, port = b, p
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '@', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if port != "" {
		b.WriteByte('+')
		b.WriteString(port)
	}
	return b.String()
}

// splitPortSuffix 自主機識別分出結尾的連接埠. 位址部分 (最後一個 `@` 之後) 必須不含
// `:`, 或為方括號括住的位址.
func splitPortSuffix(host string) (base, port string, ok bool) {
	i := strings.LastIndexByte(host, ':')
	if i < 0 || i == len(host)-1 {
		return "", "", false
	}
	for _, r := range host[i+1:] {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	base = host[:i]
	addr := base[strings.LastIndexByte(base, '@')+1:]
	if strings.Contains(addr, ":") && !(strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]")) {
		return "", "", false
	}
	return base, host[i+1:], true
}

// HostDir 回傳某主機的記錄檔目錄 `<dataDir>/history/<主機目錄>`.
func HostDir(dataDir, host string) string {
	return filepath.Join(dataDir, "history", HostDirName(host))
}

// Writer 負責建立版本 2 的記錄檔並於檔尾附加區塊. Add 系列函數只把記錄寫入記憶體緩衝,
// Flush 把累積的記錄寫成一個區塊, 以一次寫入附加到檔尾. Writer 可由多個 goroutine 同時使用.
type Writer struct {
	mu       sync.Mutex
	f        *os.File
	path     string
	ids      []int
	tBase    int64
	tsBase   float64
	lastTime int64
	hasLast  bool
	// size 為檔案長度, base 為記錄區的起點.
	size int64
	base int64
	// buf 為尚未存檔的記錄, bufFirst 與 bufLast 為其第一筆與最後一筆的時刻, bufReset
	// 表示這些記錄將寫成列表重設區塊.
	buf      []byte
	bufFirst int64
	bufLast  int64
	bufReset bool
	// prevTotal 為前一個區塊的總長度, lastReset 為最近的列表重設區塊於記錄區內的位移.
	prevTotal uint32
	lastReset int64
	// resetTime 為最近一個列表重設區塊第一筆記錄的時刻, hasReset 為假表示尚未決定任何列表重設區塊.
	resetTime int64
	hasReset  bool
	// gpuPrev 為 GPU 程序列表的比較對象.
	gpuPrev map[gpuKey]*GPUProc
	closed  bool
}

// Create 於 `<dataDir>/history/<主機目錄>/` 建立版本 2 的記錄檔並寫入固定區與說明區.
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
	le.PutUint16(fixed[8:], version2)
	le.PutUint16(fixed[10:], uint16(len(ids)))
	le.PutUint32(fixed[12:], uint32(len(meta)))
	out := append(fixed, meta...)
	if _, err := f.Write(out); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return &Writer{
		f:       f,
		path:    path,
		ids:     ids,
		tBase:   h.TWall0,
		tsBase:  h.TS0,
		size:    int64(len(out)),
		base:    int64(len(out)),
		gpuPrev: map[gpuKey]*GPUProc{},
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
func (w *Writer) NumGPUs() int { return len(w.ids) }

// GPUIDs 回傳說明區 devices.gpus 各元素的 id, 順序與取樣記錄的 gpus 相同.
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

// LastTime 回傳最後一筆記錄 (含尚未存檔者) 的時刻; 尚無記錄時 ok 為假.
func (w *Writer) LastTime() (t int64, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastTime, w.hasLast
}

// clampTime 使時刻不早於前一筆記錄, 以維持各筆時刻不遞減, 並更新最後時刻.
func (w *Writer) clampTime(t int64) int64 {
	if w.hasLast && t < w.lastTime {
		t = w.lastTime
	}
	w.lastTime, w.hasLast = t, true
	return t
}

// addRecord 把一筆記錄加入緩衝. 緩衝為空時, 這筆記錄即下一個區塊的第一筆記錄, 於此時決定該
// 區塊是否為列表重設區塊; 是的話立即把比較對象設為空列表, 使該區塊內的程序變動皆以空列表為
// 對象. 存檔失敗時緩衝保留, 此決定隨之保留.
func (w *Writer) addRecord(r *Record) {
	if len(w.buf) == 0 {
		w.bufFirst = r.Time
		w.bufReset = !w.hasReset || r.Time-w.resetTime >= resetInterval
		if w.bufReset {
			w.resetTime, w.hasReset = r.Time, true
			w.gpuPrev = map[gpuKey]*GPUProc{}
		}
	}
	w.bufLast = r.Time
	w.buf = appendRecord(w.buf, r)
}

// AddSample 加入一筆取樣記錄, 並在 GPU 程序列表與前一次取樣不同時緊接加入一筆 GPU 程序變動
// 記錄, 回傳兩者的時刻. gpus 以 id 與說明區的 GPU 配對, 缺少的 GPU 所有欄位皆無值. procs 為
// 各 GPU 的 procs 陣列; 不是 JSON 物件或沒有整數 pid 的元素不列入程序列表, 並回報於 err,
// 其他記錄照常加入.
func (w *Writer) AddSample(ts float64, intervalMS int, gpus []GPUSample, procs []GPUProcs) (int64, error) {
	var cur []GPUProc
	var errs []error
	for _, gp := range procs {
		for _, raw := range gp.Procs {
			p, err := parseGPUProc(int64(gp.GPU), raw)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			cur = append(cur, p)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.clampTime(w.timeLocked(ts))
	iv := uint32(min(max(intervalMS, 0), math.MaxUint32))
	w.addRecord(&Record{Kind: KindSample, Time: t, IntervalMS: iv, GPUs: SampleValues(w.ids, gpus)})
	if changes := diffGPU(w.gpuPrev, cur); len(changes) > 0 {
		w.addRecord(&Record{Kind: KindGPUProcs, Time: t, GPUProcs: changes})
	}
	next := make(map[gpuKey]*GPUProc, len(cur))
	for i := range cur {
		if k := cur[i].key(); next[k] == nil {
			next[k] = &cur[i]
		}
	}
	w.gpuPrev = next
	return t, errors.Join(errs...)
}

// AddError 加入一筆錯誤記錄, 回傳其時刻. 文字原樣存入, 不截斷.
func (w *Writer) AddError(ts float64, msg string) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.clampTime(w.timeLocked(ts))
	w.addRecord(&Record{Kind: KindError, Time: t, Text: msg})
	return t
}

// AddDisconnect 加入一筆斷線記錄, 回傳其時刻. t 為本機判定斷線的時刻 (微秒), 早於前一筆記錄時
// 以前一筆記錄的時刻計. 斷線記錄不重設程序列表的比較對象.
func (w *Writer) AddDisconnect(t int64, reason string) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	t = w.clampTime(t)
	w.addRecord(&Record{Kind: KindDisconnect, Time: t, Text: reason})
	return t
}

// Flush 把累積的記錄寫成一個區塊, 以一次寫入附加到檔尾; 沒有累積的記錄時不寫入. 寫入失敗時
// 把檔案截回原長度並保留緩衝, 下次存檔時連同新的記錄寫成一個區塊.
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
	if len(w.buf) > math.MaxUint32 {
		return errors.New("recording: block too large")
	}
	content := encoder().EncodeAll(w.buf, nil)
	if len(content) > math.MaxUint32-blockHeaderSize {
		return errors.New("recording: block too large")
	}
	off := w.size - w.base
	reset := w.lastReset
	if w.bufReset {
		reset = off
	}
	blk := make([]byte, blockHeaderSize, blockHeaderSize+len(content))
	copy(blk, syncMarker)
	le.PutUint32(blk[16:], uint32(len(content)))
	le.PutUint32(blk[20:], uint32(len(w.buf)))
	le.PutUint32(blk[24:], w.prevTotal)
	le.PutUint64(blk[28:], uint64(w.bufFirst))
	le.PutUint64(blk[36:], uint64(w.bufLast))
	le.PutUint64(blk[44:], uint64(reset))
	crc := crc32.Update(crc32.Checksum(blk[:52], castagnoli), castagnoli, content)
	le.PutUint32(blk[52:], crc)
	blk = append(blk, content...)
	n, err := w.f.Write(blk)
	if err != nil {
		if n > 0 {
			if terr := w.f.Truncate(w.size); terr != nil {
				// 截回失敗時檔尾留有不完整的區塊, 之後的區塊位移須以實際長度計算.
				if st, serr := w.f.Stat(); serr == nil {
					w.size = st.Size()
				}
				return errors.Join(err, terr)
			}
		}
		return err
	}
	w.size += int64(n)
	w.prevTotal = uint32(len(blk))
	if w.bufReset {
		w.lastReset = off
	}
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
