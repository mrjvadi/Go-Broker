package broker

import "go.uber.org/zap"

// Option توابع پیکربندی
type Option func(*App)

func WithLogger(logger *zap.Logger) Option {
	return func(a *App) { a.logger = logger }
}

func WithMaxJobs(n int) Option {
	return func(a *App) {
		if n > 0 {
			a.maxConcurrentJobs = n
		}
	}
}

func WithStreamLength(max int64) Option {
	return func(a *App) {
		if max > 0 {
			a.streamMaxLen = max
		}
	}
}
