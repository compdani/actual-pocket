package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/proto"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/syncdb"
	_ "github.com/compdani/actual-pocket/packages/sync-server-go/migrations"
)

// harness is a migrated PocketBase app plus the Actual routes bound to it.
type harness struct {
	app     core.App
	store   *store.Store
	engine  *syncdb.Engine
	config  *config.Config
	handler http.Handler

	admin    *core.Record
	user     *core.Record
	other    *core.Record
	disabled *core.Record

	adminToken    string
	userToken     string
	otherToken    string
	disabledToken string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	return newHarnessWithConfig(t, nil, true)
}

// newEmptyHarness is a migrated app with no users, for the bootstrap tests.
func newEmptyHarness(t *testing.T) *harness {
	t.Helper()

	return newHarnessWithConfig(t, nil, false)
}

func newHarnessWithConfig(t *testing.T, configure func(*config.Config), withUsers bool) *harness {
	t.Helper()

	dir := t.TempDir()
	cfg, err := config.LoadWith(config.Options{
		Env: map[string]string{
			"NODE_ENV":        "production",
			"ACTUAL_DATA_DIR": dir,
		},
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if configure != nil {
		configure(cfg)
	}

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  cfg.PBDataDir,
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	engine, err := syncdb.New(filepath.Join(cfg.PBDataDir, "actual_groups"))
	if err != nil {
		t.Fatalf("syncdb: %v", err)
	}
	engine.BindBackupHooks(app)

	h := &harness{
		app:    app,
		store:  store.New(app),
		engine: engine,
		config: cfg,
	}
	auth.RegisterMaintenance(app, h.store)

	if withUsers {
		h.admin = h.createUser(t, "admin@example.com", "admin-password", "ADMIN", true, true)
		h.user = h.createUser(t, "user@example.com", "user-password", "BASIC", true, false)
		h.other = h.createUser(t, "other@example.com", "other-password", "BASIC", true, false)
		h.disabled = h.createUser(t, "disabled@example.com", "disabled-password", "BASIC", false, false)

		h.adminToken = h.token(t, h.admin)
		h.userToken = h.token(t, h.user)
		h.otherToken = h.token(t, h.other)
		h.disabledToken = h.token(t, h.disabled)
	}

	server := newTestServer(t, cfg, "1.2.3", func(options *Options) {
		options.App = app
		options.Store = h.store
		options.Sync = engine
		options.Auth = auth.NewResolver(app, h.store)
	})
	h.handler = server.handler

	return h
}

func (h *harness) createUser(t *testing.T, email, password, role string, enabled, owner bool) *core.Record {
	t.Helper()

	users, err := h.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}

	record := core.NewRecord(users)
	record.Set("email", email)
	record.SetPassword(password)
	record.Set("role", role)
	record.Set("enabled", enabled)
	record.Set("owner", owner)
	record.Set("verified", true)

	if err := h.app.Save(record); err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}

	return record
}

// token issues a PocketBase auth token for the record, exactly like
// /api/collections/users/auth-with-password would.
func (h *harness) token(t *testing.T, record *core.Record) string {
	t.Helper()

	token, err := record.NewAuthToken()
	if err != nil {
		t.Fatalf("issue token for %s: %v", record.Id, err)
	}

	return token
}

// tokenWithDuration signs a token for the record with a custom lifetime.
func (h *harness) tokenWithDuration(t *testing.T, record *core.Record, duration time.Duration) string {
	t.Helper()

	users, err := h.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("find users: %v", err)
	}

	token, err := security.NewJWT(jwt.MapClaims{
		core.TokenClaimType:         core.TokenTypeAuth,
		core.TokenClaimId:           record.Id,
		core.TokenClaimCollectionId: users.Id,
		core.TokenClaimRefreshable:  true,
	}, record.TokenKey()+users.AuthToken.Secret, duration)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return token
}

