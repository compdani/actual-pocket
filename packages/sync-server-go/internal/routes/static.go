package routes

import (
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
)

// devServerURL is where the Vite dev server listens (packages/desktop-client).
// It is a variable so tests can point it at an httptest server.
var devServerURL = "http://localhost:3001"

func init() {
	// Keep the manifest MIME type the Node server serves (mime-types knows it,
	// Go's built-in table does not).
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// contentSecurityPolicy matches the CSP built in packages/sync-server/src/app.ts.
// The dev flavour also allows Vite's inline preamble and HMR websocket.
func contentSecurityPolicy(isDev bool) string {
	scriptSrc := "'self' 'unsafe-eval' blob:"
	connectSrc := "http: https:"
	if isDev {
		scriptSrc = "'self' 'unsafe-inline' 'unsafe-eval' blob:"
		connectSrc = "'self' ws: wss: http: https:"
	}

	return "default-src 'self' blob:; " +
		"img-src 'self' blob: data:; " +
		"script-src " + scriptSrc + "; " +
		"style-src 'self' 'unsafe-inline'; " +
		"font-src 'self' data:; " +
		"connect-src " + connectSrc
}

func registerStatic(actual *router.RouterGroup[*core.RequestEvent], cfg *config.Config) {
	csp := contentSecurityPolicy(cfg.IsDev())

	// COOP/COEP/CSP are only ever sent on the web frontend responses: they
	// break the PocketBase admin UI at /_/ and PocketBase's API.
	setSecurityHeaders := func(e *core.RequestEvent) {
		header := e.Response.Header()
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Embedder-Policy", "require-corp")
		header.Set("Content-Security-Policy", csp)
	}

	if cfg.IsDev() {
		target, err := url.Parse(devServerURL)
		if err != nil {
			panic(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(r *http.Request) {
			director(r)
			// http-proxy-middleware's changeOrigin: true
			r.Host = target.Host
		}

		actual.GET("/{path...}", func(e *core.RequestEvent) error {
			setSecurityHeaders(e)
			proxy.ServeHTTP(e.Response, e.Request)
			return nil
		})
		return
	}

	fsys := os.DirFS(cfg.WebRoot)
	serve := apis.Static(fsys, true)
	actual.GET("/{path...}", func(e *core.RequestEvent) error {
		setSecurityHeaders(e)
		return serve(e)
	})
}
