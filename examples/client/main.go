package main

import (
	"context"
	"log"
	"time"

	"github.com/mrjvadi/go-messaging-framework/gmf" // نام پروژه خود را جایگزین کنید
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6380"})

	clientApp := gmf.New(rdb, "task_queue", "processing_group", 0)

	log.Println("--- Sending messages to the worker ---")

	// ارسال کار حیاتی
	clientApp.Enqueue(ctx, "NEW_ORDER", map[string]string{"id": "ORD-12345"})

	// ارسال رویداد
	clientApp.Publish(ctx, "user_events", map[string]string{"username": "Alice"})

	// ارسال درخواست RPC
	resp, err := clientApp.Request(ctx, "GET_USER_INFO", map[string]int{"id": 99}, 5*time.Second)
	if err != nil {
		log.Printf("RPC Request failed: %v\n", err)
	} else {
		log.Printf("RPC Response received: %s\n", string(resp))
	}
}
