package store

import "ganigeorgiev.com/pocketbase/pocketbase/core"

// RegisterMCPRedaction blanks the hidden secret fields in every API response,
// including responses to PocketBase superusers and MCP clients. Internal reads
// through Store don't trigger OnRecordEnrich, so the server keeps working.
func RegisterMCPRedaction(app core.App, enabled bool) {
	if !enabled {
		return
	}

	app.OnRecordEnrich("actual_secrets", "actual_api_keys", "actual_pending_oauth").BindFunc(
		func(e *core.RecordEnrichEvent) error {
			switch e.Record.Collection().Name {
			case "actual_secrets":
				e.Record.Set("value", "")
			case "actual_api_keys":
				e.Record.Set("key_hash", "")
			case "actual_pending_oauth":
				e.Record.Set("code_verifier", "")
			}
			return e.Next()
		},
	)
}
