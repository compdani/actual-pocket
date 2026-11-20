// Package routes registers the Actual HTTP API and the web frontend.
package routes

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ratelimit"
)

// Options configures Register.
type Options struct {
	// App is the PocketBase app handlers are bound to.
	App core.App
	// Config is the resolved server configuration.
	Config *config.Config
	// Version is the package version, injected at build time.
	Version string
	// Stdout receives the request log and startup lines. Defaults to os.Stdout.
	Stdout io.Writer
}

// Register binds every Actual route on the PocketBase router.
//
// The Actual routes live in their own group so the global limiter and request
// logger apply to them and never to PocketBase's /api and /_ routes.
func Register(se *core.ServeEvent, opts Options) {
	cfg := opts.Config
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	actual := se.Router.Group("")
	actual.BindFunc(requestLogger(stdout))

	limiter := ratelimit.New(ratelimit.Config{
		Limit:    500,
		Window:   time.Minute,
		Disabled: cfg.IsDev(),
	})
	limiter.Identity(func(r *http.Request) string {
		return httpx.ClientIP(r, cfg.TrustedProxies)
	})
	actual.Bind(limiter.Handler())

	registerMeta(actual, cfg, opts.Version)
	registerStatic(actual, cfg)
}

// requestLogger writes one line per request: METHOD STATUS PATH. Bodies,
// headers and query strings are never logged.
func requestLogger(out io.Writer) func(e *core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		err := e.Next()

		status := e.Status()
		if status == 0 {
			if err != nil {
				status = router.ToApiError(err).Status
			} else {
				status = http.StatusOK
			}
		}

		// Best effort: a blocked writer must not break the request.
		_, _ = io.WriteString(out, e.Request.Method+" "+strconv.Itoa(status)+" "+e.Request.URL.Path+"\n")

		return err
	}
}
