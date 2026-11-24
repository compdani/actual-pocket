package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestSettingsPersist(t *testing.T) {
	dir := t.TempDir()
	store, err := loadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.Get().SyncServer.Port != defaultSyncServerPort {
		t.Fatalf("default port: %d", store.Get().SyncServer.Port)
	}
	if err := store.Update(func(s *Settings) {
		s.DocumentDir = "/budgets"
		s.SyncServer = SyncServerSettings{Port: 6000, AutoStart: true}
	}); err != nil {
		t.Fatal(err)
	}
	again, err := loadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := again.Get()
	if got.DocumentDir != "/budgets" || got.SyncServer.Port != 6000 || !got.SyncServer.AutoStart {
		t.Fatalf("reloaded: %+v", got)
	}
}

func TestDamagedSettingsAreSetAside(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := loadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.Get().DocumentDir != "" {
		t.Fatal("damaged settings were used")
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.damaged")); err != nil {
		t.Fatalf("no recovery copy: %v", err)
	}
}

func TestDefaultDocumentDirUsesElectronFolder(t *testing.T) {
	configDir := t.TempDir()
	home := t.TempDir()
	custom := t.TempDir()

	if got, want := defaultDocumentDir(configDir, home), filepath.Join(documentsFolder(home), "Actual"); got != want {
		t.Fatalf("without Electron: %s, want %s", got, want)
	}

	electron := filepath.Join(configDir, "Actual")
	if err := os.MkdirAll(electron, 0o700); err != nil {
		t.Fatal(err)
	}
	store := `{"document-dir":` + quote(custom) + `,"theme":"dark"}`
	if err := os.WriteFile(filepath.Join(electron, "global-store.json"), []byte(store), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := defaultDocumentDir(configDir, home); got != custom {
		t.Fatalf("with Electron's folder: %s, want %s", got, custom)
	}

	// A folder that no longer exists falls back to the default
	gone := `{"document-dir":` + quote(filepath.Join(custom, "gone")) + `}`
	if err := os.WriteFile(filepath.Join(electron, "global-store.json"), []byte(gone), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := defaultDocumentDir(configDir, home), filepath.Join(documentsFolder(home), "Actual"); got != want {
		t.Fatalf("with a missing folder: %s, want %s", got, want)
	}
}

func quote(s string) string {
	b := []byte{'"'}
	for _, r := range s {
		if r == '\\' || r == '"' {
			b = append(b, '\\')
		}
		b = append(b, string(r)...)
	}
	return string(append(b, '"'))
}

func TestBoundsVisible(t *testing.T) {
	screens := []*application.Screen{{WorkArea: application.Rect{X: 0, Y: 0, Width: 1440, Height: 900}}}
	if !boundsVisible(WindowBounds{X: 100, Y: 100, Width: 800, Height: 600}, screens) {
		t.Fatal("window on screen reported hidden")
	}
	if boundsVisible(WindowBounds{X: 3000, Y: 100, Width: 800, Height: 600}, screens) {
		t.Fatal("window on a disconnected screen reported visible")
	}
	if boundsVisible(WindowBounds{X: 100, Y: -500, Width: 800, Height: 600}, screens) {
		t.Fatal("window with its title bar off screen reported visible")
	}
}
