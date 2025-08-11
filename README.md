# Go-Broker (v1.0.1)

فریم‌ورک سبک پیام‌رسانی برای Go با تکیه بر **Redis Streams** و **Pub/Sub**. این README فقط بر اساس کدهای دایرکتوری `` در تگ **v1.0.1** نوشته شده و نمونه‌های قدیمی (`examples/`) عمداً نادیده گرفته شده‌اند.

---

## فهرست مطالب

- امکانات کلیدی
- معماری اجمالی
- پیش‌نیازها
- نصب و ایمپورت
- بیلد و اجرا (محلی/کانتینر)
- شروع سریع (ورکر و کلاینت)
- مرجع API (امضاهای واقعی)
- الگوها و رفتارها (Tasks / Events / RPC)
- Options (پیکربندی در v1.0.1)
- Namespacing با Group
- نکات مهم دربارهٔ پایداری، همزمانی و توقف
- یادداشت دربارهٔ examples/
- مشارکت و لایسنس

---

## امکانات کلیدی

- **Tasks (پایدار روی Streams):** خواندن با Consumer Group و تصدیق (ACK) پس از اجرای هندلر.
- **Events (غیرپایدار روی Pub/Sub):** دریافت زندهٔ پیام‌ها فقط وقتی مشترک هستید.
- **Request/Response (RPC):** ارسال درخواست روی Stream و دریافت پاسخ روی یک کانال Pub/Sub اختصاصیِ درون‌برنامه‌ای.
- **همزمانی کنترل‌شده:** محدودسازی پردازش موازی با بافر کانال داخلی.
- **لاگینگ اختیاری با zap:** تزریق `*zap.Logger`.

---

## معماری اجمالی

```
Client -- Enqueue --> Redis Stream <streamName> --(Group:<groupName>)--> Workers (Tasks)
Client -- Publish --> Redis Pub/Sub <channel> ------------------------------> Subscribers (Events)
Client -- Request --> Redis Stream <streamName> --(RPC handler)--> Worker -- Publish --> reply:<uuid>
```

---

## پیش‌نیازها

- Go 1.20+
- Redis 6+
- ماژول‌ها: `github.com/redis/go-redis/v9`, `go.uber.org/zap`

---

## نصب و ایمپورت

```bash
go get github.com/mrjvadi/Go-Broker@v1.0.1
```

```go
import (
    "github.com/mrjvadi/Go-Broker/broker"
    redis "github.com/redis/go-redis/v9"
    "go.uber.org/zap"
)
```

---

## بیلد و اجرا

> **نکته:** فولدر `examples/` قدیمی است—کدهای زیر را مستقیم در اپ خود استفاده کنید.

### اجرای محلی

1. Redis:

```bash
docker run -d --name redis -p 6379:6379 redis:7-alpine
```

2. اجرای برنامهٔ خودتان:

```bash
# در پوشهٔ پروژه‌تان
go mod init myapp
go get github.com/mrjvadi/Go-Broker@v1.0.1
go run .
```

3. بیلد باینری:

```bash
go build -o myworker .
```

### Dockerfile مینیمال

```dockerfile
FROM golang:1.22 AS build
WORKDIR /app
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/myworker .

FROM gcr.io/distroless/base-debian12
COPY --from=build /out/myworker /myworker
ENTRYPOINT ["/myworker"]
```

---

## شروع سریع (ورکر)

```go
package main

import (
    "context"
    "encoding/json"
    "log"

    "github.com/mrjvadi/Go-Broker/broker"
    redis "github.com/redis/go-redis/v9"
)

type NewOrder struct {
    ID string `json:"id"`
}

type UserReq struct {
    ID int `json:"id"`
}

type UserResp struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}

func main() {
    ctx := context.Background()
    rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

    app := broker.New(rdb, "task_queue", "main_group") // گزینه‌ها در بخش Options

    // Task handler (payload را JSON بایند کنید)
    app.OnTask("NEW_ORDER", func(c *broker.Context) error {
        var in NewOrder
        if err := c.Bind(&in); err != nil { return err }
        log.Println("processing NEW_ORDER:", in.ID)
        return nil
    })

    // Event handler (payload خام []byte است—در صورت نیاز JSON بایند کنید)
    app.OnEvent("user_events", func(c *broker.Context) error {
        log.Println("user event payload:", string(cPayload(c)))
        return nil
    })

    // RPC handler: خروجی باید []byte باشد
    app.OnRequest("GET_USER_INFO", func(c *broker.Context) ([]byte, error) {
        var req UserReq
        if err := c.Bind(&req); err != nil { return nil, err }
        out := UserResp{ID: req.ID, Name: "Alice"}
        return json.Marshal(out)
    })

    app.Run(ctx) // با لغو ctx متوقف می‌شود
}

func cPayload(c *broker.Context) []byte { var v struct{}; return (*struct{ P []byte })(nil).P }
```

> در هندلرها از `c.Bind(&T)` برای دیکد JSON استفاده کنید. در RPC، پاسخ را خودتان `json.Marshal` کنید و برگردانید.

### ارسال پیام از سرویس دیگر

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/mrjvadi/Go-Broker/broker"
    redis "github.com/redis/go-redis/v9"
)

