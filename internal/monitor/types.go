package monitor

import (
	"errors"
	"fmt"

	"gpumon/internal/protocol"
	"gpumon/internal/settings"
)

// State 為主機的連線狀態.
type State string

// 主機的四種狀態.
const (
	// StateDisconnected 為未連線.
	StateDisconnected State = "disconnected"
	// StateConnecting 為使用者按下連線至收到 devices 訊息之前, 即正在連線.
	StateConnecting State = "connecting"
	// StateConnected 為連線中.
	StateConnected State = "connected"
	// StateReconnecting 為非預期斷線後自動重新連線中.
	StateReconnecting State = "reconnecting"
)

// HostStatus 為一台主機的設定與連線狀態.
type HostStatus struct {
	// ID 為主機於設定檔中的識別字串, 呼叫本套件的其他方法時以此指定主機.
	ID string `json:"id"`
	// Name 為畫面顯示的名稱 "實際使用者名稱@位址", 亦為記錄檔的 host 成員.
	// 實際使用者名稱無法解析時為 ID.
	Name string `json:"name"`
	settings.Host
	State State `json:"state"`
	// Reason 為最近一次非預期斷線的原因, 英文. 重新連線中與自動重新連線
	// 失敗而轉為未連線時有值; 使用者按下連線或中斷連線時清除.
	Reason string `json:"reason"`
	// Retries 為本次斷線後已開始的重試次數, 1 至 MaxRetries; 非重新連線中時為 0.
	Retries int `json:"retries"`
	// MaxRetries 為重試次數的上限.
	MaxRetries int `json:"max_retries"`
	// RetryError 為最近一次重試失敗的原因, 英文.
	RetryError string `json:"retry_error"`
	// Recording 為目前寫入中的記錄檔路徑; 沒有時為空字串.
	Recording string `json:"recording"`
}

// ErrorKind 為檢查出錯誤而斷線時的錯誤種類, 供前端依語言設定顯示說明.
type ErrorKind string

// 錯誤種類. 前十種對應 sshconn 的錯誤種類.
const (
	ErrConnect        ErrorKind = "connect"
	ErrAuth           ErrorKind = "auth"
	ErrHostKey        ErrorKind = "host-key"
	ErrPlatform       ErrorKind = "platform"
	ErrUpload         ErrorKind = "upload"
	ErrExec           ErrorKind = "exec"
	ErrProtocolTooNew ErrorKind = "protocol-too-new"
	ErrConfig         ErrorKind = "config"
	// ErrFirstMessage 為遠端程式的第一行不是 devices 訊息.
	ErrFirstMessage ErrorKind = "first-message"
	// ErrProtocolMismatch 為 devices 訊息的 protocol 與部署階段所定的協定版本不同.
	ErrProtocolMismatch ErrorKind = "protocol-mismatch"
)

// ConnectError 為檢查出錯誤而斷線的通知內容. 多台主機的錯誤逐台送出.
type ConnectError struct {
	// Host 為主機的 ID, Name 為顯示名稱.
	Host string    `json:"host"`
	Name string    `json:"name"`
	Kind ErrorKind `json:"kind"`
	// Message 為可直接顯示的英文說明, 不含密碼.
	Message string `json:"message"`
}

// PromptKind 為要求輸入的種類.
type PromptKind string

// 要求輸入的種類.
const (
	PromptPassword   PromptKind = "password"
	PromptPassphrase PromptKind = "passphrase"
)

// Prompt 為要求使用者輸入登入密碼或金鑰密碼的通知內容. 前端以 ID 回覆.
type Prompt struct {
	ID string `json:"id"`
	// Host 為主機的 ID, Name 為顯示名稱.
	Host string     `json:"host"`
	Name string     `json:"name"`
	Kind PromptKind `json:"kind"`
	// User 與 Server 為要登入的帳號與主機表單的位址, 只用於登入密碼.
	User   string `json:"user"`
	Server string `json:"server"`
	// KeyFile 為金鑰檔的路徑, 只用於金鑰密碼.
	KeyFile string `json:"key_file"`
	// Attempt 為第幾次要求, 由 1 起算; 大於 1 表示前一次輸入的值遭拒.
	Attempt int `json:"attempt"`
}

// PromptClosed 為要求輸入已失效的通知內容, 例如使用者於輸入視窗開啟期間按下
// 中斷連線. 前端收到後關閉對應的輸入視窗.
type PromptClosed struct {
	ID string `json:"id"`
}

// UploadNotice 為上傳遠端程式之前的通知內容. 前端顯示通知視窗, 使用者按下確認鍵
// 後以 ID 呼叫 ConfirmUpload, 之後才開始上傳.
type UploadNotice struct {
	ID string `json:"id"`
	// Host 為主機的 ID, Name 為顯示名稱.
	Host string `json:"host"`
	Name string `json:"name"`
	// Path 為遠端的放置路徑, 恆為 ~/.gpumon/gpumon.
	Path string `json:"path"`
	// Update 為真表示更新, 為假表示首次上傳.
	Update bool `json:"update"`
	// RemoteVersion 為遠端現有的程式版號, 首次上傳時為空字串; NewVersion 為即將
	// 上傳的程式版號. 兩者皆不含內容雜湊值.
	RemoteVersion string `json:"remote_version"`
	NewVersion    string `json:"new_version"`
	// RemoteHash 為遠端現有的內容雜湊值, 遠端沒有時為 unknown, 首次上傳時
	// 為空字串; NewHash 為即將上傳的內容雜湊值.
	RemoteHash string `json:"remote_hash"`
	NewHash    string `json:"new_hash"`
	// SameVersion 為真表示更新的原因是兩者程式版號相同而內容不同, 為假表示遠端的
	// 程式版號較低.
	SameVersion bool `json:"same_version"`
}

