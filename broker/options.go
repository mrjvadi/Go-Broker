package broker

import "go.uber.org/zap"

// Option یک نوع داده برای توابع پیکربندی اختیاری است.
type Option func(*App)

// WithLogger یک گزینه برای تنظیم لاگر سفارشی است.
func WithLogger(logger *zap.Logger) Option {
	return func(a *App) {
		a.logger = logger
	}
}

// WithMaxJobs یک گزینه برای تنظیم حداکثر کارهای همزمان است.
func WithMaxJobs(n int) Option {
	return func(a *App) {
		if n > 0 {
			a.maxConcurrentJobs = n
		}
	}
}

// WithStreamLength یک گزینه برای تنظیم حداکثر طول استریم است.
func WithStreamLength(n int64) Option {
	return func(a *App) {
		a.streamMaxLen = n
	}
}