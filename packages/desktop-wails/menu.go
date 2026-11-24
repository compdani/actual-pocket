package main

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// buildMenu mirrors the Electron app's menu: app-level Undo/Redo (budget
// changes, not just text), View with reload, zoom, full screen and the
// developer tools, and the standard Window menu.
func buildMenu(d *Desktop) *application.Menu {
	menu := d.app.Menu.New()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	file := menu.AddSubmenu("File")
	file.Add("Show Budget Folder").OnClick(func(*application.Context) {
		_ = d.RevealDocumentDir()
	})
	if runtime.GOOS != "darwin" {
		file.AddSeparator()
		file.Add("Exit").OnClick(func(*application.Context) { d.app.Quit() })
	}

	edit := menu.AddSubmenu("Edit")
	edit.Add("Undo").SetAccelerator("CmdOrCtrl+Z").OnClick(func(*application.Context) {
		d.window.ExecJS("window.__actionsForMenu && window.__actionsForMenu.undo()")
	})
	edit.Add("Redo").SetAccelerator("Shift+CmdOrCtrl+Z").OnClick(func(*application.Context) {
		d.window.ExecJS("window.__actionsForMenu && window.__actionsForMenu.redo()")
	})
	edit.AddSeparator()
	edit.AddRole(application.Cut)
	edit.AddRole(application.Copy)
	edit.AddRole(application.Paste)
	if runtime.GOOS == "darwin" {
		edit.AddRole(application.PasteAndMatchStyle)
	}
	edit.AddRole(application.Delete)
	edit.AddRole(application.SelectAll)

	view := menu.AddSubmenu("View")
	view.Add("Reload").SetAccelerator("CmdOrCtrl+R").OnClick(func(*application.Context) {
		// Through the app, which saves pending changes before reloading
		d.window.ExecJS("window.Actual && window.Actual.reload ? window.Actual.reload() : window.location.reload()")
	})
	view.Add("Toggle Developer Tools").SetAccelerator(devToolsAccelerator()).OnClick(func(*application.Context) {
		d.window.OpenDevTools()
	})
	view.AddSeparator()
	view.Add("Actual Size").SetAccelerator("CmdOrCtrl+0").OnClick(func(*application.Context) { d.window.ZoomReset() })
	view.Add("Zoom In").SetAccelerator("CmdOrCtrl+Plus").OnClick(func(*application.Context) { d.window.ZoomIn() })
	view.Add("Zoom Out").SetAccelerator("CmdOrCtrl+-").OnClick(func(*application.Context) { d.window.ZoomOut() })
	view.AddSeparator()
	view.Add("Toggle Full Screen").SetAccelerator(fullscreenAccelerator()).OnClick(func(*application.Context) {
		d.window.ToggleFullscreen()
	})

	menu.AddRole(application.WindowMenu)
	return menu
}

func devToolsAccelerator() string {
	if runtime.GOOS == "darwin" {
		return "Alt+Cmd+I"
	}
	return "Ctrl+Shift+I"
}

func fullscreenAccelerator() string {
	if runtime.GOOS == "darwin" {
		return "Ctrl+Cmd+F"
	}
	return "F11"
}
