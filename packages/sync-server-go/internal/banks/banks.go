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
	opts.Test = loadTestConfig(opts.Logger)
	if opts.Test.EnableBankingBaseURL != "" {
		enableBankingBaseURL = opts.Test.EnableBankingBaseURL
	}
	if opts.Test.PluggyBaseURL != "" {
		pluggyBaseURL = opts.Test.PluggyBaseURL
	}
	if opts.Test.AkahuBaseURL != "" {
		akahuBaseURL = opts.Test.AkahuBaseURL
	}

	registerSimpleFIN(actual, opts)
	registerEnableBanking(actual, opts)
	registerPluggy(actual, opts)
	registerAkahu(actual, opts)

	registerStatusStub(actual, opts, "gocardless")
}
