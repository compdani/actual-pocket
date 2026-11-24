package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend
var frontend embed.FS

var version = "0.0.0-dev"

func main() {
	assets, err := fs.Sub(frontend, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		log.Fatal("Build the frontend first: pnpm run build:desktop")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dataDir := filepath.Join(configDir, "Actual Pocket Wails")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatal(err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	settings, err := loadSettings(dataDir)
	if err != nil {
		log.Fatal(err)
	}

	desktop := &Desktop{
		settings:           settings,
		server:             newSyncServer(dataDir),
		defaultDocumentDir: defaultDocumentDir(configDir, homeDir),
	}
	budgetFiles := &budgetFilesAPI{
		documentDir: desktop.documentDir,
		debug:       os.Getenv("ACTUAL_WAILS_DEBUG") == "1",
	}
	app := application.New(application.Options{
		Name:        "Actual Pocket",
		Description: fmt.Sprintf("Actual Pocket %s", version),
		Services:    []application.Service{application.NewService(desktop)},
		Assets: application.AssetOptions{
			Handler:        frontendHandler(assets, budgetFiles),
			DisableLogging: true,
		},
		Mac: application.MacOptions{
			// The window hides on close and returns from the Dock
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath: filepath.Join(dataDir, "WebView2"),
		},
		Linux: application.LinuxOptions{
			ApplicationID: "com.actualpocket.desktop",
			ProgramName:   "actual-pocket",
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.actualpocket.desktop",
			OnSecondInstanceLaunch: func(_ application.SecondInstanceData) {
				if desktop.window != nil {
					desktop.window.Show()
					desktop.window.Focus()
				}
			},
		},
		ShouldQuit: desktop.shouldQuit,
		OnShutdown: desktop.shutdown,
	})
	desktop.app = app
	// The local server must be up before the app asks it for anything
	desktop.autoStartSyncServer()
	desktop.window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:               "actual-pocket",
		Title:              "Actual Pocket",
		Width:              1280,
		Height:             800,
		MinWidth:           800,
		MinHeight:          600,
		URL:                "/",
		UseApplicationMenu: true,
		// Available from View > Toggle Developer Tools, as in the Electron app
		DevToolsEnabled: true,
	})
	configureWindowBehaviour(desktop)
	desktop.windowState = &windowState{desktop: desktop}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		desktop.windowState.restore()
		desktop.windowState.track()
	})
	app.Menu.Set(buildMenu(desktop))
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
