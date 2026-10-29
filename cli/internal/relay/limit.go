package relay

import (
	"sync"
	"time"
)

// limiter caps, per client address, how fast WebSockets open (a token bucket) and how many are
// open at once.
type limiter struct {
	rate    float64
	burst   float64
	maxOpen int

	mu    sync.Mutex
	addrs map[string]*addrState
}

type addrState struct {
	tokens float64
	last   time.Time
	open   int
}

func newLimiter(rate float64, burst, maxOpen int) *limiter {
	return &limiter{rate: rate, burst: float64(burst), maxOpen: maxOpen, addrs: map[string]*addrState{}}
}

// admit takes a token and an open slot for addr. When it says yes, release gives the slot back.
func (l *limiter) admit(addr string, now time.Time) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.addrs) > 10000 {
		l.sweep(now)
	}
	st := l.addrs[addr]
	if st == nil {
		st = &addrState{tokens: l.burst, last: now}
		l.addrs[addr] = st
	}
	st.tokens = min(l.burst, st.tokens+now.Sub(st.last).Seconds()*l.rate)
	st.last = now
	if st.tokens < 1 || st.open >= l.maxOpen {
		return nil, false
	}
	st.tokens--
	st.open++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			st.open--
			l.mu.Unlock()
		})
	}, true
}

// sweep forgets addresses with nothing open whose bucket has refilled.
func (l *limiter) sweep(now time.Time) {
	for addr, st := range l.addrs {
		if st.open == 0 && st.tokens+now.Sub(st.last).Seconds()*l.rate >= l.burst {
			delete(l.addrs, addr)
		}
	}
}
