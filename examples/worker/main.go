package main

import (
	"context"
	"github.com/mrjvadi/go-messaging-framework/examples/handlers"
	"github.com/mrjvadi/go-messaging-framework/gmf"
	"log"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6380"})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Could not connect to Redis: %v", err)
	}

	appInstance := gmf.New(rdb, "task_queue", "processing_group", 10)

	appInstance.OnTask("NEW_ORDER", handlers.ProcessNewOrder)
	appInstance.OnEvent("user_events", handlers.LogUserLogin)
	appInstance.OnRequest("GET_USER_INFO", handlers.GetUserInfo)

	log.Println("Starting worker server...")
	appInstance.Run(ctx)

	log.Println("Worker server shut down.")
}
