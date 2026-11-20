// Package syncdb is the per-group CRDT message log: one SQLite file per group
// under <pbDataDir>/actual_groups, exactly like the Node server's
// sync-simple.js.
//
// The log is not a PocketBase collection on purpose (D3): it is high volume,
// append-only binary data, and each sync is a hot-path write that should not
// contend with PocketBase's own database writer.
package syncdb

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	_ "modernc.org/sqlite"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt"
)

// messagesSQL is a verbatim copy of packages/sync-server/src/sql/messages.sql.
// The Node server stays the reference until Phase 15.
//
//go:embed messages.sql
var messagesSQL string

// maxFutureDrift matches the client's Timestamp maxDrift: a message further in
// the future would break sync on other devices.
const maxFutureDrift = 5 * time.Minute

// groupIDPattern is the same allow list the Node server applies to file and
// group ids.
var groupIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var (
	// ErrInvalidGroupID is returned for a group id that could escape the
	// groups directory.
	ErrInvalidGroupID = errors.New("invalid group id")
	// ErrInvalidTimestamp rejects a message whose timestamp does not parse.
	ErrInvalidTimestamp = errors.New("invalid-timestamp")
	// ErrClockDrift rejects a message more than five minutes in the future.
	ErrClockDrift = errors.New("clock-drift")
)

// Message is one CRDT message envelope, decoupled from the protobuf wire type.
type Message struct {
	Timestamp   string
	IsEncrypted bool
	Content     []byte
}

// Result is what a sync returns: the merkle trie after the insert and the
// messages that were already stored before it.
type Result struct {
	Trie     *crdt.TrieNode
	Messages []Message
}

// Engine owns the group databases.
type Engine struct {
	dir string

	// backupMu pauses syncs while PocketBase copies or restores pb_data,
	// because it copies the group files without locking them.
	backupMu sync.RWMutex

	// groups serialises the merkle read-modify-write per group. Node never
	// needed this (it is single threaded); Go does.
	groups sync.Map // group id -> *sync.Mutex

	now func() time.Time
}

// New creates an engine rooted at dir, creating the directory when missing.
func New(dir string) (*Engine, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve group directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create group directory: %w", err)
	}

	return &Engine{dir: absolute, now: time.Now}, nil
}

// Dir is the directory holding the group databases.
func (e *Engine) Dir() string { return e.dir }

// BindBackupHooks holds the write lock while PocketBase creates or restores a
// backup, so no group file is being written or deleted mid-copy.
func (e *Engine) BindBackupHooks(app core.App) {
	app.OnBackupCreate().Bind(&hook.Handler[*core.BackupEvent]{
		Id: "actualSyncBackupCreate",
		Func: func(event *core.BackupEvent) error {
			e.backupMu.Lock()
			defer e.backupMu.Unlock()
			return event.Next()
		},
	})
	app.OnBackupRestore().Bind(&hook.Handler[*core.BackupEvent]{
		Id: "actualSyncBackupRestore",
		Func: func(event *core.BackupEvent) error {
			e.backupMu.Lock()
			defer e.backupMu.Unlock()
			return event.Next()
		},
	})
}

// Sync ports sync-simple.js: read the messages newer than `since` first,
// then insert the incoming ones and update the merkle in one transaction.
func (e *Engine) Sync(groupID string, messages []Message, since string) (Result, error) {
	path, err := e.path(groupID)
	if err != nil {
		return Result{}, err
	}

	unlock := e.lockGroup(groupID)
	defer unlock()

	e.backupMu.RLock()
	defer e.backupMu.RUnlock()

	db, err := e.open(path)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()

	// This runs before the insert, and the order matters: it is the client's
	// "what did I miss" list.
	pending, err := selectMessages(db, since)
	if err != nil {
		return Result{}, err
	}

	trie, err := e.addMessages(db, messages)
	if err != nil {
		return Result{}, err
	}

	return Result{Trie: trie, Messages: pending}, nil
}

// Delete removes a group's log. It is used when a budget is reset; the next
// sync recreates the file with an empty schema.
func (e *Engine) Delete(groupID string) error {
	path, err := e.path(groupID)
	if err != nil {
		return err
	}

	unlock := e.lockGroup(groupID)
	defer unlock()

	e.backupMu.RLock()
	defer e.backupMu.RUnlock()

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete group database %q: %w", groupID, err)
	}

	return nil
}

