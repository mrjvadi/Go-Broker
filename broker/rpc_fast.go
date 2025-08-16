package broker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// RequestFast: مود سریع/Best-Effort
// - از یک PSubscribe سراسری استفاده می‌کند (ensureReplySubscriber)
// - پاسخ‌ها را با correlation_id به waiter مربوطه تحویل می‌دهد
// - زیر پیک خیلی بالا یا کرش پروسه ممکن است پاسخ گم شود (بر خلاف Request معمولی)
func (a *App) RequestFast(ctx context.Context, taskType string, payload interface{}, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// marshal هوشمند
	var b []byte
	switch v := payload.(type) {
	case []byte:
		b = v
	default:
		var err error
		b, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}

	// سابسکرایبر سراسری را (فقط یکبار) روشن کن
	a.ensureReplySubscriber()

	// قبل از XADD، waiter را ثبت کن و اگر زودرس وجود داشت همانجا تحویل بده
	corrID := nextID()
	replyTo := "reply:" + corrID
	resultCh := make(chan []byte, 1)

	a.replyMu.Lock()
	if early, ok := a.replyEarly[corrID]; ok {
		delete(a.replyEarly, corrID)
		a.replyMu.Unlock()
		resultCh <- early
	} else {
		a.replyWaiters[corrID] = resultCh
		a.replyMu.Unlock()
	}

	// درخواست RPC روی Stream (با deadline)
	args := &redis.XAddArgs{
		Stream: a.stream,
		Values: map[string]any{
			fieldType:    typRPC,
			fieldName:    taskType,
			fieldPayload: b,
			fieldReplyTo: replyTo,
			fieldCorrID:  corrID,
		},
	}
	//if a.streamMaxLen > 0 {
	//	args.MaxLenApprox = a.streamMaxLen
	//}

	ctxRW, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := a.rdb.XAdd(ctxRW, args).Result(); err != nil {
		a.replyMu.Lock()
		delete(a.replyWaiters, corrID)
		a.replyMu.Unlock()
		return nil, err
	}

	// انتظار پاسخ / تایم‌اوت / لغو
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case respBytes := <-resultCh:
		var env rpcEnvelope
		if err := json.Unmarshal(respBytes, &env); err != nil {
			return nil, err
		}
		if env.Error != "" {
			return nil, errors.New(env.Error)
		}
		return env.Body, nil

	case <-timer.C:
		a.replyMu.Lock()
		delete(a.replyWaiters, corrID)
		a.replyMu.Unlock()
		return nil, context.DeadlineExceeded

	case <-ctx.Done():
		a.replyMu.Lock()
		delete(a.replyWaiters, corrID)
		a.replyMu.Unlock()
		return nil, ctx.Err()
	}
}
