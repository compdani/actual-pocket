package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type testRange struct {
	offset int64
	data   []byte
}

// encodePatch builds a patch body as the engine does, taking the CRC of
// each replaced range from old.
func encodePatch(old []byte, size int64, ranges ...testRange) []byte {
	var buffer bytes.Buffer
	_ = binary.Write(&buffer, binary.LittleEndian, size)
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(len(ranges)))
	for _, r := range ranges {
		end := min(r.offset+int64(len(r.data)), int64(len(old)))
		var replaced []byte
		if r.offset < int64(len(old)) {
			replaced = old[r.offset:end]
		}
		_ = binary.Write(&buffer, binary.LittleEndian, r.offset)
		_ = binary.Write(&buffer, binary.LittleEndian, uint32(len(r.data)))
		_ = binary.Write(&buffer, binary.LittleEndian, crc32.ChecksumIEEE(replaced))
		buffer.Write(r.data)
	}
	return buffer.Bytes()
}

func sendPatch(api http.Handler, path, version string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, fsAPIPrefix+"patch?path="+url.QueryEscape(path),
		strings.NewReader(base64.StdEncoding.EncodeToString(body)))
	request.Header.Set("X-Actual-Desktop", "1")
	request.Header.Set("X-Actual-Body-Encoding", "base64")
	request.Header.Set("X-Actual-Body-Length", strconv.Itoa(len(body)))
	request.Header.Set("X-Actual-Base-Version", version)
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	return recorder
}

// seed writes initial contents through the API and returns their version.
func seed(t *testing.T, api http.Handler, path string, contents []byte) string {
	t.Helper()
	response := call(api, http.MethodPut, "file", path, bytes.NewReader(contents))
	if response.Code != http.StatusNoContent {
		t.Fatalf("seed: status %d", response.Code)
	}
	version := response.Header().Get("X-Actual-Version")
	if version == "" {
		t.Fatal("write returned no version")
	}
	return version
}

const dbPath = virtualDocumentRoot + "/b/db.sqlite"

func TestPatchChangesOnlyTheGivenPages(t *testing.T) {
	api, dir := newFilesAPI(t)
	old := bytes.Repeat([]byte("a"), 4096*3)
	version := seed(t, api, dbPath, old)

	page := bytes.Repeat([]byte("b"), 4096)
	response := sendPatch(api, dbPath, version, encodePatch(old, int64(len(old)), testRange{4096, page}))
	if response.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", response.Code, response.Body.String())
	}
	newVersion := response.Header().Get("X-Actual-Version")
	if newVersion == "" || newVersion == version {
		t.Fatalf("version not updated: %q", newVersion)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "b", "db.sqlite"))
	want := append(append(bytes.Repeat([]byte("a"), 4096), page...), bytes.Repeat([]byte("a"), 4096)...)
	if !bytes.Equal(onDisk, want) {
		t.Fatal("file does not hold the patched contents")
	}
	if _, err := os.Stat(filepath.Join(dir, "b", "db.sqlite"+journalSuffix)); !os.IsNotExist(err) {
		t.Fatalf("journal left behind: %v", err)
	}
	got := call(api, http.MethodGet, "file", dbPath, nil)
	if got.Header().Get("X-Actual-Version") != newVersion {
		t.Fatal("read returned another version than the patch")
	}
}

func TestPatchGrowsAndShrinksTheFile(t *testing.T) {
	api, dir := newFilesAPI(t)
	old := bytes.Repeat([]byte("a"), 8192)
	version := seed(t, api, dbPath, old)

	grown := bytes.Repeat([]byte("c"), 4096)
	response := sendPatch(api, dbPath, version, encodePatch(old, 12288, testRange{8192, grown}))
	if response.Code != http.StatusNoContent {
		t.Fatalf("grow: %d %s", response.Code, response.Body.String())
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "b", "db.sqlite"))
	if len(onDisk) != 12288 || !bytes.Equal(onDisk[8192:], grown) {
		t.Fatal("file not grown")
	}

	response = sendPatch(api, dbPath, response.Header().Get("X-Actual-Version"), encodePatch(onDisk, 4096))
	if response.Code != http.StatusNoContent {
		t.Fatalf("shrink: %d %s", response.Code, response.Body.String())
	}
	onDisk, _ = os.ReadFile(filepath.Join(dir, "b", "db.sqlite"))
	if !bytes.Equal(onDisk, bytes.Repeat([]byte("a"), 4096)) {
		t.Fatal("file not shrunk")
	}
}

