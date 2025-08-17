package main

import (
	"context"
	"log"
	"time"

	"github.com/mrjvadi/go-broker/broker" // مسیر ایمپورت بر اساس ساختار پروژه شما
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Could not connect to Redis: %v", err)
	}

	// برای ارسال، ما از همان ساختار App استفاده می‌کنیم اما متد Run آن را صدا نمی‌زنیم
	// پارامترهای group و maxJobs در اینجا اهمیتی ندارند
	clientApp := broker.New(rdb, "task_queue", "main_processing_group", broker.WithMaxJobs(500), broker.WithStreamLength(1000))

	log.Println("--- Sending messages to the worker ---")

	for i := 0; i < 100; i++ {
		// ارسال یک کار حیاتی به گروه "orders"
		go func() {
			clientApp.Enqueue(ctx, "orders.PROCESS", map[string]string{"id": "ORD-555-123"})
			log.Println("Enqueued: Task 'orders.PROCESS'")

			// ارسال یک رویداد آنی به گروه "users"
			clientApp.Publish(ctx, "users.login", map[string]string{"username": "Bob"})
			log.Println("Published: Event 'users.login'")

			// ارسال یک درخواست RPC به گروه "users" و انتظار برای پاسخ
			log.Println("Sending: RPC request 'users.GET_INFO'")
			resp, err := clientApp.Request(ctx, "users.GET_INFO", map[string]int{"id": 101}, 5*time.Second)
			if err != nil {
				log.Printf("RPC Request failed: %v\n", err)
			} else {
				log.Printf("RPC Response received: %s\n", string(resp))
			}
		}()
	}
	select {}
}