// fileSpec describes an actual_files row. Nil pointers mean the column is
// left empty (SQL NULL).
type fileSpec struct {
	ID          string
	GroupID     *string
	KeyID       *string
	EncryptMeta *string
	SyncVersion *float64
	Owner       string
	Deleted     bool
	Name        string
}

func ptr[T any](value T) *T { return &value }

// createFile inserts a budget file.
func (h *harness) createFile(t *testing.T, spec fileSpec) *core.Record {
	t.Helper()

	collection, err := h.app.FindCollectionByNameOrId("actual_files")
	if err != nil {
		t.Fatal(err)
	}

	record := core.NewRecord(collection)
	record.Set("id", spec.ID)
	if spec.GroupID != nil {
		record.Set("group_id", *spec.GroupID)
	}
	if spec.KeyID != nil {
		record.Set("encrypt_keyid", *spec.KeyID)
	}
	if spec.EncryptMeta != nil {
		record.Set("encrypt_meta", *spec.EncryptMeta)
	}
	if spec.SyncVersion != nil {
		record.Set("sync_version", *spec.SyncVersion)
	}
	record.Set("owner", spec.Owner)
	record.Set("deleted", spec.Deleted)
	if spec.Name != "" {
		record.Set("name", spec.Name)
	}

	if err := h.app.Save(record); err != nil {
		t.Fatalf("create file %s: %v", spec.ID, err)
	}

	return record
}

// standardFile is a valid, synchronisable file owned by the given user.
func (h *harness) standardFile(t *testing.T, id, groupID, keyID, ownerID string) {
	t.Helper()

	h.createFile(t, fileSpec{
		ID:          id,
		GroupID:     ptr(groupID),
		KeyID:       ptr(keyID),
		EncryptMeta: ptr(fmt.Sprintf(`{"keyId":%q}`, keyID)),
		SyncVersion: ptr(2.0),
		Owner:       ownerID,
		Name:        "Test Budget",
	})
}

// syncRequest is a helper for building a minimal valid request.
func syncRequest(fileID, groupID, keyID, since string) *pb.SyncRequest {
	return &pb.SyncRequest{
		FileId:  fileID,
		GroupId: groupID,
		KeyId:   keyID,
		Since:   since,
	}
}

// postSync sends a protobuf sync request.
func (h *harness) postSync(t *testing.T, request *pb.SyncRequest, token string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := proto.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	httpRequest := httptest.NewRequest(http.MethodPost, "/sync/sync", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/actual-sync")
	if token != "" {
		httpRequest.Header.Set("X-Actual-Token", token)
	}

	return h.do(httpRequest)
}

// do runs a request against the router.
func (h *harness) do(request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

// postJSON posts a JSON body, like the web client does.
func (h *harness) postJSON(t *testing.T, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("X-Actual-Token", token)
	}

	return h.do(request)
}

// getJSON issues a GET with an optional token.
func (h *harness) getJSON(path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("X-Actual-Token", token)
	}

	return h.do(request)
}

// decodeSyncResponse parses a successful sync response.
func decodeSyncResponse(t *testing.T, recorder *httptest.ResponseRecorder) *pb.SyncResponse {
	t.Helper()

	response := &pb.SyncResponse{}
	if err := proto.Unmarshal(recorder.Body.Bytes(), response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	return response
}

// timestampFor builds a client-shaped timestamp.
func timestampFor(at time.Time) string {
	return at.UTC().Format("2006-01-02T15:04:05.000Z") + "-0000-0123456789ABCDEF"
}

// messageAt builds an envelope.
func messageAt(at time.Time, encrypted bool, content string) *pb.MessageEnvelope {
	return &pb.MessageEnvelope{
		Timestamp:   timestampFor(at),
		IsEncrypted: encrypted,
		Content:     []byte(content),
	}
}

// jsonBody decodes a JSON response body.
func jsonBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	decoded := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
	}

	return decoded
}
