package recording

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// GPUProcs 為單張 GPU 在一筆 sample 訊息內的程序列表, Procs 的每個元素為 procs 陣列的一個 JSON 物件.
type GPUProcs struct {
	GPU   int
	Procs []json.RawMessage
}

// gpuKey 以所在 GPU 的 id 加上程序編號識別一個 GPU 程序.
type gpuKey struct{ gpu, pid int64 }

// cpuKey 以程序編號加上啟動時刻識別一個 CPU 程序.
type cpuKey struct{ pid, start int64 }

func (p *GPUProc) key() gpuKey { return gpuKey{p.GPU, p.PID} }
func (p *CPUProc) key() cpuKey { return cpuKey{p.PID, p.StartTime} }

var jsonNull = []byte("null")

// parseGPUProc 把 procs 陣列的一個元素轉為 GPUProc. 元素須為 JSON 物件, 且 pid 須為整數.
// 規格所列的成員依型別存入對應欄位; 其餘成員, 以及值為 null 或型別不符而存不進對應欄位的
// 成員, 存入 Extra, 以免遺失資訊.
func parseGPUProc(gpu int64, raw json.RawMessage) (GPUProc, error) {
	p := GPUProc{GPU: gpu}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return p, errors.New("recording: process element is not a JSON object")
	}
	extra := map[string]json.RawMessage{}
	hasPID := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return p, fmt.Errorf("recording: invalid process element: %w", err)
		}
		key := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return p, fmt.Errorf("recording: invalid process element: %w", err)
		}
		typed := !bytes.Equal(v, jsonNull) && p.setMember(key, v)
		if key == "pid" {
			hasPID = typed
		}
		if typed {
			delete(extra, key)
		} else {
			p.clearMember(key)
			extra[key] = v
		}
	}
	if _, err := dec.Token(); err != nil {
		return p, fmt.Errorf("recording: invalid process element: %w", err)
	}
	if !hasPID {
		return p, errors.New("recording: process element has no integer pid")
	}
	if len(extra) > 0 {
		s, err := encodeExtra(extra)
		if err != nil {
			return p, err
		}
		p.Extra = &s
	}
	return p, nil
}

// setMember 把成員 key 的值存入對應欄位; 不是規格所列的成員或型別不符時回傳假.
func (p *GPUProc) setMember(key string, v json.RawMessage) bool {
	switch key {
	case "pid":
		return json.Unmarshal(v, &p.PID) == nil
	case "user":
		return unmarshalPtr(v, &p.User)
	case "mem":
		return unmarshalPtr(v, &p.Mem)
	case "util":
		return unmarshalPtr(v, &p.Util)
	case "cmd":
		return unmarshalPtr(v, &p.Cmd)
	case "engines":
		return unmarshalMap(v, &p.Engines)
	case "engine_capacity":
		return unmarshalMap(v, &p.EngineCapacity)
	case "cycles":
		return unmarshalMap(v, &p.Cycles)
	case "total_cycles":
		return unmarshalMap(v, &p.TotalCycles)
	}
	return false
}

func (p *GPUProc) clearMember(key string) {
	switch key {
	case "user":
		p.User = nil
	case "mem":
		p.Mem = nil
	case "util":
		p.Util = nil
	case "cmd":
		p.Cmd = nil
	case "engines":
		p.Engines = nil
	case "engine_capacity":
		p.EngineCapacity = nil
	case "cycles":
		p.Cycles = nil
	case "total_cycles":
		p.TotalCycles = nil
	}
}

func unmarshalPtr[T any](v json.RawMessage, dst **T) bool {
	var x T
	if json.Unmarshal(v, &x) != nil {
		return false
	}
	*dst = &x
	return true
}

func unmarshalMap(v json.RawMessage, dst *map[string]uint64) bool {
	m := map[string]uint64{}
	if json.Unmarshal(v, &m) != nil {
		return false
	}
	*dst = m
	return true
}

// encodeExtra 把成員組成鍵依字典序排列的 JSON 物件文字, 值壓縮空白後原樣存放.
func encodeExtra(members map[string]json.RawMessage) (string, error) {
	b := []byte{'{'}
	for i, k := range slices.Sorted(maps.Keys(members)) {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendJSON(b, k)
		var c bytes.Buffer
		if err := json.Compact(&c, members[k]); err != nil {
			return "", fmt.Errorf("recording: invalid process element: %w", err)
		}
		b = append(b, ':')
		b = append(b, c.Bytes()...)
	}
	return string(append(b, '}')), nil
}

