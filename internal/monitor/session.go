package monitor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"gpumon/internal/protocol"
	"gpumon/internal/recording"
	"gpumon/internal/sshconn"
)

// maxLineBytes 為讀取遠端訊息的單行上限, 超過者捨棄並記錄到日誌.
const maxLineBytes = 64 << 20

// session 為一台主機的連線工作階段. 每次使用者按下連線啟動一個 run goroutine,
// 直到轉為未連線狀態為止; 自動重新連線於同一個 run goroutine 內進行.
type session struct {
	m  *Manager
	id string

	notifyMu sync.Mutex

	mu       sync.Mutex
	st       State
	reason   string
	retries  int
	retryErr string
	user     string
	rec      *recorder
	live     Live
	cmds     chan protocol.Command
	cancel   context.CancelFunc
	done     chan struct{}

	// connStart 為本次連線第一筆取樣的時刻 (記錄檔所用的時刻); 尚未收到取樣時沿用
	// 前一次連線的值, 使用者按下連線時歸零.
	connStart int64

	// 密碼與解密後的金鑰. password 為暫留供自動重新連線的登入密碼, candidate 為
	// 正在連線時使用者輸入, 尚未確定認證成功的登入密碼.
	secMu     sync.Mutex
	password  []byte
	candidate []byte
	keys      *sshconn.KeyCache
}

func newSession(m *Manager, id string) *session {
	return &session{m: m, id: id, st: StateDisconnected, live: Live{Host: id}}
}

func (s *session) state() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

func (s *session) lastUser() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user
}

func (s *session) recorderRef() *recorder {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec
}

func (s *session) recordingPath() string {
	if r := s.recorderRef(); r != nil {
		return r.path()
	}
	return ""
}

func (s *session) fill(st *HostStatus) {
	s.mu.Lock()
	st.State, st.Reason, st.Retries, st.RetryError = s.st, s.reason, s.retries, s.retryErr
	rec := s.rec
	s.mu.Unlock()
	if rec != nil {
		st.Recording = rec.path()
	}
}

func (s *session) connStartTime() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connStart
}

func (s *session) liveCopy() Live {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live
}

// notifyStatus 送出主機目前的狀態. 讀取與送出於同一個鎖內進行, 使最後送出的
// 一則必為最新的狀態. 呼叫端不得持有 m.mu.
func (s *session) notifyStatus() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if st, err := s.m.Host(s.id); err == nil {
		s.m.notify.Notify(EventHost, st)
	}
}

// start 由未連線轉為正在連線並啟動 run goroutine. 呼叫端須持有 m.mu, 並於釋放
// 後呼叫 notifyStatus.
func (s *session) start() {
	s.mu.Lock()
	if s.st != StateDisconnected {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.st, s.reason, s.retries, s.retryErr = StateConnecting, "", 0, ""
	s.cancel = cancel
	s.done = make(chan struct{})
	s.rec = newRecorder(s.m.dataDir, s.m.timing.ProcessRecord.Microseconds(), s.m.log)
	s.live = Live{Host: s.id}
	s.connStart = 0
	done := s.done
	s.mu.Unlock()
	s.secMu.Lock()
	s.keys = &sshconn.KeyCache{}
	s.secMu.Unlock()
	go func() {
		defer close(done)
		s.run(ctx)
	}()
}

// stop 中斷連線或停止重試, 等待 run goroutine 結束.
func (s *session) stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// send 把指令交給目前連線送出; 沒有連線時捨棄.
func (s *session) send(cmd protocol.Command) {
	s.mu.Lock()
	ch := s.cmds
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- cmd:
	default:
		s.m.log.Warn("command queue full, command dropped", "host", s.id, "cmd", cmd.Cmd)
	}
}

// endKind 為 run 結束的方式.
type endKind int

const (
	// endUser 為使用者中斷連線或程式結束.
	endUser endKind = iota
	// endError 為檢查出錯誤而斷線.
	endError
	// endExhausted 為自動重新連線達到次數上限.
	endExhausted
)

