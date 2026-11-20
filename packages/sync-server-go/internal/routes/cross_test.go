package routes

import (
	"bytes"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb"
)

// TestCrossImplementationMerkle pushes the same message sequence to the Node
// server in packages/sync-server and to the Go server, and compares the merkle
// strings they return. It is the Phase 4 acceptance check for the port.
//
// It skips when the prerequisites are missing:
//   - node >= 24 on PATH (or ACTUAL_NODE_BIN pointing at one),
//   - a built Node server (`corepack yarn workspace @actual-app/sync-server build`).
func TestCrossImplementationMerkle(t *testing.T) {
	nodeBin, nodeVersion := findNode(t)
	if nodeBin == "" {
		t.Skip("node >= 24 not found; set ACTUAL_NODE_BIN to a Node 24 binary")
	}

	nodeServer, err := filepath.Abs(filepath.Join("..", "..", "..", "sync-server", "build", "app.js"))
	if err != nil {
		t.Fatalf("resolve Node server path: %v", err)
	}
	if _, err := os.Stat(nodeServer); err != nil {
		t.Skip("Node server is not built; run: corepack yarn workspace @actual-app/sync-server build")
	}

	const (
		fileID    = "cross-file"
		keyID     = "cross-key"
		userID    = "cross-user"
		token     = "cross-token"
		nodeGroup = "node-group"
		goGroup   = "go-group"
	)

	node := startNodeServer(t, nodeBin, nodeServer)
	seedNodeDatabase(t, node.dataDir, userID, token, fileID, nodeGroup, keyID)
	t.Logf("node %s serving on %d", nodeVersion, node.port)

	h := newHarness(t)
	h.createFile(t, fileSpec{
		ID:          fileID,
		GroupID:     ptr(goGroup),
		KeyID:       ptr(keyID),
		EncryptMeta: ptr(fmt.Sprintf(`{"keyId":%q}`, keyID)),
		SyncVersion: ptr(2.0),
		Owner:       h.admin.Id,
	})

	roundOne := crossMessages(t)
	roundTwo := crossMessagesFrom(t, time.Date(2017, 5, 1, 9, 0, 0, 0, time.UTC))

	steps := []struct {
		name     string
		since    string
		messages []*pb.MessageEnvelope
	}{
		{"first batch", zeroSince, roundOne},
		{"second batch", "2016-01-01T00:00:00.000Z-0000-0000000000000000", roundTwo},
		{"read back", "2016-01-01T00:00:00.000Z-0000-0000000000000000", nil},
	}

	for _, step := range steps {
		nodeResponse := node.sync(t, &pb.SyncRequest{
			FileId:   fileID,
			GroupId:  nodeGroup,
			KeyId:    keyID,
			Since:    step.since,
			Messages: step.messages,
		}, token)

		goResponse := decodeSyncResponse(t, h.postSync(t, &pb.SyncRequest{
			FileId:   fileID,
			GroupId:  goGroup,
			KeyId:    keyID,
			Since:    step.since,
			Messages: step.messages,
		}, testToken))

		if nodeResponse.GetMerkle() != goResponse.GetMerkle() {
			t.Fatalf("%s: merkle mismatch\n node %s\n   go %s", step.name, summariseMerkle(nodeResponse.GetMerkle()), summariseMerkle(goResponse.GetMerkle()))
		}

		nodeTimestamps := timestampsOf(nodeResponse.GetMessages())
		goTimestamps := timestampsOf(goResponse.GetMessages())
		if len(nodeTimestamps) != len(goTimestamps) {
			t.Fatalf("%s: node returned %d messages, Go returned %d", step.name, len(nodeTimestamps), len(goTimestamps))
		}
		for index := range nodeTimestamps {
			if nodeTimestamps[index] != goTimestamps[index] {
				t.Fatalf("%s: message %d differs: node %q, go %q", step.name, index, nodeTimestamps[index], goTimestamps[index])
			}
		}
	}
}

// nodeServer is a running Node sync server.
type nodeServer struct {
	port    int
	dataDir string
	logs    *bytes.Buffer
}

func startNodeServer(t *testing.T, nodeBin, script string) *nodeServer {
	t.Helper()

	port := freePort(t)
	dataDir := t.TempDir()
	logs := &bytes.Buffer{}

	command := exec.Command(nodeBin, script)
	command.Dir = filepath.Dir(script)
	command.Env = append(os.Environ(),
		"NODE_ENV=production",
		"ACTUAL_DATA_DIR="+dataDir,
		"ACTUAL_PORT="+strconv.Itoa(port),
		"ACTUAL_HOSTNAME=127.0.0.1",
	)
	command.Stdout = logs
	command.Stderr = logs

	if err := command.Start(); err != nil {
		t.Fatalf("start Node server: %v", err)
	}

	server := &nodeServer{port: port, dataDir: dataDir, logs: logs}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		if t.Failed() {
			t.Logf("Node server log:\n%s", logs.String())
		}
	})

	server.waitForHealth(t)

	return server
}

func (n *nodeServer) waitForHealth(t *testing.T) {
	t.Helper()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", n.port)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("Node server did not become healthy on port %d:\n%s", n.port, n.logs.String())
}

