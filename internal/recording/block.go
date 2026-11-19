package recording

import (
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// blockHeaderSize 為版本 2 區塊開頭的長度.
	blockHeaderSize = 56
	// resetInterval 為寫入端產生列表重設區塊的間隔, 單位為微秒. 讀取端不依賴此值.
	resetInterval = 10 * 60 * 1000000
	// scanChunk 為搜尋同步標記時每次讀取的位元組數.
	scanChunk = 64 << 10
)

// syncMarker 為區塊開頭的同步標記.
var syncMarker = []byte{0x6B, 0xC6, 0x6F, 0xF3, 0xFA, 0x80, 0x24, 0x0F, 0x68, 0x2B, 0xE4, 0xFB, 0x0C, 0x08, 0x36, 0xBB}

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// encoder 回傳寫入端共用的 zstd 編碼器. 區塊內容已有 CRC-32C 校驗碼, 故 zstd frame 不另加校驗碼.
var encoder = sync.OnceValue(func() *zstd.Encoder {
	e, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(false))
	if err != nil {
		panic(err)
	}
	return e
})

// decoder 回傳讀取端共用的 zstd 解碼器. DecodeAll 的輸出以 dst 的容量為上限, 防止內容與區塊
// 開頭所記長度不符時配置過多記憶體.
var decoder = sync.OnceValue(func() *zstd.Decoder {
	d, err := zstd.NewReader(nil, zstd.WithDecodeAllCapLimit(true))
	if err != nil {
		panic(err)
	}
	return d
})

// blockHeader 為版本 2 區塊開頭的內容. off 為區塊於記錄區內的位移.
type blockHeader struct {
	off       int64
	c         uint32
	u         uint32
	prevTotal uint32
	tFirst    int64
	tLast     int64
	reset     int64
}

// end 回傳緊接於本區塊之後的位移.
func (h *blockHeader) end() int64 { return h.off + blockHeaderSize + int64(h.c) }

// isReset 表示本區塊為列表重設區塊.
func (h *blockHeader) isReset() bool { return h.reset == h.off }

// readAt 自記錄區位移 off 讀滿 b; 讀到檔尾時回傳 io.ErrUnexpectedEOF.
func (rf *File) readAt(b []byte, off int64) error {
	n, err := rf.f.ReadAt(b, rf.base+off)
	if n == len(b) {
		return nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// readBlock 讀取記錄區位移 off 的區塊並檢查是否有效, 有效的條件為同步標記相符, 區塊開頭與內容皆位於
// size 之內, 且校驗碼相符. 無效時 ok 為假.
func (rf *File) readBlock(off, size int64) (h blockHeader, content []byte, ok bool, err error) {
	if off < 0 || off+blockHeaderSize > size {
		return h, nil, false, nil
	}
	var hb [blockHeaderSize]byte
	if err := rf.readAt(hb[:], off); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return h, nil, false, nil
		}
		return h, nil, false, err
	}
	if !bytes.Equal(hb[:16], syncMarker) {
		return h, nil, false, nil
	}
	h = blockHeader{
		off:       off,
		c:         le.Uint32(hb[16:]),
		u:         le.Uint32(hb[20:]),
		prevTotal: le.Uint32(hb[24:]),
		tFirst:    int64(le.Uint64(hb[28:])),
		tLast:     int64(le.Uint64(hb[36:])),
		reset:     int64(le.Uint64(hb[44:])),
	}
	if h.end() > size {
		return h, nil, false, nil
	}
	content = make([]byte, h.c)
	if err := rf.readAt(content, off+blockHeaderSize); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return h, nil, false, nil
		}
		return h, nil, false, err
	}
	crc := crc32.Update(crc32.Checksum(hb[:52], castagnoli), castagnoli, content)
	if crc != le.Uint32(hb[52:]) {
		return h, nil, false, nil
	}
	return h, content, true, nil
}

