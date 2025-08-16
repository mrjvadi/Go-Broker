package broker

import (
	"context"
	"encoding/json"
)

type Context struct {
	ctx     context.Context
	payload []byte
}

func (c *Context) Bind(v any) error     { return json.Unmarshal(c.payload, v) }
func (c *Context) Ctx() context.Context { return c.ctx }
func (c *Context) Payload() []byte      { return c.payload }
