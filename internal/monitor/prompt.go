package monitor

import (
	"context"
	"strconv"
	"sync"

	"gpumon/internal/sshconn"
)

// promptBroker 轉送登入密碼與金鑰密碼的輸入要求: 以通知送出要求, 等待前端以
// AnswerPrompt 或 CancelPrompt 回覆.
type promptBroker struct {
	notify Notifier

	mu      sync.Mutex
	next    int
	waiting map[string]chan []byte
}

// ask 送出要求並等待回覆. 前端取消時回傳 sshconn.ErrCanceled; ctx 結束時送出
// PromptClosed 並回傳 ctx 的錯誤. 回傳的位元組陣列歸呼叫端所有.
func (b *promptBroker) ask(ctx context.Context, p Prompt) ([]byte, error) {
	ch := make(chan []byte, 1)
	b.mu.Lock()
	b.next++
	p.ID = strconv.Itoa(b.next)
	if b.waiting == nil {
		b.waiting = map[string]chan []byte{}
	}
	b.waiting[p.ID] = ch
	b.mu.Unlock()

	b.notify.Notify(EventPrompt, p)
	select {
	case secret, ok := <-ch:
		if !ok {
			return nil, sshconn.ErrCanceled
		}
		return secret, nil
	case <-ctx.Done():
		b.mu.Lock()
		_, still := b.waiting[p.ID]
		delete(b.waiting, p.ID)
		b.mu.Unlock()
		if !still {
			// 回覆與取消同時發生, 已送達的密碼立即銷毀.
			if secret, ok := <-ch; ok {
				clear(secret)
			}
		}
		b.notify.Notify(EventPromptClosed, PromptClosed{ID: p.ID})
		return nil, ctx.Err()
	}
}

// answer 送出 id 的回覆. secret 交由等待者處置; 找不到 id 時立即以零覆寫.
func (b *promptBroker) answer(id string, secret []byte) error {
	b.mu.Lock()
	ch, ok := b.waiting[id]
	delete(b.waiting, id)
	b.mu.Unlock()
	if !ok {
		clear(secret)
		return newErr(CodeNotFound, "No pending prompt %s", id)
	}
	ch <- secret
	return nil
}

// cancel 取消 id 的要求.
func (b *promptBroker) cancel(id string) error {
	b.mu.Lock()
	ch, ok := b.waiting[id]
	delete(b.waiting, id)
	b.mu.Unlock()
	if !ok {
		return newErr(CodeNotFound, "No pending prompt %s", id)
	}
	close(ch)
	return nil
}

// uploadBroker 轉送上傳遠端程式之前的確認: 以通知送出上傳通知, 等待前端以
// ConfirmUpload 回覆.
type uploadBroker struct {
	notify Notifier

	mu      sync.Mutex
	next    int
	waiting map[string]chan struct{}
}

// ask 送出上傳通知並等待確認. ctx 結束時送出 UploadClosed 並回傳 ctx 的錯誤.
func (b *uploadBroker) ask(ctx context.Context, n UploadNotice) error {
	ch := make(chan struct{})
	b.mu.Lock()
	b.next++
	n.ID = strconv.Itoa(b.next)
	if b.waiting == nil {
		b.waiting = map[string]chan struct{}{}
	}
	b.waiting[n.ID] = ch
	b.mu.Unlock()

	b.notify.Notify(EventUpload, n)
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiting, n.ID)
		b.mu.Unlock()
		b.notify.Notify(EventUploadClosed, UploadClosed{ID: n.ID})
		return ctx.Err()
	}
}

// confirm 確認 id 的上傳通知.
func (b *uploadBroker) confirm(id string) error {
	b.mu.Lock()
	ch, ok := b.waiting[id]
	delete(b.waiting, id)
	b.mu.Unlock()
	if !ok {
		return newErr(CodeNotFound, "No pending upload notice %s", id)
	}
	close(ch)
	return nil
}
