package auth

import (
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

// BindRevocationMiddleware applies the Actual denylist to PocketBase API auth.
func (r *Resolver) BindRevocationMiddleware(pbRouter *router.Router[*core.RequestEvent]) {
	pbRouter.Bind(&hook.Handler[*core.RequestEvent]{
		Priority: apis.DefaultLoadAuthTokenMiddlewarePriority + 1,
		Func: func(e *core.RequestEvent) error {
			if e.Auth != nil && e.Auth.Collection().Name == "users" && r.authorizationRevoked(e) {
				e.Auth = nil
			}
			return e.Next()
		},
	})
}

// BindRevocationHooks also checks the token at the refresh operation.
func (r *Resolver) BindRevocationHooks() {
	r.app.OnRecordAuthRefreshRequest("users").BindFunc(func(e *core.RecordAuthRefreshRequestEvent) error {
		if r.authorizationRevoked(e.RequestEvent) {
			return e.UnauthorizedError("The auth token is no longer valid.", nil)
		}
		return e.Next()
	})
}

func (r *Resolver) authorizationRevoked(e *core.RequestEvent) bool {
	// Use the same Authorization token PocketBase used for e.Auth.
	token := e.Request.Header.Get("Authorization")
	if len(token) > len("Bearer ") && strings.EqualFold(token[:len("Bearer ")], "Bearer ") {
		token = token[len("Bearer "):]
	}
	if token == "" {
		return false
	}
	revoked, err := r.store.IsTokenRevoked(store.HashToken(token))
	if err != nil {
		r.app.Logger().Error("Could not check token revocation", "error", err)
		return true
	}
	return revoked
}
