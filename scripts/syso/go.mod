// 本目錄為產生 Windows 執行檔圖示資源 (.syso) 的獨立工具 module, 不屬於 gpumon 主
// module, 由 build.sh 以 go run 呼叫.
module syso

go 1.26.0

require github.com/tc-hib/winres v0.3.1

require (
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	golang.org/x/image v0.45.0 // indirect
)
