package recording

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// File 為開啟供讀取的記錄檔, 版本 1 與版本 2 皆可. 所有讀取皆以 ReadAt 進行, 不將整個檔案
// 讀入記憶體, 可由多個 goroutine 同時使用, 且檔案可同時被 Writer 附加. 每次查詢以當下的
// 檔案長度為準, 版本 2 檔尾不完整的區塊與版本 1 檔尾寫入未完成的記錄皆略過.
type File struct {
	f       *os.File
	version uint16
	hdr     Header
	ids     []int
	n       int
	// base 為記錄區的起點; r 為版本 1 每筆記錄的長度.
	base int64
	r    int64
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
	v := le.Uint16(fixed[8:])
	if v != version1 && v != version2 {
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
		f:       f,
		version: v,
		hdr:     Header{Host: hj.Host, TWall0: hj.TWall0, TS0: hj.TS0, Devices: hj.Devices},
		ids:     ids,
		n:       n,
		base:    fixedSize + m,
		r:       int64(v1RecordHeaderSize + v1GPUBlockSize*n),
	}, nil
}

// Close 關閉檔案.
func (rf *File) Close() error { return rf.f.Close() }

// Version 回傳格式版本.
func (rf *File) Version() int { return int(rf.version) }

// Header 回傳說明區的內容.
func (rf *File) Header() Header { return rf.hdr }

// NumGPUs 回傳 GPU 張數 N.
func (rf *File) NumGPUs() int { return rf.n }

// GPUIDs 回傳 devices.gpus 各元素的 id.
func (rf *File) GPUIDs() []int { return append([]int(nil), rf.ids...) }

// areaSize 回傳目前記錄區的長度.
func (rf *File) areaSize() (int64, error) {
	st, err := rf.f.Stat()
	if err != nil {
		return 0, err
	}
	return max(st.Size()-rf.base, 0), nil
}

// TimeRange 回傳檔案的起訖時刻. 版本 2 為第一個有效區塊的第一筆記錄時刻與最後一個有效區塊的
// 最後一筆記錄時刻, 版本 1 為第一筆與最後一筆完整記錄的時刻. 沒有任何記錄時 ok 為假.
func (rf *File) TimeRange() (first, last int64, ok bool, err error) {
	if rf.version == version1 {
		return rf.v1TimeRange()
	}
	size, err := rf.areaSize()
	if err != nil {
		return 0, 0, false, err
	}
	fh, _, ok, err := rf.nextValid(0, size)
	if err != nil || !ok {
		return 0, 0, false, err
	}
	lh, _, ok, err := rf.prevValid(size, size)
	if err != nil || !ok {
		return 0, 0, false, err
	}
	return fh.tFirst, lh.tLast, true, nil
}

// Iter 以段為單位依序讀取記錄. 版本 2 的一段為一個區塊, 版本 1 的一段為固定筆數的記錄.
// Next 由定位處往後讀, Prev 由定位處往前讀, 兩者互不影響. 每段的記錄依寫入的先後排列.
// 讀取以 SeekTime 當下的檔案長度為準, 之後附加的內容不讀取.
type Iter struct {
	rf        *File
	withProcs bool
	size      int64

	// 版本 2 的 fwd 為 Next 開始搜尋的位移, back 為 Prev 所讀區塊的結尾上限, backHdr 為位於
	// back 的區塊開頭 (未知時為 nil), cached 為 SeekTime 時已讀取的區塊, last 為最近一次
	// 回傳的區塊.
	fwd     int64
	back    int64
	backHdr *blockHeader
	cached  *blockHeader
	cachedC []byte
	last    blockHeader

	// 版本 1 的 v1Fwd 與 v1Back 為往後與往前讀取的記錄索引, v1Total 為記錄筆數.
	v1Fwd   int
	v1Back  int
	v1Total int
}

