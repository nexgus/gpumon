package recording

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// File 為開啟供讀取的記錄檔. 所有讀取皆以 ReadAt 進行, 不將整個檔案讀入記憶體,
// 可由多個 goroutine 同時使用, 且檔案可同時被 Writer 附加.
type File struct {
	f    *os.File
	hdr  Header
	ids  []int
	n    int
	r    int64
	base int64
}

// Open 開啟記錄檔. 識別字串不符時回傳 ErrNotRecording, 格式版本不認得時回傳 *UnsupportedVersionError.
// 說明區中不認得的成員一律略過.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rf, err := openFile(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rf, nil
}

func openFile(f *os.File) (*File, error) {
	fixed := make([]byte, fixedSize)
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, fixedSize), fixed); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrNotRecording
		}
		return nil, err
	}
	if string(fixed[:8]) != magic {
		return nil, ErrNotRecording
	}
	if v := le.Uint16(fixed[8:]); v != formatVersion {
		return nil, &UnsupportedVersionError{Version: v}
	}
	n := int(le.Uint16(fixed[10:]))
	m := int64(le.Uint32(fixed[12:]))
	meta := make([]byte, m)
	if _, err := io.ReadFull(io.NewSectionReader(f, fixedSize, m), meta); err != nil {
		return nil, fmt.Errorf("recording: truncated header: %w", err)
	}
	var hj headerJSON
	if err := json.Unmarshal(meta, &hj); err != nil {
		return nil, fmt.Errorf("recording: invalid header: %w", err)
	}
	ids, err := parseGPUIDs(hj.Devices, false)
	if err != nil {
		return nil, err
	}
	if len(ids) != n {
		return nil, fmt.Errorf("recording: GPU count %d does not match devices message (%d)", n, len(ids))
	}
	if n == 0 {
		return nil, ErrNoGPU
	}
	return &File{
		f:    f,
		hdr:  Header{Host: hj.Host, TWall0: hj.TWall0, TS0: hj.TS0, Devices: hj.Devices},
		ids:  ids,
		n:    n,
		r:    int64(recordHeaderSize + gpuBlockSize*n),
		base: fixedSize + m,
	}, nil
}

// Close 關閉檔案.
func (rf *File) Close() error { return rf.f.Close() }

// Header 回傳說明區的內容.
func (rf *File) Header() Header { return rf.hdr }

// NumGPUs 回傳 GPU 張數 N.
func (rf *File) NumGPUs() int { return rf.n }

// GPUIDs 回傳 devices.gpus 各元素的 id.
func (rf *File) GPUIDs() []int { return append([]int(nil), rf.ids...) }

// Count 回傳目前檔案內完整記錄的筆數 (含接續記錄), 檔尾寫入未完成的部分不計.
func (rf *File) Count() (int, error) {
	st, err := rf.f.Stat()
	if err != nil {
		return 0, err
	}
	body := st.Size() - rf.base
	if body < 0 {
		return 0, nil
	}
	return int(body / rf.r), nil
}

func (rf *File) timeAt(i int) (int64, error) {
	var b [8]byte
	if _, err := rf.f.ReadAt(b[:], rf.base+int64(i)*rf.r+1); err != nil {
		return 0, err
	}
	return int64(le.Uint64(b[:])), nil
}

// TimeRange 回傳檔案內第一筆與最後一筆完整記錄的時刻; 沒有任何記錄時 ok 為假.
func (rf *File) TimeRange() (first, last int64, ok bool, err error) {
	n, err := rf.Count()
	if err != nil || n == 0 {
		return 0, 0, false, err
	}
	if first, err = rf.timeAt(0); err != nil {
		return 0, 0, false, err
	}
	if last, err = rf.timeAt(n - 1); err != nil {
		return 0, 0, false, err
	}
	return first, last, true, nil
}

// LowerBound 以二分搜尋回傳第一筆時刻不早於 t 的記錄索引, 皆早於 t 時回傳記錄筆數.
func (rf *File) LowerBound(t int64) (int, error) {
	n, err := rf.Count()
	if err != nil {
		return 0, err
	}
	return rf.lowerBound(t, n)
}

func (rf *File) lowerBound(t int64, n int) (int, error) {
	lo, hi := 0, n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		mt, err := rf.timeAt(mid)
		if err != nil {
			return 0, err
		}
		if mt < t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, nil
}

