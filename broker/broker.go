package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// App ساختار اصلی و مرکزی فریمورک است.
type App struct {
	client            *redis.Client
	logger            *zap.Logger
	streamName        string
	groupName         string
	consumerName      string
	taskHandlers      map[string]HandlerFunc
	eventHandlers     map[string]HandlerFunc
	rpcHandlers       map[string]RPCHandlerFunc
	maxConcurrentJobs int
	streamMaxLen      int64
}

// Group یک ساختار برای گروه‌بندی پردازشگرها با یک پیشوند مشترک است.
type Group struct {
	app    *App
	prefix string
}

// New یک نمونه جدید از اپلیکیشن را با تنظیمات اولیه و گزینه‌های اختیاری می‌سازد.
func New(client *redis.Client, stream, group string, options ...Option) *App {
	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	app := &App{
		client:            client,
		streamName:        stream,
		groupName:         group,
		consumerName:      consumerName,
		taskHandlers:      make(map[string]HandlerFunc),
		eventHandlers:     make(map[string]HandlerFunc),
		rpcHandlers:       make(map[string]RPCHandlerFunc),
		maxConcurrentJobs: 10,
		logger:            zap.NewNop(),
	}
	for _, option := range options {
		option(app)
	}
	return app
}

// Group یک گروه جدید برای ثبت پردازشگرها با پیشوند مشترک ایجاد می‌کند.
func (a *App) Group(prefix string) *Group {
	return &Group{
		app:    a,
		prefix: prefix,
	}
}

// --- متدهای ارسال پیام ---

func (a *App) Enqueue(ctx context.Context, taskType string, payload interface{}) (string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("could not marshal payload: %w", err)
	}
	values := map[string]interface{}{"type": taskType, "payload": string(payloadBytes)}
	return a.client.XAdd(ctx, &redis.XAddArgs{
		Stream: a.streamName,
		Values: values,
		MaxLen: a.streamMaxLen,
		Approx: true,
	}).Result()
}

func (a *App) Publish(ctx context.Context, channel string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("could not marshal payload: %w", err)
	}
	return a.client.Publish(ctx, channel, payloadBytes).Err()
}

func (a *App) Request(ctx context.Context, taskType string, payload interface{}, timeout time.Duration) ([]byte, error) {
	correlationID := uuid.NewString()
	replyToChannel := fmt.Sprintf("reply:%s", correlationID)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("could not marshal payload: %w", err)
	}
	pubsub := a.client.Subscribe(ctx, replyToChannel)
	defer pubsub.Close()
	values := map[string]interface{}{
		"type":           taskType,
		"payload":        string(payloadBytes),
		"reply_to":       replyToChannel,
		"correlation_id": correlationID,
	}
	if _, err := a.client.XAdd(ctx, &redis.XAddArgs{
		Stream: a.streamName,
		Values: values,
		MaxLen: a.streamMaxLen,
		Approx: true,
	}).Result(); err != nil {
		return nil, fmt.Errorf("could not enqueue request: %w", err)
	}
	select {
	case msg := <-pubsub.Channel():
		return []byte(msg.Payload), nil
	case <-time.After(timeout):
		return nil, errors.New("request timed out")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// --- متدهای ثبت پردازشگر در سطح App ---
func (a *App) OnTask(taskType string, handler HandlerFunc)     { a.taskHandlers[taskType] = handler }
func (a *App) OnEvent(channel string, handler HandlerFunc)     { a.eventHandlers[channel] = handler }
func (a *App) OnRequest(taskType string, handler RPCHandlerFunc) { a.rpcHandlers[taskType] = handler }

// --- متدهای ثبت پردازشگر در سطح Group ---
func (g *Group) OnTask(taskType string, handler HandlerFunc) {
	g.app.OnTask(fmt.Sprintf("%s.%s", g.prefix, taskType), handler)
}
func (g *Group) OnEvent(channel string, handler HandlerFunc) {
	g.app.OnEvent(fmt.Sprintf("%s.%s", g.prefix, channel), handler)
}
func (g *Group) OnRequest(taskType string, handler RPCHandlerFunc) {
	g.app.OnRequest(fmt.Sprintf("%s.%s", g.prefix, taskType), handler)
}

// --- متد اجرای ورکر ---
func (a *App) Run(ctx context.Context) {
	var wg sync.WaitGroup
	a.logger.Info("Worker App starting...")
	wg.Add(2)
	go a.runTaskProcessor(ctx, &wg)
	go a.runEventSubscriber(ctx, &wg)
	wg.Wait()
	a.logger.Info("Worker App has shut down.")
}