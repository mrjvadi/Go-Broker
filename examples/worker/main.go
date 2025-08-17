package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/mrjvadi/go-broker/broker" // مسیر ایمپورت بر اساس ساختار پروژه شما
	"github.com/mrjvadi/go-broker/examples/handlers"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func main() {
	// ساخت یک context که با سیگنال سیستم (Ctrl+C) لغو می‌شود تا خاموش شدن امن را مدیریت کند
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// اتصال به Redis
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Could not connect to Redis: %v", err)
	}

	// ساخت لاگر ساختاریافته برای محیط پروداکشن
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// ساخت اپلیکیشن ورکر با تنظیمات سفارشی
	appInstance := broker.New(rdb, "task_queue", "main_processing_group",
		broker.WithLogger(logger),     // لاگر سفارشی
		broker.WithMaxJobs(560),       // حداکثر ۲۰ کار همزمان
		broker.WithStreamLength(1000), // نگهداری ۱۰۰۰ پیام آخر در صف
	)

	// --- گروه‌بندی و ثبت پردازشگرها ---

	// گروه برای تمام کارهای مربوط به سفارشات
	orderGroup := appInstance.Group("orders")
	orderGroup.OnTask("PROCESS", handlers.ProcessNewOrder)

	// گروه برای تمام رویدادها و درخواست‌های مربوط به کاربران
	userGroup := appInstance.Group("users")
	userGroup.OnEvent("login", handlers.LogUserLogin)
	userGroup.OnRequest("GET_INFO", handlers.GetUserInfo)

	log.Println("Starting worker server...")
	// اجرای فریمورک (این متد تا زمان دریافت سیگنال خروج، بلاک می‌شود)
	appInstance.Run()

	log.Println("Worker server shut down gracefully.")
}
