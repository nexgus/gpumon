package sshconn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// KeySetupChoice 為使用者對免密碼登入詢問的回答.
type KeySetupChoice int

// 免密碼登入詢問的三種回答.
const (
	// KeySetupLater 為 "暫不設定", 不做任何事.
	KeySetupLater KeySetupChoice = iota + 1
	// KeySetupNever 為 "不再詢問". 記錄由呼叫端負責, 本套件不做任何事.
	KeySetupNever
	// KeySetupNow 為 "立即設定", 本套件產生金鑰檔 (本機沒有金鑰檔時) 並把公鑰附加
	// 至遠端的 ~/.ssh/authorized_keys.
	KeySetupNow
)

// KeySetupRequest 為詢問是否設定免密碼登入時提供給回呼函數的資訊.
type KeySetupRequest struct {
	// Host 為連線目標的顯示名稱, 即 Config.Host; User 為實際登入的使用者名稱.
	Host string
	User string
}

// KeySetupFunc 詢問使用者是否設定免密碼登入. 回傳錯誤時中止連線, 錯誤種類為
// KindCanceled. ctx 於呼叫端的 context 結束或 SSH 連線中斷時結束, 回呼函數應隨之
// 返回. 等待回呼函數的時間不計入任何逾時.
type KeySetupFunc func(ctx context.Context, req KeySetupRequest) (KeySetupChoice, error)

// KeySetupOutcome 為立即設定的結果.
type KeySetupOutcome int

// 立即設定的三種結果.
const (
	// KeySetupSucceeded 為公鑰已附加至遠端的 authorized_keys.
	KeySetupSucceeded KeySetupOutcome = iota + 1
	// KeySetupGenerateFailed 為本機沒有金鑰檔而產生金鑰檔失敗, 未附加公鑰.
	KeySetupGenerateFailed
	// KeySetupAppendFailed 為附加公鑰至遠端的 authorized_keys 失敗.
	KeySetupAppendFailed
)

// KeySetupResult 為立即設定的結果.
type KeySetupResult struct {
	Outcome KeySetupOutcome
	// KeyFile 為公鑰所屬的金鑰檔; 產生失敗時為預定產生的位置.
	KeyFile string
	// Generated 表示金鑰檔為本次產生 (或嘗試產生).
	Generated bool
	// Err 為失敗的原因, 成功時為 nil. 其說明為英文, 可直接顯示.
	Err error
}

// loginInfo 為一次登入的經過, 供判斷是否詢問免密碼登入.
type loginInfo struct {
	// byPassword 表示認證過程中曾要求登入密碼且認證成功.
	byPassword bool
	// encryptedRequested 表示遠端主機曾要求以有密碼保護的金鑰簽章.
	encryptedRequested bool
	// files 為存在且能取得公鑰的金鑰檔, 依規格 6.3 "金鑰檔的來源" 的順序.
	files []localKey
	// agentKeys 為 ssh-agent 提供的金鑰數, 不受 IdentitiesOnly 影響.
	agentKeys int
	// newKeyPath 為本機沒有金鑰檔時產生金鑰檔的位置.
	newKeyPath string
}

// eligible 回報是否符合規格 6.3 免密碼登入詢問條件中屬於本次登入的部分: 以密碼
// 登入成功, 遠端主機未曾要求以有密碼保護的金鑰簽章, 且本機有金鑰檔或本機沒有
// 金鑰檔而 ssh-agent 也沒有金鑰. 主機未記為不再詢問與不是自動重新連線兩項由呼叫端
// 以是否提供 Config.KeySetup 判斷.
func (l loginInfo) eligible() bool {
	return l.byPassword && !l.encryptedRequested && (len(l.files) > 0 || l.agentKeys == 0)
}

// OfferKeySetup 於符合條件時以 Config.KeySetup 詢問是否設定免密碼登入, 使用者選擇
// 立即設定時進行設定並以結果呼叫 Config.KeySetupDone. 等待回答期間的 keepalive
// 與連線中斷的判定與上傳確認相同 (見 waitUser), 連線中斷時回傳 KindConnect 錯誤.
// 產生金鑰檔或附加公鑰失敗不回傳錯誤, 連線流程照常繼續. timeout 為附加公鑰的遠端
// 指令的逾時上限, 亦為 keepalive 的回覆期限.
func (c *Client) OfferKeySetup(ctx context.Context, timeout time.Duration) error {
	if c.keySetup == nil || !c.login.eligible() {
		return nil
	}
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	req := KeySetupRequest{Host: c.Host, User: c.ssh.User()}
	var choice KeySetupChoice
	err := c.waitUser(ctx, timeout, "the answer about passwordless login",
		"Connecting to %s was canceled", func(wctx context.Context) error {
			var err error
			choice, err = c.keySetup(wctx, req)
			return err
		})
	if err != nil {
		return err
	}
	if choice != KeySetupNow {
		return nil
	}
	res := c.installKey(ctx, timeout)
	if ctx.Err() != nil {
		return newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", c.Host)
	}
	if c.keySetupDone != nil {
		c.keySetupDone(res)
	}
	return nil
}

