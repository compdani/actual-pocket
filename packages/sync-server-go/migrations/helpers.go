package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// defaultSyncEncryptedFileSizeLimitMB mirrors config.Upload.SyncEncryptedFileSizeLimitMB.
const defaultSyncEncryptedFileSizeLimitMB = 50

var syncEncryptedFileSizeLimitMB = defaultSyncEncryptedFileSizeLimitMB

// SetSyncEncryptedFileSizeLimitMB records the configured upload limit so that
// actual_files.blob can use it as its per-file max size. Call it before the app
// runs migrations.
func SetSyncEncryptedFileSizeLimitMB(limit int) {
	if limit > 0 {
		syncEncryptedFileSizeLimitMB = limit
	}
}

// blobMaxSize is the file field limit in bytes.
func blobMaxSize() int64 {
	return int64(syncEncryptedFileSizeLimitMB) << 20
}

// addAutodateFields adds the created/updated pair every Actual collection has.
// They help the MCP tooling sort and diff records.
func addAutodateFields(col *core.Collection) {
	col.Fields.Add(&core.AutodateField{
		Name:     "created",
		OnCreate: true,
	})
	col.Fields.Add(&core.AutodateField{
		Name:     "updated",
		OnCreate: true,
		OnUpdate: true,
	})
}

// relationTo builds a single-value relation field pointing at collectionID.
func relationTo(name, collectionID string, cascadeDelete bool) *core.RelationField {
	return &core.RelationField{
		Name:          name,
		CollectionId:  collectionID,
		CascadeDelete: cascadeDelete,
		MaxSelect:     1,
	}
}

// collectionID resolves a collection's id, for relation fields.
func collectionID(app core.App, name string) (string, error) {
	col, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		return "", err
	}
	return col.Id, nil
}

// deleteCollection removes a collection, ignoring an already missing one so a
// down migration is safe to re-run.
func deleteCollection(app core.App, name string) error {
	col, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		return nil
	}
	return app.Delete(col)
}
