// Command gpumon 為部署在遠端主機上的 GPU 取樣程式, 由本機 GUI 上傳並啟動.
//
// 目前只實作 --version 與 --help, 取樣模式尚未實作. 所有輸出一律英文.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"gpumon/internal/protocol"
	"gpumon/internal/version"
)

const usageText = `Usage: gpumon [options]

Collect GPU metrics on this host and print them as JSON Lines.

Options:
  --loop            Keep sampling under control of commands from stdin (used by the GUI)
  --interval <ms>   Sampling interval in milliseconds, 200 to 10000 (default 1000)
  --steps <N>       Number of samples to print before exiting (default 1), not valid with --loop
  --version         Print version information and exit
  --help            Print this help and exit
`

// printUsage 將用法說明寫入 w.
func printUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 解析命令列參數並執行對應的模式, 回傳結束碼.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gpumon", flag.ContinueOnError)
	// 錯誤訊息與用法說明由本函數自行輸出, 不使用 flag 套件的預設格式.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	fs.Bool("loop", false, "")
	fs.Int("interval", 1000, "")
	fs.Int("steps", 1, "")
	showVersion := fs.Bool("version", false, "")
	showHelp := fs.Bool("help", false, "")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "gpumon: %v\n", err)
		printUsage(stderr)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gpumon: unexpected argument: %s\n", fs.Arg(0))
		printUsage(stderr)
		return 2
	}

	switch {
	case *showHelp:
		printUsage(stdout)
		return 0
	case *showVersion:
		fmt.Fprintf(stdout, "gpumon %s (protocol %d)\n", version.Full(), protocol.Version)
		return 0
	default:
		fmt.Fprintln(stderr, "gpumon: sampling is not implemented yet")
		return 1
	}
}
