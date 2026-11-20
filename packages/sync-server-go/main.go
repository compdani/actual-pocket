// Command actual-server is the Actual Budget sync server, a Go + PocketBase
// replacement for packages/sync-server.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/migratecmd"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/routes"
	"github.com/compdani/actual-pocket/packages/sync-server-go/migrations"
)

// Version is injected at build time:
//
//	-ldflags "-X main.Version=$(jq -r .version ../sync-server/package.json)"
var Version = "0.0.0-dev"

func main() {
	if err := run(os.Args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	for _, warning := range cfg.Warnings {
		fmt.Fprintln(stderr, "WARNING:", warning)
	}
	logStartup(stdout, cfg)

	// The blob field's max size is fixed when its migration runs.
	migrations.SetSyncEncryptedFileSizeLimitMB(cfg.Upload.SyncEncryptedFileSizeLimitMB)

	// Group databases live inside pb_data so PocketBase backups cover them.
	if err := os.MkdirAll(filepath.Join(cfg.PBDataDir, "actual_groups"), 0o700); err != nil {
		return fmt.Errorf("create group database directory: %w", err)
	}

	os.Args = injectDefaultCommand(args, cfg)

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  cfg.PBDataDir,
		HideStartBanner: true,
	})

	// Collections are defined only in hand-written Go migrations.
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{Automigrate: false})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		// Electron and Docker health checks wait for this exact line.
		se.StartBannerFunc = func(_ string) {
			fmt.Fprintf(stdout, "Listening on %s:%d...\n", cfg.Hostname, cfg.Port)
		}

		routes.Register(se, routes.Options{
			App:     app,
			Config:  cfg,
			Version: Version,
			Stdout:  stdout,
		})

		return se.Next()
	})

	return app.Start()
}

// logStartup prints the resolved configuration, including absolute data paths,
// so container and desktop logs show where state actually lives.
func logStartup(out io.Writer, cfg *config.Config) {
	if cfg.ConfigPath != "" {
		fmt.Fprintf(out, "Loaded config from %s\n", cfg.ConfigPath)
	}
	fmt.Fprintf(out, "Data directory: %s\n", cfg.DataDir)
	fmt.Fprintf(out, "Server files: %s\n", cfg.ServerFiles)
	fmt.Fprintf(out, "PocketBase data directory: %s\n", cfg.PBDataDir)
	fmt.Fprintf(out, "Web root: %s\n", cfg.WebRoot)
	fmt.Fprintf(out, "Mode: %s (%s)\n", cfg.Mode, cfg.Env)

	if cfg.IsDev() {
		fmt.Fprintln(out, "Running in development mode - Proxying frontend routes to React Dev Server")
		return
	}

	fmt.Fprintln(out, "Running in production mode - Serving static React app")
	if _, err := os.Stat(cfg.WebRoot); err != nil {
		fmt.Fprintf(out, "WARNING: web root %q is not readable; the web app will not be served: %v\n", cfg.WebRoot, err)
	}
}

// injectDefaultCommand makes `./actual-server` behave like `./actual-server
// serve --http=<host>:<port>`, the way the Node server started with no
// arguments.
func injectDefaultCommand(args []string, cfg *config.Config) []string {
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		return args
	}

	addr := net.JoinHostPort(cfg.Hostname, strconv.Itoa(cfg.Port))

	injected := make([]string, 0, len(args)+2)
	injected = append(injected, args[0], "serve", "--http="+addr)
	injected = append(injected, args[1:]...)

	return injected
}
