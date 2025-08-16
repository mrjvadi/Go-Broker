package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mrjvadi/go-broker/broker"
	"github.com/redis/go-redis/v9"
)

type Order struct {
	ID     int    `json:"id"`
	UserID int    `json:"user_id"`
	Title  string `json:"title"`
}

func main() {
	// Redis برای سرور: نیازی به تغییر ReadTimeout نیست
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:6379",
		DB:           0,
		PoolSize:     128,
		MinIdleConns: 16,
	})
	defer rdb.Close()

	app := broker.New(
		rdb,
		"app_stream", // اسم استریم
		"app_group",  // اسم گروه مصرف‌کننده
		broker.WithMaxJobs(64),
		broker.WithStreamLength(100_000),
	)

	orders := app.Group("orders")

	// Task handler
	orders.OnTask("create", func(c *broker.Context) error {
		var o Order
		if err := c.Bind(&o); err != nil {
			log.Printf("[TASK orders.create] bind err: %v", err)
			return err
		}
		// شبیه‌سازی پردازش
		time.Sleep(5 * time.Millisecond)
		log.Printf("[TASK orders.create] processed order: %+v", o)
		return nil
	})

	// Event handler (Pub/Sub)
	orders.OnEvent("events", func(c *broker.Context) error {
		var m map[string]any
		_ = c.Bind(&m) // اختیاری
		log.Printf("[EVENT orders.events] got: %s", string(c.Payload()))
		return nil
	})

	// RPC handler (Reliable/ Fast فرقی برای سرور ندارد)
	app.OnRequest("user.get", func(c *broker.Context) ([]byte, error) {
		var req struct {
			ID int `json:"id"`
		}
		_ = c.Bind(&req)
		resp := map[string]any{
			"id":    req.ID,
			"name":  "Alice",
			"email": "alice@example.com",
		}
		// پاسخ باید []byte (JSON) باشد؛ app خودش envelope می‌کند
		return mustJSON(resp), nil
	})

	// graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("server running …")
	app.Run(ctx) // بلوکه می‌شود تا سیگنال برسد
	log.Println("server stopped")
}

func mustJSON(v any) []byte {
	b, _ := jsonMarshal(v)
	return b
}

// از json استاندارد استفاده کن (اینجا فقط برای کوتاه‌نویسی)
func jsonMarshal(v any) ([]byte, error) {
	type m = map[string]any
	switch vv := v.(type) {
	case []byte:
		return vv, nil
	default:
		return json.Marshal(v)
	}
}
