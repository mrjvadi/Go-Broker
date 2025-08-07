package gmf

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
)

// HandlerFunc یک نوع داده برای تمام توابع پردازشگر یک‌طرفه (Tasks/Events) است.
// این توابع یک محتوای বাইت (payload) دریافت کرده و در صورت بروز مشکل، خطا برمی‌گردانند.
type HandlerFunc func(ctx context.Context, payload []byte) error

// RPCHandlerFunc یک نوع داده برای تمام توابع پردازشگر درخواست/پاسخ (RPC) است.
// این توابع علاوه بر خطا، یک پاسخ (response) نیز به صورت বাইت برمی‌گردانند.
type RPCHandlerFunc func(ctx context.Context, payload []byte) (response []byte, err error)

// App ساختار اصلی و مرکزی فریمورک است.
// این ساختار تمام تنظیمات و پردازشگرها را در خود نگهداری می‌کند.
type App struct {
	client            *redis.Client             // کلاینت متصل به Redis
	streamName        string                    // نام صف اصلی برای کارهای ماندگار (Streams)
	groupName         string                    // نام گروه مصرف‌کنندگان برای پردازش مشارکتی
	consumerName      string                    // نام منحصر به فرد این نمونه از ورکر
	taskHandlers      map[string]HandlerFunc    // نگاشت نوع کار به پردازشگر آن
	eventHandlers     map[string]HandlerFunc    // نگاشت نام کانال به پردازشگر رویداد آن
	rpcHandlers       map[string]RPCHandlerFunc // نگاشت نوع درخواست RPC به پردازشگر آن
	maxConcurrentJobs int                       // حداکثر تعداد کارهای همزمانی که می‌توانند اجرا شوند
}

// New یک نمونه جدید از اپلیکیشن را با تنظیمات اولیه می‌سازد.
// client: کلاینت متصل به Redis.
// stream: نام صف اصلی برای کارها.
// group: نام گروه مصرف‌کنندگان. این نام برای مقیاس‌پذیری حیاتی است.
// maxJobs: حداکثر تعداد کارهای همزمان. اگر صفر یا منفی باشد، مقدار پیش‌فرض ۱۰ در نظر گرفته می‌شود.
func New(client *redis.Client, stream, group string, maxJobs int) *App {
	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("%s-%d", hostname, os.Getpid()) // ساخت یک نام یکتا برای این ورکر
	if maxJobs <= 0 {
		maxJobs = 10
	}
	return &App{
		client:            client,
		streamName:        stream,
		groupName:         group,
		consumerName:      consumerName,
		taskHandlers:      make(map[string]HandlerFunc),
		eventHandlers:     make(map[string]HandlerFunc),
		rpcHandlers:       make(map[string]RPCHandlerFunc),
		maxConcurrentJobs: maxJobs,
	}
}

// --- متدهای ارسال پیام ---

// Enqueue یک کار حیاتی (Task) را به صف ماندگار (Stream) اضافه می‌کند.
// این کارها تا زمانی که پردازش و تایید (ACK) نشوند، در صف باقی می‌مانند.
func (a *App) Enqueue(ctx context.Context, taskType string, payload interface{}) (string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("could not marshal payload: %w", err)
	}
	values := map[string]interface{}{"type": taskType, "payload": string(payloadBytes)}
	return a.client.XAdd(ctx, &redis.XAddArgs{Stream: a.streamName, Values: values}).Result()
}

// Publish یک رویداد آنی (Event) را در یک کانال Pub/Sub منتشر می‌کند.
// اگر هیچ شنونده‌ای در لحظه ارسال وجود نداشته باشد، این پیام از بین می‌رود.
func (a *App) Publish(ctx context.Context, channel string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("could not marshal payload: %w", err)
	}
	return a.client.Publish(ctx, channel, payloadBytes).Err()
}

// Request یک درخواست RPC ارسال کرده و با یک Timeout مشخص منتظر پاسخ می‌ماند.
// این متد از ترکیب Streams (برای ارسال درخواست) و Pub/Sub (برای دریافت پاسخ) استفاده می‌کند.
func (a *App) Request(ctx context.Context, taskType string, payload interface{}, timeout time.Duration) ([]byte, error) {
	// ۱. یک شناسه یکتا برای این تراکنش خاص ایجاد می‌کنیم.
	correlationID := uuid.NewString()
	// ۲. یک نام کانال Pub/Sub موقت و منحصر به فرد برای دریافت پاسخ می‌سازیم.
	replyToChannel := fmt.Sprintf("reply:%s", correlationID)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("could not marshal payload: %w", err)
	}

	// ۳. قبل از ارسال درخواست، به کانال پاسخ گوش می‌دهیم.
	pubsub := a.client.Subscribe(ctx, replyToChannel)
	defer pubsub.Close()

	// ۴. درخواست را به صف ماندگار Stream ارسال می‌کنیم.
	// اطلاعات مربوط به نحوه پاسخ‌دهی (reply_to, correlation_id) همراه پیام ارسال می‌شود.
	values := map[string]interface{}{
		"type":           taskType,
		"payload":        string(payloadBytes),
		"reply_to":       replyToChannel,
		"correlation_id": correlationID,
	}
	if _, err := a.client.XAdd(ctx, &redis.XAddArgs{Stream: a.streamName, Values: values}).Result(); err != nil {
		return nil, fmt.Errorf("could not enqueue request: %w", err)
	}

	// ۵. با استفاده از select، منتظر یکی از سه حالت زیر می‌مانیم.
	select {
	case msg := <-pubsub.Channel(): // حالت اول: پاسخ با موفقیت دریافت شد.
		return []byte(msg.Payload), nil
	case <-time.After(timeout): // حالت دوم: زمان انتظار به پایان رسید.
		return nil, errors.New("request timed out")
	case <-ctx.Done(): // حالت سوم: context اصلی برنامه (مثلا به دلیل خاموش شدن) لغو شد.
		return nil, ctx.Err()
	}
}

