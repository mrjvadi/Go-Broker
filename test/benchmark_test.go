package test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mrjvadi/go-broker/broker"
)

func newRedisClient(addr string, db int, poolSize, minIdle int, readTimeout time.Duration) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		DB:           db,
		PoolSize:     poolSize,    // پیشنهاد: ≈ 2× همزمانی
		MinIdleConns: minIdle,     // کانکشن‌های گرم
		ReadTimeout:  readTimeout, // روی Pub/Sub: 0 (بدون deadline)
		WriteTimeout: 200 * time.Millisecond,
		// MaxRetries: 1, // اگر می‌خوای سخت‌گیر باشه
	})
}

func newBrokerForBench(b *testing.B) (context.Context, context.CancelFunc, *redis.Client, *broker.App, string, string) {
	b.Helper()

	addr := getenv("REDIS_ADDR", "localhost:6379")
	db := getenvInt("REDIS_DB", 15) // DB جدا برای بنچ
	rdb := newRedisClient(addr, db, 512, 128, 200*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	if err := rdb.Ping(ctx).Err(); err != nil {
		b.Fatalf("redis ping failed: %v", err)
	}

	// اختیاری: تمیز کردن DB برای حذف نویز
	if getenv("REDIS_FLUSHDB", "1") == "1" {
		if err := rdb.FlushDB(ctx).Err(); err != nil {
			b.Fatalf("flushdb failed: %v", err)
		}
	}

	stream := fmt.Sprintf("bench_stream:%d", time.Now().UnixNano())
	group := fmt.Sprintf("bench_group:%d", time.Now().UnixNano())

	br := broker.New(
		rdb,
		stream,
		group,
		broker.WithMaxJobs(3000),
		broker.WithStreamLength(100_000),
	)

	// shutdown تمیز
	b.Cleanup(func() {
		cancel()
		_ = rdb.Close()
	})

	return ctx, cancel, rdb, br, stream, group
}

// ------------------------------------------------------------
// Benchmark 1: Task – Throughput (enqueue N و صبر برای مصرف همه)
// ------------------------------------------------------------
func BenchmarkTask_Throughput(b *testing.B) {
	ctx, _, _, br, _, _ := newBrokerForBench(b)

	done := make(chan struct{}, 1<<20) // بافر بزرگ برای جلوگیری از بلاک‌شدن handler

	br.OnTask("bench_task", func(c *broker.Context) error {
		select {
		case done <- struct{}{}:
		default:
			// اگر پر شد، ادامه بده تا نویز ایجاد نشه
		}
		return nil
	})

	// Run بلوکه است؛ در تست با goroutine بالا می‌آید
	go br.Run(ctx)
	time.Sleep(150 * time.Millisecond) // فرصت برای ready شدن consumer

	payload := []byte("payload")
	b.ReportAllocs()

	// Warmup nhỏ
	const warm = 512
	for i := 0; i < warm; i++ {
		if _, err := br.Enqueue(ctx, "bench_task", payload); err != nil {
			b.Fatalf("warmup enqueue failed: %v", err)
		}
	}
	for i := 0; i < warm; i++ {
		<-done
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := br.Enqueue(ctx, "bench_task", payload); err != nil {
			b.Fatalf("enqueue failed: %v", err)
		}
	}
	for i := 0; i < b.N; i++ {
		<-done
	}
	b.StopTimer()
}

// ------------------------------------------------------------
// Benchmark 2: Task – Parallel (E2E)
// ------------------------------------------------------------
func BenchmarkTask_Parallel(b *testing.B) {
	ctx, _, _, br, _, _ := newBrokerForBench(b)

	done := make(chan struct{}, 1<<20)

	br.OnTask("bench_task", func(c *broker.Context) error {
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	go br.Run(ctx)
	time.Sleep(150 * time.Millisecond)

	payload := []byte("payload")
	b.ReportAllocs()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := br.Enqueue(ctx, "bench_task", payload); err != nil {
				b.Fatalf("enqueue failed: %v", err)
			}
			<-done
		}
	})
	b.StopTimer()
}

// ------------------------------------------------------------
// Benchmark 3: RPC – Parallel (E2E)
// هر iteration یک request کامل (round-trip) را می‌سنجد.
// ------------------------------------------------------------
func BenchmarkRPC_Parallel(b *testing.B) {
	ctx := context.Background()

	addr := getenv("REDIS_ADDR", "localhost:6379")
	db := getenvInt("REDIS_DB", 15)

	// دو کلاینت جدا برای سرور و کلاینت، با Pool مناسب
	rdbSrv := newRedisClient(addr, db, 256, 64, 200*time.Millisecond) // سرور
	rdbCli := newRedisClient(addr, db, 256, 64, 0)                    // کلاینت Pub/Sub → ReadTimeout=0

	// بستن کلاینت‌ها در انتها
	b.Cleanup(func() {
		_ = rdbSrv.Close()
		_ = rdbCli.Close()
	})

	if err := rdbSrv.Ping(ctx).Err(); err != nil {
		b.Fatalf("redis (srv) ping failed: %v", err)
	}
	if err := rdbCli.Ping(ctx).Err(); err != nil {
		b.Fatalf("redis (cli) ping failed: %v", err)
	}

	// اختیاری: محیط پاک برای بنچ
	if getenv("REDIS_FLUSHDB", "1") == "1" {
		if err := rdbSrv.FlushDB(ctx).Err(); err != nil {
			b.Fatalf("flushdb failed: %v", err)
		}
	}

	stream := fmt.Sprintf("bench_stream:%d", time.Now().UnixNano())
	group := fmt.Sprintf("bench_group:%d", time.Now().UnixNano())

	// سرور
	brSrv := broker.New(
		rdbSrv, stream, group,
		broker.WithMaxJobs(64),
		broker.WithStreamLength(100_000),
	)
	brSrv.OnRequest("GET_INFO", func(c *broker.Context) ([]byte, error) {
		return []byte("ok"), nil
	})

	// کلاینت
	brCli := broker.New(
		rdbCli, stream, group,
		broker.WithMaxJobs(64),
		broker.WithStreamLength(100_000), // ✅ اصلاح اشتباه تایپی
	)

	// بالا آوردن سرور
	serverCtx, cancel := context.WithCancel(ctx)
	// ترتیبِ صحیحِ بستن‌ها: اول اپ کلاینت (بستن Pub/Sub)، بعد کلاینت Redis
	b.Cleanup(func() {
		cancel()
		_ = brCli.Close() // سابسکرایبر reply:* بسته شود
		_ = rdbCli.Close()
	})

	go brSrv.Run(serverCtx)

	// کمی صبر تا سرور آماده شنونده‌ها بشه
	time.Sleep(250 * time.Millisecond)

	// Warm-up: تثبیت سابسکرایبر و مسیر رفت/برگشت
	if _, err := brCli.Request(ctx, "GET_INFO", []byte("ping"), 5*time.Second); err != nil {
		b.Fatalf("warmup rpc failed: %v", err)
	}

	reqPayload := []byte(`{"id":101}`)
	timeout := 5 * time.Second
	b.ReportAllocs()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := brCli.Request(ctx, "GET_INFO", reqPayload, timeout); err != nil {
				b.Fatalf("rpc request failed: %v", err)
			}
		}
	})
	b.StopTimer()
}

// -------------------- helpers --------------------

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		_, _ = fmt.Sscanf(v, "%d", &n)
		if n != 0 {
			return n
		}
	}
	return def
}
