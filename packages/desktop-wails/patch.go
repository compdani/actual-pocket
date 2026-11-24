package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Page patches let the engine save a budget by sending only the pages that
// changed since its last save, instead of the whole file.
//
// A patch is applied through a redo journal next to the file: the journal is
// written, synced and renamed into place before the file is touched, then the
// file is patched and synced, then the journal is removed. A journal found
// later (the app stopped mid-patch) is complete by construction and is
// replayed; replaying is idempotent. So the file is always the old version
// or the new one, never a mix.
//
// A patch names the file version it was made against (see fileVersion) and
// the CRC of every byte range it overwrites; a file changed by anything else
// is refused with 412, and the engine falls back to a full write.

const journalSuffix = ".actual-journal"

// Journal layout: magic, target name, the patch as sent (see parsePatch),
// then the SHA-256 of everything before it.
var journalMagic = []byte("ACTUALJ1")

// Patch body layout (little endian): new file size u64, range count u32,
// then for each range: offset u64, length u32, CRC-32 (IEEE) of the bytes
// the range replaces u32, and the new bytes.
type pageRange struct {
	Offset int64
	OldCRC uint32
	Data   []byte
}

type pagePatch struct {
	Size   int64
	Ranges []pageRange
}

// The largest file a patch may produce; far beyond any budget
const maxPatchedSize = int64(1) << 40

var errPatchConflict = errors.New("the file changed since the engine last saved it")

// Hooks for tests that simulate the app stopping at each step.
var (
	afterJournalWritten = func() error { return nil }
	afterFirstRange     = func() error { return nil }
)

// fileVersion identifies the contents of a file cheaply: its size and
// modification time in nanoseconds. Every write through the host changes it.
func fileVersion(info fs.FileInfo) string {
	return fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UnixNano())
}

func parsePatch(body []byte) (*pagePatch, error) {
	reader := bytes.NewReader(body)
	var size int64
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
		return nil, errors.New("patch: missing size")
	}
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return nil, errors.New("patch: missing range count")
	}
	if size < 0 || size > maxPatchedSize {
		return nil, errors.New("patch: invalid size")
	}
	patch := &pagePatch{Size: size}
	var end int64
	for i := uint32(0); i < count; i++ {
		var offset int64
		var length, crc uint32
		if err := binary.Read(reader, binary.LittleEndian, &offset); err != nil {
			return nil, errors.New("patch: truncated range")
		}
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
			return nil, errors.New("patch: truncated range")
		}
		if err := binary.Read(reader, binary.LittleEndian, &crc); err != nil {
			return nil, errors.New("patch: truncated range")
		}
		// Ranges are sorted, apart and inside the new size
		if offset < end || offset+int64(length) > size || int64(length) > int64(reader.Len()) {
			return nil, errors.New("patch: invalid range")
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, errors.New("patch: truncated range")
		}
		patch.Ranges = append(patch.Ranges, pageRange{Offset: offset, OldCRC: crc, Data: data})
		end = offset + int64(length)
	}
	if reader.Len() != 0 {
		return nil, errors.New("patch: trailing bytes")
	}
	return patch, nil
}

func encodeJournal(name string, body []byte) []byte {
	var buffer bytes.Buffer
	buffer.Write(journalMagic)
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(len(name)))
	buffer.WriteString(name)
	buffer.Write(body)
	sum := sha256.Sum256(buffer.Bytes())
	buffer.Write(sum[:])
	return buffer.Bytes()
}

// decodeJournal returns the patch body a journal holds for name, or an
// error when the journal is damaged or belongs to another file.
func decodeJournal(name string, journal []byte) ([]byte, error) {
	if len(journal) < len(journalMagic)+4+sha256.Size || !bytes.HasPrefix(journal, journalMagic) {
		return nil, errors.New("journal: damaged")
	}
	content, sum := journal[:len(journal)-sha256.Size], journal[len(journal)-sha256.Size:]
	if expected := sha256.Sum256(content); !bytes.Equal(expected[:], sum) {
		return nil, errors.New("journal: checksum mismatch")
	}
	rest := content[len(journalMagic):]
	nameLength := binary.LittleEndian.Uint32(rest)
	rest = rest[4:]
	if uint64(nameLength) > uint64(len(rest)) || string(rest[:nameLength]) != name {
		return nil, errors.New("journal: belongs to another file")
	}
	return rest[nameLength:], nil
}

// applyPatch patches rel in place through its journal. The caller has
// checked the base version.
func applyPatch(root *os.Root, rel string, body []byte) error {
	patch, err := parsePatch(body)
	if err != nil {
		return err
	}
	if err := checkOldRanges(root, rel, patch); err != nil {
		return err
	}
	journal := rel + journalSuffix
	if err := writeAtomically(root, journal, encodeJournal(filepath.Base(rel), body)); err != nil {
		return err
	}
	if err := afterJournalWritten(); err != nil {
		return err
	}
	if err := patchInPlace(root, rel, patch); err != nil {
		// The journal stays: the next access replays it
		return err
	}
	if err := root.Remove(journal); err != nil {
		return err
	}
	syncDir(root, filepath.Dir(rel))
	return nil
}

// checkOldRanges makes sure each range still holds what the engine diffed
// against, on top of the version check.
func checkOldRanges(root *os.Root, rel string, patch *pagePatch) error {
	file, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	for _, r := range patch.Ranges {
		length := int64(len(r.Data))
		if r.Offset >= info.Size() {
			length = 0
		} else if r.Offset+length > info.Size() {
			length = info.Size() - r.Offset
		}
		old := make([]byte, length)
		if _, err := file.ReadAt(old, r.Offset); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if crc32.ChecksumIEEE(old) != r.OldCRC {
			return errPatchConflict
		}
	}
	return nil
}

func patchInPlace(root *os.Root, rel string, patch *pagePatch) error {
	file, err := root.OpenFile(rel, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	for i, r := range patch.Ranges {
		if _, err := file.WriteAt(r.Data, r.Offset); err != nil {
			file.Close()
			return err
		}
		if i == 0 {
			if err := afterFirstRange(); err != nil {
				file.Close()
				return err
			}
		}
	}
	if err := file.Truncate(patch.Size); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// recoverJournal finishes a patch the app did not get to complete, or drops
// a journal that cannot be used. Writing a journal is atomic, so a damaged
// one means the patch never started on the file, which is then intact.
func recoverJournal(root *os.Root, rel string) error {
	journal := rel + journalSuffix
	contents, err := root.ReadFile(journal)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	body, err := decodeJournal(filepath.Base(rel), contents)
	if err == nil {
		var patch *pagePatch
		if patch, err = parsePatch(body); err == nil {
			if err := patchInPlace(root, rel, patch); err != nil {
				return fmt.Errorf("replay the unfinished save of %s: %w", rel, err)
			}
		}
	}
	if err != nil {
		log.Printf("budget files: dropping unusable journal for %s: %v", rel, err)
	}
	if err := root.Remove(journal); err != nil {
		return err
	}
	syncDir(root, filepath.Dir(rel))
	return nil
}

// writeAtomically replaces rel with contents through a synced temporary file
// and a rename, then syncs the directory so the rename itself is durable.
func writeAtomically(root *os.Root, rel string, contents []byte) error {
	return writeRootFile(root, rel, bytes.NewReader(contents), int64(len(contents)))
}

// syncDir makes a rename or removal in dir durable. Windows cannot sync a
// directory, and NTFS journals the rename itself.
func syncDir(root *os.Root, dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	handle, err := root.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

func isJournal(name string) bool {
	return strings.HasSuffix(name, journalSuffix)
}
