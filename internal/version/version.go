// Package version 集中管理 gpumon 的版本資訊, 遠端程式與本機 GUI 共用.
//
// 程式版號直接編輯常數 VERSION; GUI 的 commit hash 與建置時間由 build.sh 於建置期以
// -ldflags -X 注入, 未經 build.sh 建置時為 unknown. 遠端程式不注入這兩項, 其版本
// 以檔案內容的雜湊值識別, 見 ContentHash.
package version

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// VERSION 為程式版號, 形式為 x.y.z[-stage.N], stage 為 alpha, beta 或 rc,
// 各數字皆不補零. build.sh 於建置前檢查此格式.
const VERSION = "0.3.0"

// 以下變數於建置 GUI 時注入, 建置遠端程式時不注入.
var (
	// GitCommitHash 為建置當下 commit hash 的前 7 個字元, 工作目錄有未提交的
	// 修改時加上 -dirty.
	GitCommitHash = "unknown"
	// BuildDate 為建置時間, ISO 8601 extended 格式並含時區偏移.
	BuildDate = "unknown"
)

// Full 回傳完整版本字串 x.y.z[-stage.N]+<commit>[-dirty].
func Full() string {
	if GitCommitHash == "unknown" {
		return VERSION
	} else {
		return VERSION + "+" + GitCommitHash
	}
}

// ContentHash 回傳遠端程式檔案內容的 SHA-256 雜湊值, 以小寫十六進位表示, 取前 7 個
// 字元. 遠端程式與 GUI 皆以此函數計算.
func ContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:7]
}

// SelfContentHash 讀取目前執行檔的內容並回傳其 ContentHash.
func SelfContentHash() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	return ContentHash(content), nil
}

// RemoteFull 回傳遠端程式 --version 輸出的完整版本字串 x.y.z[-stage.N]+<內容雜湊值>.
// 讀取自身執行檔失敗時只回傳程式版號.
func RemoteFull() string {
	h, err := SelfContentHash()
	if err != nil {
		return VERSION
	}
	return VERSION + "+" + h
}
