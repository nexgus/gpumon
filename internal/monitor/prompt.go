package monitor

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"

	"gpumon/internal/sshconn"
)

// idSeq 產生對話框的識別字串. 所有種類的對話框共用同一個序列, 識別字串互不重複.
type idSeq struct{ n atomic.Uint64 }

func (s *idSeq) next() string { return strconv.FormatUint(s.n.Add(1), 10) }

// promptBroker 轉送登入密碼與金鑰密碼的輸入要求: 以通知送出要求, 等待前端以
// AnswerPrompt 或 CancelPrompt 回覆.
type promptBroker struct {
	notify Notifier
	ids    *idSeq

	mu      sync.Mutex
	waiting map[string]chan []byte
}

// ask 送出要求並等待回覆. 前端取消時回傳 sshconn.ErrCanceled; ctx 結束時送出
// PromptClosed 並回傳 ctx 的錯誤. 回傳的位元組陣列歸呼叫端所有.
func (b *promptBroker) ask(ctx context.Context, p Prompt) ([]byte, error) {
	ch := make(chan []byte, 1)
	p.ID = b.ids.next()
	b.mu.Lock()
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
	ids    *idSeq

	mu      sync.Mutex
	waiting map[string]chan struct{}
}

// ask 送出上傳通知並等待確認. ctx 結束時送出 UploadClosed 並回傳 ctx 的錯誤.
func (b *uploadBroker) ask(ctx context.Context, n UploadNotice) error {
	ch := make(chan struct{})
	n.ID = b.ids.next()
	b.mu.Lock()
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

// replyBroker 轉送需要使用者以選擇回覆的詢問 (主機金鑰的確認, 免密碼登入的詢問):
// 以通知送出詢問, 等待前端回覆. T 為回覆的型別.
type replyBroker[T any] struct {
	notify Notifier
	ids    *idSeq
	// event 與 closed 為詢問與詢問失效的通知名稱; closedData 回傳詢問失效的通知
	// 內容; what 為找不到詢問時錯誤說明所用的名稱.
	event, closed string
	closedData    func(id string) any
	what          string

	mu      sync.Mutex
	waiting map[string]chan T
}

// ask 以 data(id) 為內容送出詢問並等待回覆. ctx 結束時送出詢問失效的通知並回傳
// ctx 的錯誤.
func (b *replyBroker[T]) ask(ctx context.Context, data func(id string) any) (T, error) {
	ch := make(chan T, 1)
	id := b.ids.next()
	b.mu.Lock()
	if b.waiting == nil {
		b.waiting = map[string]chan T{}
	}
	b.waiting[id] = ch
	b.mu.Unlock()

	b.notify.Notify(b.event, data(id))
	select {
	case v := <-ch:
		return v, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiting, id)
		b.mu.Unlock()
		b.notify.Notify(b.closed, b.closedData(id))
		var zero T
		return zero, ctx.Err()
	}
}

// reply 送出 id 的回覆. 詢問已失效時回傳 CodeNotFound.
func (b *replyBroker[T]) reply(id string, v T) error {
	b.mu.Lock()
	ch, ok := b.waiting[id]
	delete(b.waiting, id)
	b.mu.Unlock()
	if !ok {
		return newErr(CodeNotFound, "No pending %s %s", b.what, id)
	}
	ch <- v
	return nil
}
