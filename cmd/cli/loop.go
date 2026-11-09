package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gpumon/internal/protocol"
)

// sampleTimeout 為單次取樣的逾時. NVML 偶爾會卡住 (例如 GPU 重設中), 逾時即
// 輸出 error 訊息, 主迴圈不等待.
const sampleTimeout = 3 * time.Second

// maxLineLen 為 stdin 單行的最大長度, 超過者整行捨棄並回應 error 訊息.
const maxLineLen = 64 * 1024

// collector 為主迴圈所需的取樣介面, 由 gpu.Collector 實作, 測試時可替換.
type collector interface {
	Devices() []protocol.Device
	Sample(withProcs bool) []protocol.GPUSample
	Close()
}

// clock 產生 ts. 啟動時讀取一次系統時鐘, 之後每筆 ts 為該讀數加上單調時鐘
// 量得的經過時間, 不受遠端校時影響.
type clock struct {
	start time.Time
}

func newClock() clock {
	return clock{start: time.Now()}
}

// now 回傳目前的 ts. time.Since 以 time.Now 內含的單調時鐘讀數計算經過時間.
func (c clock) now() protocol.Timestamp {
	wall := c.start.UnixNano() + time.Since(c.start).Nanoseconds()
	return protocol.Timestamp(float64(wall/int64(time.Millisecond)) / 1000)
}

// output 將訊息逐行寫入 stdout. os.Stdout 沒有使用者層級的緩衝, 每行以一次
// Write 寫出, 即每行後 flush.
type output struct {
	w io.Writer
}

// send 將 v 編碼為一行 JSON 寫出; 寫入失敗 (例如 EPIPE) 時回傳錯誤.
func (o output) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = o.w.Write(b)
	return err
}

// stdinLine 為 stdin 讀到的一行; tooLong 為 true 時該行超過 maxLineLen 而被捨棄.
type stdinLine struct {
	text    string
	tooLong bool
}

// readLines 逐行讀取 r 並送入 ch, 讀到 EOF 或錯誤時關閉 ch.
func readLines(r io.Reader, ch chan<- stdinLine) {
	defer close(ch)
	br := bufio.NewReaderSize(r, maxLineLen)
	for {
		b, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// 捨棄本行其餘部分直到換行字元.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			ch <- stdinLine{tooLong: true}
			if err != nil {
				return
			}
			continue
		}
		if len(b) > 0 {
			ch <- stdinLine{text: string(b)}
		}
		if err != nil {
			return
		}
	}
}

// loopConfig 為主迴圈的設定.
type loopConfig struct {
	intervalMs int
	// steps 大於 0 時為不帶 --loop 的模式, 輸出 steps 筆 sample 後結束, 不讀
	// stdin, 也不啟用看門狗.
	steps int
	stdin io.Reader
	// watchdog 為看門狗逾時, 測試時可縮短.
	watchdog time.Duration
	// signals 為結束訊號的來源, nil 時自行向作業系統註冊.
	signals <-chan os.Signal
}

// sampleResult 為取樣 goroutine 的結果.
type sampleResult struct {
	ts   protocol.Timestamp
	gpus []protocol.GPUSample
}