// addMessages inserts new messages and folds them into the merkle trie. Any
// rejected timestamp rolls the whole batch back.
func (e *Engine) addMessages(db *sql.DB, messages []Message) (*crdt.TrieNode, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin sync transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	trie, err := readMerkle(tx)
	if err != nil {
		return nil, err
	}

	for _, message := range messages {
		inserted, err := insertMessage(tx, message)
		if err != nil {
			return nil, err
		}
		if !inserted {
			// A duplicate: the server already has it, so its timestamp is not
			// re-validated (Node behaves the same way).
			continue
		}

		timestamp, ok := crdt.Parse(message.Timestamp)
		if !ok {
			return nil, ErrInvalidTimestamp
		}
		if timestamp.Millis()-e.now().UnixMilli() > maxFutureDrift.Milliseconds() {
			return nil, ErrClockDrift
		}

		trie = crdt.Insert(trie, timestamp)
	}

	trie = crdt.Prune(trie)

	if err := saveMerkle(tx, trie); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit sync transaction: %w", err)
	}

	return trie, nil
}

func insertMessage(tx *sql.Tx, message Message) (bool, error) {
	result, err := tx.Exec(
		`INSERT OR IGNORE INTO messages_binary (timestamp, is_encrypted, content)
		 VALUES (?, ?, ?)`,
		message.Timestamp,
		boolToInt(message.IsEncrypted),
		message.Content,
	)
	if err != nil {
		return false, fmt.Errorf("insert message: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count inserted messages: %w", err)
	}

	return affected > 0, nil
}

// selectMessages returns the stored messages newer than since, ordered by
// timestamp, before the batch is inserted.
func selectMessages(db *sql.DB, since string) ([]Message, error) {
	rows, err := db.Query(
		`SELECT timestamp, is_encrypted, content
		 FROM messages_binary
		 WHERE timestamp > ?
		 ORDER BY timestamp`,
		since,
	)
	if err != nil {
		return nil, fmt.Errorf("select messages: %w", err)
	}
	defer rows.Close()

	messages := []Message{}
	for rows.Next() {
		var (
			timestamp   string
			isEncrypted int64
			content     []byte
		)
		if err := rows.Scan(&timestamp, &isEncrypted, &content); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, Message{
			Timestamp:   timestamp,
			IsEncrypted: isEncrypted == 1,
			Content:     content,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}

	return messages, nil
}

func readMerkle(tx *sql.Tx) (*crdt.TrieNode, error) {
	var encoded string

	err := tx.QueryRow(`SELECT merkle FROM messages_merkles LIMIT 1`).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		// No merkle yet (first sync of a file): the JS server starts from {}.
		return &crdt.TrieNode{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read merkle: %w", err)
	}

	trie := &crdt.TrieNode{}
	if err := json.Unmarshal([]byte(encoded), trie); err != nil {
		return nil, fmt.Errorf("decode merkle: %w", err)
	}

	return trie, nil
}

func saveMerkle(tx *sql.Tx, trie *crdt.TrieNode) error {
	encoded, err := json.Marshal(trie)
	if err != nil {
		return fmt.Errorf("encode merkle: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO messages_merkles (id, merkle) VALUES (1, ?)
		 ON CONFLICT (id) DO UPDATE SET merkle = ?`,
		string(encoded),
		string(encoded),
	)
	if err != nil {
		return fmt.Errorf("save merkle: %w", err)
	}

	return nil
}

// open opens a group database, running the schema on a brand new file. The
// default rollback journal is kept: WAL would add -wal/-shm side files that a
// reset or a backup would have to handle too.
func (e *Engine) open(path string) (*sql.DB, error) {
	needsInit := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		needsInit = true
	}

	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open group database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if needsInit {
		if _, err := db.Exec(messagesSQL); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialise group database: %w", err)
		}
	}

	return db, nil
}

// path validates the group id and resolves its database file, keeping the
// result inside the engine's directory.
func (e *Engine) path(groupID string) (string, error) {
	if !groupIDPattern.MatchString(groupID) {
		return "", ErrInvalidGroupID
	}

	path := filepath.Join(e.dir, "group-"+groupID+".sqlite")

	// Belt and braces: the pattern already rules out traversal.
	if filepath.Dir(path) != e.dir {
		return "", ErrInvalidGroupID
	}

	return path, nil
}

func (e *Engine) lockGroup(groupID string) func() {
	value, _ := e.groups.LoadOrStore(groupID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()

	return mutex.Unlock
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
