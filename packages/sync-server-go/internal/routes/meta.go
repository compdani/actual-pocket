package routes

import (
	"runtime"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
)

// BuildName is the name the Node server reports on /info. Clients and the
// desktop app check it, so it deliberately keeps the Node package name.
const BuildName = "@actual-app/sync-server"

// BuildDescription mirrors packages/sync-server/package.json.
const BuildDescription = "actual syncing server"

type infoResponse struct {
	Build buildInfo `json:"build"`
}

type buildInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type healthResponse struct {
	Status string `json:"status"`
}

// memoryUsage mirrors Node's process.memoryUsage() field set and order.
type memoryUsage struct {
	RSS          uint64 `json:"rss"`
	HeapTotal    uint64 `json:"heapTotal"`
	HeapUsed     uint64 `json:"heapUsed"`
	External     uint64 `json:"external"`
	ArrayBuffers uint64 `json:"arrayBuffers"`
}

type metricsResponse struct {
	Mem    memoryUsage `json:"mem"`
	Uptime float64     `json:"uptime"`
}

// metaStart is when the process started, for /metrics' uptime field.
var metaStart = time.Now()

func registerMeta(actual *router.RouterGroup[*core.RequestEvent], cfg *config.Config, version string) {
	// res.send(config.get('mode')) is a string, so Express labels it text/html.
	actual.GET("/mode", func(e *core.RequestEvent) error {
		return httpx.Text(e, 200, cfg.Mode)
	})

	actual.GET("/health", func(e *core.RequestEvent) error {
		return httpx.JSON(e, 200, healthResponse{Status: "UP"})
	})

	actual.GET("/info", func(e *core.RequestEvent) error {
		return httpx.JSON(e, 200, infoResponse{Build: buildInfo{
			Name:        BuildName,
			Description: BuildDescription,
			Version:     version,
		}})
	})

	actual.GET("/metrics", func(e *core.RequestEvent) error {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)

		return httpx.JSON(e, 200, metricsResponse{
			Mem: memoryUsage{
				RSS:          stats.Sys,
				HeapTotal:    stats.HeapSys,
				HeapUsed:     stats.HeapAlloc,
				External:     0,
				ArrayBuffers: 0,
			},
			Uptime: time.Since(metaStart).Seconds(),
		})
	})
}
