package api

import (
	"net"
	"sync"
	"time"
)

type ipWindow struct {
	start time.Time
	used  int
}
type ipLimiter struct {
	mu        sync.Mutex
	windows   map[string]ipWindow
	nextSweep time.Time
}

func (l *ipLimiter) allow(remote string, limit int, now time.Time) bool {
	return l.allowN(remote, limit, 1, now)
}

// allowN charges cost requests to the peer's window. A cost above the limit
// is charged as the whole window, so a large request can still succeed once
// per minute instead of never.
func (l *ipLimiter) allowN(remote string, limit, cost int, now time.Time) bool {
	if cost < 1 {
		cost = 1
	}
	if cost > limit {
		cost = limit
	}
	if limit == 0 {
		return true
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
	w, exists := l.windows[ip]
	if !exists && len(l.windows) >= 50000 {
		return false
	}
	if !exists || now.Sub(w.start) >= time.Minute {
		w = ipWindow{start: now}
	}
	if w.used+cost > limit {
		return false
	}
	w.used += cost
	l.windows[ip] = w
	return true
}
