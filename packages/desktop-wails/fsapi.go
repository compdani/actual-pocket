package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// The budget engine's virtual budget folder (packages/loot-core
// native-host.ts NATIVE_DOCUMENT_ROOT), mapped onto the folder on disk.
const virtualDocumentRoot = "/documents/Actual"

const fsAPIPrefix = "/__actual/fs/"

// Atomic writes go through a temporary file with this prefix next to the
// target; listings skip them.
const tempFilePrefix = ".actual-tmp-"

// budgetFilesAPI serves the engine's file operations on the budget folder.
// It is reachable only through the app's own asset server, not a network
// port, and every request must carry the X-Actual-Desktop header so a link
// or image inside the app cannot use it. All access goes through os.Root,
// which refuses paths and symlinks that leave the folder.
type budgetFilesAPI struct {
	documentDir func() string
	// Logs every request and its outcome (ACTUAL_WAILS_DEBUG=1)
	debug bool
	// One request at a time, so a journal is never replayed while a patch
	// of the same file is being written
	mu sync.Mutex
}

// The engine never patches more than this in one request
const maxPatchBody = int64(1) << 31

func (a *budgetFilesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Actual-Desktop") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.debug {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		w = recorder
		defer func() {
			log.Printf("budget files: %s %s %s -> %d (%s bytes)", r.Method, r.URL.Path,
				r.URL.Query().Get("path"), recorder.status, r.Header.Get("X-Actual-Body-Length"))
		}()
	}
	endpoint := strings.TrimPrefix(r.URL.Path, fsAPIPrefix)
	dir := a.documentDir()

	if endpoint == "list" && r.Method == http.MethodGet {
		a.list(w, dir)
		return
	}

	rel, ok := relativePath(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeFolderError(w, dir, err)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		writeFolderError(w, dir, err)
		return
	}
	defer root.Close()

	// Finish a save the app did not complete before anything reads or
	// writes the file
	if rel != "." && !isJournal(rel) {
		if err := recoverJournal(root, rel); err != nil {
			writeFolderError(w, filepath.Join(dir, rel), err)
			return
		}
	}

	switch {
	case endpoint == "file" && r.Method == http.MethodGet:
		serveFile(w, root, dir, rel)
	case endpoint == "file" && r.Method == http.MethodPut:
		if rel == "." {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		body, length, err := requestBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		err = writeRootFile(root, rel, body, length)
		if errors.Is(err, errBodyLength) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			writeFolderError(w, filepath.Join(dir, rel), err)
			return
		}
		setVersionHeader(w, root, rel)
		w.WriteHeader(http.StatusNoContent)
	// A POST like the other writes, rather than the less common PATCH, which
	// the app's URL scheme handlers are not known to pass through
	case endpoint == "patch" && r.Method == http.MethodPost:
		a.patch(w, r, root, dir, rel)
	case endpoint == "file" && r.Method == http.MethodDelete:
		if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			writeFolderError(w, filepath.Join(dir, rel), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case endpoint == "stat" && r.Method == http.MethodGet:
		info, err := root.Stat(rel)
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeFolderError(w, filepath.Join(dir, rel), err)
			return
		}
		writeJSON(w, map[string]any{
			"mtimeMs": info.ModTime().UnixMilli(),
			"isDir":   info.IsDir(),
		})
	case endpoint == "mkdir" && r.Method == http.MethodPost:
		if err := root.MkdirAll(rel, 0o755); err != nil {
			writeFolderError(w, filepath.Join(dir, rel), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case endpoint == "rmdir" && r.Method == http.MethodPost:
		// Only an empty directory goes; anything else is left in place
		if rel != "." {
			_ = root.Remove(rel)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// patch applies a page patch (see patch.go) when the file is still at the
// version the engine diffed against.
func (a *budgetFilesAPI) patch(w http.ResponseWriter, r *http.Request, root *os.Root, dir, rel string) {
	info, err := root.Stat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, errPatchConflict.Error(), http.StatusPreconditionFailed)
		return
	}
	if err != nil {
		writeFolderError(w, filepath.Join(dir, rel), err)
		return
	}
	if base := r.Header.Get("X-Actual-Base-Version"); base == "" || base != fileVersion(info) {
		http.Error(w, errPatchConflict.Error(), http.StatusPreconditionFailed)
		return
	}
	reader, length, err := requestBody(r)
	if err != nil || length > maxPatchBody {
		http.Error(w, "invalid patch body", http.StatusBadRequest)
		return
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		http.Error(w, errBodyLength.Error(), http.StatusBadRequest)
		return
	}
	if extra, _ := reader.Read(make([]byte, 1)); extra != 0 {
		http.Error(w, errBodyLength.Error(), http.StatusBadRequest)
		return
	}
	err = applyPatch(root, rel, body)
	switch {
	case errors.Is(err, errPatchConflict):
		http.Error(w, err.Error(), http.StatusPreconditionFailed)
	case err != nil && strings.HasPrefix(err.Error(), "patch:"):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		writeFolderError(w, filepath.Join(dir, rel), err)
	default:
		setVersionHeader(w, root, rel)
		w.WriteHeader(http.StatusNoContent)
	}
}

func setVersionHeader(w http.ResponseWriter, root *os.Root, rel string) {
	if info, err := root.Stat(rel); err == nil {
		w.Header().Set("X-Actual-Version", fileVersion(info))
	}
}

// list returns every file and directory in the budget folder, as virtual
// paths, creating the folder when it does not exist yet.
func (a *budgetFilesAPI) list(w http.ResponseWriter, dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeFolderError(w, dir, err)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		writeFolderError(w, dir, err)
		return
	}
	defer root.Close()
	// Finish saves the app did not complete, so listed files are whole
	var journals []string
	_ = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && isJournal(name) {
			journals = append(journals, strings.TrimSuffix(filepath.FromSlash(name), journalSuffix))
		}
		return nil
	})
	for _, target := range journals {
		if err := recoverJournal(root, target); err != nil {
			writeFolderError(w, filepath.Join(dir, target), err)
			return
		}
	}
	files := []string{}
	dirs := []string{virtualDocumentRoot}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if strings.HasPrefix(entry.Name(), tempFilePrefix) || isJournal(entry.Name()) {
			return nil
		}
		virtual := virtualDocumentRoot + "/" + name
		switch {
		case entry.IsDir():
			dirs = append(dirs, virtual)
		case entry.Type().IsRegular():
			files = append(files, virtual)
		}
		return nil
	})
	if err != nil {
		writeFolderError(w, dir, err)
		return
	}
	writeJSON(w, map[string]any{"files": files, "dirs": dirs})
}

