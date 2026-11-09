package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gpumon/internal/protocol"
)

// shorthandHelp 為簡寫指令 /help 輸出到 stderr 的說明.
const shorthandHelp = `Commands (JSON lines starting with '{', or shorthand starting with '/'):
  /set_interval [ms]        {"cmd":"set_interval","ms":500}   interval 200 to 10000 ms, default 1000
  /set_fields [field ...]   {"cmd":"set_fields","fields":[...]} omit fields to output all
  /sample_now               {"cmd":"sample_now"}              take a sample immediately
  /ping                     {"cmd":"ping"}                    reply with pong
  /help                     print this help to stderr (shorthand only)
Fields: util mem_used temp power_w clock_gpu clock_mem fan enc dec procs
`

// errBlank 表示該行為空白行, 不視為指令也不回應錯誤.
var errBlank = errors.New("blank line")

// input 為 stdin 一行解析後的結果.
type input struct {
	cmd protocol.Command
	// help 為 true 表示簡寫專用的 /help, 此時 cmd 無意義.
	help bool
}

// parseLine 依首字元分類並解析 stdin 的一行. 以 '{' 開頭為 JSON 指令, 以 '/'
// 開頭為簡寫, 空白行回傳 errBlank, 其餘皆為不合法輸入. 本函數只解析格式,
// 參數範圍由 validateInterval 與 validateFields 檢查, 不認得的 JSON 指令名稱
// 由呼叫端回應錯誤.
func parseLine(line string) (input, error) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return input{}, errBlank
	}
	switch line[0] {
	case '{':
		return parseJSON(line)
	case '/':
		return parseShorthand(line[1:])
	default:
		return input{}, errors.New("invalid input: a command must be a JSON object starting with '{' or a shorthand starting with '/'")
	}
}

// parseJSON 解析一行 JSON 指令.
func parseJSON(line string) (input, error) {
	var c protocol.Command
	if err := json.Unmarshal([]byte(line), &c); err != nil {
		return input{}, fmt.Errorf("invalid JSON command: %v", err)
	}
	if c.Cmd == "" {
		return input{}, errors.New("invalid JSON command: missing \"cmd\"")
	}
	return input{cmd: c}, nil
}

// parseShorthand 解析去除開頭 '/' 之後的簡寫. 指令名稱與 JSON 的 cmd 相同,
// 參數以空白分隔.
func parseShorthand(s string) (input, error) {
	args := strings.Fields(s)
	if len(args) == 0 {
		return input{}, errors.New("invalid shorthand: missing command name after '/'")
	}
	name, args := args[0], args[1:]
	switch name {
	case "help":
		if len(args) > 0 {
			return input{}, errors.New("/help takes no arguments")
		}
		return input{help: true}, nil
	case protocol.CmdSetInterval:
		c := protocol.Command{Cmd: name}
		switch len(args) {
		case 0:
		case 1:
			ms, err := strconv.Atoi(args[0])
			if err != nil {
				return input{}, fmt.Errorf("set_interval: invalid ms %q, expected an integer", args[0])
			}
			c.Ms = &ms
		default:
			return input{}, errors.New("set_interval takes at most one argument")
		}
		return input{cmd: c}, nil
	case protocol.CmdSetFields:
		c := protocol.Command{Cmd: name}
		if len(args) > 0 {
			c.Fields = args
		}
		return input{cmd: c}, nil
	case protocol.CmdSampleNow, protocol.CmdPing:
		if len(args) > 0 {
			return input{}, fmt.Errorf("%s takes no arguments", name)
		}
		return input{cmd: protocol.Command{Cmd: name}}, nil
	default:
		return input{}, fmt.Errorf("unknown command: %s", name)
	}
}

// validateInterval 回傳 set_interval 實際套用的間隔, ms 為 nil 時為預設值.
func validateInterval(ms *int) (int, error) {
	if ms == nil {
		return protocol.DefaultIntervalMs, nil
	}
	if !protocol.ValidInterval(*ms) {
		return 0, fmt.Errorf("set_interval: ms must be between %d and %d, got %d",
			protocol.MinIntervalMs, protocol.MaxIntervalMs, *ms)
	}
	return *ms, nil
}

// fieldSet 為 set_fields 選取的欄位.
type fieldSet map[string]bool

// allFields 回傳選取全部欄位的 fieldSet.
func allFields() fieldSet {
	s := fieldSet{}
	for _, f := range protocol.SampleFields {
		s[f] = true
	}
	return s
}

// validateFields 回傳 set_fields 實際套用的欄位; fields 為 nil 時為全部欄位,
// 含不認得的欄位名稱時回傳錯誤且不套用.
func validateFields(fields []string) (fieldSet, error) {
	if fields == nil {
		return allFields(), nil
	}
	s := fieldSet{}
	for _, f := range fields {
		if !protocol.IsSampleField(f) {
			return nil, fmt.Errorf("set_fields: unknown field %q", f)
		}
		s[f] = true
	}
	return s, nil
}

// list 依 protocol.SampleFields 的順序列出已選取的欄位.
func (s fieldSet) list() []string {
	out := []string{}
	for _, f := range protocol.SampleFields {
		if s[f] {
			out = append(out, f)
		}
	}
	return out
}

// apply 將未選取的欄位自取樣中移除.
func (s fieldSet) apply(g *protocol.GPUSample) {
	if !s[protocol.FieldUtil] {
		g.Util = nil
	}
	if !s[protocol.FieldMemUsed] {
		g.MemUsed = nil
	}
	if !s[protocol.FieldTemp] {
		g.Temp = nil
	}
	if !s[protocol.FieldPowerW] {
		g.PowerW = nil
	}
	if !s[protocol.FieldClockGPU] {
		g.ClockGPU = nil
	}
	if !s[protocol.FieldClockMem] {
		g.ClockMem = nil
	}
	if !s[protocol.FieldFan] {
		g.Fan = nil
	}
	if !s[protocol.FieldEnc] {
		g.Enc = nil
	}
	if !s[protocol.FieldDec] {
		g.Dec = nil
	}
	if !s[protocol.FieldProcs] {
		g.Procs = nil
	}
}
