// 本目錄為產生第三方授權清單的獨立工具 module, 不屬於 gpumon 主 module, 由
// build.sh 以 go run 呼叫.
module licgen

go 1.26.0

require github.com/google/licensecheck v0.3.1
