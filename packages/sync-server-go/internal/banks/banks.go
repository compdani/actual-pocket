// Package banks hosts the bank sync providers.
//
// Until a provider is ported (Phase 13), its routes are not registered and its
// /status endpoint reports "not configured", which is exactly what a fresh
// server without credentials looks like. The client calls /status for every
// provider when a budget opens, so without these stubs it shows an error.
package banks

import (
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
)

// Providers is the order the plan ports them in.
var Providers = []string{
	"simplefin",
	"enablebanking",
	"pluggyai",
	"akahu",
	"gocardless",
}

type statusResponse struct {
	Status string `json:"status"`
	Data   struct {
		Configured bool `json:"configured"`
	} `json:"data"`
}

// Register binds the not-ported status stubs.
func Register(actual *router.RouterGroup[*core.RequestEvent], resolver *auth.Resolver) {
	if resolver == nil {
		return
	}

	for _, provider := range Providers {
		provider := provider
		actual.POST("/"+provider+"/status", func(e *core.RequestEvent) error {
			if resolver.ResolveSession(e, "") == nil {
				return nil
			}

			response := statusResponse{Status: "ok"}
			response.Data.Configured = false

			return httpx.JSON(e, http.StatusOK, response)
		})
	}
}