// SeekTime 回傳定位於時刻 t 的 Iter. 第一次 Next 回傳含有第一筆時刻不早於 t 的記錄的那一段,
// 該段可能也含有較早的記錄; 沒有這樣的記錄時 Next 不回傳任何段, Prev 由檔尾往前讀.
// withProcs 為假時, 程序記錄與程序變動記錄不解析也不列入結果.
func (rf *File) SeekTime(t int64, withProcs bool) (*Iter, error) {
	size, err := rf.areaSize()
	if err != nil {
		return nil, err
	}
	it := &Iter{rf: rf, withProcs: withProcs, size: size}
	if rf.version == version1 {
		n := int(size / rf.r)
		i, err := rf.v1LowerBound(t, n)
		if err != nil {
			return nil, err
		}
		seg := i / v1SegmentRecords * v1SegmentRecords
		it.v1Fwd, it.v1Back, it.v1Total = seg, seg, n
		return it, nil
	}
	lo, err := rf.lowerBlock(t, size)
	if err != nil {
		return nil, err
	}
	h, content, ok, err := rf.nextValid(lo, size)
	if err != nil {
		return nil, err
	}
	if !ok {
		it.fwd, it.back = size, size
		return it, nil
	}
	it.fwd, it.back, it.backHdr = h.off, h.off, &h
	it.cached, it.cachedC = &h, content
	return it, nil
}

// lowerBlock 以二分搜尋 (docs/recording-format-v2.md 6.3) 回傳一個位移, 自該處往後找到的第一個
// 有效區塊即最後一筆記錄時刻不早於 t 的第一個有效區塊. 只讀取區塊開頭與校驗碼, 不解壓縮.
func (rf *File) lowerBlock(t, size int64) (int64, error) {
	first, _, ok, err := rf.nextValid(0, size)
	if err != nil || !ok || first.tLast >= t {
		return 0, err
	}
	last, _, ok, err := rf.prevValid(size, size)
	if err != nil {
		return 0, err
	}
	if !ok || last.tLast < t {
		return size, nil
	}
	lo, hi := first.end(), last.off
	for lo < hi {
		mid := lo + (hi-lo)/2
		h, _, ok, err := rf.nextValid(mid, size)
		if err != nil {
			return 0, err
		}
		if ok && h.off < hi && h.tLast < t {
			lo = h.end()
		} else {
			hi = mid
		}
	}
	return lo, nil
}

// Next 回傳往後的下一段記錄; 已到結尾時 ok 為假.
func (it *Iter) Next() (recs []Record, ok bool, err error) {
	rf := it.rf
	if rf.version == version1 {
		if it.v1Fwd >= it.v1Total {
			return nil, false, nil
		}
		start := it.v1Fwd
		it.v1Fwd += v1SegmentRecords
		recs, err := rf.v1ReadIndexRange(start, start+v1SegmentRecords, it.v1Total, it.withProcs)
		return recs, err == nil, err
	}
	for it.fwd < it.size {
		var h blockHeader
		var content []byte
		if it.cached != nil && it.cached.off == it.fwd {
			h, content, ok = *it.cached, it.cachedC, true
			it.cached, it.cachedC = nil, nil
		} else if h, content, ok, err = rf.nextValid(it.fwd, it.size); err != nil {
			return nil, false, err
		}
		if !ok {
			it.fwd = it.size
			break
		}
		recs, ok = rf.decodeBlock(&h, content, it.withProcs)
		if !ok {
			it.fwd = h.off + 1
			continue
		}
		it.fwd = h.end()
		it.last = h
		return recs, true, nil
	}
	return nil, false, nil
}

