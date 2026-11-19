package recording

import (
	"errors"
	"io"
)

// 版本 1 的記錄配置, 定義見 docs/recording-format-v1.md.
const (
	v1RecordHeaderSize = 11
	v1GPUBlockSize     = 20
	// v1KindContinue 為版本 1 的接續記錄, 讀取時併入所屬的記錄.
	v1KindContinue Kind = 3
	// v1SegmentRecords 為版本 1 檔案以 Iter 讀取時每段的記錄筆數.
	v1SegmentRecords = 2048
	// v1ReadBytes 為版本 1 每次讀取的位元組數上限.
	v1ReadBytes = 1 << 20
)

// 版本 1 各欄位代表無值的數值.
const (
	v1NoValue8   uint8  = 255
	v1NoValueI16 int16  = 32767
	v1NoValue16  uint16 = 65535
	v1NoValue64  uint64 = 18446744073709551615
)

// v1Count 回傳目前檔案內完整記錄的筆數 (含接續記錄), 檔尾寫入未完成的部分不計.
func (rf *File) v1Count() (int, error) {
	size, err := rf.areaSize()
	if err != nil {
		return 0, err
	}
	return int(size / rf.r), nil
}

func (rf *File) v1TimeAt(i int) (int64, error) {
	var b [8]byte
	if err := rf.readAt(b[:], int64(i)*rf.r+1); err != nil {
		return 0, err
	}
	return int64(le.Uint64(b[:])), nil
}

func (rf *File) v1TimeRange() (first, last int64, ok bool, err error) {
	n, err := rf.v1Count()
	if err != nil || n == 0 {
		return 0, 0, false, err
	}
	if first, err = rf.v1TimeAt(0); err != nil {
		return 0, 0, false, err
	}
	if last, err = rf.v1TimeAt(n - 1); err != nil {
		return 0, 0, false, err
	}
	return first, last, true, nil
}

// v1LowerBound 以二分搜尋回傳第一筆時刻不早於 t 的記錄索引, 皆早於 t 時回傳 n.
func (rf *File) v1LowerBound(t int64, n int) (int, error) {
	lo, hi := 0, n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		mt, err := rf.v1TimeAt(mid)
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

type v1Cursor struct {
	rf     *File
	loaded int
	total  int
	buf    []byte
	pos    int
	cnt    int
}

// index 回傳下一筆將被取出的記錄索引.
func (c *v1Cursor) index() int { return c.loaded - c.cnt + c.pos }

// next 取出下一筆記錄, 已無完整記錄時回傳 nil.
func (c *v1Cursor) next() ([]byte, error) {
	if c.pos == c.cnt {
		if c.loaded >= c.total {
			return nil, nil
		}
		r := int(c.rf.r)
		want := min(max(1, v1ReadBytes/r), c.total-c.loaded)
		if need := want * r; cap(c.buf) < need {
			c.buf = make([]byte, need)
		}
		buf := c.buf[:want*r]
		got, err := c.rf.f.ReadAt(buf, c.rf.base+int64(c.loaded)*c.rf.r)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		c.cnt = got / r
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

func (c *v1Cursor) back() { c.pos-- }

// v1ReadIndexRange 讀取索引 [start, end) 之間開頭的記錄. start 落在某則訊息的接續記錄中間時,
// 該則訊息的殘餘接續記錄略過; end 落在訊息中間時, 讀到該則訊息完整為止. withProcs 為假時
// 程序記錄不列入結果.
func (rf *File) v1ReadIndexRange(start, end, total int, withProcs bool) ([]Record, error) {
	start = max(start, 0)
	end = min(end, total)
	if start >= end {
		return nil, nil
	}
	c := &v1Cursor{rf: rf, loaded: start, total: total}
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
			out = append(out, rf.v1DecodeSample(rec))
		case KindError, KindProcesses, KindDisconnect:
			kind, t := Kind(rec[0]), int64(le.Uint64(rec[1:]))
			text, ok, err := rf.v1CollectText(c, rec)
			if err != nil {
				return nil, err
			}
			if ok && (withProcs || kind != KindProcesses) {
				out = append(out, Record{Kind: kind, Time: t, Text: text})
			}
		}
	}
	return out, nil
}

// v1CollectText 重組首記錄與其接續記錄的文字. 接續記錄不足 (寫入未完成或檔案毀損) 時 ok 為假.
// rec 的內容於讀取接續記錄後可能被覆寫, 故先複製所需欄位.
func (rf *File) v1CollectText(c *v1Cursor, rec []byte) (string, bool, error) {
	size := v1GPUBlockSize * rf.n
	length := int(le.Uint16(rec[9:]))
	text := make([]byte, 0, length)
	text = append(text, rec[v1RecordHeaderSize:v1RecordHeaderSize+min(size, length)]...)
	for len(text) < length {
		next, err := c.next()
		if err != nil {
			return "", false, err
		}
		if next == nil {
			return "", false, nil
		}
		if Kind(next[0]) != v1KindContinue {
			c.back()
			return "", false, nil
		}
		text = append(text, next[v1RecordHeaderSize:v1RecordHeaderSize+min(size, length-len(text))]...)
	}
	return string(text), true, nil
}

// v1DecodeSample 解出取樣記錄, 代表無值的數值換為無值.
func (rf *File) v1DecodeSample(rec []byte) Record {
	out := Record{
		Kind:       KindSample,
		Time:       int64(le.Uint64(rec[1:])),
		IntervalMS: uint32(le.Uint16(rec[9:])),
		GPUs:       make([]GPUValues, rf.n),
	}
	for i := range out.GPUs {
		b := rec[v1RecordHeaderSize+v1GPUBlockSize*i:]
		g := &out.GPUs[i]
		g.ID = rf.ids[i]
		for m, v := range [...]uint8{b[0], b[1], b[2], b[3]} {
			if v != v1NoValue8 {
				g.set(Metric(m), int64(v))
			}
		}
		if v := int16(le.Uint16(b[4:])); v != v1NoValueI16 {
			g.set(MetricTemp, int64(v))
		}
		for k, m := range [...]Metric{MetricPowerW, MetricClockGPU, MetricClockMem} {
			if v := le.Uint16(b[6+2*k:]); v != v1NoValue16 {
				g.set(m, int64(v))
			}
		}
		if v := le.Uint64(b[12:]); v != v1NoValue64 {
			g.Has |= 1 << MetricMemUsed
			g.MemUsed = v
		}
	}
	return out
}