// run 進行連線流程與自動重新連線, 結束時轉為未連線狀態.
func (s *session) run(ctx context.Context) {
	end := endUser
	defer func() { s.finish(end) }()

	c, err := s.connect(ctx, false)
	if err != nil {
		if s.failed(ctx, err) {
			end = endError
		}
		return
	}
	var disc *disconnect
	for {
		reason := s.serve(ctx, c, disc)
		if ctx.Err() != nil {
			return
		}
		disc = &disconnect{time: s.m.now().UnixMicro(), reason: reason}
		s.m.log.Info("connection lost", "host", s.id, "reason", reason)
		c, end = s.reconnect(ctx, disc)
		if c == nil {
			return
		}
	}
}

// disconnect 為一次非預期斷線的時刻與原因.
type disconnect struct {
	time   int64
	reason string
}

// failed 處理使用者按下連線後, 收到 devices 訊息之前的失敗. 回傳是否為檢查出
// 錯誤而斷線; 使用者取消時不送出錯誤.
func (s *session) failed(ctx context.Context, err error) bool {
	if ctx.Err() != nil || sshconn.KindOf(err) == sshconn.KindCanceled {
		return false
	}
	s.reportError(err)
	return true
}

func (s *session) reportError(err error) {
	ce := ConnectError{Host: s.id, Kind: kindOf(err), Message: err.Error()}
	if st, herr := s.m.Host(s.id); herr == nil {
		ce.Name = st.Name
	}
	s.m.log.Warn("connection failed", "host", s.id, "kind", ce.Kind, "err", ce.Message)
	s.m.notify.Notify(EventError, ce)
}

// reconnect 依第 4 章的規則自動重新連線. 成功時回傳新的連線; 否則回傳 nil 與
// 結束的方式.
func (s *session) reconnect(ctx context.Context, disc *disconnect) (*conn, endKind) {
	s.mu.Lock()
	s.st, s.reason, s.retries, s.retryErr = StateReconnecting, disc.reason, 0, ""
	s.mu.Unlock()
	start := time.Now()
	for i := 1; i <= s.m.timing.MaxRetries; i++ {
		if i > 1 {
			// 重試對齊心跳的時刻, 即判定斷線後每隔一個心跳間隔一次.
			// 前一次重試超過一個間隔才結束時, 順延至其後的第一個心跳時刻.
			hb := s.m.timing.Heartbeat
			since := time.Since(start)
			k := max(time.Duration(i-1), (since+hb-1)/hb)
			t := time.NewTimer(k*hb - since)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, endUser
			case <-t.C:
			}
		}
		s.mu.Lock()
		s.retries = i
		s.mu.Unlock()
		s.notifyStatus()
		c, err := s.connect(ctx, true)
		if err == nil {
			return c, endUser
		}
		if ctx.Err() != nil {
			return nil, endUser
		}
		if !retryable(err) {
			s.m.log.Info("reconnection stopped", "host", s.id, "attempt", i, "err", err)
			s.reportError(err)
			return nil, endError
		}
		s.m.log.Info("reconnection attempt failed", "host", s.id, "attempt", i, "err", err)
		s.mu.Lock()
		s.retryErr = err.Error()
		s.mu.Unlock()
		s.notifyStatus()
	}
	return nil, endExhausted
}

// finish 轉為未連線狀態: 關閉記錄檔, 銷毀密碼與解密後的金鑰.
func (s *session) finish(end endKind) {
	if rec := s.recorderRef(); rec != nil {
		rec.close()
	}
	s.wipe()
	s.mu.Lock()
	s.st = StateDisconnected
	if end != endExhausted {
		s.reason, s.retryErr = "", ""
	}
	s.retries = 0
	s.cmds = nil
	s.cancel = nil
	s.mu.Unlock()
	s.notifyStatus()
}

// wipe 以零覆寫並解除引用暫留的登入密碼, 並清除解密後的金鑰.
func (s *session) wipe() {
	s.secMu.Lock()
	defer s.secMu.Unlock()
	clear(s.password)
	clear(s.candidate)
	s.password, s.candidate = nil, nil
	if s.keys != nil {
		s.keys.Clear()
		s.keys = nil
	}
}

// errNoSecret 為自動重新連線時需要使用者輸入才能認證. 自動重新連線不再要求輸入.
var errNoSecret = errors.New("the saved password was rejected or a password is required")

// uploadPath 為通知視窗顯示的遠端放置路徑.
const uploadPath = "~/" + sshconn.RemoteDir + "/" + sshconn.RemoteName