func TestPatchRefusesAChangedFile(t *testing.T) {
	api, dir := newFilesAPI(t)
	old := bytes.Repeat([]byte("a"), 8192)
	version := seed(t, api, dbPath, old)
	patch := encodePatch(old, 8192, testRange{0, bytes.Repeat([]byte("b"), 4096)})

	if got := sendPatch(api, dbPath, "1-1", patch).Code; got != http.StatusPreconditionFailed {
		t.Fatalf("stale version: %d, want 412", got)
	}
	if got := sendPatch(api, virtualDocumentRoot+"/missing/db.sqlite", version, patch).Code; got != http.StatusPreconditionFailed {
		t.Fatalf("missing file: %d, want 412", got)
	}

	// Changed by something else in place, with the version forged to match:
	// the replaced bytes no longer match their CRC
	file := filepath.Join(dir, "b", "db.sqlite")
	info, _ := os.Stat(file)
	changed := bytes.Repeat([]byte("z"), 8192)
	_ = os.WriteFile(file, changed, 0o644)
	_ = os.Chtimes(file, info.ModTime(), info.ModTime())
	if got := sendPatch(api, dbPath, version, patch).Code; got != http.StatusPreconditionFailed {
		t.Fatalf("changed contents: %d, want 412", got)
	}
	if onDisk, _ := os.ReadFile(file); !bytes.Equal(onDisk, changed) {
		t.Fatal("a refused patch changed the file")
	}
}

func TestPatchRefusesMalformedPatches(t *testing.T) {
	api, _ := newFilesAPI(t)
	old := bytes.Repeat([]byte("a"), 8192)
	version := seed(t, api, dbPath, old)
	page := bytes.Repeat([]byte("b"), 4096)
	for name, body := range map[string][]byte{
		"range past the size": encodePatch(old, 4096, testRange{4096, page}),
		"overlapping ranges":  encodePatch(old, 8192, testRange{4096, page}, testRange{0, page}),
		"truncated":           encodePatch(old, 8192, testRange{0, page})[:100],
		"empty":               {},
	} {
		if got := sendPatch(api, dbPath, version, body).Code; got != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, got)
		}
	}
}

func TestPatchRecoversAfterStoppingMidway(t *testing.T) {
	stop := errors.New("the app stopped")
	for name, hook := range map[string]*func() error{
		"after the journal": &afterJournalWritten,
		"after one range":   &afterFirstRange,
	} {
		t.Run(name, func(t *testing.T) {
			api, dir := newFilesAPI(t)
			old := bytes.Repeat([]byte("a"), 4096*3)
			version := seed(t, api, dbPath, old)
			first, last := bytes.Repeat([]byte("b"), 4096), bytes.Repeat([]byte("c"), 4096)
			patch := encodePatch(old, int64(len(old)), testRange{0, first}, testRange{8192, last})

			saved := *hook
			*hook = func() error { return stop }
			response := sendPatch(api, dbPath, version, patch)
			*hook = saved
			if response.Code == http.StatusNoContent {
				t.Fatal("the interrupted patch reported success")
			}
			if _, err := os.Stat(filepath.Join(dir, "b", "db.sqlite"+journalSuffix)); err != nil {
				t.Fatalf("no journal after the interruption: %v", err)
			}

			// The next access finishes the save
			got := call(api, http.MethodGet, "file", dbPath, nil).Body.Bytes()
			want := append(append(first, bytes.Repeat([]byte("a"), 4096)...), last...)
			if !bytes.Equal(got, want) {
				t.Fatal("the unfinished save was not completed")
			}
			if _, err := os.Stat(filepath.Join(dir, "b", "db.sqlite"+journalSuffix)); !os.IsNotExist(err) {
				t.Fatal("journal left behind after recovery")
			}
		})
	}
}

func TestDamagedJournalIsDropped(t *testing.T) {
	api, dir := newFilesAPI(t)
	old := bytes.Repeat([]byte("a"), 4096)
	seed(t, api, dbPath, old)
	journal := encodeJournal("db.sqlite", encodePatch(old, 4096, testRange{0, bytes.Repeat([]byte("b"), 4096)}))
	journal[len(journal)-1] ^= 0xff
	if err := os.WriteFile(filepath.Join(dir, "b", "db.sqlite"+journalSuffix), journal, 0o644); err != nil {
		t.Fatal(err)
	}
	// Listing recovers journals too, and never shows them
	response := call(api, http.MethodGet, "list", "", nil)
	if strings.Contains(response.Body.String(), journalSuffix) {
		t.Fatalf("journal listed: %s", response.Body.String())
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "b", "db.sqlite"))
	if !bytes.Equal(onDisk, old) {
		t.Fatal("a damaged journal was applied")
	}
	if _, err := os.Stat(filepath.Join(dir, "b", "db.sqlite"+journalSuffix)); !os.IsNotExist(err) {
		t.Fatal("damaged journal kept")
	}
}

func TestJournalAndTempFilesAreNotAddressable(t *testing.T) {
	api, _ := newFilesAPI(t)
	for _, path := range []string{dbPath + journalSuffix, virtualDocumentRoot + "/b/" + tempFilePrefix + "x"} {
		if got := call(api, http.MethodGet, "file", path, nil).Code; got != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", path, got)
		}
	}
}