// UploadClosed 為上傳通知已失效的通知內容, 例如等待確認期間使用者按下中斷連線或
// SSH 連線中斷. 前端收到後關閉對應的通知視窗.
type UploadClosed struct {
	ID string `json:"id"`
}

// HostKeyInfo 為一把主機金鑰的種類與指紋.
type HostKeyInfo struct {
	// KeyType 為金鑰種類, 例如 ssh-ed25519.
	KeyType string `json:"key_type"`
	// Fingerprint 為 OpenSSH 格式的 SHA256 指紋, 例如 "SHA256:...".
	Fingerprint string `json:"fingerprint"`
}

// HostKeyPrompt 為遠端主機的主機金鑰需要使用者確認的通知內容. 前端以 ID 呼叫
// AnswerHostKey 回覆.
type HostKeyPrompt struct {
	ID string `json:"id"`
	// Host 為主機的 ID, Name 為顯示名稱.
	Host string `json:"host"`
	Name string `json:"name"`
	// Changed 為假表示未知的主機, 即兩份 known_hosts 皆沒有該主機的記錄; 為真表示
	// 主機金鑰已變更, 即有記錄但皆與遠端主機的金鑰不同.
	Changed bool `json:"changed"`
	// Key 為遠端主機目前的主機金鑰.
	Key HostKeyInfo `json:"key"`
	// OldKeys 為已記錄的主機金鑰, 相同者只列一次; 未知的主機時為空陣列.
	OldKeys []HostKeyInfo `json:"old_keys"`
}

// HostKeyClosed 為主機金鑰的確認已失效的通知內容, 例如等待回覆期間使用者按下中斷
// 連線. 前端收到後關閉對應的視窗.
type HostKeyClosed struct {
	ID string `json:"id"`
}

// KeySetupChoice 為使用者對免密碼登入詢問的回答.
type KeySetupChoice string

// 免密碼登入詢問的三種回答.
const (
	// KeySetupNow 為 "立即設定".
	KeySetupNow KeySetupChoice = "now"
	// KeySetupLater 為 "暫不設定".
	KeySetupLater KeySetupChoice = "later"
	// KeySetupNever 為 "不再詢問".
	KeySetupNever KeySetupChoice = "never"
)

// KeySetupPrompt 為詢問是否設定免密碼登入的通知內容. 前端以 ID 呼叫
// AnswerKeySetup 回覆.
type KeySetupPrompt struct {
	ID string `json:"id"`
	// Host 為主機的 ID, Name 為顯示名稱.
	Host string `json:"host"`
	Name string `json:"name"`
}

// KeySetupClosed 為免密碼登入的詢問已失效的通知內容, 例如等待回覆期間 SSH 連線
// 中斷或使用者按下中斷連線. 前端收到後關閉對應的視窗.
type KeySetupClosed struct {
	ID string `json:"id"`
}

// KeySetupOutcome 為立即設定免密碼登入的結果.
type KeySetupOutcome string

// 立即設定的三種結果.
const (
	// KeySetupSucceeded 為公鑰已附加至遠端的 ~/.ssh/authorized_keys.
	KeySetupSucceeded KeySetupOutcome = "success"
	// KeySetupGenerateFailed 為產生金鑰檔失敗, 未附加公鑰.
	KeySetupGenerateFailed KeySetupOutcome = "generate-failed"
	// KeySetupAppendFailed 為附加公鑰失敗; 該主機已記為不再詢問.
	KeySetupAppendFailed KeySetupOutcome = "append-failed"
)

// KeySetupResult 為立即設定免密碼登入的結果通知內容. 前端顯示後由使用者關閉, 不需
// 回覆; 連線流程不等待此視窗.
type KeySetupResult struct {
	// Host 為主機的 ID, Name 為顯示名稱.
	Host    string          `json:"host"`
	Name    string          `json:"name"`
	Outcome KeySetupOutcome `json:"outcome"`
	// KeyFile 為本機金鑰檔的路徑: 成功與附加失敗時為公鑰所屬的金鑰檔, 產生失敗時
	// 為預定產生的位置.
	KeyFile string `json:"key_file"`
	// Generated 表示金鑰檔為本次產生 (或嘗試產生).
	Generated bool `json:"generated"`
	// Reason 為失敗的原因, 英文, 可直接顯示; 成功時為空字串.
	Reason string `json:"reason"`
}