// relativePath turns a virtual path under the budget folder into a slash
// separated path relative to it ("." for the folder itself).
func relativePath(virtual string) (string, bool) {
	if virtual == virtualDocumentRoot {
		return ".", true
	}
	rest, ok := strings.CutPrefix(virtual, virtualDocumentRoot+"/")
	if !ok || rest == "" || strings.HasPrefix(rest, "/") || strings.ContainsAny(rest, "\\:\x00") {
		return "", false
	}
	cleaned := path.Clean(rest)
	if cleaned != rest || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	// The host's own journal and temporary files are not the engine's
	base := path.Base(cleaned)
	if isJournal(base) || strings.HasPrefix(base, tempFilePrefix) {
		return "", false
	}
	return filepath.FromSlash(cleaned), true
}

func serveFile(w http.ResponseWriter, root *os.Root, dir, rel string) {
	file, err := root.Open(rel)
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeFolderError(w, filepath.Join(dir, rel), err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Actual-Version", fileVersion(info))
	if _, err := io.Copy(w, file); err != nil {
		log.Printf("budget files: read %s: %v", rel, err)
	}
}

var errBodyLength = errors.New("the file arrived incomplete; nothing was written")

// requestBody returns the bytes of a write and how many there must be. The
// engine sends base64 text, since WKWebView drops binary bodies on the
// app's URL scheme, with the decoded length, which is required so a lost or
// cut body is refused rather than saved.
func requestBody(r *http.Request) (io.Reader, int64, error) {
	length, err := strconv.ParseInt(r.Header.Get("X-Actual-Body-Length"), 10, 64)
	if err != nil || length < 0 {
		return nil, 0, errors.New("missing X-Actual-Body-Length")
	}
	switch r.Header.Get("X-Actual-Body-Encoding") {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r.Body), length, nil
	case "":
		return r.Body, length, nil
	default:
		return nil, 0, errors.New("unsupported X-Actual-Body-Encoding")
	}
}

// writeRootFile replaces rel with the body atomically: written and synced to
// a temporary file in the same directory, then renamed over the target. A
// body that is not exactly length bytes leaves the target untouched.
func writeRootFile(root *os.Root, rel string, body io.Reader, length int64) error {
	dir := filepath.Dir(rel)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := filepath.Join(dir, tempFilePrefix+hex.EncodeToString(suffix))
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	// One byte past the expected length is enough to tell a longer body
	written, copyErr := io.Copy(file, io.LimitReader(body, length+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = root.Remove(tmp)
		if copyErr != nil && !isFilesystemError(copyErr) {
			// A malformed body (bad base64) rather than the disk
			return fmt.Errorf("%w: %v", errBodyLength, copyErr)
		}
		return err
	}
	if written != length {
		_ = root.Remove(tmp)
		return fmt.Errorf("%w (%d of %d bytes)", errBodyLength, written, length)
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	syncDir(root, dir)
	return nil
}

// writeFolderError reports a filesystem failure with the real path and an
// errno-style code, which the engine shows on its "data folder" error
// screen.
func writeFolderError(w http.ResponseWriter, path string, err error) {
	log.Printf("budget files: %s: %v", path, err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": err.Error(),
		"path":    path,
		"code":    errorCode(err),
	})
}

func isFilesystemError(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr)
}

func errorCode(err error) string {
	var errno syscall.Errno
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "EACCES"
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT"
	case errors.Is(err, syscall.ENOSPC):
		return "ENOSPC"
	case errors.Is(err, syscall.EROFS):
		return "EROFS"
	case errors.As(err, &errno):
		return "EIO"
	default:
		return ""
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Flush and Unwrap pass through: Wails' asset server finishes responses
// through http.Flusher, and a wrapper hiding it leaves requests hanging.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
