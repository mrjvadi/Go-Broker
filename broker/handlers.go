package broker

// HandlerFunc برای Tasks / Events
type HandlerFunc func(c *Context) error

// RPCHandlerFunc: پاسخ خام []byte
// اگر JSON می‌خوای، در هندلر از c.Bind(...) استفاده کن و خروجی را خودت JSON کن.
type RPCHandlerFunc func(c *Context) ([]byte, error)