// installKey 於本機沒有金鑰檔時產生金鑰檔, 再把本機第一個金鑰檔的公鑰附加至遠端的
// ~/.ssh/authorized_keys.
func (c *Client) installKey(ctx context.Context, timeout time.Duration) KeySetupResult {
	var res KeySetupResult
	var path string
	var pub ssh.PublicKey
	if len(c.login.files) > 0 {
		path, pub = c.login.files[0].path, c.login.files[0].pub
	} else {
		path = c.login.newKeyPath
		res.Generated = true
		var err error
		pub, err = generateKeyFile(path)
		if err != nil {
			res.Outcome, res.KeyFile, res.Err = KeySetupGenerateFailed, path, err
			return res
		}
	}
	res.KeyFile = path
	if err := c.appendAuthorizedKey(ctx, authorizedKeyLine(path, pub), timeout); err != nil {
		res.Outcome, res.Err = KeySetupAppendFailed, err
		return res
	}
	res.Outcome = KeySetupSucceeded
	return res
}

// generateKeyFile 在 path 產生一個沒有金鑰密碼的 Ed25519 金鑰檔 (OpenSSH 私鑰
// 格式), 公鑰寫入 path 加上 .pub 的檔案. 所在目錄不存在時建立, 權限為 0700;
// 金鑰檔的權限為 0600, 公鑰檔為 0644, 與 ssh-keygen 相同. path 已存在時不覆寫,
// 回傳錯誤; 公鑰檔已存在時覆寫. 公鑰檔寫入失敗時刪除已寫入的金鑰檔.
func generateKeyFile(path string) (ssh.PublicKey, error) {
	edPub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("cannot generate a key: %w", err)
	}
	comment := keyComment()
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, fmt.Errorf("cannot encode the key: %w", err)
	}
	pub, err := ssh.NewPublicKey(edPub)
	if err != nil {
		return nil, fmt.Errorf("cannot encode the public key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%s already exists but is not a usable key file", path)
		}
		return nil, err
	}
	_, err = f.Write(pem.EncodeToMemory(block))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
		if comment != "" {
			line += " " + comment
		}
		err = os.WriteFile(path+".pub", []byte(line+"\n"), 0o644)
	}
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	return pub, nil
}

// keyComment 回傳產生金鑰檔時的註解 "本機使用者名稱@本機主機名稱", 與 ssh-keygen
// 相同. 取不到時回傳可取得的部分或空字串.
func keyComment() string {
	u := localUserName()
	h, _ := os.Hostname()
	switch {
	case u != "" && h != "":
		return u + "@" + h
	case u != "":
		return u
	}
	return h
}

// authorizedKeyLine 回傳附加至 authorized_keys 的一行. path 加上 .pub 的檔案為
// 同一把公鑰時沿用其註解, 否則不含註解.
func authorizedKeyLine(path string, pub ssh.PublicKey) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	data, err := os.ReadFile(path + ".pub")
	if err != nil {
		return line
	}
	filePub, comment, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil || string(filePub.Marshal()) != string(pub.Marshal()) {
		return line
	}
	if comment = strings.TrimSpace(comment); comment != "" {
		line += " " + comment
	}
	return line
}

// authorizedKeysScript 為附加公鑰的遠端指令, %s 代入已引用的公鑰行. ~/.ssh 與
// authorized_keys 不存在時建立, 權限依序為 0700 與 0600; 既存者不更動權限. 既有
// 內容只附加, 檔尾沒有換行時先補換行. 錯誤訊息導向 stdout, 供失敗時說明原因.
const authorizedKeysScript = `{ set -e; cd; ` +
	`if [ ! -d .ssh ]; then mkdir -m 700 .ssh; fi; ` +
	`if [ ! -e .ssh/authorized_keys ]; then (umask 077 && : > .ssh/authorized_keys); fi; ` +
	`if [ -s .ssh/authorized_keys ] && [ -n "$(tail -c 1 .ssh/authorized_keys)" ]; then printf '\n' >> .ssh/authorized_keys; fi; ` +
	`printf '%%s\n' %s >> .ssh/authorized_keys; } 2>&1`

// appendAuthorizedKey 把 line 附加至遠端的 ~/.ssh/authorized_keys.
func (c *Client) appendAuthorizedKey(ctx context.Context, line string, timeout time.Duration) error {
	out, code, err := c.run(ctx, shCommand(fmt.Sprintf(authorizedKeysScript, shellQuote(line))), timeout)
	if err != nil {
		return err
	}
	if code != 0 {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if msg == "" {
			msg = fmt.Sprintf("the command exited with status %d", code)
		}
		return fmt.Errorf("cannot add the public key to ~/.ssh/authorized_keys on %s: %s", c.Host, msg)
	}
	return nil
}
