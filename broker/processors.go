package broker

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// runTaskProcessor حلقه پردازش برای کارها (Streams) است.
func (a *App) runTaskProcessor(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	a.client.XGroupCreateMkStream(ctx, a.streamName, a.groupName, "0").Err()
	limiter := make(chan struct{}, a.maxConcurrentJobs)

	for {
		if ctx.Err() != nil {
			return
		}

		result, err := a.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: a.groupName, Consumer: a.consumerName,
			Streams: []string{a.streamName, ">"}, Count: 10, Block: 2 * time.Second,
		}).Result()

		if err != nil {
			if err != redis.Nil && err != context.Canceled {
				a.logger.Warn("Error reading from stream", zap.Error(err))
			}
			continue
		}

		for _, stream := range result {
			for _, message := range stream.Messages {
				go func(msg redis.XMessage) {
					limiter <- struct{}{}
					defer func() { <-limiter }()
					defer func() {
						if r := recover(); r != nil {
							a.logger.Error("Panic recovered in task processor", zap.Any("panic", r), zap.String("msg_id", msg.ID))
						}
					}()

					handlerCtx, cancel := context.WithCancel(ctx)
					defer cancel()

					c := &Context{ctx: handlerCtx, app: a, payload: []byte(msg.Values["payload"].(string)), msgID: msg.ID}
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
								a.logger.Error("Task handler returned an error", zap.Error(err), zap.String("msg_id", c.msgID))
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
				go func(m *redis.Message) {
					handlerCtx, cancel := context.WithCancel(ctx)
					defer cancel()
					c := &Context{ctx: handlerCtx, app: a, payload: []byte(m.Payload)}
					handler(c)
				}(msg)
			}
		}
	}
}
