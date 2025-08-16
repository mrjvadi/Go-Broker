# Go-Broker

پیام‌بر سبک برای Go با Redis (Streams + Pub/Sub)

این پکیج یک «اپ پیام‌محور» ساده می‌سازد که سه الگوی اصلی را پوشش می‌دهد:

- **Task**‌: صفِ کارها روی Redis Stream (تک‌مصرفی با consumer-group)
- **Event**‌: رویدادهای fan-out با Pub/Sub
- **RPC**‌: درخواست/پاسخ (درخواست روی Stream، پاسخ روی Pub/Sub)

> نکته‌ی کلیدی: خودِ `App.Run(ctx)` **بلوکه** می‌ماند؛ هندلرها به‌صورت خودکار در **goroutine** با سقف همزمانی اجرا می‌شوند.

---

## نصب

```bash
go get github.com/mrjvadi/go-broker@latest
```

پیش‌نیاز: Redis 6+ (پیشنهادی 7)

برای اجرای محلی:

```bash
docker run --rm -p 6379:6379 redis:7
```

---

## شروع سریع

### سرور (Consumer)

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/mrjvadi/go-broker/broker"
)

type Order struct {
	ID     int    `json:"id"`
	UserID int    `json:"user_id"`
	Title  string `json:"title"`
}

