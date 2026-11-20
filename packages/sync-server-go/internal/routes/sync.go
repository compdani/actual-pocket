package routes

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
	"google.golang.org/protobuf/proto"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/syncdb"
)

// syncFormatVersion is the internal format of the sync messages. It must match
// SYNC_FORMAT_VERSION in packages/sync-server/src/app-sync/validation.ts.
const syncFormatVersion = 2

// fileIDPattern is the Node server's file/group id allow list.
var fileIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// sinceRequiredBody is Node's 422 payload for a missing `since`.
type sinceRequiredBody struct {
	Details string `json:"details"`
	Reason  string `json:"reason"`
	Status  string `json:"status"`
}

func registerSync(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	actual.POST("/sync/sync", opts.handleSync).
		Bind(apis.BodyLimit(int64(opts.Config.Upload.FileSizeSyncLimitMB) << 20))
}

// handleSync ports app.post('/sync') from packages/sync-server/src/app-sync.ts.
func (o Options) handleSync(e *core.RequestEvent) error {
	body, err := io.ReadAll(e.Request.Body)
	if err != nil {
		// apis.BodyLimit wraps the body and reports the per-route limit.
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return httpx.Fail(e, http.StatusRequestEntityTooLarge, "request-entity-too-large")
		}
		return httpx.Error(e)
	}

	request := &pb.SyncRequest{}
	if err := proto.Unmarshal(body, request); err != nil {
		return httpx.Error(e)
	}

	if request.GetSince() == "" {
		return httpx.JSON(e, http.StatusUnprocessableEntity, sinceRequiredBody{
			Details: "since-required",
			Reason:  "unprocessable-entity",
			Status:  "error",
		})
	}

	session, ok := o.authenticateBudgetRoute(e)
	if !ok {
		return nil
	}

	file, ok := o.verifySyncFile(e, request.GetFileId(), session.apiKeyFileIDs)
	if !ok {
		return nil
	}

	if !o.canAccessFile(e, file, session) {
		return nil
	}

	if validationError := validateSyncedFile(request.GetGroupId(), request.GetKeyId(), file); validationError != "" {
		return httpx.Text(e, http.StatusBadRequest, validationError)
	}

	result, err := o.Sync.Sync(request.GetGroupId(), toSyncdbMessages(request.GetMessages()), request.GetSince())
	if err != nil {
		if errors.Is(err, syncdb.ErrInvalidTimestamp) || errors.Is(err, syncdb.ErrClockDrift) {
			return httpx.Text(e, http.StatusBadRequest, err.Error())
		}
		return httpx.Error(e)
	}

	merkle, err := json.Marshal(result.Trie)
	if err != nil {
		return httpx.Error(e)
	}

	response, err := proto.Marshal(&pb.SyncResponse{
		Merkle:   string(merkle),
		Messages: toEnvelopes(result.Messages),
	})
	if err != nil {
		return httpx.Error(e)
	}

	e.Response.Header().Set("Content-Type", "application/actual-sync")
	e.Response.Header().Set("X-ACTUAL-SYNC-METHOD", "simple")
	e.Response.WriteHeader(http.StatusOK)

	_, err = e.Response.Write(response)
	return err
}

// verifySyncFile ports verifyFileExists: API key budgets are checked before
// the id, then the file lookup. A missing file is a 400, not a 404
// (NODE-PARITY: the Node server has a FIXME about this).
func (o Options) verifySyncFile(e *core.RequestEvent, fileID string, apiKeyFileIDs []string) (*store.File, bool) {
	if apiKeyFileIDs != nil && !contains(apiKeyFileIDs, fileID) {
		_ = httpx.Text(e, http.StatusForbidden, "file-access-not-allowed")
		return nil, false
	}

	if !fileIDPattern.MatchString(fileID) {
		_ = httpx.Text(e, http.StatusBadRequest, "invalid fileId")
		return nil, false
	}

	file, err := o.Store.File(fileID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = httpx.Text(e, http.StatusBadRequest, "file-not-found")
			return nil, false
		}
		_ = httpx.Error(e)
		return nil, false
	}

	return file, true
}

