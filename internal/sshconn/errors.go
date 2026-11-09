package sshconn

import (
	"errors"
	"fmt"
)

// Kind 為錯誤的種類, 供呼叫端決定顯示錯誤或自動重新連線.
type Kind int

// 錯誤種類. 各種類的使用者說明見 Error.Message.
const (
	// KindConnect 為無法建立連線, 包括網路錯誤, 名稱解析失敗, 逾時, 以及
	// 連線建立後遠端操作逾時 (遠端無回應).
	KindConnect Kind = iota + 1
	// KindAuth 為認證失敗.
	KindAuth
	// KindHostKey 為遠端的 host key 與 known_hosts 所收錄者不符.
	KindHostKey
	// KindPlatform 為遠端平台不符, 或無法取得遠端的平台資訊與家目錄.
	KindPlatform
	// KindUpload 為上傳遠端程式失敗.
	KindUpload
	// KindExec 為遠端程式無法執行.
	KindExec
	// KindProtocolTooNew 為遠端的協定版本高於 GUI 的協定版本.
	KindProtocolTooNew
	// KindConfig 為連線設定不合法, 例如缺少使用者名稱, 金鑰檔不存在或
	// ~/.ssh/config 無法解析.
	KindConfig
	// KindCanceled 為呼叫端取消, 包括 context 取消與回呼函數回傳 ErrCanceled.
	KindCanceled
)

// String 回傳種類的英文名稱, 供除錯使用.
func (k Kind) String() string {
	switch k {
	case KindConnect:
		return "connect"
	case KindAuth:
		return "auth"
	case KindHostKey:
		return "host-key"
	case KindPlatform:
		return "platform"
	case KindUpload:
		return "upload"
	case KindExec:
		return "exec"
	case KindProtocolTooNew:
		return "protocol-too-new"
	case KindConfig:
		return "config"
	case KindCanceled:
		return "canceled"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// ErrCanceled 由回呼函數回傳, 表示使用者取消輸入; 連線隨即中止, 錯誤種類為
// KindCanceled.
var ErrCanceled = errors.New("canceled by user")

// Error 為本套件回傳的錯誤. Message 為可直接顯示給使用者的英文說明, Err 為
// 底層原因, 供除錯使用. 兩者皆不含密碼.
type Error struct {
	Kind    Kind
	Message string
	Err     error
}

// Error 回傳 Message 與底層原因.
func (e *Error) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return e.Message + ": " + e.Err.Error()
}

// Unwrap 回傳底層原因.
func (e *Error) Unwrap() error { return e.Err }

// KindOf 回傳 err 鏈中第一個 *Error 的種類; 不含 *Error 時回傳 0.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func newError(kind Kind, err error, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...), Err: err}
}