// Prev 回傳往前的上一段記錄; 已到開頭時 ok 為假.
func (it *Iter) Prev() (recs []Record, ok bool, err error) {
	rf := it.rf
	if rf.version == version1 {
		if it.v1Back <= 0 {
			return nil, false, nil
		}
		end := it.v1Back
		it.v1Back = max(0, end-v1SegmentRecords)
		recs, err := rf.v1ReadIndexRange(it.v1Back, end, it.v1Total, it.withProcs)
		return recs, err == nil, err
	}
	for it.back > 0 {
		h, content, ok, err := rf.blockBefore(it.back, it.backHdr, it.size)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			it.back = 0
			break
		}
		it.back, it.backHdr = h.off, &h
		recs, ok = rf.decodeBlock(&h, content, it.withProcs)
		if !ok {
			it.backHdr = nil
			continue
		}
		it.last = h
		return recs, true, nil
	}
	return nil, false, nil
}

// ReadRange 讀取時刻在 [from, to] 閉區間內的所有記錄, 依檔案順序回傳, 含程序記錄.
func (rf *File) ReadRange(from, to int64) ([]Record, error) {
	if from > to {
		return nil, nil
	}
	it, err := rf.SeekTime(from, true)
	if err != nil {
		return nil, err
	}
	var out []Record
	for {
		recs, ok, err := it.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		for _, r := range recs {
			if r.Time > to {
				return out, nil
			}
			if r.Time >= from {
				out = append(out, r)
			}
		}
	}
}

// Backward 由時刻不晚於 t 的最後一筆記錄起往前逐筆呼叫 fn, fn 回傳假時停止. withProcs 的意義與 SeekTime 相同.
func (rf *File) Backward(t int64, withProcs bool, fn func(rec *Record) bool) error {
	return rf.eachBackward(t, withProcs, func(rec *Record, _ recLoc) bool { return fn(rec) })
}

// recLoc 為一筆版本 2 記錄的位置, 即所在區塊與其在區塊內的索引.
type recLoc struct {
	blk blockHeader
	idx int
}

// eachBackward 由時刻不晚於 t 的最後一筆記錄起, 往前逐筆呼叫 fn, fn 回傳假時停止.
// 版本 2 的 loc 為該筆記錄的位置, 版本 1 時無意義.
func (rf *File) eachBackward(t int64, withProcs bool, fn func(rec *Record, loc recLoc) bool) error {
	seekT := t
	if t < math.MaxInt64 {
		seekT = t + 1
	}
	it, err := rf.SeekTime(seekT, withProcs)
	if err != nil {
		return err
	}
	recs, ok, err := it.Next()
	for {
		if err != nil {
			return err
		}
		for i := len(recs) - 1; ok && i >= 0; i-- {
			if recs[i].Time <= t && !fn(&recs[i], recLoc{it.last, i}) {
				return nil
			}
		}
		if recs, ok, err = it.Prev(); err == nil && !ok {
			return nil
		}
	}
}

// ProcessList 為某次取樣的 GPU 程序列表. Procs 為 JSON 陣列, 每個元素為 sample 訊息 procs 陣列的
// 一個元素, 另加成員 gpu (該程序所在 GPU 的 id).
type ProcessList struct {
	Time  int64
	Procs json.RawMessage
}

