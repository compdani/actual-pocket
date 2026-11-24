package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func newFilesAPI(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Actual")
	return &budgetFilesAPI{documentDir: func() string { return dir }}, dir
}

func call(handler http.Handler, method, endpoint, path string, body io.Reader) *httptest.ResponseRecorder {
	target := fsAPIPrefix + endpoint
	if path != "" {
		target += "?path=" + url.QueryEscape(path)
	}
	var payload []byte
	if body != nil {
		payload, _ = io.ReadAll(body)
	}
	request := httptest.NewRequest(method, target, strings.NewReader(base64.StdEncoding.EncodeToString(payload)))
	request.Header.Set("X-Actual-Desktop", "1")
	if method == http.MethodPut {
		request.Header.Set("X-Actual-Body-Encoding", "base64")
		request.Header.Set("X-Actual-Body-Length", strconv.Itoa(len(payload)))
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestBudgetFilesRoundTrip(t *testing.T) {
	api, dir := newFilesAPI(t)
	path := virtualDocumentRoot + "/My-Budget-abc/db.sqlite"

	if got := call(api, http.MethodGet, "file", path, nil).Code; got != http.StatusNotFound {
		t.Fatalf("missing file: status %d, want 404", got)
	}
	if got := call(api, http.MethodPut, "file", path, strings.NewReader("first")).Code; got != http.StatusNoContent {
		t.Fatalf("write: status %d", got)
	}
	if got := call(api, http.MethodPut, "file", path, strings.NewReader("second")).Code; got != http.StatusNoContent {
		t.Fatalf("overwrite: status %d", got)
	}
	response := call(api, http.MethodGet, "file", path, nil)
	if response.Code != http.StatusOK || response.Body.String() != "second" {
		t.Fatalf("read: %d %q", response.Code, response.Body.String())
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "My-Budget-abc", "db.sqlite"))
	if err != nil || string(onDisk) != "second" {
		t.Fatalf("file on disk: %q %v", onDisk, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "My-Budget-abc"))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}

	response = call(api, http.MethodGet, "stat", path, nil)
	var stat struct {
		MtimeMs int64 `json:"mtimeMs"`
		IsDir   bool  `json:"isDir"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &stat); err != nil || stat.MtimeMs == 0 || stat.IsDir {
		t.Fatalf("stat: %s %v", response.Body.String(), err)
	}

	if got := call(api, http.MethodDelete, "file", path, nil).Code; got != http.StatusNoContent {
		t.Fatalf("delete: status %d", got)
	}
	if got := call(api, http.MethodDelete, "file", path, nil).Code; got != http.StatusNoContent {
		t.Fatalf("deleting a missing file must succeed, got %d", got)
	}
	call(api, http.MethodPost, "rmdir", virtualDocumentRoot+"/My-Budget-abc", nil)
	if _, err := os.Stat(filepath.Join(dir, "My-Budget-abc")); !os.IsNotExist(err) {
		t.Fatalf("empty directory not removed: %v", err)
	}
}

func TestBudgetFilesListing(t *testing.T) {
	api, dir := newFilesAPI(t)
	call(api, http.MethodPut, "file", virtualDocumentRoot+"/a/metadata.json", strings.NewReader("{}"))
	call(api, http.MethodPost, "mkdir", virtualDocumentRoot+"/a/backups", nil)
	if err := os.WriteFile(filepath.Join(dir, "a", tempFilePrefix+"x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	response := call(api, http.MethodGet, "list", "", nil)
	var listing struct {
		Files []string `json:"files"`
		Dirs  []string `json:"dirs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if strings.Join(listing.Files, ",") != virtualDocumentRoot+"/a/metadata.json" {
		t.Fatalf("files: %v", listing.Files)
	}
	want := virtualDocumentRoot + "," + virtualDocumentRoot + "/a," + virtualDocumentRoot + "/a/backups"
	if strings.Join(listing.Dirs, ",") != want {
		t.Fatalf("dirs: %v", listing.Dirs)
	}
}

func TestBudgetFilesRefusals(t *testing.T) {
	api, dir := newFilesAPI(t)
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, fsAPIPrefix+"list", nil)
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("request without the header: %d, want 403", recorder.Code)
	}

	for _, path := range []string{
		"/documents/Actual/../secret.txt",
		"/documents/Actual/a/../../secret.txt",
		"/documents/Other/x",
		"/etc/passwd",
		"/documents/Actual//x",
		"/documents/Actual/a\\..\\..\\secret.txt",
		"/documents/Actual/C:/x",
	} {
		if got := call(api, http.MethodGet, "file", path, nil).Code; got != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", path, got)
		}
	}

	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		if got := call(api, http.MethodGet, "file", virtualDocumentRoot+"/link", nil); got.Code == http.StatusOK {
			t.Fatalf("followed a symlink out of the folder: %q", got.Body.String())
		}
	}
}

func TestBudgetFilesFolderError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	dir := filepath.Join(parent, "Actual")
	api := &budgetFilesAPI{documentDir: func() string { return dir }}
	response := call(api, http.MethodGet, "list", "", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", response.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["path"] != dir || body["code"] != "EACCES" {
		t.Fatalf("error body: %v", body)
	}
}

func TestBudgetFilesRefuseIncompleteBodies(t *testing.T) {
	api, dir := newFilesAPI(t)
	path := virtualDocumentRoot + "/b/db.sqlite"
	if got := call(api, http.MethodPut, "file", path, strings.NewReader("original")).Code; got != http.StatusNoContent {
		t.Fatalf("write: status %d", got)
	}
	for name, setup := range map[string]func(*http.Request){
		// What WKWebView does to binary bodies: the body is simply missing
		"dropped body": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(""))
			r.Header.Set("X-Actual-Body-Encoding", "base64")
			r.Header.Set("X-Actual-Body-Length", "8")
		},
		"cut body": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(base64.StdEncoding.EncodeToString([]byte("repl"))))
			r.Header.Set("X-Actual-Body-Encoding", "base64")
			r.Header.Set("X-Actual-Body-Length", "8")
		},
		"longer body": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(base64.StdEncoding.EncodeToString([]byte("replacement"))))
			r.Header.Set("X-Actual-Body-Encoding", "base64")
			r.Header.Set("X-Actual-Body-Length", "8")
		},
		"bad base64": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader("not base64!!"))
			r.Header.Set("X-Actual-Body-Encoding", "base64")
			r.Header.Set("X-Actual-Body-Length", "8")
		},
		"no length": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader("cmVwbGFjZWQ="))
			r.Header.Set("X-Actual-Body-Encoding", "base64")
		},
	} {
		request := httptest.NewRequest(http.MethodPut, fsAPIPrefix+"file?path="+url.QueryEscape(path), nil)
		request.Header.Set("X-Actual-Desktop", "1")
		setup(request)
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, recorder.Code)
		}
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "b", "db.sqlite"))
	if err != nil || string(onDisk) != "original" {
		t.Fatalf("file changed by a refused write: %q %v", onDisk, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "b"))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}
