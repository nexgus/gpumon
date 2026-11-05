// Package version 集中管理 gpumon 的版本資訊, 遠端程式與本機 GUI 共用.
//
// 程式版號直接編輯常數 VERSION; commit hash 與建置時間由 build.sh 於建置期以
// -ldflags -X 注入, 未經 build.sh 建置時為 unknown.
package version

// VERSION 為程式版號, 形式為 x.y.z[-stage.N], stage 為 alpha, beta 或 rc,
// 各數字皆不補零. build.sh 於建置前檢查此格式.
const VERSION = "0.1.0"

// 以下變數於建置期注入.
var (
	// GitCommitHash 為建置當下 commit hash 的前 7 個字元, 工作目錄有未提交的
	// 修改時加上 -dirty.
	GitCommitHash = "unknown"
	// BuildDate 為建置時間, ISO 8601 extended 格式並含時區偏移.
	BuildDate = "unknown"
)

// Full 回傳完整版本字串 x.y.z[-stage.N]+<commit>[-dirty].
func Full() string {
	return VERSION + "+" + GitCommitHash
}
