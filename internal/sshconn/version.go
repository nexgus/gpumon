package sshconn

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 程式版號的階段, 數值愈大愈高. 無階段即正式版, 為最高.
const (
	stageAlpha = iota
	stageBeta
	stageRC
	stageNone
)

// ProgramVersion 為程式版號 x.y.z[-stage.N], 不含 commit 與 dirty 字樣.
type ProgramVersion struct {
	Major, Minor, Patch int
	stage               int
	StageNumber         int
}

var programVersionRE = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)

// versionOutputRE 為規格 5.7 所定 --version 輸出的正規表示式.
var versionOutputRE = regexp.MustCompile(
	`^gpumon (0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?\+([0-9a-f]{7}|unknown)(-dirty)? \(protocol ([1-9][0-9]*)\)$`)

// ParseProgramVersion 解析 x.y.z[-stage.N] 形式的程式版號.
func ParseProgramVersion(s string) (ProgramVersion, error) {
	m := programVersionRE.FindStringSubmatch(s)
	if m == nil {
		return ProgramVersion{}, fmt.Errorf("invalid program version %q", s)
	}
	return programVersionFromMatch(m[1:7])
}

// programVersionFromMatch 由正規表示式的六個群組 (x, y, z, 階段段落, 階段, N)
// 組成程式版號. 數字超出 int 範圍時視為不合格式.
func programVersionFromMatch(g []string) (ProgramVersion, error) {
	var v ProgramVersion
	nums := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range nums {
		n, err := strconv.Atoi(g[i])
		if err != nil {
			return ProgramVersion{}, fmt.Errorf("invalid program version number %q", g[i])
		}
		*p = n
	}
	switch g[4] {
	case "":
		v.stage = stageNone
	case "alpha":
		v.stage = stageAlpha
	case "beta":
		v.stage = stageBeta
	case "rc":
		v.stage = stageRC
	}
	if g[4] != "" {
		n, err := strconv.Atoi(g[5])
		if err != nil {
			return ProgramVersion{}, fmt.Errorf("invalid stage number %q", g[5])
		}
		v.StageNumber = n
	}
	return v, nil
}

// Stage 回傳階段名稱, 正式版為空字串.
func (v ProgramVersion) Stage() string {
	switch v.stage {
	case stageAlpha:
		return "alpha"
	case stageBeta:
		return "beta"
	case stageRC:
		return "rc"
	}
	return ""
}

// String 回傳 x.y.z[-stage.N] 形式的字串.
func (v ProgramVersion) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if st := v.Stage(); st != "" {
		s += fmt.Sprintf("-%s.%d", st, v.StageNumber)
	}
	return s
}

// Compare 依規格第 4 章的順序比較兩個程式版號: 先比 x, y, z, 再比階段 (alpha,
// beta, rc, 無階段由低至高), 最後比階段後的數字. v 較低時回傳負值, 相同時回傳
// 0, 較高時回傳正值.
func (v ProgramVersion) Compare(o ProgramVersion) int {
	pairs := [][2]int{
		{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch},
		{v.stage, o.stage}, {v.StageNumber, o.StageNumber},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// RemoteInfo 為自遠端程式 --version 輸出取得的資訊.
type RemoteInfo struct {
	Version  ProgramVersion
	Protocol int
}

// ParseVersionOutput 解析遠端程式 --version 的輸出. 輸出須恰為一行, 去除結尾的
// 一個換行字元後須符合規格 5.7 的正規表示式.
func ParseVersionOutput(out string) (RemoteInfo, error) {
	line := strings.TrimSuffix(out, "\n")
	m := versionOutputRE.FindStringSubmatch(line)
	if m == nil {
		return RemoteInfo{}, fmt.Errorf("unrecognized version output")
	}
	v, err := programVersionFromMatch(m[1:7])
	if err != nil {
		return RemoteInfo{}, err
	}
	proto, err := strconv.Atoi(m[9])
	if err != nil {
		return RemoteInfo{}, fmt.Errorf("invalid protocol version %q", m[9])
	}
	return RemoteInfo{Version: v, Protocol: proto}, nil
}

// uploadAction 為決定是否上傳的結果.
type uploadAction int

const (
	actionUpload uploadAction = iota + 1
	actionUseExisting
	actionCheckRunning
)

// decideUpload 依規格第 4 章的對照表, 由遠端的程式版號 (無法取得時 remote 為
// nil) 與 GUI 的程式版號決定處理方式. 回傳 actionCheckRunning 時, 呼叫端須檢查
// 遠端程式是否正在執行, 再以 decideAfterRunningCheck 決定.
func decideUpload(remote *ProgramVersion, gui ProgramVersion) uploadAction {
	if remote == nil {
		return actionUpload
	}
	if remote.Compare(gui) >= 0 {
		return actionUseExisting
	}
	return actionCheckRunning
}

// decideAfterRunningCheck 處理遠端的程式版號低於 GUI 的情形: 正在執行時不上傳,
// 否則上傳並覆寫.
func decideAfterRunningCheck(running bool) uploadAction {
	if running {
		return actionUseExisting
	}
	return actionUpload
}

// isProgramRunning 判斷 `ls -l /proc/[0-9]*/exe` 的輸出中, 是否有任一列的
// " -> " 之後的文字等於 fullPath, 或等於 fullPath 後接 " (deleted)".
func isProgramRunning(lsOutput, fullPath string) bool {
	for _, line := range strings.Split(lsOutput, "\n") {
		i := strings.Index(line, " -> ")
		if i < 0 {
			continue
		}
		target := strings.TrimSuffix(line[i+len(" -> "):], "\r")
		if target == fullPath || target == fullPath+" (deleted)" {
			return true
		}
	}
	return false
}

// protocolResult 為協定版本比對的結果.
type protocolResult int

const (
	protocolEqual protocolResult = iota + 1
	protocolOlder
	protocolNewer
)

// compareProtocol 比對即將使用的遠端程式的協定版本與 GUI 的協定版本.
func compareProtocol(remote, gui int) protocolResult {
	switch {
	case remote == gui:
		return protocolEqual
	case remote < gui:
		return protocolOlder
	}
	return protocolNewer
}
