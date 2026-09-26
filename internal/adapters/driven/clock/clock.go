// Package clock provides the system implementation of ports.Clock.
package clock

import (
	"time"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// System is the real wall clock.
type System struct{}

// New returns the system clock.
func New() System { return System{} }

// Now returns the current time.
func (System) Now() time.Time { return time.Now() }

// NewTicker wraps time.NewTicker.
func (System) NewTicker(d time.Duration) ports.Ticker { return ticker{time.NewTicker(d)} }

type ticker struct{ t *time.Ticker }

func (t ticker) C() <-chan time.Time { return t.t.C }
func (t ticker) Stop()               { t.t.Stop() }
