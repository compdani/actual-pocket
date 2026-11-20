package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

// newMux builds a router with a single route behind the limiter. `respond`
// decides the status the route handler writes.
func newMux(t *testing.T, config Config, identity func() string, respond func(e *core.RequestEvent) error) http.Handler {
	t.Helper()

	limiter := New(config)
	if identity != nil {
		limiter.Identity(func(*http.Request) string { return identity() })
	}

	r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
		return &core.RequestEvent{Event: router.Event{Response: w, Request: req}}, nil
	})
	r.GET("/", respond).Bind(limiter.Handler())

	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	return mux
}

func ok(e *core.RequestEvent) error {
	return e.String(200, "ok")
}

func get(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	return recorder
}

func TestLimitAndHeaders(t *testing.T) {
	handler := newMux(t, Config{Limit: 2, Window: time.Minute}, nil, ok)

	first := get(t, handler)
	if first.Code != 200 {
		t.Fatalf("first status = %d, want 200", first.Code)
	}
	want := map[string]string{
		"RateLimit-Policy":    "2;w=60",
		"RateLimit-Limit":     "2",
		"RateLimit-Remaining": "1",
		"RateLimit-Reset":     "60",
	}
	for key, value := range want {
		if got := first.Header().Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}

	if second := get(t, handler); second.Code != 200 {
		t.Fatalf("second status = %d, want 200", second.Code)
	}

	third := get(t, handler)
	if third.Code != http.StatusTooManyRequests {
		t.Fatalf("third status = %d, want 429", third.Code)
	}
	if got, want := third.Body.String(), DefaultMessage; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := third.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	retryAfter, err := strconv.Atoi(third.Header().Get("Retry-After"))
	if err != nil || retryAfter < 0 || retryAfter > 60 {
		t.Errorf("Retry-After = %q, want 0..60", third.Header().Get("Retry-After"))
	}
	if got := third.Header().Get("RateLimit-Remaining"); got != "0" {
		t.Errorf("RateLimit-Remaining = %q, want 0", got)
	}
}

func TestKeysAreIndependentPerClient(t *testing.T) {
	ip := "203.0.113.1"
	handler := newMux(t, Config{Limit: 1, Window: time.Minute}, func() string { return ip }, ok)

	if got := get(t, handler); got.Code != 200 {
		t.Fatalf("status = %d, want 200", got.Code)
	}
	if got := get(t, handler); got.Code != 429 {
		t.Fatalf("status = %d, want 429", got.Code)
	}

	ip = "203.0.113.2"
	if got := get(t, handler); got.Code != 200 {
		t.Fatalf("other client status = %d, want 200", got.Code)
	}
}

func TestDisabled(t *testing.T) {
	handler := newMux(t, Config{Limit: 1, Window: time.Minute, Disabled: true}, nil, ok)

	for i := 0; i < 5; i++ {
		response := get(t, handler)
		if response.Code != 200 {
			t.Fatalf("request %d status = %d, want 200", i, response.Code)
		}
		if got := response.Header().Get("RateLimit-Limit"); got != "" {
			t.Fatalf("request %d: RateLimit-Limit = %q, want no headers when disabled", i, got)
		}
	}
}

func TestSkipSuccessfulRequests(t *testing.T) {
	status := 200
	handler := newMux(t, Config{
		Limit:                  1,
		Window:                 time.Minute,
		SkipSuccessfulRequests: true,
	}, func() string { return "203.0.113.1" }, func(e *core.RequestEvent) error {
		return e.String(status, "response")
	})

	// A successful request is given back.
	if got := get(t, handler); got.Code != 200 {
		t.Fatalf("status = %d, want 200", got.Code)
	}
	if got := get(t, handler); got.Code != 200 {
		t.Fatalf("status = %d, want 200", got.Code)
	}

	// A failed request keeps its slot.
	status = 400
	if got := get(t, handler); got.Code != 400 {
		t.Fatalf("status = %d, want 400", got.Code)
	}
	if got := get(t, handler); got.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", got.Code)
	}
}

func TestJSONMessage(t *testing.T) {
	handler := newMux(t, Config{
		Limit:   0,
		Window:  time.Minute,
		Message: map[string]string{"status": "error", "reason": "too-many-requests"},
	}, nil, ok)

	// Limit 0 disables the limiter, matching the guard in handle.
	if got := get(t, handler); got.Code != 200 {
		t.Errorf("status = %d, want 200", got.Code)
	}

	message := struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}{Status: "error", Reason: "too-many-requests"}
	handler = newMux(t, Config{Limit: 1, Window: time.Minute, Message: message}, nil, ok)
	if got := get(t, handler); got.Code != 200 {
		t.Fatalf("status = %d, want 200", got.Code)
	}
	response := get(t, handler)
	if response.Code != 429 {
		t.Fatalf("status = %d, want 429", response.Code)
	}
	if got, want := response.Body.String(), `{"status":"error","reason":"too-many-requests"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
}

func TestIPv6IsGroupedBySubnet(t *testing.T) {
	ip := "2001:db8:abcd:1200::1"
	handler := newMux(t, Config{Limit: 1, Window: time.Minute}, func() string { return ip }, ok)

	if got := get(t, handler); got.Code != 200 {
		t.Fatalf("status = %d, want 200", got.Code)
	}

	// Same /56 subnet, different interface identifier: same bucket.
	ip = "2001:db8:abcd:12ff::2"
	if got := get(t, handler); got.Code != 429 {
		t.Fatalf("status = %d, want 429 for the same /56", got.Code)
	}

	// A different /56 is a different bucket.
	ip = "2001:db8:abcd:1300::1"
	if got := get(t, handler); got.Code != 200 {
		t.Fatalf("status = %d, want 200 for a different /56", got.Code)
	}
}

func TestWindowExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := New(Config{Limit: 1, Window: time.Minute})
	limiter.now = func() time.Time { return now }
	limiter.Identity(func(*http.Request) string { return "203.0.113.1" })

	r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
		return &core.RequestEvent{Event: router.Event{Response: w, Request: req}}, nil
	})
	r.GET("/", ok).Bind(limiter.Handler())
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}

	if got := get(t, mux); got.Code != 200 {
		t.Fatalf("status = %d, want 200", got.Code)
	}
	if got := get(t, mux); got.Code != 429 {
		t.Fatalf("status = %d, want 429", got.Code)
	}

	now = now.Add(61 * time.Second)
	if got := get(t, mux); got.Code != 200 {
		t.Fatalf("status after the window = %d, want 200", got.Code)
	}
}