// canAccessFile ports requireFileAccess: owner, server admin, or an explicit
// grant.
func (o Options) canAccessFile(e *core.RequestEvent, file *store.File, session *budgetSession) bool {
	if session.apiKeyFileIDs == nil && (file.Owner == session.userID || o.Store.IsAdmin(session.userID)) {
		return true
	}

	count, err := o.Store.CountUserAccess(file.ID, session.userID)
	if err != nil {
		_ = httpx.Error(e)
		return false
	}
	if count > 0 {
		return true
	}

	_ = httpx.Text(e, http.StatusForbidden, "file-access-not-allowed")
	return false
}

// validateSyncedFile is a direct port of
// packages/sync-server/src/app-sync/validation.ts.
func validateSyncedFile(groupID, keyID string, file *store.File) string {
	// syncVersion is stored in a SMALLINT column but typed as a string on the
	// Node side, so it is coerced before comparing.
	if file.SyncVersion == nil || *file.SyncVersion < syncFormatVersion {
		return "file-old-version"
	}

	// When resetting sync state there is no group id and the file is awaiting
	// an upload.
	if file.GroupID == nil {
		return "file-needs-upload"
	}

	// The file must be encrypted with the key it is registered with.
	uploadedKeyID := ""
	if file.EncryptMeta != nil {
		var meta struct {
			KeyID string `json:"keyId"`
		}
		if err := json.Unmarshal([]byte(*file.EncryptMeta), &meta); err != nil {
			return "file-key-mismatch"
		}
		uploadedKeyID = meta.KeyID
	}
	registeredKeyID := ""
	if file.EncryptKeyID != nil {
		registeredKeyID = *file.EncryptKeyID
	}
	if uploadedKeyID != registeredKeyID {
		return "file-key-mismatch"
	}

	// The changes belong to an old group: the file was reset.
	if groupID != *file.GroupID {
		return "file-has-reset"
	}

	// The data is encrypted with a different key than the registered one.
	if keyID != registeredKeyID {
		return "file-has-new-key"
	}

	return ""
}

func toSyncdbMessages(envelopes []*pb.MessageEnvelope) []syncdb.Message {
	messages := make([]syncdb.Message, 0, len(envelopes))
	for _, envelope := range envelopes {
		messages = append(messages, syncdb.Message{
			Timestamp:   envelope.GetTimestamp(),
			IsEncrypted: envelope.GetIsEncrypted(),
			Content:     envelope.GetContent(),
		})
	}
	return messages
}

func toEnvelopes(messages []syncdb.Message) []*pb.MessageEnvelope {
	envelopes := make([]*pb.MessageEnvelope, 0, len(messages))
	for _, message := range messages {
		envelopes = append(envelopes, &pb.MessageEnvelope{
			Timestamp:   message.Timestamp,
			IsEncrypted: message.IsEncrypted,
			Content:     message.Content,
		})
	}
	return envelopes
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// budgetSession is the acting identity for a route that accepts API keys.
type budgetSession struct {
	userID string
	// apiKeyFileIDs is nil for a normal session and non-nil (possibly empty)
	// for an API key.
	apiKeyFileIDs []string
}

// authenticateBudgetRoute is the Phase 4 stand-in for PocketBase auth. Tests
// configure SyncTestToken/SyncTestUserID; with neither set the route answers
// 401, as it must until Phase 5 replaces this with auth.Resolve.
//
// TODO(phase 5): delete this and call auth.ResolveBudgetSession.
func (o Options) authenticateBudgetRoute(e *core.RequestEvent) (*budgetSession, bool) {
	unauthorized := func() {
		_ = httpx.FailDetails(e, http.StatusUnauthorized, "unauthorized", "token-not-found")
	}

	if o.SyncTestToken == "" || o.SyncTestUserID == "" {
		unauthorized()
		return nil, false
	}

	provided := e.Request.Header.Get("X-Actual-Token")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(o.SyncTestToken)) != 1 {
		unauthorized()
		return nil, false
	}

	// Tests act as any seeded user through this header.
	userID := o.SyncTestUserID
	if override := e.Request.Header.Get("X-Actual-Test-User"); override != "" {
		userID = override
	}

	return &budgetSession{userID: userID}, true
}