// --- متدهای ثبت پردازشگر ---

// OnTask یک پردازشگر برای کارهای حیاتی (Stream) ثبت می‌کند.
func (a *App) OnTask(taskType string, handler HandlerFunc) { a.taskHandlers[taskType] = handler }

// OnEvent یک پردازشگر برای رویدادهای آنی (Pub/Sub) ثبت می‌کند.
func (a *App) OnEvent(channel string, handler HandlerFunc) { a.eventHandlers[channel] = handler }

// OnRequest یک پردازشگر برای کارهای RPC ثبت می‌کند.
func (a *App) OnRequest(taskType string, handler RPCHandlerFunc) { a.rpcHandlers[taskType] = handler }

// --- متدهای اجرای ورکر ---

// Run متد اصلی است که تمام شنونده‌ها را در Goroutine های مجزا راه‌اندازی و مدیریت می‌کند.
func (a *App) Run(ctx context.Context) {
	var wg sync.WaitGroup
	log.Println("Worker App starting all processors...")

	wg.Add(2) // ما دو پردازشگر اصلی داریم: یکی برای Streams و یکی برای Pub/Sub.
	go a.runTaskProcessor(ctx, &wg)
	go a.runEventSubscriber(ctx, &wg)

	wg.Wait() // منتظر می‌ماند تا هر دو پردازشگر با دریافت سیگنال خروج، به اتمام برسند.
	log.Println("Worker App has shut down.")
}

// runTaskProcessor حلقه پردازش برای کارها (Streams) است.
func (a *App) runTaskProcessor(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	// این دستور گروه مصرف‌کننده را می‌سازد. اگر از قبل وجود داشته باشد، خطا نمی‌دهد.
	// MkStream تضمین می‌کند که اگر Stream وجود نداشت، ساخته شود.
	a.client.XGroupCreateMkStream(ctx, a.streamName, a.groupName, "0").Err()

	// این کانال به عنوان یک سمافور، تعداد کارهای همزمان را محدود می‌کند.
	limiter := make(chan struct{}, a.maxConcurrentJobs)

	for {
		select {
		case <-ctx.Done(): // اگر context لغو شد، از حلقه خارج شو.
			return
		default:
			// این دستور به صورت مسدودکننده (Blocking) منتظر پیام‌های جدید می‌ماند.
			// '>' یعنی فقط پیام‌هایی را بخوان که به هیچ مصرف‌کننده دیگری در این گروه تحویل داده نشده.
			result, err := a.client.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group: a.groupName, Consumer: a.consumerName,
				Streams: []string{a.streamName, ">"}, Count: 1, Block: 1 * time.Second,
			}).Result()

			if err != nil {
				continue
			} // در صورت خطا یا نبود پیام، حلقه ادامه می‌یابد.

			for _, message := range result[0].Messages {
				limiter <- struct{}{} // یک جایگاه از استخر همزمانی را اشغال کن.
				go func(msg redis.XMessage) {
					defer func() { <-limiter }() // پس از اتمام، جایگاه را آزاد کن.

					taskType := msg.Values["type"].(string)
					payload := []byte(msg.Values["payload"].(string))

					// بررسی می‌کند که آیا پیام حاوی فیلد reply_to است یا نه.
					if replyTo, ok := msg.Values["reply_to"].(string); ok {
						// اگر بله، این یک درخواست RPC است.
						if handler, found := a.rpcHandlers[taskType]; found {
							response, err := handler(ctx, payload)
							if err == nil {
								// پاسخ را به کانال موقت Pub/Sub ارسال کن.
								a.client.Publish(ctx, replyTo, response)
							}
						}
					} else {
						// در غیر این صورت، یک کار عادی و یک‌طرفه است.
						if handler, found := a.taskHandlers[taskType]; found {
							if err := handler(ctx, payload); err != nil {
								log.Printf("Error processing task %s: %v", msg.ID, err)
							}
						}
					}
					// در هر صورت، پیام اصلی را از Stream تایید (ACK) می‌کنیم تا دوباره پردازش نشود.
					a.client.XAck(ctx, a.streamName, a.groupName, msg.ID)
				}(message)
			}
		}
	}
}

// runEventSubscriber حلقه پردازش برای رویدادها (Pub/Sub) است.
func (a *App) runEventSubscriber(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	if len(a.eventHandlers) == 0 {
		return
	} // اگر هیچ پردازشگر رویدادی ثبت نشده، خارج شو.

	// لیستی از تمام کانال‌هایی که باید به آن‌ها گوش دهیم.
	channels := make([]string, 0, len(a.eventHandlers))
	for ch := range a.eventHandlers {
		channels = append(channels, ch)
	}

	pubsub := a.client.Subscribe(ctx, channels...)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if handler, found := a.eventHandlers[msg.Channel]; found {
				// هر رویداد در یک Goroutine مجزا پردازش می‌شود.
				go handler(ctx, []byte(msg.Payload))
			}
		}
	}
}
