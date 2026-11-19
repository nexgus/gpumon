package recording

import (
	"errors"
	"slices"

	"google.golang.org/protobuf/encoding/protowire"
)

// 版本 2 記錄的 protobuf 欄位編號, 定義見 docs/recording-format-v2.md 第 5 章.
const (
	recTime       = 1
	recSample     = 2
	recError      = 3
	recDisconnect = 4
	recGPUProcs   = 5
	recCPUProcs   = 6

	sampleInterval = 1
	sampleGPUs     = 2

	textText = 1

	changesProcs = 1

	engineValues = 1
	mapKey       = 1
	mapValue     = 2
)

// GPUProc 的欄位編號.
const (
	gpGPU            = 1
	gpPID            = 2
	gpEnded          = 3
	gpUser           = 4
	gpMem            = 5
	gpUtil           = 6
	gpCmd            = 7
	gpEngines        = 8
	gpEngineCapacity = 9
	gpCycles         = 10
	gpTotalCycles    = 11
	gpExtra          = 12
	gpCleared        = 13
)

// CPUProc 的欄位編號.
const (
	cpPID       = 1
	cpStartTime = 2
	cpEnded     = 3
	cpPPID      = 4
	cpUser      = 5
	cpCmd       = 6
	cpCPUTimeMS = 7
	cpMem       = 8
	cpExtra     = 9
	cpCleared   = 10
)

// GPUProc 為 GPU 程序變動記錄中一個程序的變動, 亦用來表示還原後列表中的一個程序 (此時
// Ended 為假, Cleared 為空). 指標欄位為 nil 與 map 為 nil 皆表示無值; map 不為 nil 但沒有
// 元素表示有值而內容為空.
type GPUProc struct {
	GPU            int64
	PID            int64
	Ended          bool
	User           *string
	Mem            *uint64
	Util           *int64
	Cmd            *string
	Engines        map[string]uint64
	EngineCapacity map[string]uint64
	Cycles         map[string]uint64
	TotalCycles    map[string]uint64
	// Extra 為 procs 元素中其他成員組成的 JSON 物件文字.
	Extra *string
	// Cleared 為由有值變為無值的欄位編號.
	Cleared []uint32
}

// CPUProc 為 CPU 程序變動記錄中一個程序的變動, 亦用來表示還原後列表中的一個程序.
type CPUProc struct {
	PID       int64
	StartTime int64
	Ended     bool
	PPID      *int64
	User      *string
	Cmd       *string
	CPUTimeMS *uint64
	Mem       *uint64
	Extra     *string
	Cleared   []uint32
}

var errProto = errors.New("recording: malformed record")

func appendVarintField(b []byte, num protowire.Number, v uint64) []byte {
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func appendBytesField(b []byte, num protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func appendStringField(b []byte, num protowire.Number, v string) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendString(b, v)
}

// appendRecord 把一筆記錄編碼為 Record 訊息, 以 varint 長度為前綴附加於 b 之後.
func appendRecord(b []byte, r *Record) []byte {
	var m []byte
	if r.Time != 0 {
		m = appendVarintField(m, recTime, uint64(r.Time))
	}
	switch r.Kind {
	case KindSample:
		m = appendBytesField(m, recSample, encodeSample(r))
	case KindError:
		m = appendBytesField(m, recError, encodeText(r.Text))
	case KindDisconnect:
		m = appendBytesField(m, recDisconnect, encodeText(r.Text))
	case KindGPUProcs:
		var c []byte
		for i := range r.GPUProcs {
			c = appendBytesField(c, changesProcs, encodeGPUProc(&r.GPUProcs[i]))
		}
		m = appendBytesField(m, recGPUProcs, c)
	case KindCPUProcs:
		var c []byte
		for i := range r.CPUProcs {
			c = appendBytesField(c, changesProcs, encodeCPUProc(&r.CPUProcs[i]))
		}
		m = appendBytesField(m, recCPUProcs, c)
	}
	return protowire.AppendBytes(b, m)
}

func encodeText(s string) []byte {
	if s == "" {
		return nil
	}
	return appendStringField(nil, textText, s)
}

func encodeSample(r *Record) []byte {
	var b []byte
	if r.IntervalMS != 0 {
		b = appendVarintField(b, sampleInterval, uint64(r.IntervalMS))
	}
	for i := range r.GPUs {
		b = appendBytesField(b, sampleGPUs, encodeGPUValues(&r.GPUs[i]))
	}
	return b
}

func encodeGPUValues(g *GPUValues) []byte {
	var b []byte
	for m := Metric(0); m < numMetrics; m++ {
		if g.Has&(1<<m) == 0 {
			continue
		}
		var v uint64
		switch m {
		case MetricTemp:
			v = protowire.EncodeZigZag(g.Temp)
		case MetricMemUsed:
			v = g.MemUsed
		default:
			v = uint64(*g.Int(m))
		}
		b = appendVarintField(b, protowire.Number(m)+1, v)
	}
	return b
}

func encodeEngineValues(m map[string]uint64) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b []byte
	for _, k := range keys {
		var e []byte
		e = appendStringField(e, mapKey, k)
		e = appendVarintField(e, mapValue, m[k])
		b = appendBytesField(b, engineValues, e)
	}
	return b
}

