package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The Go/PocketBase sync server (packages/sync-server-go), shipped next to
// the app's executable.
const syncServerBinary = "actual-server"

// How long a start may take before it is reported as failed. PocketBase runs
// its migrations on first start.
const syncServerStartTimeout = 30 * time.Second

// SyncServer runs the bundled sync server as a child process, listening on
// the loopback interface only, as the Electron app's built-in server did.
type SyncServer struct {
	dataDir string
	mu      sync.Mutex
	cmd     *exec.Cmd
	port    int
	done    chan struct{}
}

type SyncServerStatus struct {
	// Available is false when this build ships without the server
	Available bool `json:"available"`
	Running   bool `json:"running"`
	Port      int  `json:"port"`
	AutoStart bool `json:"autoStart"`
}

func newSyncServer(dataDir string) *SyncServer {
	return &SyncServer{dataDir: filepath.Join(dataDir, "sync-server")}
}

func syncServerPath() (string, error) {
	name := syncServerBinary
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return sidecarPath(name)
}

func syncServerAvailable() bool {
	path, err := syncServerPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (s *SyncServer) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

// Start launches the server on port and waits until it accepts requests.
func (s *SyncServer) Start(port int) error {
	if port <= 0 || port > 65535 {
		return errors.New("ports must be within range 1 - 65535")
	}
	s.Stop()
	binary, err := syncServerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return fmt.Errorf("create the server's data folder: %w", err)
	}
	cmd := exec.Command(binary)
	cmd.Dir = s.dataDir
	cmd.Env = append(os.Environ(),
		"NODE_ENV=production",
		fmt.Sprintf("ACTUAL_PORT=%d", port),
		"ACTUAL_HOSTNAME=127.0.0.1",
		"ACTUAL_DATA_DIR="+s.dataDir,
	)
	// Serve the browser app too, so other devices can use this computer's
	// server through a reverse proxy, as with the Electron app
	if webRoot, err := resourcePath("web"); err == nil {
		if _, err := os.Stat(filepath.Join(webRoot, "index.html")); err == nil {
			cmd.Env = append(cmd.Env, "ACTUAL_WEB_ROOT="+webRoot)
		}
	}
	hideConsoleWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = logWriter("sync server")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the sync server: %w", err)
	}

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		signalled := false
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("sync server: %s", line)
			// The server prints this once it is listening (main.go)
			if !signalled && strings.HasPrefix(line, "Listening on ") {
				signalled = true
				close(started)
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		log.Printf("sync server stopped: %v", err)
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
		}
		s.mu.Unlock()
		close(done)
	}()

	s.mu.Lock()
	s.cmd, s.port, s.done = cmd, port, done
	s.mu.Unlock()

	select {
	case <-started:
		return nil
	case <-done:
		return errors.New("the sync server stopped while starting; is the port already in use?")
	case <-time.After(syncServerStartTimeout):
		s.Stop()
		return fmt.Errorf("the sync server did not start within %s", syncServerStartTimeout)
	}
}

// Stop ends the server, asking it to shut down first.
func (s *SyncServer) Stop() {
	s.mu.Lock()
	cmd, done := s.cmd, s.done
	s.cmd = nil
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := interruptProcess(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

type logWriter string

func (w logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		log.Printf("%s: %s", string(w), line)
	}
	return len(p), nil
}

var _ io.Writer = logWriter("")

// StartSyncServer starts the built-in server on port and starts it again on
// every launch, until it is stopped.
func (d *Desktop) StartSyncServer(port int) error {
	if !syncServerAvailable() {
		return errors.New("this build does not include the sync server")
	}
	if err := d.server.Start(port); err != nil {
		return err
	}
	return d.settings.Update(func(s *Settings) {
		s.SyncServer.Port = port
		s.SyncServer.AutoStart = true
	})
}

// StopSyncServer stops the built-in server and keeps it off on launch.
func (d *Desktop) StopSyncServer() error {
	d.server.Stop()
	return d.settings.Update(func(s *Settings) { s.SyncServer.AutoStart = false })
}

func (d *Desktop) GetSyncServerStatus() SyncServerStatus {
	settings := d.settings.Get().SyncServer
	return SyncServerStatus{
		Available: syncServerAvailable(),
		Running:   d.server.running(),
		Port:      settings.Port,
		AutoStart: settings.AutoStart,
	}
}

// autoStartSyncServer starts the server at launch when the user left it on.
// The window waits for it, as the Electron app did, so the app does not find
// its server unreachable on the first request.
func (d *Desktop) autoStartSyncServer() {
	settings := d.settings.Get().SyncServer
	if !settings.AutoStart || !syncServerAvailable() {
		return
	}
	if err := d.server.Start(settings.Port); err != nil {
		log.Printf("sync server: could not start at launch: %v", err)
	}
}
