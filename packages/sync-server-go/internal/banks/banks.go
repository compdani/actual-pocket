// Package banks hosts the bank-sync providers.
package banks

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

// Register binds the ported providers. GoCardless is intentionally not ported:
// it closed to new sign-ups in July 2025 and is winding down, so it keeps the
// not-configured stub.
func Register(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	registerSimpleFIN(actual, opts)

	registerStatusStub(actual, opts, "enablebanking")
	registerStatusStub(actual, opts, "pluggyai")
	registerStatusStub(actual, opts, "akahu")
	registerStatusStub(actual, opts, "gocardless")
}