// auth 回傳連線所用的回呼函數. 自動重新連線時只使用暫留的登入密碼, 不要求輸入;
// 需要上傳遠端程式時, 首次連線與自動重新連線皆送出上傳通知並等待確認.
func (s *session) auth(ctx context.Context, reconnect bool) Auth {
	s.secMu.Lock()
	keys := s.keys
	s.secMu.Unlock()
	name := s.id
	if st, err := s.m.Host(s.id); err == nil {
		name = st.Name
	}
	confirm := func(uctx context.Context, req sshconn.UploadRequest) error {
		return s.m.uploads.ask(uctx, UploadNotice{
			Host: s.id, Name: name, Path: uploadPath,
			Update: req.Update, RemoteVersion: req.RemoteVersion, NewVersion: req.NewVersion,
		})
	}
	if reconnect {
		return Auth{
			KeyCache:      keys,
			ConfirmUpload: confirm,
			Password: func(_ context.Context, req sshconn.PasswordRequest) ([]byte, error) {
				s.secMu.Lock()
				defer s.secMu.Unlock()
				if req.Attempt > 1 || s.password == nil {
					return nil, errNoSecret
				}
				return s.password, nil
			},
			Passphrase: func(context.Context, sshconn.PassphraseRequest) ([]byte, error) {
				return nil, errNoSecret
			},
		}
	}
	return Auth{
		KeyCache:      keys,
		ConfirmUpload: confirm,
		Password: func(pctx context.Context, req sshconn.PasswordRequest) ([]byte, error) {
			b, err := s.m.prompts.ask(pctx, Prompt{
				Host: s.id, Name: name, Kind: PromptPassword, User: req.User, Server: req.Host, Attempt: req.Attempt,
			})
			if err != nil {
				return nil, err
			}
			s.secMu.Lock()
			clear(s.candidate)
			s.candidate = b
			s.secMu.Unlock()
			return b, nil
		},
		Passphrase: func(pctx context.Context, req sshconn.PassphraseRequest) ([]byte, error) {
			return s.m.prompts.ask(pctx, Prompt{
				Host: s.id, Name: name, Kind: PromptPassphrase, KeyFile: req.KeyFile, Attempt: req.Attempt,
			})
		},
	}
}

// commitPassword 於連線成功後把使用者輸入的登入密碼轉為暫留; 失敗時銷毀.
func (s *session) commitPassword(ok bool) {
	s.secMu.Lock()
	defer s.secMu.Unlock()
	if s.candidate == nil {
		return
	}
	if ok {
		clear(s.password)
		s.password = s.candidate
	} else {
		clear(s.candidate)
	}
	s.candidate = nil
}

// conn 為已收到 devices 訊息的一條連線.
type conn struct {
	link       Link
	lines      *lineReader
	devices    protocol.Devices
	devicesRaw json.RawMessage
	intervalMS int
}

// checkError 為本套件於連線流程中檢查出的錯誤.
type checkError struct {
	kind ErrorKind
	msg  string
}

func (e *checkError) Error() string { return e.msg }

// kindOf 回傳錯誤的種類.
func kindOf(err error) ErrorKind {
	var ce *checkError
	if errors.As(err, &ce) {
		return ce.kind
	}
	switch sshconn.KindOf(err) {
	case sshconn.KindAuth:
		return ErrAuth
	case sshconn.KindHostKey:
		return ErrHostKey
	case sshconn.KindPlatform:
		return ErrPlatform
	case sshconn.KindUpload:
		return ErrUpload
	case sshconn.KindExec:
		return ErrExec
	case sshconn.KindProtocolTooNew:
		return ErrProtocolTooNew
	case sshconn.KindConfig:
		return ErrConfig
	}
	return ErrConnect
}

// retryable 回報自動重新連線時此錯誤是否繼續重試. 認證失敗與檢查出的錯誤停止
// 重試.
func retryable(err error) bool {
	return kindOf(err) == ErrConnect
}

