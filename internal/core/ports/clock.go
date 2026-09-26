package ports

import "time"

// Clock abstracts time so that behavior is deterministic in tests and in
// demo mode.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker is the subset of time.Ticker used by Huginn.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