func appendCleared(b []byte, num protowire.Number, cleared []uint32) []byte {
	if len(cleared) == 0 {
		return b
	}
	var p []byte
	for _, c := range cleared {
		p = protowire.AppendVarint(p, uint64(c))
	}
	return appendBytesField(b, num, p)
}

func encodeGPUProc(p *GPUProc) []byte {
	var b []byte
	if p.GPU != 0 {
		b = appendVarintField(b, gpGPU, uint64(p.GPU))
	}
	if p.PID != 0 {
		b = appendVarintField(b, gpPID, uint64(p.PID))
	}
	if p.Ended {
		b = appendVarintField(b, gpEnded, 1)
	}
	if p.User != nil {
		b = appendStringField(b, gpUser, *p.User)
	}
	if p.Mem != nil {
		b = appendVarintField(b, gpMem, *p.Mem)
	}
	if p.Util != nil {
		b = appendVarintField(b, gpUtil, uint64(*p.Util))
	}
	if p.Cmd != nil {
		b = appendStringField(b, gpCmd, *p.Cmd)
	}
	for i, m := range []map[string]uint64{p.Engines, p.EngineCapacity, p.Cycles, p.TotalCycles} {
		if m != nil {
			b = appendBytesField(b, protowire.Number(gpEngines+i), encodeEngineValues(m))
		}
	}
	if p.Extra != nil {
		b = appendStringField(b, gpExtra, *p.Extra)
	}
	return appendCleared(b, gpCleared, p.Cleared)
}

func encodeCPUProc(p *CPUProc) []byte {
	var b []byte
	if p.PID != 0 {
		b = appendVarintField(b, cpPID, uint64(p.PID))
	}
	if p.StartTime != 0 {
		b = appendVarintField(b, cpStartTime, uint64(p.StartTime))
	}
	if p.Ended {
		b = appendVarintField(b, cpEnded, 1)
	}
	if p.PPID != nil {
		b = appendVarintField(b, cpPPID, uint64(*p.PPID))
	}
	if p.User != nil {
		b = appendStringField(b, cpUser, *p.User)
	}
	if p.Cmd != nil {
		b = appendStringField(b, cpCmd, *p.Cmd)
	}
	if p.CPUTimeMS != nil {
		b = appendVarintField(b, cpCPUTimeMS, *p.CPUTimeMS)
	}
	if p.Mem != nil {
		b = appendVarintField(b, cpMem, *p.Mem)
	}
	if p.Extra != nil {
		b = appendStringField(b, cpExtra, *p.Extra)
	}
	return appendCleared(b, cpCleared, p.Cleared)
}

// field 為解碼中的一個欄位. 型別不符預期的欄位由呼叫端視為未知欄位略過.
type field struct {
	num protowire.Number
	typ protowire.Type
	v   uint64
	b   []byte
}

