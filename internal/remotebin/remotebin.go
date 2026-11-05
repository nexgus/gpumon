// Package remotebin 內嵌 linux/amd64 的遠端程式, 供本機 GUI 上傳至遠端主機.
//
// gpumon.bin 由 build.sh 於建置 GUI 之前複製至本目錄, 不入版控.
package remotebin

import _ "embed"

// Binary 為遠端程式執行檔的內容.
//
//go:embed gpumon.bin
var Binary []byte
