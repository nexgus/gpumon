// Command gpumon 為部署在遠端主機上的 GPU 取樣程式, 由本機 GUI 上傳並啟動.
//
// 不帶 --loop 時輸出一行 devices 與 --steps 行 sample 後結束, 供手動除錯;
// 帶 --loop 時持續取樣, 由 stdin 的指令控制, 供 GUI 使用. 所有輸出一律英文.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"gpumon/internal/gpu"
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

In --loop mode, type /help for the commands accepted on stdin.
`

// printUsage 將用法說明寫入 w.
func printUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// options 為解析後的命令列參數.
type options struct {
	loop        bool
	intervalMs  int
	steps       int
	showVersion bool
	showHelp    bool
}

// parseArgs 解析命令列參數並檢查值的範圍. 回傳 flag.ErrHelp 表示使用者要求
// 用法說明 (-h).
func parseArgs(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("gpumon", flag.ContinueOnError)
	// 錯誤訊息與用法說明由呼叫端自行輸出, 不使用 flag 套件的預設格式.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	fs.BoolVar(&o.loop, "loop", false, "")
	fs.IntVar(&o.intervalMs, "interval", protocol.DefaultIntervalMs, "")
	fs.IntVar(&o.steps, "steps", 1, "")
	fs.BoolVar(&o.showVersion, "version", false, "")
	fs.BoolVar(&o.showHelp, "help", false, "")

	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	if o.showHelp || o.showVersion {
		return o, nil
	}
	stepsSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "steps" {
			stepsSet = true
		}
	})
	if o.loop && stepsSet {
		return o, errors.New("--steps cannot be used with --loop")
	}
	if !protocol.ValidInterval(o.intervalMs) {
		return o, fmt.Errorf("--interval must be between %d and %d, got %d",
			protocol.MinIntervalMs, protocol.MaxIntervalMs, o.intervalMs)
	}
	if o.steps < 1 {
		return o, fmt.Errorf("--steps must be at least 1, got %d", o.steps)
	}
	return o, nil
}

// run 解析命令列參數並執行對應的模式, 回傳結束碼.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, err := parseArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "gpumon: %v\n", err)
		printUsage(stderr)
		return 2
	}

	switch {
	case o.showHelp:
		printUsage(stdout)
		return 0
	case o.showVersion:
		fmt.Fprintf(stdout, "gpumon %s (protocol %d)\n", version.Full(), protocol.Version)
		return 0
	}

	// 註冊 SIGPIPE 後, 寫入已關閉的 stdout 會得到 EPIPE 錯誤而不是直接被終止,
	// 主迴圈得以照常結束並呼叫 nvmlShutdown. 通知本身不需處理.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)

	logf := func(format string, args ...any) {
		fmt.Fprintf(stderr, "gpumon: "+format+"\n", args...)
	}
	col := gpu.Open(logf)
	hostname, _ := os.Hostname()

	cfg := loopConfig{intervalMs: o.intervalMs, stdin: stdin, watchdog: protocol.WatchdogTimeout}
	if !o.loop {
		cfg.steps = o.steps
	}
	return runLoop(cfg, col, stdout, stderr, hostname, version.VERSION)
}
