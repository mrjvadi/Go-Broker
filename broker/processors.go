package broker

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// runTaskProcessor حلقه پردازش برای کارها (Streams) است.
func (a *App) runTaskProcessor(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	a.client.XGroupCreateMkStream(ctx, a.streamName, a.groupName, "0").Err()
	limiter := make(chan struct{}, a.maxConcurrentJobs)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			result, err := a.client.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group: a.groupName, Consumer: a.consumerName,
				Streams: []string{a.streamName, ">"}, Count: 1, Block: 1 * time.Second,
			}).Result()
			if err != nil {
				continue
			}
			for _, message := range result[0].Messages {
				limiter <- struct{}{}
				go func(msg redis.XMessage) {
					defer func() { <-limiter }()
					c := &Context{ctx: ctx, app: a, payload: []byte(msg.Values["payload"].(string)), msgID: msg.ID}
					taskType := msg.Values["type"].(string)
					if replyTo, ok := msg.Values["reply_to"].(string); ok {
						if handler, found := a.rpcHandlers[taskType]; found {
							response, err := handler(c)
							if err == nil {
								respBytes, _ := json.Marshal(response)
								a.client.Publish(ctx, replyTo, respBytes)
							}
						}
					} else {
						if handler, found := a.taskHandlers[taskType]; found {
							if err := handler(c); err != nil {
								log.Printf("Error processing task %s: %v", c.msgID, err)
							}
						}
					}
					a.client.XAck(ctx, a.streamName, a.groupName, msg.ID)
				}(message)
			}
		}
	}
}

// runEventSubscriber حلقه پردازش برای رویدادها (Pub/Sub) است.
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