// runLoop 輸出 devices 訊息後進入主迴圈, 直到任一結束條件成立, 回傳結束碼.
// 結束條件為 stdin EOF, stdout 寫入失敗, 收到 SIGHUP, SIGTERM 或 SIGINT, 以及
// 看門狗逾時; 其中 stdin EOF 與看門狗只適用於 --loop 模式, 不帶 --loop 時另於
// 輸出 steps 筆 sample 後結束.
func runLoop(cfg loopConfig, col collector, stdout, stderr io.Writer, hostname, progVersion string) int {
	logf := func(format string, args ...any) {
		fmt.Fprintf(stderr, "gpumon: "+format+"\n", args...)
	}
	clk := newClock()
	out := output{w: stdout}

	sigs := cfg.signals
	if sigs == nil {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(ch)
		sigs = ch
	}

	busy := false
	results := make(chan sampleResult, 1)
	defer func() { closeCollector(col, busy, results) }()

	devs := protocol.Devices{
		Type:     protocol.TypeDevices,
		Protocol: protocol.Version,
		Version:  progVersion,
		Hostname: hostname,
		GPUs:     col.Devices(),
	}
	if err := out.send(devs); err != nil {
		logf("stdout write failed, exiting: %v", err)
		return 1
	}

	var lines chan stdinLine
	var watchdog <-chan time.Time
	var watchdogTimer *time.Timer
	if cfg.steps == 0 {
		lines = make(chan stdinLine, 16)
		go readLines(cfg.stdin, lines)
		watchdogTimer = time.NewTimer(cfg.watchdog)
		defer watchdogTimer.Stop()
		watchdog = watchdogTimer.C
	}

	fields := allFields()
	interval := time.Duration(cfg.intervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timeout := time.NewTimer(sampleTimeout)
	timeout.Stop()
	timedOut := false
	written := 0

	start := func() {
		if busy {
			return
		}
		busy = true
		timedOut = false
		withProcs := fields[protocol.FieldProcs]
		ts := clk.now()
		go func() {
			results <- sampleResult{ts: ts, gpus: col.Sample(withProcs)}
		}()
		timeout.Reset(sampleTimeout)
	}
	sendError := func(msg string) error {
		return out.send(protocol.Error{Type: protocol.TypeError, TS: clk.now(), Msg: msg})
	}

	start()
	for {
		select {
		case <-ticker.C:
			start()

		case r := <-results:
			busy = false
			if timedOut {
				// 逾時後才回來的結果已過時, 捨棄.
				timedOut = false
				continue
			}
			timeout.Stop()
			for i := range r.gpus {
				fields.apply(&r.gpus[i])
			}
			if err := out.send(protocol.Sample{Type: protocol.TypeSample, TS: r.ts, GPUs: r.gpus}); err != nil {
				logf("stdout write failed, exiting: %v", err)
				return 1
			}
			written++
			if cfg.steps > 0 && written >= cfg.steps {
				return 0
			}

		case <-timeout.C:
			timedOut = true
			if err := sendError(fmt.Sprintf("sampling timed out after %s", sampleTimeout)); err != nil {
				logf("stdout write failed, exiting: %v", err)
				return 1
			}
			if cfg.steps > 0 {
				written++
				if written >= cfg.steps {
					return 1
				}
			}

		case l, ok := <-lines:
			if !ok {
				logf("stdin closed, exiting")
				return 0
			}
			watchdogTimer.Reset(cfg.watchdog)
			var err error
			if l.tooLong {
				err = sendError(fmt.Sprintf("invalid input: line longer than %d bytes", maxLineLen))
			} else {
				err = handleLine(l.text, out, stderr, sendError, &fields, ticker, start)
			}
			if err != nil {
				logf("stdout write failed, exiting: %v", err)
				return 1
			}

		case s := <-sigs:
			logf("received %v, exiting", s)
			return 0

		case <-watchdog:
			logf("no input for %s, exiting", cfg.watchdog)
			return 1
		}
	}
}

// handleLine 解析並執行 stdin 的一行, 回傳的錯誤只代表 stdout 寫入失敗; 指令
// 本身的錯誤以 error 訊息回應.
func handleLine(text string, out output, stderr io.Writer, sendError func(string) error,
	fields *fieldSet, ticker *time.Ticker, sampleNow func()) error {
	in, err := parseLine(text)
	if errors.Is(err, errBlank) {
		return nil
	}
	if err != nil {
		return sendError(err.Error())
	}
	if in.help {
		fmt.Fprint(stderr, shorthandHelp)
		return nil
	}
	c := in.cmd
	switch c.Cmd {
	case protocol.CmdSetInterval:
		ms, err := validateInterval(c.Ms)
		if err != nil {
			return sendError(err.Error())
		}
		ticker.Reset(time.Duration(ms) * time.Millisecond)
		return out.send(protocol.Ack{Type: protocol.TypeAck, Cmd: c.Cmd, Ms: &ms})
	case protocol.CmdSetFields:
		fs, err := validateFields(c.Fields)
		if err != nil {
			return sendError(err.Error())
		}
		*fields = fs
		return out.send(protocol.Ack{Type: protocol.TypeAck, Cmd: c.Cmd, Fields: fs.list()})
	case protocol.CmdSampleNow:
		// 已有取樣進行中時不另起一次, 該次結果即為所求.
		sampleNow()
		return nil
	case protocol.CmdPing:
		return out.send(protocol.Pong{Type: protocol.TypePong})
	default:
		return sendError(fmt.Sprintf("unknown command: %s", c.Cmd))
	}
}

// closeCollector 結束前關閉取樣來源 (含 nvmlShutdown). 取樣仍卡在進行中時
// 最多等候 1 秒, 逾時則不呼叫, 避免與卡住的 NVML 呼叫並行而無法結束.
func closeCollector(col collector, busy bool, results <-chan sampleResult) {
	if busy {
		select {
		case <-results:
		case <-time.After(time.Second):
			return
		}
	}
	done := make(chan struct{})
	go func() {
		col.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}