// appendJSON 以 JSON 編碼 v 並附加於 b 之後, 不跳脫 HTML 字元.
func appendJSON(b []byte, v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return append(b, "null"...)
	}
	return append(b, bytes.TrimRight(buf.Bytes(), "\n")...)
}

// diffPtr 比較一個 optional 欄位, 有變動時寫入 out 或 cleared, 回傳是否有變動.
func diffPtr[T comparable](prev, cur *T, out **T, cleared *[]uint32, num uint32) bool {
	switch {
	case cur == nil && prev == nil:
		return false
	case cur == nil:
		*cleared = append(*cleared, num)
		return true
	case prev == nil || *prev != *cur:
		*out = cur
		return true
	}
	return false
}

func diffMap(prev, cur map[string]uint64, out *map[string]uint64, cleared *[]uint32, num uint32) bool {
	switch {
	case cur == nil && prev == nil:
		return false
	case cur == nil:
		*cleared = append(*cleared, num)
		return true
	case prev == nil || !maps.Equal(prev, cur):
		*out = cur
		return true
	}
	return false
}

// diffGPU 依 docs/recording-format-v2.md 5.4 比較前一次與本次取樣的 GPU 程序列表, 回傳要寫入的變動.
// cur 中識別相同的程序只取第一個. 只在前一次取樣列表中的程序依識別欄位排序後列於最後.
func diffGPU(prev map[gpuKey]*GPUProc, cur []GPUProc) []GPUProc {
	var out []GPUProc
	seen := make(map[gpuKey]bool, len(cur))
	for i := range cur {
		c := &cur[i]
		k := c.key()
		if seen[k] {
			continue
		}
		seen[k] = true
		p := prev[k]
		if p == nil {
			out = append(out, *c)
			continue
		}
		ch := GPUProc{GPU: c.GPU, PID: c.PID}
		changed := diffPtr(p.User, c.User, &ch.User, &ch.Cleared, gpUser)
		changed = diffPtr(p.Mem, c.Mem, &ch.Mem, &ch.Cleared, gpMem) || changed
		changed = diffPtr(p.Util, c.Util, &ch.Util, &ch.Cleared, gpUtil) || changed
		changed = diffPtr(p.Cmd, c.Cmd, &ch.Cmd, &ch.Cleared, gpCmd) || changed
		changed = diffMap(p.Engines, c.Engines, &ch.Engines, &ch.Cleared, gpEngines) || changed
		changed = diffMap(p.EngineCapacity, c.EngineCapacity, &ch.EngineCapacity, &ch.Cleared, gpEngineCapacity) || changed
		changed = diffMap(p.Cycles, c.Cycles, &ch.Cycles, &ch.Cleared, gpCycles) || changed
		changed = diffMap(p.TotalCycles, c.TotalCycles, &ch.TotalCycles, &ch.Cleared, gpTotalCycles) || changed
		changed = diffPtr(p.Extra, c.Extra, &ch.Extra, &ch.Cleared, gpExtra) || changed
		if changed {
			out = append(out, ch)
		}
	}
	var ended []gpuKey
	for k := range prev {
		if !seen[k] {
			ended = append(ended, k)
		}
	}
	slices.SortFunc(ended, func(a, b gpuKey) int { return cmp.Or(cmp.Compare(a.gpu, b.gpu), cmp.Compare(a.pid, b.pid)) })
	for _, k := range ended {
		out = append(out, GPUProc{GPU: k.gpu, PID: k.pid, Ended: true})
	}
	return out
}

// gpuList 為還原中的 GPU 程序列表.
type gpuList map[gpuKey]*GPUProc