func main() {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	defer rdb.Close()

	app := broker.New(rdb, "app_stream", "app_group",
		broker.WithMaxJobs(64),
		broker.WithStreamLength(100_000),
	)

	orders := app.Group("orders")

	// Task handler
	orders.OnTask("create", func(c *broker.Context) error {
		var o Order
		_ = c.Bind(&o)
		log.Printf("[TASK orders.create] %+v", o)
		return nil
	})

	// Event handler (Pub/Sub)
	orders.OnEvent("events", func(c *broker.Context) error {
		log.Printf("[EVENT orders.events] %s", string(c.Payload()))
		return nil
	})

	// RPC handler (سرور برای هر دو مود یکسان است)
	app.OnRequest("user.get", func(c *broker.Context) ([]byte, error) {
		return []byte(`{"id":42,"name":"Alice"}`), nil
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("server running …")
	app.Run(ctx) // بلوکه تا زمان سیگنال
}
```

### کلاینت (Reliable و Fast)

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/mrjvadi/go-broker/broker"
)

func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:6379",
		ReadTimeout: 0, // برای مود Fast بهتر است 0 باشد
	})
	defer rdb.Close()

	app := broker.New(rdb, "app_stream", "app_group",
		broker.WithMaxJobs(32),
	)

	ctx := context.Background()

	// 1) Task
	_, _ = app.Group("orders").Enqueue(ctx, "create", map[string]any{"id": 101, "user_id": 7, "title": "first"})

	// 2) Event
	_ = app.Group("orders").Publish(ctx, "events", map[string]any{"type": "created", "id": 101})

	// 3) RPC — حالت قابل اعتماد (Reliable)
	resp1, err := app.Request(ctx, "user.get", map[string]int{"id": 42}, 5*time.Second)
	fmt.Println("reliable:", string(resp1), err)

	// 4) RPC — حالت سریع/Best-Effort (Fast)
	resp2, err := app.RequestFast(ctx, "user.get", map[string]int{"id": 43}, 5*time.Second)
	fmt.Println("fast:", string(resp2), err)
}
```

---

## مفاهیم و طراحی

- **Stream + Group**: پیام‌های Task/RPC روی Redis Stream ذخیره می‌شوند و با `XREADGROUP` خوانده می‌شوند. هر `App` یک `stream` و یک `group` دارد.
- **Task**: پیام یک‌بار مصرف است؛ پس از اجرای موفق، `XACK` می‌شود.
- **Event**: روی کانال Pub/Sub منتشر می‌شود؛ همه‌ی مشترک‌ها دریافت می‌کنند.
- **RPC**: درخواست روی Stream، پاسخ روی Pub/Sub با کانال `reply:<correlation_id>`.
- **Goroutine handlers**: هر پیام/رویداد در goroutine اجرا می‌شود؛ سقف همزمانی با `WithMaxJobs(n)`.
- **Group(prefix)**: برای namespacing؛ مثلاً `Group("orders").OnTask("create", ...)` معادل نام کامل `orders.create` است.

---

## API خلاصه

```go
app := broker.New(rdb, streamName, groupName, opts...)

app.Run(ctx)                          // اجرای بلوکه‌شونده
app.Close()                           // بستن Subscriberها و انتظار برای کارهای درحال انجام

app.OnTask(name, HandlerFunc)         // ثبت Task handler
app.OnEvent(channel, HandlerFunc)     // ثبت Event handler (Pub/Sub)
app.OnRequest(name, RPCHandlerFunc)   // ثبت RPC handler

app.Enqueue(ctx, name, payload)       // ارسال Task روی Stream
app.Publish(ctx, channel, payload)    // انتشار Event روی Pub/Sub

// RPC — دو مود:
app.Request(ctx, name, payload, timeout)       // Reliable (هر درخواست یک Subscribe موقتی)
app.RequestFast(ctx, name, payload, timeout)   // Fast/Best-Effort (PSubscribe سراسری)

// Namespacing
orders := app.Group("orders")
orders.OnTask("create", h)
orders.Publish(ctx, "events", data)
orders.Request(ctx, "get", req, 3*time.Second)
```

**Options**:

- `WithMaxJobs(n int)` — سقف همزمانی اجرای هندلرها.
- `WithStreamLength(n int64)` — محدود کردن طول تقریبی stream (با Trim approximate).
- `WithLogger(*zap.Logger)` — لاگر سفارشی (اختیاری).

---

## نکات پایداری و عملکرد

- برای کلاینتی که از `` استفاده می‌کند، `ReadTimeout` را **۰** بگذار تا Pub/Sub قطع و وصل نشود.
- ترتیب بستن‌ها:
  1. کلاینت: **اول** `app.Close()` سپس `redisClient.Close()`
  2. سرور: روی سیگنال، `Run(ctx)` به‌صورت تمیز خارج می‌شود.
- برای بار بالا، `PoolSize` و `MinIdleConns` را افزایش بده.
- برای JSON کم‌هزینه‌تر می‌توان از `[]byte` مستقیم یا کتابخانه‌ی سریع‌تر استفاده کرد.

---

## بنچمارک‌ها

اجرای بنچ‌های نمونه:

```bash
go test ./test -bench . -run ^$
```

متغیرهای محیطی (اختیاری):

- `REDIS_ADDR` (پیش‌فرض: `localhost:6379`)
- `REDIS_DB` (پیش‌فرض: `15`)
- `REDIS_FLUSHDB` (پیش‌فرض: `1` برای پاک‌سازی قبل از هر بنچ)

> توجه: مود **Reliable** امن‌تر است ولی به‌دلیل Subscribe موقتی روی هر درخواست، کندتر از **Fast** خواهد بود.

---

## عیب‌یابی (Troubleshooting)

- ``** یا **``:
  - روی کلاینت Pub/Sub (مود Fast)، `ReadTimeout=0` بگذار.
  - ترتیب بستن: ابتدا `app.Close()` سپس `redis.Close()`.
- **«نه تایم‌اوت، نه خروجی» در RPC**:
  - مطمئن شو `reply_to` و `correlation_id` در درخواست تنظیم شده و سرور روی **همان کانال** publish می‌کند.
  - در مود Fast، سابسکرایبر سراسری (`ensureReplySubscriber`) باید فعال باشد؛ در `RequestFast` به‌صورت خودکار فعال می‌شود.
  - اگر لازمه با `redis-cli MONITOR` مسیر پیام را بررسی کن.

---

## لایسنس

MIT (یا طبق فایل LICENSE ریپو)

---

## نگهداری‌کننده

- @mrjvadi — هر پیشنهادی داشتی خوشحال می‌شیم!