// findMarker 回傳位移不小於 from 的第一個同步標記的位移, 沒有時回傳 -1.
func (rf *File) findMarker(from, size int64) (int64, error) {
	ml := int64(len(syncMarker))
	buf := make([]byte, scanChunk+ml-1)
	for pos := from; pos+ml <= size; {
		n := min(int64(len(buf)), size-pos)
		if err := rf.readAt(buf[:n], pos); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return -1, nil
			}
			return -1, err
		}
		if i := bytes.Index(buf[:n], syncMarker); i >= 0 {
			return pos + int64(i), nil
		}
		if pos+n >= size {
			break
		}
		pos += n - (ml - 1)
	}
	return -1, nil
}

// findMarkerBack 回傳位移小於 before 的最後一個同步標記的位移, 沒有時回傳 -1.
func (rf *File) findMarkerBack(before, size int64) (int64, error) {
	ml := int64(len(syncMarker))
	buf := make([]byte, scanChunk+ml-1)
	end := min(before+ml-1, size)
	for end >= ml {
		start := max(0, end-int64(len(buf)))
		if err := rf.readAt(buf[:end-start], start); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return -1, nil
			}
			return -1, err
		}
		if i := bytes.LastIndex(buf[:end-start], syncMarker); i >= 0 {
			return start + int64(i), nil
		}
		if start == 0 {
			break
		}
		end = start + ml - 1
	}
	return -1, nil
}

// nextValid 回傳位移不小於 from 的第一個有效區塊.
func (rf *File) nextValid(from, size int64) (h blockHeader, content []byte, ok bool, err error) {
	for from < size {
		p, err := rf.findMarker(from, size)
		if err != nil || p < 0 {
			return h, nil, false, err
		}
		if h, content, ok, err = rf.readBlock(p, size); err != nil || ok {
			return h, content, ok, err
		}
		from = p + 1
	}
	return h, nil, false, nil
}

// prevValid 回傳結尾不超過 before 的最後一個有效區塊, 由 before 往前搜尋同步標記.
func (rf *File) prevValid(before, size int64) (h blockHeader, content []byte, ok bool, err error) {
	for before > 0 {
		p, err := rf.findMarkerBack(before, size)
		if err != nil || p < 0 {
			return h, nil, false, err
		}
		h, content, ok, err = rf.readBlock(p, size)
		if err != nil {
			return h, nil, false, err
		}
		if ok && h.end() <= before {
			return h, content, true, nil
		}
		before = p
	}
	return h, nil, false, nil
}

// blockBefore 回傳位移 off 之前的有效區塊. next 為位於 off 的區塊開頭, 已知時先依其位移 24 的
// 欄位找前一個區塊; 該處不是緊接於 off 之前的有效區塊時, 改由 off 往前搜尋同步標記.
func (rf *File) blockBefore(off int64, next *blockHeader, size int64) (h blockHeader, content []byte, ok bool, err error) {
	if next != nil && next.prevTotal > 0 && int64(next.prevTotal) <= off {
		h, content, ok, err = rf.readBlock(off-int64(next.prevTotal), size)
		if err != nil || (ok && h.end() == off) {
			return h, content, ok, err
		}
	}
	return rf.prevValid(off, size)
}

// decodeBlock 解壓縮區塊內容並解出各筆記錄. 解壓失敗, 解壓後長度與區塊開頭不符或記錄無法
// 解析時 ok 為假, 呼叫端視該區塊為無效.
func (rf *File) decodeBlock(h *blockHeader, content []byte, withProcs bool) (recs []Record, ok bool) {
	raw, err := decoder().DecodeAll(content, make([]byte, 0, h.u))
	if err != nil || len(raw) != int(h.u) {
		return nil, false
	}
	for len(raw) > 0 {
		msg, n := protowire.ConsumeBytes(raw)
		if n < 0 {
			return nil, false
		}
		raw = raw[n:]
		rec, ok, err := decodeRecord(msg, rf.ids, withProcs)
		if err != nil {
			return nil, false
		}
		if ok {
			recs = append(recs, rec)
		}
	}
	return recs, true
}
