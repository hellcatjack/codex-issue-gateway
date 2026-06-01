package runner

import "time"

type Watchdog struct {
	NoActivityTimeout time.Duration
}

func (w Watchdog) Expired(now, lastActivity time.Time) bool {
	return now.Sub(lastActivity) > w.NoActivityTimeout
}
