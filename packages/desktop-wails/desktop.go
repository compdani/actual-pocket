package main

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Desktop exposes only operations that require a native host. Budget queries
// and sync run in the browser worker, which stores budget files on disk
// through budgetFilesAPI.
type Desktop struct {
	app      *application.App
	window   *application.WebviewWindow
	settings *SettingsStore
	server   *SyncServer
	// Where budgets live until the user picks a folder
	defaultDocumentDir string
	mu                 sync.Mutex
	ready              bool
	quit               bool
	oauth              *oauthReceiver
	closing            *time.Timer
	windowState        *windowState
}

type FileFilter struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
}

type ImportedFile struct {
	Name     string `json:"name"`
	Contents []byte `json:"contents"`
}

func (d *Desktop) OpenFile(filters []FileFilter) (*ImportedFile, error) {
	dialog := d.app.Dialog.OpenFile().CanChooseFiles(true).AttachToWindow(d.window)
	if last := d.settings.Get().LastFileDialogDir; last != "" {
		if info, err := os.Stat(last); err == nil && info.IsDir() {
			dialog.SetDirectory(last)
		}
	}
	for _, filter := range filters {
		patterns := make([]string, 0, len(filter.Extensions))
		for _, extension := range filter.Extensions {
			patterns = append(patterns, "*."+strings.TrimPrefix(extension, "."))
		}
		dialog.AddFilter(filter.Name, strings.Join(patterns, ";"))
	}
	filename, err := dialog.PromptForSingleSelection()
	if err != nil || filename == "" {
		return nil, err
	}
	// Open the next import where this one came from
	if err := d.settings.Update(func(s *Settings) { s.LastFileDialogDir = filepath.Dir(filename) }); err != nil {
		log.Printf("could not remember the import folder: %v", err)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read selected file: %w", err)
	}
	return &ImportedFile{Name: filepath.Base(filename), Contents: contents}, nil
}

func (d *Desktop) SaveFile(contents []byte, filename, title string) error {
	dialog := d.app.Dialog.SaveFile().
		SetFilename(filepath.Base(filename)).
		AttachToWindow(d.window)
	if title != "" {
		dialog.SetMessage(title)
	}
	selected, err := dialog.PromptForSingleSelection()
	if err != nil || selected == "" {
		return err
	}
	return os.WriteFile(selected, contents, 0o600)
}

func (d *Desktop) OpenURL(rawURL string) error {
	if !isExternalURL(rawURL) {
		return errors.New("only HTTP, HTTPS, and mailto links can be opened")
	}
	return d.app.Browser.OpenURL(rawURL)
}

func isExternalURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User != nil {
		return false
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Hostname() != ""
	case "mailto":
		return parsed.Opaque != ""
	default:
		return false
	}
}

func (d *Desktop) Ready() {
	d.mu.Lock()
	d.ready = true
	d.mu.Unlock()
}

func (d *Desktop) ConfirmClose() {
	d.mu.Lock()
	d.quit = true
	if d.closing != nil {
		d.closing.Stop()
	}
	d.mu.Unlock()
	d.app.Quit()
}

func (d *Desktop) CloseFailed(message string) {
	d.cancelClose()
	d.app.Dialog.Error().SetTitle("Unable to save budget").
		SetMessage(message).AttachToWindow(d.window).Show()
}

func (d *Desktop) LogError(message string) {
	log.Printf("WebView: %s", message)
}

// quitting reports whether a quit was confirmed (or requested before the
// app was ready), so closing the window should end the app.
func (d *Desktop) quitting() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.quit
}

func (d *Desktop) shouldQuit() bool {
	d.mu.Lock()
	canQuit := !d.ready || d.quit
	if !canQuit && d.closing == nil {
		d.closing = time.AfterFunc(10*time.Second, d.offerForceQuit)
	}
	d.mu.Unlock()
	if !canQuit {
		d.app.Event.Emit("desktop:closing")
	}
	return canQuit
}

func (d *Desktop) cancelClose() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing != nil {
		d.closing.Stop()
		d.closing = nil
	}
}

func (d *Desktop) offerForceQuit() {
	d.mu.Lock()
	quit := d.quit
	d.mu.Unlock()
	if quit {
		return
	}
	dialog := d.app.Dialog.Question().SetTitle("Budget is still saving").
		SetMessage("The app has not finished saving. Keep waiting, or quit and risk losing recent changes?").
		AttachToWindow(d.window)
	dialog.AddButton("Keep waiting").SetAsDefault().SetAsCancel().OnClick(d.cancelClose)
	dialog.AddButton("Quit without saving").OnClick(d.ConfirmClose)
	dialog.Show()
}

func (d *Desktop) shutdown() {
	d.cancelClose()
	if d.windowState != nil {
		d.windowState.save()
	}
	d.server.Stop()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.oauth != nil {
		d.oauth.close()
	}
}
