package broker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

func (a *App) Enqueue(ctx context.Context, taskType string, payload interface{}) (string, error) {
	// اگر payload []byte باشد، بدون marshal بفرست
	var b []byte
	switch v := payload.(type) {
	case []byte:
		b = v
	default:
		var err error
		b, err = json.Marshal(payload)
		if err != nil {
			return "", err
		}
	}
	args := &redis.XAddArgs{
		Stream: a.stream,
		Values: map[string]any{
			fieldType:    typTask,
			fieldName:    taskType,
			fieldPayload: b,
		},
	}
	//if a.streamMaxLen > 0 {
	//	args.MaxLenApprox = a.streamMaxLen
	//}
	return a.rdb.XAdd(ctx, args).Result()
}

func (a *App) Publish(ctx context.Context, channel string, payload interface{}) error {
	var b []byte
	switch v := payload.(type) {
	case []byte:
		b = v
	default:
		var err error
		b, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	return a.rdb.Publish(ctx, channel, b).Err()
}

// Request (Reliable): برای هر درخواست یک SUBSCRIBE موقتی → بدون گم‌شدن پاسخ
func (a *App) Request(ctx context.Context, taskType string, payload interface{}, timeout time.Duration) ([]byte, error) {
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

	// 1) ساخت reply channel و Subscribe قبل از ارسال درخواست
	corrID := nextID()
	replyTo := "reply:" + corrID
	sub := a.rdb.Subscribe(ctx, replyTo)
	defer sub.Close()

	// تضمین ثبت اشتراک (الگوی رسمی go-redis)
	if _, err := sub.Receive(ctx); err != nil {
		return nil, err
	}

	// 2) ارسال درخواست روی Stream (با deadline)
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
		return nil, err
	}

	// 3) انتظار پاسخ
	ctxWait, cancelWait := context.WithTimeout(ctx, timeout)
	defer cancelWait()

	msg, err := sub.ReceiveMessage(ctxWait)
	if err != nil {
		return nil, err
	}

	var env rpcEnvelope
	if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
		return nil, err
	}
	if env.Error != "" {
		return nil, errors.New(env.Error)
	}
	return env.Body, nil
}