// ReadRange 讀取時刻在 [from, to] 閉區間內的記錄, 依檔案順序回傳.
func (rf *File) ReadRange(from, to int64) ([]Record, error) {
	if from > to {
		return nil, nil
	}
	n, err := rf.Count()
	if err != nil {
		return nil, err
	}
	lo, err := rf.lowerBound(from, n)
	if err != nil {
		return nil, err
	}
	hi := n
	if to < math.MaxInt64 {
		if hi, err = rf.lowerBound(to+1, n); err != nil {
			return nil, err
		}
	}
	return rf.readIndexRange(lo, hi, n)
}

// ReadIndexRange 讀取索引 [start, end) 之間開頭的記錄. start 落在某則訊息的接續記錄中間時,
// 該則訊息的殘餘接續記錄略過; end 落在訊息中間時, 讀到該則訊息完整為止.
func (rf *File) ReadIndexRange(start, end int) ([]Record, error) {
	n, err := rf.Count()
	if err != nil {
		return nil, err
	}
	return rf.readIndexRange(start, end, n)
}

const blockBytes = 1 << 20

type cursor struct {
	rf     *File
	loaded int
	total  int
	buf    []byte
	pos    int
	cnt    int
}

// index 回傳下一筆將被取出的記錄索引.
func (c *cursor) index() int { return c.loaded - c.cnt + c.pos }

// next 取出下一筆記錄, 已無完整記錄時回傳 nil.
func (c *cursor) next() ([]byte, error) {
	if c.pos == c.cnt {
		if c.loaded >= c.total {
			return nil, nil
		}
		want := min(max(1, int(blockBytes/c.rf.r)), c.total-c.loaded)
		if need := want * int(c.rf.r); cap(c.buf) < need {
			c.buf = make([]byte, need)
		}
		buf := c.buf[:want*int(c.rf.r)]
		got, err := c.rf.f.ReadAt(buf, c.rf.base+int64(c.loaded)*c.rf.r)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		c.cnt = got / int(c.rf.r)
		c.pos = 0
		c.loaded += c.cnt
		if c.cnt == 0 {
			c.total = c.loaded
			return nil, nil
		}
	}
	r := int(c.rf.r)
	rec := c.buf[c.pos*r : (c.pos+1)*r]
	c.pos++
	return rec, nil
}

func (c *cursor) back() { c.pos-- }

func (rf *File) readIndexRange(start, end, total int) ([]Record, error) {
	start = max(start, 0)
	end = min(end, total)
	if start >= end {
		return nil, nil
	}
	c := &cursor{rf: rf, loaded: start, total: total}
	var out []Record
	for c.index() < end {
		rec, err := c.next()
		if err != nil {
			return nil, err
		}
		if rec == nil {
			break
		}
		switch Kind(rec[0]) {
		case KindSample:
			out = append(out, rf.decodeSample(rec))
		case KindError, KindProcesses, KindDisconnect:
			kind, t := Kind(rec[0]), int64(le.Uint64(rec[1:]))
			text, ok, err := rf.collectText(c, rec)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, Record{Kind: kind, Time: t, Text: text})
			}
		}
	}
	return out, nil
}

// collectText 重組首記錄與其接續記錄的文字. 接續記錄不足 (寫入未完成或檔案毀損) 時 ok 為假.
// rec 的內容於讀取接續記錄後可能被覆寫, 故先複製所需欄位.
func (rf *File) collectText(c *cursor, rec []byte) (string, bool, error) {
	size := gpuBlockSize * rf.n
	length := int(le.Uint16(rec[9:]))
	text := make([]byte, 0, length)
	text = append(text, rec[recordHeaderSize:recordHeaderSize+min(size, length)]...)
	for len(text) < length {
		next, err := c.next()
		if err != nil {
			return "", false, err
		}
		if next == nil {
			return "", false, nil
		}
		if Kind(next[0]) != KindContinue {
			c.back()
			return "", false, nil
		}
		text = append(text, next[recordHeaderSize:recordHeaderSize+min(size, length-len(text))]...)
	}
	return string(text), true, nil
}

func (rf *File) decodeSample(rec []byte) Record {
	out := Record{
		Kind:       KindSample,
		Time:       int64(le.Uint64(rec[1:])),
		IntervalMS: le.Uint16(rec[9:]),
		GPUs:       make([]GPUValues, rf.n),
	}
	for i := range out.GPUs {
		b := rec[recordHeaderSize+gpuBlockSize*i:]
		out.GPUs[i] = GPUValues{
			ID:       rf.ids[i],
			Util:     b[0],
			Fan:      b[1],
			Enc:      b[2],
			Dec:      b[3],
			Temp:     int16(le.Uint16(b[4:])),
			PowerW:   le.Uint16(b[6:]),
			ClockGPU: le.Uint16(b[8:]),
			ClockMem: le.Uint16(b[10:]),
			MemUsed:  le.Uint64(b[12:]),
		}
	}
	return out
}
