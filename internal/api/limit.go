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
	if w.used >= limit {
		return false
	}
	w.used++
	l.windows[ip] = w
	return true
}