// eachField 依序解出訊息中的每個欄位並呼叫 fn. 長度前綴型的欄位內容放在 b, varint 型放在 v,
// 其他型別 (fixed32, fixed64 與群組) 只略過.
func eachField(msg []byte, fn func(f *field) error) error {
	var f field
	for len(msg) > 0 {
		num, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return errProto
		}
		msg = msg[n:]
		f = field{num: num, typ: typ}
		switch typ {
		case protowire.VarintType:
			f.v, n = protowire.ConsumeVarint(msg)
		case protowire.BytesType:
			f.b, n = protowire.ConsumeBytes(msg)
		default:
			n = protowire.ConsumeFieldValue(num, typ, msg)
		}
		if n < 0 {
			return errProto
		}
		msg = msg[n:]
		if typ == protowire.VarintType || typ == protowire.BytesType {
			if err := fn(&f); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeRecord 解碼一筆 Record 訊息. withProcs 為假時程序變動記錄不解析內容, ok 為假.
// oneof 未設定的記錄 ok 亦為假, 由呼叫端略過.
func decodeRecord(msg []byte, ids []int, withProcs bool) (rec Record, ok bool, err error) {
	var body []byte
	err = eachField(msg, func(f *field) error {
		switch {
		case f.num == recTime && f.typ == protowire.VarintType:
			rec.Time = int64(f.v)
		case f.typ != protowire.BytesType:
		case f.num == recSample:
			rec.Kind, body = KindSample, f.b
		case f.num == recError:
			rec.Kind, body = KindError, f.b
		case f.num == recDisconnect:
			rec.Kind, body = KindDisconnect, f.b
		case f.num == recGPUProcs:
			rec.Kind, body = KindGPUProcs, f.b
		case f.num == recCPUProcs:
			rec.Kind, body = KindCPUProcs, f.b
		}
		return nil
	})
	if err != nil || rec.Kind == 0 {
		return rec, false, err
	}
	switch rec.Kind {
	case KindSample:
		err = decodeSample(body, ids, &rec)
	case KindError, KindDisconnect:
		rec.Text, err = decodeText(body)
	case KindGPUProcs:
		if !withProcs {
			return rec, false, nil
		}
		err = eachField(body, func(f *field) error {
			if f.num != changesProcs || f.typ != protowire.BytesType {
				return nil
			}
			p, err := decodeGPUProc(f.b)
			rec.GPUProcs = append(rec.GPUProcs, p)
			return err
		})
	case KindCPUProcs:
		if !withProcs {
			return rec, false, nil
		}
		err = eachField(body, func(f *field) error {
			if f.num != changesProcs || f.typ != protowire.BytesType {
				return nil
			}
			p, err := decodeCPUProc(f.b)
			rec.CPUProcs = append(rec.CPUProcs, p)
			return err
		})
	}
	return rec, err == nil, err
}

func decodeText(b []byte) (string, error) {
	var s string
	err := eachField(b, func(f *field) error {
		if f.num == textText && f.typ == protowire.BytesType {
			s = string(f.b)
		}
		return nil
	})
	return s, err
}

func decodeSample(b []byte, ids []int, rec *Record) error {
	rec.GPUs = make([]GPUValues, len(ids))
	for i, id := range ids {
		rec.GPUs[i].ID = id
	}
	i := 0
	return eachField(b, func(f *field) error {
		switch {
		case f.num == sampleInterval && f.typ == protowire.VarintType:
			rec.IntervalMS = uint32(f.v)
		case f.num == sampleGPUs && f.typ == protowire.BytesType:
			if i < len(rec.GPUs) {
				if err := decodeGPUValues(f.b, &rec.GPUs[i]); err != nil {
					return err
				}
			}
			i++
		}
		return nil
	})
}

func decodeGPUValues(b []byte, g *GPUValues) error {
	return eachField(b, func(f *field) error {
		if f.typ != protowire.VarintType || f.num < 1 || f.num > protowire.Number(numMetrics) {
			return nil
		}
		m := Metric(f.num - 1)
		switch m {
		case MetricTemp:
			g.set(m, protowire.DecodeZigZag(f.v))
		case MetricMemUsed:
			g.Has |= 1 << m
			g.MemUsed = f.v
		default:
			g.set(m, int64(f.v))
		}
		return nil
	})
}

func decodeEngineValues(b []byte) (map[string]uint64, error) {
	m := map[string]uint64{}
	err := eachField(b, func(f *field) error {
		if f.num != engineValues || f.typ != protowire.BytesType {
			return nil
		}
		var k string
		var v uint64
		err := eachField(f.b, func(e *field) error {
			switch {
			case e.num == mapKey && e.typ == protowire.BytesType:
				k = string(e.b)
			case e.num == mapValue && e.typ == protowire.VarintType:
				v = e.v
			}
			return nil
		})
		m[k] = v
		return err
	})
	return m, err
}

// decodeCleared 解出 cleared 欄位, packed 與逐筆兩種編碼皆接受.
func decodeCleared(dst []uint32, f *field) ([]uint32, error) {
	if f.typ == protowire.VarintType {
		return append(dst, uint32(f.v)), nil
	}
	b := f.b
	for len(b) > 0 {
		v, n := protowire.ConsumeVarint(b)
		if n < 0 {
			return dst, errProto
		}
		dst = append(dst, uint32(v))
		b = b[n:]
	}
	return dst, nil
}

func ptr[T any](v T) *T { return &v }

func decodeGPUProc(b []byte) (GPUProc, error) {
	var p GPUProc
	err := eachField(b, func(f *field) error {
		varint, bytes := f.typ == protowire.VarintType, f.typ == protowire.BytesType
		var err error
		switch {
		case f.num == gpGPU && varint:
			p.GPU = int64(f.v)
		case f.num == gpPID && varint:
			p.PID = int64(f.v)
		case f.num == gpEnded && varint:
			p.Ended = f.v != 0
		case f.num == gpUser && bytes:
			p.User = ptr(string(f.b))
		case f.num == gpMem && varint:
			p.Mem = ptr(f.v)
		case f.num == gpUtil && varint:
			p.Util = ptr(int64(f.v))
		case f.num == gpCmd && bytes:
			p.Cmd = ptr(string(f.b))
		case f.num >= gpEngines && f.num <= gpTotalCycles && bytes:
			var m map[string]uint64
			m, err = decodeEngineValues(f.b)
			*p.engineField(int(f.num)) = m
		case f.num == gpExtra && bytes:
			p.Extra = ptr(string(f.b))
		case f.num == gpCleared && (varint || bytes):
			p.Cleared, err = decodeCleared(p.Cleared, f)
		}
		return err
	})
	return p, err
}

// engineField 回傳欄位編號 num (8 至 11) 對應的 EngineValues 欄位.
func (p *GPUProc) engineField(num int) *map[string]uint64 {
	switch num {
	case gpEngines:
		return &p.Engines
	case gpEngineCapacity:
		return &p.EngineCapacity
	case gpCycles:
		return &p.Cycles
	}
	return &p.TotalCycles
}

func decodeCPUProc(b []byte) (CPUProc, error) {
	var p CPUProc
	err := eachField(b, func(f *field) error {
		varint, bytes := f.typ == protowire.VarintType, f.typ == protowire.BytesType
		var err error
		switch {
		case f.num == cpPID && varint:
			p.PID = int64(f.v)
		case f.num == cpStartTime && varint:
			p.StartTime = int64(f.v)
		case f.num == cpEnded && varint:
			p.Ended = f.v != 0
		case f.num == cpPPID && varint:
			p.PPID = ptr(int64(f.v))
		case f.num == cpUser && bytes:
			p.User = ptr(string(f.b))
		case f.num == cpCmd && bytes:
			p.Cmd = ptr(string(f.b))
		case f.num == cpCPUTimeMS && varint:
			p.CPUTimeMS = ptr(f.v)
		case f.num == cpMem && varint:
			p.Mem = ptr(f.v)
		case f.num == cpExtra && bytes:
			p.Extra = ptr(string(f.b))
		case f.num == cpCleared && (varint || bytes):
			p.Cleared, err = decodeCleared(p.Cleared, f)
		}
		return err
	})
	return p, err
}
