// Package ratelimit implements the in-memory, express-rate-limit compatible
// limiter used on the Actual routes.
package ratelimit

import (
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
)

// DefaultMessage is express-rate-limit's default body. Express sends a string
// body as text/html, so this must not be JSON.
const DefaultMessage = "Too many requests, please try again later."

// Config describes one limiter, mirroring the express-rate-limit options the
// Node server uses.
type Config struct {
	// Limit is the number of requests allowed per window.
	Limit int
	// Window is the length of the counting window.
	Window time.Duration
	// Message is the response body on a 429. A string is sent as text/html, any
	// other value as JSON.
	Message any
	// SkipSuccessfulRequests gives the request back when it finishes with a
	// status below 400, matching `skipSuccessfulRequests: true`.
	SkipSuccessfulRequests bool
	// Disabled turns the limiter into a pass-through. Used in development.
	Disabled bool
}

// Limiter counts requests per client IP.
type Limiter struct {
	config Config

	mu       sync.Mutex
	hits     map[string]*entry
	lastGC   time.Time
	now      func() time.Time
	identity func(*http.Request) string
}

type entry struct {
	count   int
	resetAt time.Time
}

// New creates a limiter.
func New(config Config) *Limiter {
	if config.Message == nil {
		config.Message = DefaultMessage
	}
	return &Limiter{
		config: config,
		hits:   map[string]*entry{},
		now:    time.Now,
	}
}

// Identity overrides how a request is keyed. The default is the client IP
// resolved through httpx.ClientIP.
func (l *Limiter) Identity(fn func(*http.Request) string) {
	l.identity = fn
}

// Handler returns the middleware to bind on a route or route group.
func (l *Limiter) Handler() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "actualRateLimit",
		Func: func(e *core.RequestEvent) error {
			return l.handle(e)
		},
	}
}

func (l *Limiter) handle(e *core.RequestEvent) error {
	if l.config.Disabled || l.config.Limit <= 0 {
		return e.Next()
	}

	now := l.now()
	key := l.key(e.Request)
	count, resetAt := l.increment(key, now)

	header := e.Response.Header()
	header.Set("RateLimit-Policy", strconv.Itoa(l.config.Limit)+";w="+strconv.Itoa(int(l.config.Window/time.Second)))
	header.Set("RateLimit-Limit", strconv.Itoa(l.config.Limit))
	header.Set("RateLimit-Remaining", strconv.Itoa(max(0, l.config.Limit-count)))
	header.Set("RateLimit-Reset", strconv.Itoa(resetSeconds(now, resetAt)))

	if count > l.config.Limit {
		header.Set("Retry-After", strconv.Itoa(resetSeconds(now, resetAt)))
		return l.respond(e)
	}

	if !l.config.SkipSuccessfulRequests {
		return e.Next()
	}

	err := e.Next()
	if e.Status() < 400 {
		l.decrement(key, resetAt)
	}
	return err
}

func (l *Limiter) respond(e *core.RequestEvent) error {
	switch message := l.config.Message.(type) {
	case string:
		return httpx.Text(e, http.StatusTooManyRequests, message)
	default:
		return httpx.JSON(e, http.StatusTooManyRequests, message)
	}
}

func (l *Limiter) key(r *http.Request) string {
	identity := l.identity
	if identity == nil {
		identity = func(r *http.Request) string { return httpx.ClientIP(r, nil) }
	}
	ip := identity(r)
	return maskIPv6(ip)
}

func (l *Limiter) increment(key string, now time.Time) (int, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.collect(now)

	current, ok := l.hits[key]
	if !ok || now.After(current.resetAt) {
		current = &entry{resetAt: now.Add(l.config.Window)}
		l.hits[key] = current
	}
	current.count++

	return current.count, current.resetAt
}

func (l *Limiter) decrement(key string, resetAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok := l.hits[key]
	if !ok || !current.resetAt.Equal(resetAt) || current.count <= 0 {
		return
	}
	current.count--
}

// collect drops expired entries; it runs at most once per window.
func (l *Limiter) collect(now time.Time) {
	if !l.lastGC.IsZero() && now.Sub(l.lastGC) < l.config.Window {
		return
	}
	l.lastGC = now
	for key, current := range l.hits {
		if now.After(current.resetAt) {
			delete(l.hits, key)
		}
	}
}

func resetSeconds(now, resetAt time.Time) int {
	seconds := int((resetAt.Sub(now) + time.Second - 1) / time.Second)
	return max(0, seconds)
}

// maskIPv6 reproduces express-rate-limit's default key generator, which groups
// IPv6 clients by their /56 subnet.
func maskIPv6(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if addr.Is4() || addr.Is4In6() {
		return ip
	}
	prefix, err := addr.Prefix(56)
	if err != nil {
		return ip
	}
	return prefix.String()
}
