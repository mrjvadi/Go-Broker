package broker

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// runTaskProcessor: مصرف‌کننده‌ی Stream (Tasks + RPC)
func (a *App) runTaskProcessor(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	// ساخت گروه (اگه از قبل بوده، خطا رو نادیده بگیر)
	_ = a.client.XGroupCreateMkStream(ctx, a.streamName, a.groupName, "0").Err()

	limiter := make(chan struct{}, a.maxConcurrentJobs)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			result, err := a.client.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    a.groupName,
				Consumer: a.consumerName,
				Streams:  []string{a.streamName, ">"},
				Count:    1,
				Block:    1 * time.Second,
			}).Result()
			if err != nil || len(result) == 0 || len(result[0].Messages) == 0 {
				continue
			}

			for _, message := range result[0].Messages {
				limiter <- struct{}{}
				go func(msg redis.XMessage) {
					defer func() { <-limiter }()

					taskType := getString(msg.Values["type"])
					raw := getString(msg.Values["payload"])

					c := &Context{
						ctx:     ctx,
						app:     a,
						payload: []byte(raw), // خام
						msgID:   msg.ID,
					}

					if replyTo, ok := msg.Values["reply_to"].(string); ok && replyTo != "" {
						// --- RPC branch ---
						if handler, found := a.rpcHandlers[taskType]; found {
							response, err := handler(c) // []byte خام

							env := rpcResponseEnvelope{
								CorrelationID: getString(msg.Values["correlation_id"]),
								Body:          response, // خام
							}
							if err != nil {
								env.Error = err.Error()
							}

							wire, _ := json.Marshal(&env) // فقط envelope را JSON می‌کنیم
							_ = a.client.Publish(ctx, replyTo, wire).Err()
						}
					} else {
						// --- Task branch ---
						if handler, found := a.taskHandlers[taskType]; found {
							if err := handler(c); err != nil {
								log.Printf("Error processing task %s: %v", c.msgID, err)
							}
						}
					}

					// Ack در هر دو شاخه
					_ = a.client.XAck(ctx, a.streamName, a.groupName, msg.ID).Err()
				}(message)
			}
		}
	}
}

func getString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// runEventSubscriber: مصرف‌کننده‌ی Pub/Sub برای Eventها
func (a *App) runEventSubscriber(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	if len(a.eventHandlers) == 0 {
		return
	}

	channels := make([]string, 0, len(a.eventHandlers))
	for ch := range a.eventHandlers {
		channels = append(channels, ch)
	}

	pubsub := a.client.Subscribe(ctx, channels...)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if handler, found := a.eventHandlers[msg.Channel]; found {
				c := &Context{ctx: ctx, app: a, payload: []byte(msg.Payload)}
				go handler(c)
			}
		}
	}
}
