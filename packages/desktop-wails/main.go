package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"

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
		log.Fatal("Build the frontend first: yarn build:desktop:wails")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dataDir := filepath.Join(configDir, "Actual Pocket Wails")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatal(err)
	}

	desktop := &Desktop{}
	app := application.New(application.Options{
		Name:        "Actual Pocket",
		Description: fmt.Sprintf("Actual Pocket %s", version),
		Services:    []application.Service{application.NewService(desktop)},
		Assets: application.AssetOptions{
			Handler:        frontendHandler(assets),
			DisableLogging: true,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
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
	desktop.window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:               "actual-pocket",
		Title:              "Actual Pocket",
		Width:              1280,
		Height:             800,
		MinWidth:           800,
		MinHeight:          600,
		URL:                "/",
		UseApplicationMenu: true,
		DevToolsEnabled:    os.Getenv("ACTUAL_WAILS_DEBUG") == "1",
	})
	desktop.window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !desktop.shouldQuit() {
			event.Cancel()
		}
	})
	menu := app.Menu.New()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	menu.AddRole(application.FileMenu)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)
	if os.Getenv("ACTUAL_WAILS_DEBUG") == "1" {
		menu.AddSubmenu("Develop").Add("Web Inspector").OnClick(func(_ *application.Context) {
			desktop.window.OpenDevTools()
		})
	}
	app.Menu.Set(menu)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
