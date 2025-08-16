package broker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

func (a *App) consumeStream(ctx context.Context) {
	c := a.rdb

	for {
		if ctx.Err() != nil {
			return
		}

		res, err := c.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    a.group,
			Consumer: a.consumerID,
			Streams:  []string{a.stream, ">"},
			Count:    int64(cap(a.sem)), // هر بار به اندازهٔ سقف همزمانی
			Block:    a.pollBlock,
		}).Result()

		if err == redis.Nil {
			continue
		}
		if err != nil {
			// خطای موقتی، کمی صبر کن
			time.Sleep(150 * time.Millisecond)
			continue
		}

		for _, str := range res {
			for _, msg := range str.Messages {
				m := msg // capture
				a.withConcurrency(func() {
					a.routeMessage(ctx, c, m)
				})
			}
		}
	}
}

func (a *App) routeMessage(ctx context.Context, c *redis.Client, m redis.XMessage) {
	// آماده کردن کانتکست و payload
	raw, _ := m.Values[fieldPayload].(string)
	body := []byte(raw)
	name, _ := m.Values[fieldName].(string)
	typ, _ := m.Values[fieldType].(string)
	corrID, _ := m.Values[fieldCorrID].(string)

	defer func() {
		// همیشه بعد از اجرا ACK می‌کنیم
		_ = c.XAck(ctx, a.stream, a.group, m.ID).Err()
	}()

	switch typ {
	case typTask:
		a.handleTask(ctx, name, body)

	case typRPC:
		replyTo, _ := m.Values[fieldReplyTo].(string)
		a.handleRPC(ctx, name, body, replyTo, corrID)

	default:
		// ناشناخته—ACK می‌شود و رها
	}
}

func (a *App) handleTask(ctx context.Context, name string, body []byte) {
	a.mu.RLock()
	h := a.taskHandlers[name]
	a.mu.RUnlock()
	if h == nil {
		a.logf("no task handler for %q", name)
		return
	}
	_ = h(&Context{ctx: ctx, payload: body})
}

func (a *App) handleRPC(ctx context.Context, name string, body []byte, replyTo, corrID string) {
	a.mu.RLock()
	h := a.rpcHandlers[name]
	a.mu.RUnlock()
	if h == nil {
		a.logf("no rpc handler for %q", name)
		return
	}

	resp, err := h(&Context{ctx: ctx, payload: body})
	env := rpcEnvelope{
		CorrelationID: corrID,
		Body:          resp,
	}
	if err != nil {
		env.Error = err.Error()
		env.Body = nil
	}
	b, _ := json.Marshal(env)

	// پاسخ را روی Pub/Sub ارسال کن
	_ = a.rdb.Publish(ctx, replyTo, b).Err()
}
