package broker

import (
	"time"

	"go.uber.org/zap"
)

type Option func(*App)

func WithLogger(l *zap.Logger) Option {
	return func(a *App) {
		if l != nil {
			a.logger = l
		}
	}
}

func WithMaxJobs(n int) Option {
	return func(a *App) {
		if n > 0 {
			a.maxJobs = n
			a.sem = make(chan struct{}, a.maxJobs)
		}
	}
}

func WithStreamLength(n int64) Option {
	return func(a *App) {
		if n >= 0 {
			a.streamMaxLen = n
		}
	}
}

// (اختیاری) تغییر مدت بلاک XREADGROUP
func withPollBlock(d time.Duration) Option {
	return func(a *App) {
		if d > 0 {
			a.pollBlock = d
		}
	}
}
