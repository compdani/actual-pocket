package store

import (
	"strings"

	"ganigeorgiev.com/fexpr"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/search"
)

// RegisterMCPRedaction hides credentials from record responses, including
// superuser responses. Full superusers still have access to the database and
// backups.
func RegisterMCPRedaction(app core.App, enabled bool) {
	if !enabled {
		return
	}

	fields := map[string]string{
		"actual_secrets":       "value",
		"actual_api_keys":      "key_hash",
		"actual_pending_oauth": "code_verifier",
	}
	collections := []string{"actual_secrets", "actual_api_keys", "actual_pending_oauth"}

	app.OnRecordEnrich(collections...).BindFunc(func(e *core.RecordEnrichEvent) error {
		record := e.Record
		if err := e.Next(); err != nil {
			return err
		}
		// PocketBase unhides fields for superusers during default enrichment.
		record.Hide(fields[record.Collection().Name])
		return nil
	})

	app.OnRecordsListRequest(collections...).BindFunc(func(e *core.RecordsListRequestEvent) error {
		field := fields[e.Collection.Name]
		query := e.Request.URL.Query()
		if filterReferencesField(query.Get("filter"), field) {
			return e.BadRequestError("Filtering by a redacted field is disabled.", nil)
		}
		for _, sort := range search.ParseSortFromString(query.Get("sort")) {
			if referencesField(sort.Name, field) {
				return e.BadRequestError("Sorting by a redacted field is disabled.", nil)
			}
		}
		return e.Next()
	})

	app.OnRecordUpdateRequest(collections...).BindFunc(func(e *core.RecordRequestEvent) error {
		field := fields[e.Collection.Name]
		if e.Record.GetString(field) == "" {
			// An admin form may submit an empty string for a hidden field.
			e.Record.SetRaw(field, e.Record.Original().GetRaw(field))
		}
		return e.Next()
	})
}

func filterReferencesField(filter, field string) bool {
	scanner := fexpr.NewScanner([]byte(filter))
	for {
		token, err := scanner.Scan()
		if err != nil || token.Type == fexpr.TokenEOF {
			return false
		}
		if filterTokenReferencesField(token, field) {
			return true
		}
	}
}

func filterTokenReferencesField(token fexpr.Token, field string) bool {
	if token.Type == fexpr.TokenIdentifier {
		return referencesField(token.Literal, field)
	}
	if args, ok := token.Meta.([]fexpr.Token); ok {
		for _, arg := range args {
			if filterTokenReferencesField(arg, field) {
				return true
			}
		}
	}
	return false
}

func referencesField(identifier, field string) bool {
	for _, part := range strings.FieldsFunc(identifier, func(r rune) bool { return r == '.' || r == ':' }) {
		if strings.EqualFold(part, field) {
			return true
		}
	}
	return false
}
