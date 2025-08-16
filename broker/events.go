package broker

import (
	"context"

	"github.com/redis/go-redis/v9"
)

func (a *App) startEventSubscriber(channel string, h HandlerFunc) {
	a.mu.Lock()
	if _, ok := a.eventSubs[channel]; ok {
		a.mu.Unlock()
		return // قبلاً روشن شده
	}
	sub := a.rdb.Subscribe(context.Background(), channel)
	a.eventSubs[channel] = sub
	a.mu.Unlock()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer sub.Close()

		// بافر مناسب برای رویدادها
		ch := sub.Channel(redis.WithChannelSize(1024))
		for msg := range ch {
			payload := []byte(msg.Payload)
			a.withConcurrency(func() {
				_ = h(&Context{ctx: context.Background(), payload: payload})
			})
		}
	}()
}

func (a *App) closeEventSubs() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for ch, sub := range a.eventSubs {
		_ = sub.Close()
		delete(a.eventSubs, ch)
	}
}
