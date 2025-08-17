package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mrjvadi/go-broker/broker" // Make sure this import path is correct for your project
	"github.com/redis/go-redis/v9"
)

func newRedisClient(addr string, db int, poolSize, minIdle int, readTimeout time.Duration) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		DB:           db,
		PoolSize:     poolSize,
		MinIdleConns: minIdle,
		ReadTimeout:  readTimeout,
		WriteTimeout: 200 * time.Millisecond,
	})
}

func newBrokerForBench(b *testing.B) (context.Context, *broker.App, string, string) {
	b.Helper()

	addr := getenv("REDIS_ADDR", "localhost:6379")
	db := getenvInt("REDIS_DB", 15)
	rdb := newRedisClient(addr, db, 512, 128, 200*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	if err := rdb.Ping(ctx).Err(); err != nil {
		b.Fatalf("redis ping failed: %v", err)
	}

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

	// *** CRITICAL FIX HERE: Graceful shutdown order ***
	b.Cleanup(func() {
		cancel()        // 1. Signal shutdown to all goroutines
		br.Close()      // 2. Wait for broker goroutines to finish
		_ = rdb.Close() // 3. Close the Redis connection
	})

	return ctx, br, stream, group
}

func BenchmarkTask_Throughput(b *testing.B) {
	ctx, br, _, _ := newBrokerForBench(b)

	done := make(chan struct{}, 1<<20)
	br.OnTask("bench_task", func(c *broker.Context) error {
		done <- struct{}{}
		return nil
	})

	go br.Run() // Using the new Run() without context

	time.Sleep(150 * time.Millisecond)
	payload := []byte("payload")
	b.ReportAllocs()
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

func BenchmarkTask_Parallel(b *testing.B) {
	ctx, br, _, _ := newBrokerForBench(b)

	done := make(chan struct{}, 1<<20)
	br.OnTask("bench_task", func(c *broker.Context) error {
		done <- struct{}{}
		return nil
	})

	go br.Run()

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

func BenchmarkRPC_Parallel(b *testing.B) {
	ctx := context.Background()
	addr := getenv("REDIS_ADDR", "localhost:6379")
	db := getenvInt("REDIS_DB", 15)

	rdbSrv := newRedisClient(addr, db, 256, 64, 200*time.Millisecond)
	rdbCli := newRedisClient(addr, db, 256, 64, 0)

	if getenv("REDIS_FLUSHDB", "1") == "1" {
		if err := rdbSrv.FlushDB(ctx).Err(); err != nil {
			b.Fatalf("flushdb failed: %v", err)
		}
	}

	stream := fmt.Sprintf("bench_stream:%d", time.Now().UnixNano())
	group := fmt.Sprintf("bench_group:%d", time.Now().UnixNano())

	// Server
	brSrv := broker.New(rdbSrv, stream, group)
	brSrv.OnRequest("GET_INFO", func(c *broker.Context) (interface{}, error) {
		return []byte("ok"), nil
	})

	// Client
	brCli := broker.New(rdbCli, stream, group)

	// *** CRITICAL FIX HERE: Graceful shutdown order ***
	b.Cleanup(func() {
		brSrv.Close()
		brCli.Close()
		_ = rdbSrv.Close()
		_ = rdbCli.Close()
	})

	go brSrv.Run()
	time.Sleep(250 * time.Millisecond)

	reqPayload := []byte(`{"id":101}`)
	timeout := 5 * time.Second
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := brCli.Request(ctx, "GET_INFO", reqPayload, timeout); err != nil {
				// Don't kill the benchmark on timeout, as it can happen under load
				if !errors.Is(err, context.DeadlineExceeded) {
					b.Logf("rpc request failed: %v", err)
				}
			}
		}
	})
	b.StopTimer()
}

// --- helpers ---
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
