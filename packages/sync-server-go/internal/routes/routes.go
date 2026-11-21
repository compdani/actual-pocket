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

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/banks"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ratelimit"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/syncdb"
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
	// Store reads the Actual collections.
	Store *store.Store
	// Sync is the per-group CRDT message log.
	Sync *syncdb.Engine
	// Auth resolves the acting user for /sync and /account.
	Auth *auth.Resolver
	// SyncStats records a summary after each successful sync.
	SyncStats *syncStatsRecorder
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

	if opts.Auth != nil {
		opts.Auth.BindRevocationMiddleware(se.Router)
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

	if opts.Store != nil {
		opts.SyncStats = newSyncStatsRecorder(opts.Store)
	}

	registerMeta(actual, cfg, opts.Version)
	bankOptions := banks.Options{
		Store:  opts.Store,
		Auth:   opts.Auth,
		Config: opts.Config,
	}
	if opts.App != nil {
		bankOptions.Logger = opts.App.Logger()
	}
	banks.Register(actual, bankOptions)
	registerSync(actual, opts)
	registerAccount(actual, opts)
	registerOAuth2(actual, opts)
	registerCORSProxy(actual, opts)
	registerAdmin(actual, opts)
	registerSecrets(actual, opts)
	registerAPIKeys(actual, opts)
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