// LiveGPU 為一張 GPU 最新一筆取樣的數值. 取不到的數值為 nil.
type LiveGPU struct {
	ID       int     `json:"id"`
	Util     *int    `json:"util"`
	MemUsed  *uint64 `json:"mem_used"`
	Temp     *int    `json:"temp"`
	PowerW   *int    `json:"power_w"`
	ClockGPU *int    `json:"clock_gpu"`
	ClockMem *int    `json:"clock_mem"`
	Fan      *int    `json:"fan"`
	Enc      *int    `json:"enc"`
	Dec      *int    `json:"dec"`
	// UtilComputed 表示 Util 由本機以各程序的累計值算出 (Intel).
	UtilComputed bool `json:"util_computed"`
}

// LiveProcess 為程序列表的一列.
type LiveProcess struct {
	GPU  int     `json:"gpu"`
	PID  int     `json:"pid"`
	User string  `json:"user"`
	Mem  *uint64 `json:"mem"`
	// Util 為程序的 GPU 使用率; NVIDIA 取自遠端, AMD 與 Intel 由本機以累計值
	// 算出, 取不到時為 nil.
	Util *int   `json:"util"`
	Cmd  string `json:"cmd"`
}

// Live 為一台主機的即時資料, 即最新一筆取樣與程序列表.
type Live struct {
	Host string `json:"host"`
	// Devices 為本次連線收到的 devices 訊息; 尚未收到時為 nil.
	Devices *protocol.Devices `json:"devices"`
	// ConnStart 為本次連線第一筆取樣的時刻 (Unix 微秒), 監看畫面的曲線自此時刻起繪製;
	// 本次連線尚未收到取樣時為 0.
	ConnStart int64 `json:"conn_start"`
	// HasSample 為假時, 以下欄位無意義.
	HasSample bool `json:"has_sample"`
	// Time 為最新一筆取樣的取樣時刻, 依 6.2 的時間基準換算, 單位為 Unix 微秒.
	Time int64 `json:"time"`
	// IntervalMS 為該筆取樣所附的取樣間隔.
	IntervalMS int           `json:"interval_ms"`
	GPUs       []LiveGPU     `json:"gpus"`
	Procs      []LiveProcess `json:"procs"`
}

// DevicesEvent 為收到 devices 訊息的通知內容.
type DevicesEvent struct {
	Host    string           `json:"host"`
	Devices protocol.Devices `json:"devices"`
}

// 通知的名稱. 每種通知的內容型別依序為 HostStatus, Live, DevicesEvent,
// ConnectError, Prompt, PromptClosed, UploadNotice, UploadClosed, HostKeyPrompt,
// HostKeyClosed, KeySetupPrompt, KeySetupClosed 與 KeySetupResult.
const (
	EventHost           = "monitor:host"
	EventSample         = "monitor:sample"
	EventDevices        = "monitor:devices"
	EventError          = "monitor:error"
	EventPrompt         = "monitor:prompt"
	EventPromptClosed   = "monitor:prompt-closed"
	EventUpload         = "monitor:upload"
	EventUploadClosed   = "monitor:upload-closed"
	EventHostKey        = "monitor:host-key"
	EventHostKeyClosed  = "monitor:host-key-closed"
	EventKeySetup       = "monitor:key-setup"
	EventKeySetupClosed = "monitor:key-setup-closed"
	EventKeySetupResult = "monitor:key-setup-result"
)

// Notifier 接收本套件送出的通知. Notify 不得阻塞.
type Notifier interface {
	Notify(name string, data any)
}

// NotifierFunc 讓一般函數可作為 Notifier.
type NotifierFunc func(name string, data any)

// Notify 呼叫 f.
func (f NotifierFunc) Notify(name string, data any) { f(name, data) }

// Code 為本套件回傳給呼叫端的錯誤代碼, 供前端依語言設定顯示說明.
type Code string

// 錯誤代碼.
const (
	// CodeInvalid 為傳入的值不合法.
	CodeInvalid Code = "invalid"
	// CodeNotFound 為找不到指定的主機, 記錄檔, 輸入要求, 上傳通知或詢問.
	CodeNotFound Code = "not-found"
	// CodeDuplicate 為已有相同 "實際使用者名稱@位址" 的主機.
	CodeDuplicate Code = "duplicate"
	// CodeUserRequired 為使用者名稱留空且 ~/.ssh/config 未設定使用者名稱.
	CodeUserRequired Code = "user-required"
	// CodeBusy 為主機處於正在連線, 連線中或重新連線中, 不可編輯或刪除.
	CodeBusy Code = "busy"
	// CodeActive 為記錄檔正在寫入中, 不可刪除.
	CodeActive Code = "active"
	// CodeFailed 為其他錯誤, 例如設定檔寫入失敗.
	CodeFailed Code = "failed"
	// CodeClosed 為程式正在結束.
	CodeClosed Code = "closed"
)

// Error 為本套件回傳的錯誤. 經 Wails 傳給前端時, 錯誤的 cause 為
// {"code": ..., "message": ...}.
type Error struct {
	Code Code `json:"code"`
	// Message 為英文說明.
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func newErr(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf 回傳 err 鏈中第一個 *Error 的代碼; 不含 *Error 時回傳空字串.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
