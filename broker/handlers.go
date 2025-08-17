package broker

// HandlerFunc یک نوع داده برای تمام توابع پردازشگر یک‌طرفه است.
type HandlerFunc func(c *Context) error

// RPCHandlerFunc یک نوع داده برای پردازشگرهای درخواست/پاسخ است.
type RPCHandlerFunc func(c *Context) (response interface{}, err error)