// ProcessesAt 回傳時刻不晚於 t 的最近一次取樣的 GPU 程序列表 cur, 與其前一次取樣的列表 prev,
// 供換算使用率. 沒有這樣的取樣或該次取樣的列表無法還原時 cur 為 nil. 沒有前一次取樣, 兩者
// 之間有斷線記錄, 或前一次取樣的列表無法還原時 prev 為 nil.
//
// 版本 1 以程序記錄為單位, cur 為時刻不晚於 t 的最近一筆程序記錄, prev 為其前一筆程序記錄.
// 版本 2 依 docs/recording-format-v2.md 6.5 由列表重設區塊起套用程序變動記錄還原.
func (rf *File) ProcessesAt(t int64) (cur, prev *ProcessList, err error) {
	if rf.version == version1 {
		return rf.v1ProcessesAt(t)
	}
	var s, p *Record
	var sLoc, pLoc recLoc
	err = rf.eachBackward(t, true, func(rec *Record, loc recLoc) bool {
		switch rec.Kind {
		case KindDisconnect:
			// 斷線前後的累計值不可相減, 不必再找前一次取樣.
			return s == nil
		case KindSample:
			if s == nil {
				s, sLoc = rec, loc
				return true
			}
			p, pLoc = rec, loc
			return false
		}
		return true
	})
	if err != nil || s == nil {
		return nil, nil, err
	}
	targets := []recLoc{sLoc}
	start := sLoc.blk.reset
	if p != nil {
		targets = append(targets, pLoc)
		start = pLoc.blk.reset
	}
	lists, err := rf.restore(start, targets)
	if err != nil {
		return nil, nil, err
	}
	if lists[0] != nil {
		cur = &ProcessList{Time: s.Time, Procs: lists[0]}
	}
	if p != nil && lists[1] != nil {
		prev = &ProcessList{Time: p.Time, Procs: lists[1]}
	}
	if cur == nil {
		prev = nil
	}
	return cur, prev, nil
}

// restore 自記錄區位移 start 的列表重設區塊起依序套用程序變動記錄, 回傳 targets 所列各次取樣
// 的 GPU 程序列表; 無法還原者為 nil. 跨越列表重設區塊時列表重設為空, 中途遇到無效區塊時,
// 直到下一個列表重設區塊之前的取樣皆無法還原.
func (rf *File) restore(start int64, targets []recLoc) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, len(targets))
	left := len(targets)
	for _, tg := range targets {
		if tg.blk.off < start {
			return out, nil
		}
	}
	size, err := rf.areaSize()
	if err != nil {
		return nil, err
	}
	gl, cl := gpuList{}, cpuList{}
	valid := false
	pending := -1
	finish := func() {
		if pending >= 0 {
			if valid {
				out[pending] = gl.json()
			}
			pending = -1
			left--
		}
	}
	for pos, expect := start, start; left > 0; {
		h, content, ok, err := rf.nextValid(pos, size)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		// 某次取樣的程序變動記錄與該取樣寫於同一個區塊, 因此遇到無效區塊或列表重設區塊時,
		// 尚待下一筆取樣才結算的列表已完整, 先行結算.
		if h.off != expect {
			finish()
			valid = false
		}
		recs, ok := rf.decodeBlock(&h, content, true)
		if !ok {
			finish()
			valid = false
			pos, expect = h.off+1, -1
			continue
		}
		if h.isReset() {
			finish()
			gl, cl, valid = gpuList{}, cpuList{}, true
		}
		if left == 0 {
			return out, nil
		}
		for i := range recs {
			switch recs[i].Kind {
			case KindSample:
				finish()
				if left == 0 {
					return out, nil
				}
				for k, tg := range targets {
					if tg.blk.off == h.off && tg.idx == i {
						pending = k
					}
				}
			case KindGPUProcs:
				gl.apply(recs[i].GPUProcs)
			case KindCPUProcs:
				cl.apply(recs[i].CPUProcs)
			}
		}
		pos, expect = h.end(), h.end()
	}
	finish()
	return out, nil
}

// v1ProcessesAt 為版本 1 的 ProcessesAt.
func (rf *File) v1ProcessesAt(t int64) (cur, prev *ProcessList, err error) {
	err = rf.eachBackward(t, true, func(rec *Record, _ recLoc) bool {
		switch rec.Kind {
		case KindProcesses:
			if cur == nil {
				cur = &ProcessList{Time: rec.Time, Procs: json.RawMessage(rec.Text)}
				return true
			}
			prev = &ProcessList{Time: rec.Time, Procs: json.RawMessage(rec.Text)}
			return false
		case KindDisconnect:
			// 斷線前後的累計值不可相減, 不必再找前一筆.
			return cur == nil
		}
		return true
	})
	return cur, prev, err
}
