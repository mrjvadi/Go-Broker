package broker

type HandlerFunc func(c *Context) error
type RPCHandlerFunc func(c *Context) ([]byte, error)

const (
	fieldType    = "type"           // "task" | "rpc"
	fieldName    = "name"           // نام Task/RPC
	fieldPayload = "payload"        // []byte (json-encoded if not []byte)
	fieldReplyTo = "reply_to"       // کانال پاسخ برای RPC
	fieldCorrID  = "correlation_id" // ID تطبیق پاسخ

	typTask = "task"
	typRPC  = "rpc"
)

// Envelope پاسخ‌های RPC
type rpcEnvelope struct {
	CorrelationID string `json:"correlation_id"`
	Body          []byte `json:"body"` // json این را base64 می‌کند
	Error         string `json:"error"`
}
