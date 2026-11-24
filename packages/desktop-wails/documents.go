package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// documentDir is the folder the budget engine's files live in.
func (d *Desktop) documentDir() string {
	if dir := d.settings.Get().DocumentDir; dir != "" {
		return dir
	}
	return d.defaultDocumentDir
}

// GetDocumentDir returns the budget folder on disk, for the settings screen.
func (d *Desktop) GetDocumentDir() string {
	return d.documentDir()
}

// ChooseFolder lets the user pick a folder and returns its path, or "" when
// the dialog was cancelled.
func (d *Desktop) ChooseFolder(title string) (string, error) {
	dialog := d.app.Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		AttachToWindow(d.window)
	if title != "" {
		dialog.SetTitle(title)
	}
	return dialog.PromptForSingleSelection()
}

// SetDocumentDir makes dir the budget folder without moving anything. The
// app reloads afterwards so the engine lists the new folder.
func (d *Desktop) SetDocumentDir(dir string) error {
	if err := checkUsableFolder(dir); err != nil {
		return err
	}
	return d.settings.Update(func(s *Settings) { s.DocumentDir = dir })
}

// MoveBudgetDirectory copies the budget folder's contents into dir, makes
// dir the budget folder, then removes what was copied from the old one. The
// caller saves and closes budgets first, and reloads afterwards.
func (d *Desktop) MoveBudgetDirectory(dir string) error {
	current := d.documentDir()
	if err := checkUsableFolder(dir); err != nil {
		return err
	}
	if within(dir, current) || within(current, dir) {
		return errors.New("the new folder must not be inside the current one, or contain it")
	}
	entries, err := os.ReadDir(current)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the current folder: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), tempFilePrefix) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, entry.Name())); err == nil {
			return fmt.Errorf("%s already exists in the new folder", entry.Name())
		}
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), tempFilePrefix) {
			continue
		}
		if err := copyTree(filepath.Join(current, entry.Name()), filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("copy %s: %w", entry.Name(), err)
		}
	}
	if err := d.settings.Update(func(s *Settings) { s.DocumentDir = dir }); err != nil {
		return err
	}
	// The move worked; a copy left behind (permissions, a file in use) only
	// costs space, so it is logged rather than reported
	for _, entry := range entries {
		removeWithRetry(filepath.Join(current, entry.Name()))
	}
	_ = os.Remove(current)
	return nil
}

// OpenInFileManager shows a file from a note's link in Finder or Explorer.
// Like the Electron app, only absolute local paths are accepted.
func (d *Desktop) OpenInFileManager(target string) error {
	if !filepath.IsAbs(target) || strings.HasPrefix(filepath.ToSlash(target), "//") {
		return errors.New("only absolute paths on this computer can be shown")
	}
	return revealInFileManager(target)
}

// RevealDocumentDir opens the budget folder in Finder or Explorer.
func (d *Desktop) RevealDocumentDir() error {
	return openInFileManager(d.documentDir())
}

func revealInFileManager(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", target).Start()
	case "windows":
		return exec.Command("explorer", "/select,", target).Start()
	default:
		return openInFileManager(filepath.Dir(target))
	}
}

func openInFileManager(dir string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", dir).Start()
	case "windows":
		return exec.Command("explorer", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// checkUsableFolder refuses folders that do not exist or that the app may
// not create files in (Windows Controlled Folder Access blocks writes
// without changing permissions, so a write is the only reliable test).
func checkUsableFolder(dir string) error {
	if dir == "" || !filepath.IsAbs(dir) {
		return errors.New("choose a folder on this computer")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("the folder does not exist: %w", err)
	}
	if !info.IsDir() {
		return errors.New("that is a file, not a folder")
	}
	probe, err := os.MkdirTemp(dir, ".actual-write-test-")
	if err != nil {
		return fmt.Errorf("the app is not allowed to create files in %s: %w", dir, err)
	}
	return os.Remove(probe)
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyTree copies a file or directory, keeping modification times so
// backups keep their dates.
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(destination, 0o755)
		case entry.Type().IsRegular():
			if err := copyFile(name, destination, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(destination, info.ModTime(), info.ModTime())
		default:
			// Links and devices are not budget files
			return nil
		}
	})
}

func copyFile(source, target string, perm fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func removeWithRetry(path string) {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.RemoveAll(path); err == nil {
			return
		}
		time.Sleep(time.Duration(200*(attempt+1)) * time.Millisecond)
	}
	log.Printf("could not remove %s from the old budget folder: %v", path, err)
}
