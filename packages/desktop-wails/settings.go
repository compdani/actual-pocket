package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Settings are the desktop host's own preferences. The app's preferences
// live with the budget engine; these are the ones the host needs before the
// WebView starts (the budget folder, the local server) or that only it uses.
type Settings struct {
	// DocumentDir is the folder holding the budget directories
	DocumentDir string `json:"documentDir,omitempty"`
	SyncServer  SyncServerSettings `json:"syncServer"`
	Window      *WindowBounds      `json:"window,omitempty"`
	// LastFileDialogDir is where the import dialog opens next
	LastFileDialogDir string `json:"lastFileDialogDir,omitempty"`
}

type SyncServerSettings struct {
	Port      int  `json:"port,omitempty"`
	AutoStart bool `json:"autoStart"`
}

type WindowBounds struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

const defaultSyncServerPort = 5007

// SettingsStore loads and saves Settings as JSON in the host's data folder.
type SettingsStore struct {
	path string
	mu   sync.Mutex
	data Settings
}

func loadSettings(dataDir string) (*SettingsStore, error) {
	store := &SettingsStore{path: filepath.Join(dataDir, "settings.json")}
	contents, err := os.ReadFile(store.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read settings: %w", err)
	default:
		if err := json.Unmarshal(contents, &store.data); err != nil {
			// A damaged file must not stop the app; keep a copy for recovery
			_ = os.Rename(store.path, store.path+".damaged")
			store.data = Settings{}
		}
	}
	if store.data.SyncServer.Port == 0 {
		store.data.SyncServer.Port = defaultSyncServerPort
	}
	return store, nil
}

func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// Update changes the settings under the lock and saves them atomically.
func (s *SettingsStore) Update(change func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	change(&next)
	contents, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.path, contents, 0o600); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	s.data = next
	return nil
}

// writeFileAtomic replaces path with contents so readers see the old or the
// new file, never a partial one.
func writeFileAtomic(path string, contents []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(contents); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// defaultDocumentDir is where budgets live until the user picks a folder:
// the folder the Electron app used, so its budgets open in place. That is
// the `document-dir` Electron stored, when it still exists, and otherwise
// Documents/Actual in the home folder.
func defaultDocumentDir(configDir, homeDir string) string {
	if dir := electronDocumentDir(configDir); dir != "" {
		return dir
	}
	return filepath.Join(documentsFolder(homeDir), "Actual")
}

// electronDocumentDir reads the budget folder from the Electron app's
// global-store.json (its user data folder is named after the product,
// "Actual").
func electronDocumentDir(configDir string) string {
	contents, err := os.ReadFile(filepath.Join(configDir, "Actual", "global-store.json"))
	if err != nil {
		return ""
	}
	var prefs struct {
		DocumentDir string `json:"document-dir"`
	}
	if json.Unmarshal(contents, &prefs) != nil || prefs.DocumentDir == "" {
		return ""
	}
	if info, err := os.Stat(prefs.DocumentDir); err != nil || !info.IsDir() {
		return ""
	}
	return prefs.DocumentDir
}

// documentsFolder is the user's Documents folder, as Electron resolved it.
func documentsFolder(homeDir string) string {
	if dir := xdgDocumentsDir(homeDir); dir != "" {
		return dir
	}
	return filepath.Join(homeDir, "Documents")
}