// connect 進行連線流程的步驟 1 至 5.
func (s *session) connect(ctx context.Context, reconnect bool) (*conn, error) {
	h, ok := s.m.store.Host(s.id)
	if !ok {
		return nil, &checkError{kind: ErrConfig, msg: fmt.Sprintf("Host %s no longer exists", s.id)}
	}
	timeout := s.m.timing.ConnectTimeout
	if reconnect {
		timeout = s.m.timing.ReconnectTimeout
	}
	interval := IntervalMS
	link, err := s.m.dialer.Dial(ctx, Target{Conn: h.HostConn, IntervalMS: interval, ConnectTimeout: timeout}, s.auth(ctx, reconnect))
	if err != nil {
		s.commitPassword(false)
		return nil, err
	}
	c, err := s.handshake(ctx, link, h.Addr)
	if err != nil {
		link.Close()
		s.commitPassword(false)
		return nil, err
	}
	c.intervalMS = interval
	s.commitPassword(true)
	return c, nil
}

// handshake 讀取並檢查第一行 devices 訊息.
func (s *session) handshake(ctx context.Context, link Link, addr string) (*conn, error) {
	go s.logStderr(link.Stderr())
	lines := newLineReader(link.Stdout(), s.m.log, s.id)
	timer := time.NewTimer(s.m.timing.DevicesTimeout)
	defer timer.Stop()
	var line []byte
	select {
	case <-ctx.Done():
		lines.stop()
		return nil, ctx.Err()
	case <-timer.C:
		lines.stop()
		return nil, &checkError{kind: ErrConnect, msg: fmt.Sprintf("The remote program on %s did not send device information in time", addr)}
	case l, ok := <-lines.c:
		if !ok {
			lines.stop()
			if err := waitBriefly(link); sshconn.KindOf(err) == sshconn.KindExec {
				return nil, err
			}
			return nil, &checkError{kind: ErrConnect, msg: fmt.Sprintf("The remote program on %s ended before sending device information", addr)}
		}
		line = l
	}
	var dev protocol.Devices
	if err := json.Unmarshal(line, &dev); err != nil || dev.Type != protocol.TypeDevices {
		lines.stop()
		return nil, &checkError{kind: ErrFirstMessage, msg: fmt.Sprintf("The remote program on %s did not start with device information", addr)}
	}
	if dev.Protocol != link.Protocol() {
		lines.stop()
		return nil, &checkError{kind: ErrProtocolMismatch, msg: fmt.Sprintf(
			"The remote program on %s reported protocol %d, but protocol %d was expected", addr, dev.Protocol, link.Protocol())}
	}
	if dev.GPUs == nil {
		dev.GPUs = []protocol.Device{}
	}
	return &conn{link: link, lines: lines, devices: dev, devicesRaw: json.RawMessage(bytes.Clone(line))}, nil
}

// waitBriefly 等待連線結束的原因, 最多 2 秒.
func waitBriefly(link Link) error {
	ch := make(chan error, 1)
	go func() { ch <- link.Wait() }()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		return nil
	}
}

func (s *session) logStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		s.m.log.Info("remote stderr", "host", s.id, "line", sc.Text())
	}
}

