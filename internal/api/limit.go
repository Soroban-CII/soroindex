package api

import (
	"net"
	"sync"
	"time"
)

type ipWindow struct {
	start    time.Time
	used     int
	remaining int // remaining requests after this one
}
type ipLimiter struct {
	mu        sync.Mutex
	windows   map[string]ipWindow
	nextSweep time.Time
}

// newIPLimiter creates a new limiter with the given request limit per minute.
func newIPLimiter(limit int) *ipLimiter {
	return &ipLimiter{windows: make(map[string]ipWindow), nextSweep: time.Now().Add(time.Minute)}
}

// allowN charges cost requests to the peer's window. A cost above the limit
// is charged as the whole window, so a large request can still succeed once
// per minute instead of never. Returns the remaining count after the call
// and the time at which the current window resets (epoch seconds).
func (l *ipLimiter) allowN(remote string, limit, cost int, now time.Time) (bool, int, int64) {
	if cost < 1 {
		cost = 1
	}
	if cost > limit {
		cost = limit
	}
	if limit == 0 {
		return true, limit, 0
	}
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = map[string]ipWindow{}
	}
	if !now.Before(l.nextSweep) {
		for key, w := range l.windows {
			if now.Sub(w.start) >= time.Minute {
				delete(l.windows, key)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	resetEpoch := l.nextSweep.Unix()
	w, exists := l.windows[ip]
	if !exists && len(l.windows) >= 50000 {
		return false, 0, 0
	}
	if !exists || now.Sub(w.start) >= time.Minute {
		w = ipWindow{start: now, remaining: limit - cost}
	} else {
		w.remaining -= cost
		if w.remaining < 0 {
			return false, 0, 0
		}
	}
	w.used += cost
	l.windows[ip] = w
	return true, w.remaining, resetEpoch
}
