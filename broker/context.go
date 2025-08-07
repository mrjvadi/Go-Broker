package broker

import (
	"context"
	"encoding/json"
)

// Context یک پوشش دور پیام‌های دریافتی است و متدهای کمکی را ارائه می‌دهد.
type Context struct {
	ctx     context.Context
	app     *App
	payload []byte
	msgID   string
}

// Bind محتوای پیام (payload) را در یک ساختار (struct) می‌ریزد.
func (c *Context) Bind(v interface{}) error {
	return json.Unmarshal(c.payload, v)
}

// Ctx کانتکست اصلی Go را برمی‌گرداند.
func (c *Context) Ctx() context.Context {
	return c.ctx
}