func (n *nodeServer) sync(t *testing.T, request *pb.SyncRequest, token string) *pb.SyncResponse {
	t.Helper()

	body, err := proto.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/sync/sync", n.port)
	httpRequest, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	httpRequest.Header.Set("Content-Type", "application/actual-sync")
	httpRequest.Header.Set("X-Actual-Token", token)

	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatalf("post to Node server: %v", err)
	}
	defer response.Body.Close()

	payload := new(bytes.Buffer)
	_, _ = payload.ReadFrom(response.Body)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("Node server returned %d: %s", response.StatusCode, payload.String())
	}

	decoded := &pb.SyncResponse{}
	if err := proto.Unmarshal(payload.Bytes(), decoded); err != nil {
		t.Fatalf("decode Node response: %v", err)
	}

	return decoded
}

// seedNodeDatabase inserts the user, session and file rows the Node server
// would normally hold in account.sqlite.
func seedNodeDatabase(t *testing.T, dataDir, userID, token, fileID, groupID, keyID string) {
	t.Helper()

	path := filepath.Join(dataDir, "server-files", "account.sqlite")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open Node account db: %v", err)
	}
	defer db.Close()

	statements := []struct {
		sql    string
		params []any
	}{
		{
			`INSERT INTO users (id, user_name, display_name, role, enabled, owner) VALUES (?, ?, ?, 'ADMIN', 1, 1)`,
			[]any{userID, "cross@example.com", "Cross"},
		},
		{
			`INSERT INTO sessions (token, expires_at, user_id, auth_method) VALUES (?, -1, ?, 'password')`,
			[]any{token, userID},
		},
		{
			`INSERT INTO files (id, group_id, sync_version, encrypt_meta, encrypt_keyid, encrypt_salt, encrypt_test, deleted, name, owner)
			 VALUES (?, ?, 2, ?, ?, NULL, NULL, 0, 'Cross Budget', ?)`,
			[]any{fileID, groupID, fmt.Sprintf(`{"keyId":%q}`, keyID), keyID, userID},
		},
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement.sql, statement.params...); err != nil {
			t.Fatalf("seed Node db: %v", err)
		}
	}
}

// crossMessages builds a deterministic sequence that covers the merkle's tricky
// cases: spread across years, a cluster inside one minute, duplicates and both
// encryption flags.
func crossMessages(t *testing.T) []*pb.MessageEnvelope {
	t.Helper()

	base := time.Date(2015, 1, 1, 6, 0, 0, 0, time.UTC)
	messages := make([]*pb.MessageEnvelope, 0, 60)

	for index := 0; index < 40; index++ {
		at := base.Add(time.Duration(index*37) * 24 * time.Hour).Add(time.Duration(index%11) * time.Minute)
		messages = append(messages, crossMessage(at, index%0xffff, index%3 == 0, []byte(fmt.Sprintf("payload-%d", index))))
	}

	// One minute with several counters.
	cluster := base.Add(200 * 24 * time.Hour)
	for counter := 0; counter < 5; counter++ {
		messages = append(messages, crossMessage(cluster, counter, false, []byte("cluster")))
	}

	// Duplicates: the server must ignore them without changing the merkle.
	messages = append(messages, messages[3], messages[17], messages[len(messages)-1])

	// A binary payload.
	messages = append(messages, crossMessage(base.Add(300*24*time.Hour), 7, true, []byte{0x00, 0x01, 0xfe, 0xff}))

	return messages
}

func crossMessagesFrom(t *testing.T, start time.Time) []*pb.MessageEnvelope {
	t.Helper()

	messages := make([]*pb.MessageEnvelope, 0, 3)
	for index := 0; index < 3; index++ {
		messages = append(messages, crossMessage(start.Add(time.Duration(index)*time.Hour), index, false, []byte(fmt.Sprintf("later-%d", index))))
	}

	return messages
}

func crossMessage(at time.Time, counter int, encrypted bool, content []byte) *pb.MessageEnvelope {
	timestamp := fmt.Sprintf("%s-%04X-%s",
		at.UTC().Format("2006-01-02T15:04:05.000Z"),
		counter,
		"CROSSNODE0000001",
	)

	return &pb.MessageEnvelope{
		Timestamp:   timestamp,
		IsEncrypted: encrypted,
		Content:     content,
	}
}

func timestampsOf(messages []*pb.MessageEnvelope) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.GetTimestamp())
	}
	return out
}

func summariseMerkle(merkle string) string {
	const limit = 300
	if len(merkle) <= limit {
		return merkle
	}
	return merkle[:limit] + "..."
}

func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

// findNode returns a Node 24+ binary and its version.
func findNode(t *testing.T) (string, string) {
	t.Helper()

	candidates := []string{}
	if custom := os.Getenv("ACTUAL_NODE_BIN"); custom != "" {
		candidates = append(candidates, custom)
	}
	candidates = append(candidates, "node")

	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}

		output, err := exec.Command(path, "--version").Output()
		if err != nil {
			continue
		}

		version := strings.TrimSpace(string(output))
		major, err := strconv.Atoi(strings.TrimPrefix(strings.SplitN(version, ".", 2)[0], "v"))
		if err != nil || major < 24 {
			continue
		}

		return path, version
	}

	return "", ""
}