// serve 處理一條連線的訊息直到連線結束, 回傳斷線原因; 使用者中斷連線時回傳空
// 字串. disc 為前一次非預期斷線, 第一次連線時為 nil.
func (s *session) serve(ctx context.Context, c *conn, disc *disconnect) string {
	link := c.link
	defer link.Close()
	defer c.lines.stop()

	cmds := make(chan protocol.Command, 16)
	out := make(chan []byte, 16)
	stopWriter := make(chan struct{})
	defer close(stopWriter)
	go func() {
		for {
			select {
			case <-stopWriter:
				return
			case b := <-out:
				if _, err := link.Stdin().Write(b); err != nil {
					return
				}
			}
		}
	}()
	sendCmd := func(cmd protocol.Command) {
		b, err := json.Marshal(cmd)
		if err != nil {
			return
		}
		select {
		case out <- append(b, '\n'):
		default:
			s.m.log.Warn("command queue full, command dropped", "host", s.id, "cmd", cmd.Cmd)
		}
	}

	user := link.User()
	h, _ := s.m.store.Host(s.id)
	name := h.Addr
	if user != "" {
		name = user + "@" + h.Addr
	}
	s.mu.Lock()
	s.st, s.reason, s.retries, s.retryErr = StateConnected, "", 0, ""
	s.user = user
	s.cmds = cmds
	s.live = Live{Host: s.id, Devices: &c.devices}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cmds = nil
		s.mu.Unlock()
	}()
	s.m.log.Info("connected", "host", s.id, "name", name, "gpus", len(c.devices.GPUs))
	s.notifyStatus()
	s.m.notify.Notify(EventDevices, DevicesEvent{Host: s.id, Devices: c.devices})

	rec := s.recorderRef()
	defer rec.flush()
	h2 := &handler{s: s, c: c, rec: rec, name: name, disc: disc, interval: c.intervalMS, util: newUtilTracker()}

	waitCh := make(chan error, 1)
	go func() { waitCh <- link.Wait() }()
	heartbeat := time.NewTicker(s.m.timing.Heartbeat)
	defer heartbeat.Stop()
	flush := time.NewTicker(s.m.timing.Flush)
	defer flush.Stop()
	watchdog := time.NewTimer(s.m.timing.Watchdog)
	defer watchdog.Stop()

	for {
		select {
		case <-ctx.Done():
			return ""
		case line, ok := <-c.lines.c:
			if !ok {
				return endReason(waitBriefly(link))
			}
			watchdog.Reset(s.m.timing.Watchdog)
			h2.handle(line)
		case <-heartbeat.C:
			sendCmd(protocol.Command{Cmd: protocol.CmdPing})
		case <-flush.C:
			rec.flush()
		case <-watchdog.C:
			return fmt.Sprintf("No response from the remote host for %d seconds", int(s.m.timing.Watchdog.Round(time.Second)/time.Second))
		case err := <-waitCh:
			return endReason(err)
		case cmd := <-cmds:
			sendCmd(cmd)
		}
	}
}

// endReason 把連線結束的原因轉為英文的斷線原因.
func endReason(err error) string {
	var exit interface{ ExitStatus() int }
	switch {
	case err == nil:
		return "The remote program exited"
	case errors.As(err, &exit):
		return fmt.Sprintf("The remote program exited with status %d", exit.ExitStatus())
	}
	return "Connection lost: " + err.Error()
}

// handler 處理一條連線收到的訊息.
type handler struct {
	s    *session
	c    *conn
	rec  *recorder
	name string
	disc *disconnect

	interval int
	util     *utilTracker
	started  bool
	connTime int64
	tBase    int64
	tsBase   float64
}

// rawSample 只取出 sample 訊息內各 GPU 的程序原文, 供程序記錄原樣存入.
type rawSample struct {
	GPUs []struct {
		ID    int               `json:"id"`
		Procs []json.RawMessage `json:"procs"`
	} `json:"gpus"`
}

func (h *handler) handle(line []byte) {
	log := h.s.m.log
	typ, err := protocol.MessageType(line)
	if err != nil {
		log.Warn("unparsable line from the remote program", "host", h.s.id, "line", clip(line))
		return
	}
	switch typ {
	case protocol.TypeSample:
		var smp protocol.Sample
		var raw rawSample
		if json.Unmarshal(line, &smp) != nil || json.Unmarshal(line, &raw) != nil {
			log.Warn("unparsable sample from the remote program", "host", h.s.id, "line", clip(line))
			return
		}
		h.sample(&smp, &raw)
	case protocol.TypeAck:
		var ack protocol.Ack
		if json.Unmarshal(line, &ack) != nil {
			log.Warn("unparsable ack from the remote program", "host", h.s.id, "line", clip(line))
			return
		}
		if ack.Cmd == protocol.CmdSetInterval && ack.Ms != nil {
			h.interval = *ack.Ms
		}
	case protocol.TypeError:
		var e protocol.Error
		if json.Unmarshal(line, &e) != nil {
			log.Warn("unparsable error message from the remote program", "host", h.s.id, "line", clip(line))
			return
		}
		log.Info("remote error", "host", h.s.id, "msg", e.Msg)
		if h.started {
			h.rec.addError(float64(e.TS), e.Msg)
		}
	case protocol.TypePong:
	default:
		log.Warn("unexpected message type from the remote program", "host", h.s.id, "type", typ)
	}
}

