package broker

import (
	"context"
	"encoding/json"
)

// Context داده‌های پیام و کمک‌متدها
type Context struct {
	ctx     context.Context
	app     *App
	payload []byte
	msgID   string
}

// Bind: payload را داخل ساختار v دیکد می‌کند (اگر JSON باشد)
func (c *Context) Bind(v interface{}) error {
	return json.Unmarshal(c.payload, v)
}

// Ctx: کانتکست اصلی را برمی‌گرداند
func (c *Context) Ctx() context.Context {
	return c.ctx
}
