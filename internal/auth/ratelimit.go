package auth

import (
	"sync"
	"time"
)

const (
	maxFailures  = 5
	lockDuration = 15 * time.Minute
	globalLimit  = 60
	globalWindow = time.Minute
	maxIPEntries = 1000
)

type ipEntry struct {
	fails       int
	lockedUntil time.Time
	lastSeen    time.Time
}

// Limiter 是进程内的登录限流器：IP 连续失败锁定 + 全局限速。
type Limiter struct {
	mu          sync.Mutex
	ips         map[string]*ipEntry
	windowStart time.Time
	globalCount int
}

func NewLimiter() *Limiter {
	return &Limiter{ips: make(map[string]*ipEntry)}
}

// Allow 返回是否放行，以及建议的 Retry-After 秒数。
func (l *Limiter) Allow(ip string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= globalWindow {
		l.windowStart = now
		l.globalCount = 0
	}
	l.globalCount++
	if l.globalCount > globalLimit {
		retry := int(globalWindow.Seconds() - now.Sub(l.windowStart).Seconds())
		if retry < 1 {
			retry = 1
		}
		return false, retry
	}

	if e, ok := l.ips[ip]; ok && e.lockedUntil.After(now) {
		retry := int(time.Until(e.lockedUntil).Seconds())
		if retry < 1 {
			retry = 1
		}
		return false, retry
	}
	return true, 0
}

// Fail 记录一次失败；连续 5 次后锁定 15 分钟。
func (l *Limiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if len(l.ips) > maxIPEntries {
		l.ips = make(map[string]*ipEntry)
	}
	for k, e := range l.ips {
		if !e.lockedUntil.After(now) && now.Sub(e.lastSeen) > lockDuration {
			delete(l.ips, k)
		}
	}

	e, ok := l.ips[ip]
	if !ok {
		e = &ipEntry{}
		l.ips[ip] = e
	}
	e.lastSeen = now
	e.fails++
	if e.fails >= maxFailures {
		e.fails = 0
		e.lockedUntil = now.Add(lockDuration)
	}
}

// Success 清零该 IP 的失败计数。
func (l *Limiter) Success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.ips, ip)
}