func (h *handler) sample(smp *protocol.Sample, raw *rawSample) {
	ts := float64(smp.TS)
	if !h.started {
		h.started = true
		h.tBase, h.tsBase = h.s.m.now().UnixMicro(), ts
		cs := connStart{host: h.name, devicesRaw: h.c.devicesRaw, tBase: h.tBase, tsBase: ts}
		for _, g := range h.c.devices.GPUs {
			cs.gpuIDs = append(cs.gpuIDs, g.ID)
		}
		if h.disc != nil {
			cs.reconnected, cs.discTime, cs.discReason = true, h.disc.time, h.disc.reason
		}
		h.rec.begin(cs)
	}

	vendors := map[int]string{}
	for _, d := range h.c.devices.GPUs {
		vendors[d.ID] = d.Vendor
	}
	u := h.util.update(ts, smp.GPUs)
	gpus := make([]protocol.GPUSample, len(smp.GPUs))
	live := make([]LiveGPU, len(smp.GPUs))
	var procs []LiveProcess
	for i, g := range smp.GPUs {
		lg := LiveGPU{ID: g.ID, Util: g.Util, MemUsed: g.MemUsed, Temp: g.Temp, PowerW: g.PowerW,
			ClockGPU: g.ClockGPU, ClockMem: g.ClockMem, Fan: g.Fan, Enc: g.Enc, Dec: g.Dec}
		if g.Util == nil && vendors[g.ID] == protocol.VendorIntel {
			if v, ok := u.gpu[g.ID]; ok {
				g.Util = &v
				lg.Util, lg.UtilComputed = &v, true
			}
		}
		gpus[i], live[i] = g, lg
		for _, p := range g.Procs {
			lp := LiveProcess{GPU: g.ID, PID: p.PID, User: p.User, Mem: p.Mem, Util: p.Util, Cmd: p.Cmd}
			if lp.Util == nil {
				if v, ok := u.proc[procKey{g.ID, p.PID}]; ok {
					lp.Util = &v
				}
			}
			procs = append(procs, lp)
		}
	}
	gp := make([]recording.GPUProcs, 0, len(raw.GPUs))
	for _, g := range raw.GPUs {
		gp = append(gp, recording.GPUProcs{GPU: g.ID, Procs: g.Procs})
	}

	t, ok := h.rec.addSample(ts, h.interval, gpus, gp)
	if !ok {
		t = h.tBase + int64(math.Round((ts-h.tsBase)*1e6))
	}
	if procs == nil {
		procs = []LiveProcess{}
	}
	if h.connTime == 0 {
		h.connTime = t
	}
	h.s.mu.Lock()
	lv := Live{Host: h.s.id, Devices: &h.c.devices, ConnStart: h.connTime, HasSample: true, Time: t, IntervalMS: h.interval, GPUs: live, Procs: procs}
	h.s.live = lv
	h.s.connStart = h.connTime
	h.s.mu.Unlock()
	h.s.m.notify.Notify(EventSample, lv)
}

// clip 截短日誌中的資料行.
func clip(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}

// lineReader 於另一個 goroutine 逐行讀取遠端程式的 stdout.
type lineReader struct {
	c    chan []byte
	quit chan struct{}
	once sync.Once
}

func newLineReader(r io.Reader, log interface {
	Warn(msg string, args ...any)
}, host string) *lineReader {
	lr := &lineReader{c: make(chan []byte, 64), quit: make(chan struct{})}
	go func() {
		defer close(lr.c)
		br := bufio.NewReaderSize(r, 64<<10)
		var buf []byte
		tooLong := false
		for {
			chunk, err := br.ReadSlice('\n')
			if len(buf)+len(chunk) > maxLineBytes {
				if !tooLong {
					log.Warn("line from the remote program is too long, discarded", "host", host)
				}
				tooLong, buf = true, buf[:0]
			} else if !tooLong {
				buf = append(buf, chunk...)
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if err == nil {
				if !tooLong {
					line := bytes.TrimRight(buf, "\r\n")
					if len(bytes.TrimSpace(line)) > 0 {
						select {
						case lr.c <- bytes.Clone(line):
						case <-lr.quit:
							return
						}
					}
				}
				buf, tooLong = buf[:0], false
				continue
			}
			return
		}
	}()
	return lr
}

func (lr *lineReader) stop() {
	lr.once.Do(func() { close(lr.quit) })
}
