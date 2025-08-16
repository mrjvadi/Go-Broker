package main

import (
	"context"
	"fmt"
	"log"
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
	// اگر می‌خوای از RequestFast استفاده کنی، بهتره ReadTimeout=0 باشه تا Pub/Sub قطع نشه
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:6379",
		DB:           0,
		PoolSize:     128,
		MinIdleConns: 16,
		ReadTimeout:  0, // برای Fast مفیده
		WriteTimeout: 200 * time.Millisecond,
	})
	defer rdb.Close()

	// کلاینت نیازی به هندلر ندارد (می‌تونی داشته باشی ولی معمولاً نه)
	app := broker.New(
		rdb,
		"app_stream",
		"app_group",
		broker.WithMaxJobs(32),
		broker.WithStreamLength(100_000),
	)

	ctx := context.Background()
	orders := app.Group("orders")

	// 1) Task: enqueue
	if _, err := orders.Enqueue(ctx, "create", Order{
		ID: 101, UserID: 7, Title: "First order",
	}); err != nil {
		log.Fatalf("enqueue failed: %v", err)
	}
	fmt.Println("task enqueued")

	// 2) Event: publish
	if err := orders.Publish(ctx, "events", map[string]any{
		"type": "created",
		"id":   101,
	}); err != nil {
		log.Fatalf("publish failed: %v", err)
	}
	fmt.Println("event published")

	// 3) RPC (Reliable): هیچ پاسخی گم نمی‌شود؛ برای هر درخواست یک SUBSCRIBE موقتی
	{
		respBytes, err := app.Request(ctx, "user.get", map[string]int{"id": 42}, 5*time.Second)
		if err != nil {
			log.Fatalf("RPC reliable failed: %v", err)
		}
		fmt.Printf("RPC reliable response: %s\n", string(respBytes))
	}

	// 4) RPC (Fast / Best-Effort): سریع‌تر، ولی ممکن است در شرایط نادر گم شود
	{
		respBytes, err := app.RequestFast(ctx, "user.get", map[string]int{"id": 43}, 5*time.Second)
		if err != nil {
			log.Fatalf("RPC fast failed: %v", err)
		}
		fmt.Printf("RPC fast response: %s\n", string(respBytes))
	}
}
