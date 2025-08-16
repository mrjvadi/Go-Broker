package broker

import (
	"context"
	"time"
)

type Group struct {
	app    *App
	prefix string
}

func (a *App) Group(prefix string) *Group {
	return &Group{app: a, prefix: prefix}
}

func (g *Group) sub(name string) string {
	if g.prefix == "" || name == "" {
		if name == "" {
			return g.prefix
		}
		return name
	}
	return g.prefix + "." + name // اگر خواستی ":" بذار
}

func (g *Group) Group(suffix string) *Group {
	return &Group{app: g.app, prefix: g.sub(suffix)}
}

// رجیستری‌ها
func (g *Group) OnTask(name string, h HandlerFunc)       { g.app.OnTask(g.sub(name), h) }
func (g *Group) OnEvent(channel string, h HandlerFunc)   { g.app.OnEvent(g.sub(channel), h) }
func (g *Group) OnRequest(name string, h RPCHandlerFunc) { g.app.OnRequest(g.sub(name), h) }

// کلاینت‌ها
func (g *Group) Enqueue(ctx context.Context, name string, payload any) (string, error) {
	return g.app.Enqueue(ctx, g.sub(name), payload)
}
func (g *Group) Publish(ctx context.Context, channel string, payload any) error {
	return g.app.Publish(ctx, g.sub(channel), payload)
}
func (g *Group) Request(ctx context.Context, name string, payload any, timeout time.Duration) ([]byte, error) {
	return g.app.Request(ctx, g.sub(name), payload, timeout)
}
func (g *Group) RequestFast(ctx context.Context, name string, payload any, timeout time.Duration) ([]byte, error) {
	return g.app.RequestFast(ctx, g.sub(name), payload, timeout)
}