// apply 依 docs/recording-format-v2.md 6.5 套用一筆 GPU 程序變動記錄.
func (l gpuList) apply(changes []GPUProc) {
	for i := range changes {
		c := &changes[i]
		k := c.key()
		if c.Ended {
			delete(l, k)
			continue
		}
		p := l[k]
		if p == nil {
			p = &GPUProc{GPU: c.GPU, PID: c.PID}
			l[k] = p
		}
		if c.User != nil {
			p.User = c.User
		}
		if c.Mem != nil {
			p.Mem = c.Mem
		}
		if c.Util != nil {
			p.Util = c.Util
		}
		if c.Cmd != nil {
			p.Cmd = c.Cmd
		}
		for num := gpEngines; num <= gpTotalCycles; num++ {
			if m := *c.engineField(num); m != nil {
				*p.engineField(num) = m
			}
		}
		if c.Extra != nil {
			p.Extra = c.Extra
		}
		for _, num := range c.Cleared {
			switch num {
			case gpUser:
				p.User = nil
			case gpMem:
				p.Mem = nil
			case gpUtil:
				p.Util = nil
			case gpCmd:
				p.Cmd = nil
			case gpEngines, gpEngineCapacity, gpCycles, gpTotalCycles:
				*p.engineField(int(num)) = nil
			case gpExtra:
				p.Extra = nil
			}
		}
	}
}

// json 回傳列表的 JSON 陣列, 格式與版本 1 的程序記錄相同, 每個元素為一個 procs 元素, 另加成員
// gpu. 元素依 GPU id 與程序編號排序.
func (l gpuList) json() json.RawMessage {
	keys := slices.SortedFunc(maps.Keys(l), func(a, b gpuKey) int {
		return cmp.Or(cmp.Compare(a.gpu, b.gpu), cmp.Compare(a.pid, b.pid))
	})
	b := []byte{'['}
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = l[k].appendJSON(b)
	}
	return append(b, ']')
}

// appendJSON 把程序還原為 procs 元素的 JSON 物件並附加成員 gpu.
func (p *GPUProc) appendJSON(b []byte) []byte {
	b = append(b, `{"pid":`...)
	b = strconv.AppendInt(b, p.PID, 10)
	member := func(name string, v any) {
		b = append(b, `,"`...)
		b = append(b, name...)
		b = append(b, `":`...)
		b = appendJSON(b, v)
	}
	if p.User != nil {
		member("user", *p.User)
	}
	if p.Mem != nil {
		member("mem", *p.Mem)
	}
	if p.Util != nil {
		member("util", *p.Util)
	}
	if p.Cmd != nil {
		member("cmd", *p.Cmd)
	}
	for i, name := range []string{"engines", "engine_capacity", "cycles", "total_cycles"} {
		if m := *p.engineField(gpEngines + i); m != nil {
			member(name, m)
		}
	}
	if p.Extra != nil {
		e := bytes.TrimSpace([]byte(*p.Extra))
		if len(e) > 2 && e[0] == '{' && e[len(e)-1] == '}' && json.Valid(e) {
			b = append(b, ',')
			b = append(b, e[1:len(e)-1]...)
		}
	}
	b = append(b, `,"gpu":`...)
	b = strconv.AppendInt(b, p.GPU, 10)
	return append(b, '}')
}

// cpuList 為還原中的 CPU 程序列表.
type cpuList map[cpuKey]*CPUProc

// apply 依 docs/recording-format-v2.md 6.5 套用一筆 CPU 程序變動記錄.
func (l cpuList) apply(changes []CPUProc) {
	for i := range changes {
		c := &changes[i]
		k := c.key()
		if c.Ended {
			delete(l, k)
			continue
		}
		p := l[k]
		if p == nil {
			p = &CPUProc{PID: c.PID, StartTime: c.StartTime}
			l[k] = p
		}
		if c.PPID != nil {
			p.PPID = c.PPID
		}
		if c.User != nil {
			p.User = c.User
		}
		if c.Cmd != nil {
			p.Cmd = c.Cmd
		}
		if c.CPUTimeMS != nil {
			p.CPUTimeMS = c.CPUTimeMS
		}
		if c.Mem != nil {
			p.Mem = c.Mem
		}
		if c.Extra != nil {
			p.Extra = c.Extra
		}
		for _, num := range c.Cleared {
			switch num {
			case cpPPID:
				p.PPID = nil
			case cpUser:
				p.User = nil
			case cpCmd:
				p.Cmd = nil
			case cpCPUTimeMS:
				p.CPUTimeMS = nil
			case cpMem:
				p.Mem = nil
			case cpExtra:
				p.Extra = nil
			}
		}
	}
}
