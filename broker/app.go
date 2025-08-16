package broker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type App struct {
	rdb    *redis.Client
	stream string
	group  string

	// رجیستری هندلرها
	mu            sync.RWMutex
	taskHandlers  map[string]HandlerFunc
	eventHandlers map[string]HandlerFunc
	rpcHandlers   map[string]RPCHandlerFunc
	eventSubs     map[string]*redis.PubSub

	// اجرای داخلی
	startOnce    sync.Once
	started      bool
	wg           sync.WaitGroup
	sem          chan struct{} // سقف همزمانی هندلرها
	maxJobs      int
	pollBlock    time.Duration
	streamMaxLen int64

	// لاگ
	logger *zap.Logger

	consumerID string

	// ---------- برای مود Fast (best-effort) ----------
	replyOnce    sync.Once
	replyCtx     context.Context
	replyCancel  context.CancelFunc
	replySub     *redis.PubSub
	replyMu      sync.Mutex
	replyWaiters map[string]chan []byte // corrID -> result chan
	replyEarly   map[string][]byte      // پاسخ‌های زودرس
}

func New(client *redis.Client, stream, group string, options ...Option) *App {
	a := &App{
		rdb:           client,
		stream:        stream,
		group:         group,
		taskHandlers:  make(map[string]HandlerFunc),
		eventHandlers: make(map[string]HandlerFunc),
		rpcHandlers:   make(map[string]RPCHandlerFunc),
		eventSubs:     make(map[string]*redis.PubSub),
		maxJobs:       10,
		pollBlock:     2 * time.Second,
		streamMaxLen:  0,
		logger:        zap.NewNop(),
		consumerID:    defaultConsumerID(),

		replyWaiters: make(map[string]chan []byte),
		replyEarly:   make(map[string][]byte),
	}
	a.sem = make(chan struct{}, a.maxJobs)
	for _, opt := range options {
		opt(a)
	}
	return a
}

// ثبت هندلرها
func (a *App) OnTask(name string, h HandlerFunc) {
	a.mu.Lock()
	a.taskHandlers[name] = h
	a.mu.Unlock()
}

func (a *App) OnEvent(channel string, h HandlerFunc) {
	a.mu.Lock()
	a.eventHandlers[channel] = h
	started := a.started
	a.mu.Unlock()
	if started {
		a.startEventSubscriber(channel, h)
	}
}

func (a *App) OnRequest(name string, h RPCHandlerFunc) {
	a.mu.Lock()
	a.rpcHandlers[name] = h
	a.mu.Unlock()
}

// Run بلوکه می‌ماند تا ctx لغو شود.
func (a *App) Run(ctx context.Context) {
	a.startOnce.Do(func() {
		a.mu.Lock()
		a.started = true
		a.mu.Unlock()

		// ایجاد Consumer Group اگر وجود ندارد
		if err := a.rdb.XGroupCreateMkStream(ctx, a.stream, a.group, "$").Err(); err != nil && !isGroupExists(err) {
			a.logf("XGroupCreateMkStream error: %v", err)
		}

		// استارت مصرف Stream برای Task/RPC
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.consumeStream(ctx)
		}()

		// استارت subscriber برای Eventهای از قبل ثبت‌شده
		a.mu.RLock()
		for ch, h := range a.eventHandlers {
			a.startEventSubscriber(ch, h)
		}
		a.mu.RUnlock()
	})

	// بلوکه تا لغو
	<-ctx.Done()

	// خاموشی تمیز
	a.closeEventSubs()
	a.closeReplySub()
	a.wg.Wait()
}

func (a *App) Close() error {
	a.closeEventSubs()
	a.closeReplySub()
	a.wg.Wait()
	return nil
}

// ---------- داخلی ----------

func isGroupExists(err error) bool {
	if err == nil {
		return false
	}
	return false
}

func (a *App) withConcurrency(fn func()) {
	a.sem <- struct{}{}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() { <-a.sem }()
		fn()
	}()
}

func (a *App) logf(format string, args ...any) {
	if a.logger != nil {
		a.logger.Sugar().Infof(format, args...)
	}
}

func defaultConsumerID() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "host"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// ---------- PSubscribe پاسخ‌های RPC برای مود Fast ----------
func (a *App) ensureReplySubscriber() {
	a.replyOnce.Do(func() {
		a.replyCtx, a.replyCancel = context.WithCancel(context.Background())
		a.replySub = a.rdb.PSubscribe(a.replyCtx, "reply:*")

		// کانال با بافر بزرگ برای جلوگیری از drop
		ch := a.replySub.Channel(redis.WithChannelSize(4096))

		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			defer func() { _ = a.replySub.Close() }()

			for msg := range ch {
				id := extractReplyID(msg.Channel) // "reply:<id>" → "<id>"
				if id == "" {
					continue
				}

				// تحویل قبل از حذف (با fallback غیرمسدودکننده)
				a.replyMu.Lock()
				waiter, ok := a.replyWaiters[id]
				a.replyMu.Unlock()

				if ok {
					payload := []byte(msg.Payload)
					delivered := false
					select {
					case waiter <- payload:
						delivered = true
					default:
						// اگر نادر بود و جا نشد، در پس‌زمینه ارسال کن
						go func(ch chan []byte, p []byte) { ch <- p }(waiter, payload)
						delivered = true
					}
					if delivered {
						a.replyMu.Lock()
						delete(a.replyWaiters, id)
						a.replyMu.Unlock()
					}
				} else {
					// پاسخ زودرس—هنوز منتظری ثبت نشده
					a.replyMu.Lock()
					a.replyEarly[id] = []byte(msg.Payload)
					a.replyMu.Unlock()
				}
			}
		}()
	})
}

func (a *App) closeReplySub() {
	if a.replyCancel != nil {
		a.replyCancel()
	}
}

func extractReplyID(channel string) string {
	const p = "reply:"
	if strings.HasPrefix(channel, p) && len(channel) > len(p) {
		return channel[len(p):]
	}
	return ""
}
