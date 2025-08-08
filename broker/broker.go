package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// App ساختار اصلی فریم‌ورک
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

	// --- RPC pooled reply subscriber ---
	rpOnce         sync.Once
	rpSub          *redis.PubSub
	rpReplyChannel string
	rpPending      sync.Map // map[cid]chan rpcResponseEnvelope
	rpTimerPool    sync.Pool
}

// Group برای ثبت پردازشگرها با پیشوند مشترک
type Group struct {
	app    *App
	prefix string
}

// envelope پاسخ RPC (بدنه خام است)
type rpcResponseEnvelope struct {
	CorrelationID string `json:"correlation_id"`
	Body          []byte `json:"body,omitempty"`
	Error         string `json:"error,omitempty"`
}

// New سازنده
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
		streamMaxLen:      0,
		logger:            zap.NewNop(),
	}
	for _, option := range options {
		option(app)
	}
	// pool تایمر برای مسیر RPC
	app.rpTimerPool = sync.Pool{
		New: func() any { return time.NewTimer(time.Hour) },
	}
	return app
}

// Group ساخت گروه با پیشوند
func (a *App) Group(prefix string) *Group {
	return &Group{app: a, prefix: prefix}
}

// --- ارسال پیام‌ها ---

func (a *App) Enqueue(ctx context.Context, taskType string, payload interface{}) (string, error) {
	// payload را تا حد امکان خام بفرستیم، ولی برای سازگاری، انواع غیر []byte/string را JSON کنیم
	var payloadBytes []byte
	switch v := payload.(type) {
	case nil:
		payloadBytes = nil
	case []byte:
		payloadBytes = v
	case string:
		payloadBytes = []byte(v)
	default:
		buf, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal payload: %w", err)
		}
		payloadBytes = buf
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
	var payloadBytes []byte
	switch v := payload.(type) {
	case nil:
		payloadBytes = nil
	case []byte:
		payloadBytes = v
	case string:
		payloadBytes = []byte(v)
	default:
		buf, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		payloadBytes = buf
	}
	return a.client.Publish(ctx, channel, payloadBytes).Err()
}

// ensureRPCSubscriber: یک‌بار Subscribe روی reply channel اختصاصی این App
func (a *App) ensureRPCSubscriber() {
	a.rpOnce.Do(func() {
		a.rpReplyChannel = "reply:" + uuid.NewString()
		// کانتکست بلندمدت برای عمر طولانی سابسکرایب
		a.rpSub = a.client.Subscribe(context.Background(), a.rpReplyChannel)

		ch := a.rpSub.Channel()
		go func() {
			for msg := range ch {
				var env rpcResponseEnvelope
				if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
					continue
				}
				if v, ok := a.rpPending.LoadAndDelete(env.CorrelationID); ok {
					wait := v.(chan rpcResponseEnvelope)
					select {
					case wait <- env:
					default:
					}
					close(wait)
				}
			}
		}()
	})
}

// getTimer/putTimer: مدیریت تایمر با pool برای کاهش allocs
func (a *App) getTimer(d time.Duration) *time.Timer {
	tm := a.rpTimerPool.Get().(*time.Timer)
	if !tm.Stop() {
		select {
		case <-tm.C:
		default:
		}
	}
	tm.Reset(d)
	return tm
}
func (a *App) putTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	a.rpTimerPool.Put(t)
}

// Request: RPC خام (payload/response به صورت []byte)، فقط envelope JSON می‌شود
func (a *App) Request(ctx context.Context, taskType string, payload interface{}, timeout time.Duration) ([]byte, error) {
	if taskType == "" {
		return nil, errors.New("empty task type")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	a.ensureRPCSubscriber()

	// آماده‌سازی payload خام
	var payloadBytes []byte
	switch v := payload.(type) {
	case nil:
		payloadBytes = nil
	case []byte:
		payloadBytes = v
	case string:
		payloadBytes = []byte(v)
	default:
		// سازگاری: اگر کاربر struct/map می‌دهد، اینجا JSON کن
		buf, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		payloadBytes = buf
	}

	correlationID := uuid.NewString()

	// کانال انتظار پاسخ مخصوص همین correlation
	wait := make(chan rpcResponseEnvelope, 1)
	a.rpPending.Store(correlationID, wait)

	values := map[string]interface{}{
		"type":           taskType,
		"payload":        string(payloadBytes), // خام
		"reply_to":       a.rpReplyChannel,     // کانال دائمی
		"correlation_id": correlationID,
	}

	if _, err := a.client.XAdd(ctx, &redis.XAddArgs{
		Stream: a.streamName,
		Values: values,
		MaxLen: a.streamMaxLen,
		Approx: true,
	}).Result(); err != nil {
		a.rpPending.Delete(correlationID)
		return nil, fmt.Errorf("enqueue request: %w", err)
	}

	tm := a.getTimer(timeout)
	defer a.putTimer(tm)

	select {
	case env := <-wait:
		if env.Error != "" {
			return nil, errors.New(env.Error)
		}
		return env.Body, nil
	case <-tm.C:
		a.rpPending.Delete(correlationID)
		return nil, errors.New("request timed out")
	case <-ctx.Done():
		a.rpPending.Delete(correlationID)
		return nil, ctx.Err()
	}
}

// ثبت پردازشگرها (App-level)
func (a *App) OnTask(taskType string, handler HandlerFunc)       { a.taskHandlers[taskType] = handler }
func (a *App) OnEvent(channel string, handler HandlerFunc)       { a.eventHandlers[channel] = handler }
func (a *App) OnRequest(taskType string, handler RPCHandlerFunc) { a.rpcHandlers[taskType] = handler }

// ثبت پردازشگرها (Group-level)
func (g *Group) OnTask(taskType string, handler HandlerFunc) {
	g.app.OnTask(fmt.Sprintf("%s.%s", g.prefix, taskType), handler)
}
func (g *Group) OnEvent(channel string, handler HandlerFunc) {
	g.app.OnEvent(fmt.Sprintf("%s.%s", g.prefix, channel), handler)
}
func (g *Group) OnRequest(taskType string, handler RPCHandlerFunc) {
	g.app.OnRequest(fmt.Sprintf("%s.%s", g.prefix, taskType), handler)
}

// اجرای ورکر
func (a *App) Run(ctx context.Context) {
	var wg sync.WaitGroup
	a.logger.Info("Worker App starting...")
	wg.Add(2)
	go a.runTaskProcessor(ctx, &wg)
	go a.runEventSubscriber(ctx, &wg)
	wg.Wait()

	// بستن PubSub دائمی RPC
	_ = a.Close()

	a.logger.Info("Worker App has shut down.")
}

// Close: بستن PubSub دائمی RPC (برای جلوگیری از لاگ‌های use of closed network connection)
func (a *App) Close() error {
	if a.rpSub != nil {
		_ = a.rpSub.Close()
		a.rpSub = nil
	}
	return nil
}
