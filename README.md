# Go-Broker: فریمورک پیام‌رسانی با Redis

Go-Broker یک فریمورک کوچک، قدرتمند و سطح بالا برای مدیریت پردازش‌های پس‌زمینه و ارتباطات رویدادمحور در برنامه‌های Go با استفاده از Redis (نسخه 9) است. این فریمورک با الهام از سادگی Gin، پیچیدگی‌های کار با Redis Streams و Pub/Sub را پنهان کرده و یک رابط تمیز و ساده برای توسعه‌دهندگان فراهم می‌کند.

---

## 🚀 ویژگی‌های کلیدی

* **پشتیبانی از سه الگوی پیام‌رسانی:**
    1.  **کارهای حیاتی (Tasks):** با استفاده از `Redis Streams` برای تضمین پردازش قابل اعتماد و ماندگار.
    2.  **رویدادهای آنی (Events):** با استفاده از `Redis Pub/Sub` برای اطلاع‌رسانی‌های زنده و غیرماندگار.
    3.  **درخواست/پاسخ (RPC):** الگویی برای ارسال یک کار و انتظار برای دریافت پاسخ با قابلیت تعیین Timeout.
* **مدیریت خودکار Goroutine:** برای هر کار یا رویداد دریافتی، یک Goroutine جدید به صورت خودکار ایجاد و مدیریت می‌شود.
* **کنترل همزمانی:** قابلیت تعیین حداکثر تعداد کارهای همزمان برای جلوگیری از فشار بیش از حد به منابع سیستم.
* **خاموش شدن امن (Graceful Shutdown):** با دریافت سیگنال خروج، برنامه منتظر می‌ماند تا کارهای در حال پردازش به اتمام برسند.
* **رابط کاربری ساده و سطح بالا (High-level API):** متدهای ساده مانند `Enqueue`, `Publish`, `Request` برای ارسال و `OnTask`, `OnEvent`, `OnRequest` برای ثبت پردازشگرها.

---
## ⚙️ پارامترهای اصلی

هنگام ساخت یک نمونه جدید از اپلیکیشن، شما چند پارامتر کلیدی را مشخص می‌کنید:
`broker.New(client, streamName, groupName, maxJobs)`

* `streamName`: نام صف اصلی شما برای کارهای حیاتی و ماندگار (Tasks و RPC).
* `groupName`: **یک نام ضروری برای گروه مصرف‌کنندگان (Consumer Group)**. این نام به چندین نمونه از برنامه شما (مثلاً اگر برنامه را روی چند سرور اجرا کنید) اجازه می‌دهد تا به صورت هماهنگ از یک صف مشترک (`streamName`) کارها را بردارند بدون اینکه یک کار را دو بار پردازش کنند. این کلید اصلی برای **مقیاس‌پذیری (Scalability)** و **پایداری (Fault Tolerance)** سیستم شماست.
* `maxJobs`: حداکثر تعداد کارهایی که به صورت همزمان از صف Stream پردازش می‌شوند.

---

## ⚡ نحوه استفاده

### ۱. راه‌اندازی ورکر (`examples/worker/main.go`)

برنامه اصلی شما فقط وظیفه راه‌اندازی و اجرای ورکر را بر عهده دارد.

```go
package main

import (
    "context"
    "go-broker-example/broker"
    "go-broker-example/examples/handlers"
    "[github.com/redis/go-redis/v9](https://github.com/redis/go-redis/v9)"
)

func main() {
    // ... ساخت context و اتصال به Redis ...
    appInstance := broker.New(rdb, "task_queue", "main_group", 10)

    // ثبت پردازشگرها
    appInstance.OnTask("NEW_ORDER", handlers.ProcessNewOrder)
    appInstance.OnEvent("user_events", handlers.LogUserLogin)
    appInstance.OnRequest("GET_USER_INFO", handlers.GetUserInfo)

    // اجرای فریمورک
    appInstance.Run(ctx)
}
```

### ۲. ارسال پیام از کلاینت (`examples/client/main.go`)

در هر بخش دیگری از برنامه خود می‌توانید با ساخت یک نمونه از `broker.App`، به راحتی پیام ارسال کنید.

```go
package main

import (
    "context"
    "go-broker-example/broker"
    "[github.com/redis/go-redis/v9](https://github.com/redis/go-redis/v9)"
)

func main() {
    // ... اتصال به Redis ...
    clientApp := broker.New(rdb, "task_queue", "main_group", 0)

    // ارسال یک کار حیاتی
    clientApp.Enqueue(ctx, "NEW_ORDER", map[string]string{"id": "ORD-12345"})

    // ارسال یک رویداد آنی
    clientApp.Publish(ctx, "user_events", map[string]string{"username": "Alice"})

    // ارسال یک درخواست RPC
    resp, err := clientApp.Request(ctx, "GET_USER_INFO", map[string]int{"id": 99}, 5*time.Second)
}
```