func main() {
    ctx := context.Background()
    rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    client := broker.New(rdb, "task_queue", "main_group")

    // 1) Task
    _ = client.Enqueue(ctx, "NEW_ORDER", map[string]any{"id": "ORD-12345"})

    // 2) Event
    _ = client.Publish(ctx, "user_events", map[string]any{"username": "alice"})

    // 3) RPC (timeout اختیاری؛ پیش‌فرض اگر <=0 باشد، 5s)
    body, err := client.Request(ctx, "GET_USER_INFO", map[string]any{"id": 99}, 3*time.Second)
    if err != nil { panic(err) }
    var resp map[string]any
    _ = json.Unmarshal(body, &resp)
    fmt.Println("RPC response:", resp)
}
```

---

## مرجع API (امضاهای واقعی)

```go
// سازنده
func New(client *redis.Client, stream, group string, options ...Option) *App

// ارسال
func (a *App) Enqueue(ctx context.Context, taskType string, payload interface{}) (string, error)
func (a *App) Publish(ctx context.Context, channel string, payload interface{}) error
func (a *App) Request(ctx context.Context, taskType string, payload interface{}, timeout time.Duration) ([]byte, error)

// ثبت هندلرها
func (a *App) OnTask(taskType string, handler HandlerFunc)
func (a *App) OnEvent(channel string, handler HandlerFunc)
func (a *App) OnRequest(taskType string, handler RPCHandlerFunc)

// اجرا و توقف
func (a *App) Run(ctx context.Context)
func (a *App) Close() error

// Namespacing
func (a *App) Group(prefix string) *Group

// انواع هندلر و کانتکست
type HandlerFunc func(c *Context) error
type RPCHandlerFunc func(c *Context) ([]byte, error)

// Context
func (c *Context) Bind(v interface{}) error
func (c *Context) Ctx() context.Context
```

---

## الگوها و رفتارها

### Tasks (Streams)

- پیام با فیلدهای `type` و `payload` به استریم `streamName` افزوده می‌شود. اگر `WithStreamLength` فعال شده باشد، با **MAXLEN \~ N** تقریباً تریم می‌شود.
- مصرف با `XREADGROUP` و `Consumer = <hostname>-<pid>`.
- **ACK همیشه بعد از اجرای هندلر انجام می‌شود، حتی اگر خطا برگردانید.** بنابراین **ریتراِی خودکار داخلی وجود ندارد** و در صورت نیاز باید خودتان پیام را مجدداً صف کنید/لاگ کنید.
- در این نسخه **Claim خودکار پیام‌های Pending** پیاده‌سازی نشده است.

### Events (Pub/Sub)

- فقط کانال‌هایی که برایشان `OnEvent` ثبت شده، Subscribe می‌شوند.
- پیام‌ها غیرپایدارند؛ اگر آنلاین نباشید، از دست می‌روند.

### Request/Response (RPC)

- درخواست به Stream می‌رود و پاسخ روی یک کانال Pub/Sub اختصاصی به شکل **envelope** برمی‌گردد:
  ```json
  {"correlation_id":"...","body":"<raw bytes>","error":""}
  ```
- هندلر RPC باید `[]byte` برگرداند. برای JSON، خودتان `json.Marshal` کنید.
- اگر `timeout <= 0` باشد، **۵ ثانیه** در نظر گرفته می‌شود.

---

## Options (v1.0.1)

> پترن Functional Options برای پیکربندی اولیهٔ `App`.

| نام گزینه          | امضا                       | پیش‌فرض        | توضیح                                                                 |
| ------------------ | -------------------------- | -------------- | --------------------------------------------------------------------- |
| `WithLogger`       | `func(*zap.Logger) Option` | `zap.NewNop()` | تزریق لاگر سفارشی.                                                    |
| `WithMaxJobs`      | `func(int) Option`         | `10`           | بیشینهٔ Jobهای همزمان (بافر کانال داخلی). فقط اگر `n>0` تنظیم می‌شود. |
| `WithStreamLength` | `func(int64) Option`       | `0` (غیرفعال)  | اگر `>0` باشد، افزودن با `MAXLEN ~ N` انجام می‌شود (تریم تقریبی).     |

نمونهٔ استفاده:

```go
logr, _ := zap.NewProduction()
app := broker.New(rdb, "task_queue", "main_group",
    broker.WithLogger(logr),
    broker.WithMaxJobs(32),
    broker.WithStreamLength(100_000),
)
```

---

## Namespacing با Group

می‌توانید دسته‌ای از هندلرها را با پیشوند مشترک ثبت کنید:

```go
g := app.Group("billing")
g.OnTask("NEW_ORDER", taskHandler)        // billing.NEW_ORDER
g.OnEvent("user_events", eventHandler)    // billing.user_events
g.OnRequest("GET_USER_INFO", rpcHandler)  // billing.GET_USER_INFO
```

---

## نکات مهم: پایداری، همزمانی، توقف

- **همزمانی:** خوانش هر بار `Count=1` است ولی هر پیام در goroutine جداگانه اجرا می‌شود و توسط `WithMaxJobs` محدود می‌گردد.
- **توقف:** با لغو `ctx` حلقه‌های خوانش متوقف می‌شوند، ولی goroutineهای درحال‌اجرای هندلر تا اتمام ادامه می‌دهند (انتظار صریح برای آن‌ها وجود ندارد).
- **ACK روی خطا:** چون ACK بدون شرط انجام می‌شود، برای Retry باید منطق خودتان را اضافه کنید (مثلاً re-enqueue یا DLQ در لایهٔ اپ).
- **Events:** خطای هندلر Event لاگ/مدیریت داخلی ندارد—در صورت نیاز خودتان لاگ کنید.

---

## یادداشت دربارهٔ examples/

- نمونه‌های موجود با API فعلی همگام نیستند. برای تست از کدهای همین README استفاده کنید.

---

## مشارکت و لایسنس

پیشنهادها و PRها خوش‌آمدند. لایسنس: MIT.

