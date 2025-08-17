package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

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

	replyOnce    sync.Once
	replySub     *redis.PubSub
	replyMu      sync.Mutex
	replyWaiters map[string]chan *redis.Message

	shutdownOnce sync.Once
	shutdownCh   chan struct{}
}

type Group struct {
	app    *App
	prefix string
}

func New(client *redis.Client, stream, group string, options ...Option) *App {
	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	return &App{
		client:            client,
		streamName:        stream,
		groupName:         group,
		consumerName:      consumerName,
		taskHandlers:      make(map[string]HandlerFunc),
		eventHandlers:     make(map[string]HandlerFunc),
		rpcHandlers:       make(map[string]RPCHandlerFunc),
		maxConcurrentJobs: 50,
		logger:            zap.NewNop(),
		replyWaiters:      make(map[string]chan *redis.Message),
		shutdownCh:        make(chan struct{}),
	}
}

func (a *App) Group(prefix string) *Group {
	return &Group{app: a, prefix: prefix}
}

func (a *App) Enqueue(ctx context.Context, taskType string, payload interface{}) (string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("could not marshal payload: %w", err)
	}
	keys := []string{a.streamName}
	args := []interface{}{"task", taskType, string(payloadBytes), "", ""}
	return enqueueLua.Run(ctx, a.client, keys, args...).String(), err
}

func (a *App) Publish(ctx context.Context, channel string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("could not marshal payload: %w", err)
	}
	keys := []string{channel}
	return publishLua.Run(ctx, a.client, keys, payloadBytes).Err()
}

func (a *App) Request(ctx context.Context, taskType string, payload interface{}, timeout time.Duration) ([]byte, error) {
	a.ensureReplySubscriber(ctx)
	correlationID := uuid.NewString()
	replyToChannel := fmt.Sprintf("reply:%s", correlationID)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("could not marshal payload: %w", err)
	}

	ch := make(chan *redis.Message, 1)
	a.replyMu.Lock()
	a.replyWaiters[correlationID] = ch
	a.replyMu.Unlock()
	defer func() {
		a.replyMu.Lock()
		delete(a.replyWaiters, correlationID)
		a.replyMu.Unlock()
	}()

	keys := []string{a.streamName}
	args := []interface{}{"rpc", taskType, string(payloadBytes), replyToChannel, correlationID}
	if _, err := enqueueLua.Run(ctx, a.client, keys, args...).Result(); err != nil {
		return nil, fmt.Errorf("could not enqueue request: %w", err)
	}

	select {
	case msg := <-ch:
		return []byte(msg.Payload), nil
	case <-time.After(timeout):
		return nil, errors.New("request timed out")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *App) OnTask(taskType string, handler HandlerFunc)       { a.taskHandlers[taskType] = handler }
func (a *App) OnEvent(channel string, handler HandlerFunc)       { a.eventHandlers[channel] = handler }
func (a *App) OnRequest(taskType string, handler RPCHandlerFunc) { a.rpcHandlers[taskType] = handler }
func (g *Group) OnTask(taskType string, handler HandlerFunc) {
	g.app.OnTask(fmt.Sprintf("%s.%s", g.prefix, taskType), handler)
}
func (g *Group) OnEvent(channel string, handler HandlerFunc) {
	g.app.OnEvent(fmt.Sprintf("%s.%s", g.prefix, channel), handler)
}
func (g *Group) OnRequest(taskType string, handler RPCHandlerFunc) {
	g.app.OnRequest(fmt.Sprintf("%s.%s", g.prefix, taskType), handler)
}

func (a *App) Run() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup
	a.logger.Info("Worker App starting...")
	a.ensureReplySubscriber(ctx)

	wg.Add(2)
	go a.runTaskProcessor(ctx, &wg)
	go a.runEventSubscriber(ctx, &wg)

	<-ctx.Done()
	a.Close() // Trigger graceful shutdown
	wg.Wait()
	a.logger.Info("Worker App has shut down gracefully.")
}

func (a *App) Close() {
	a.shutdownOnce.Do(func() {
		close(a.shutdownCh)
		if a.replySub != nil {
			a.replySub.Close()
		}
	})
}

func (a *App) ensureReplySubscriber(ctx context.Context) {
	a.replyOnce.Do(func() {
		a.replySub = a.client.PSubscribe(ctx, "reply:*")
		go a.dispatchReplies(ctx)
	})
}

func (a *App) dispatchReplies(ctx context.Context) {
	ch := a.replySub.Channel()
	for {
		select {
		case <-a.shutdownCh:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			correlationID := strings.TrimPrefix(msg.Channel, "reply:")
			a.replyMu.Lock()
			waiter, found := a.replyWaiters[correlationID]
			a.replyMu.Unlock()
			if found {
				waiter <- msg
			}
		}
	}
}
