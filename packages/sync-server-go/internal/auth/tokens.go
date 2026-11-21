package auth

import (
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// IssueToken mints a PocketBase auth token for record with the users
// collection's configured lifetime.
//
// PocketBase tokens carry a whole-second exp claim, so two logins in the same
// second for the same user produce byte-identical tokens; logging out one of
// them would revoke the other too. The issuer remembers the last exp it handed
// out per user and pushes the next one forward by whole seconds until it is
// strictly greater.
func (r *Resolver) IssueToken(record *core.Record) (string, error) {
	duration := record.Collection().AuthToken.Duration

	r.tokensMu.Lock()
	if r.lastTokenExp == nil {
		r.lastTokenExp = make(map[string]int64)
	}

	exp := time.Now().Unix() + duration
	if last, ok := r.lastTokenExp[record.Id]; ok && exp <= last {
		exp = last + 1
	}
	r.lastTokenExp[record.Id] = exp
	r.tokensMu.Unlock()

	// NewStaticAuthToken computes exp as now + duration, so this lands exactly
	// on the chosen second while still going through PocketBase's signer.
	return record.NewStaticAuthToken(time.Until(time.Unix(exp, 0)))
}

// PruneIssuedTokens forgets expiries that already passed. Tokens that expired
// can no longer collide with a fresh one, so they need no memory.
func (r *Resolver) PruneIssuedTokens(now int64) int {
	r.tokensMu.Lock()
	defer r.tokensMu.Unlock()

	pruned := 0
	for id, exp := range r.lastTokenExp {
		if exp <= now {
			delete(r.lastTokenExp, id)
			pruned++
		}
	}

	return pruned
}
