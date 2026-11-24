package main

import (
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// windowState remembers the window's position and size between launches,
// as the Electron app did.
type windowState struct {
	desktop *Desktop
	mu      sync.Mutex
	timer   *time.Timer
}

// restore applies the saved bounds when they still fit on a connected
// screen; otherwise the window keeps its default size and position.
func (s *windowState) restore() {
	saved := s.desktop.settings.Get().Window
	if saved == nil || saved.Width < 400 || saved.Height < 300 {
		return
	}
	if !boundsVisible(*saved, s.desktop.app.Screen.GetAll()) {
		return
	}
	window := s.desktop.window
	window.SetBounds(application.Rect{X: saved.X, Y: saved.Y, Width: saved.Width, Height: saved.Height})
	if saved.Maximised {
		window.Maximise()
	}
}

// boundsVisible reports whether a usable part of the window (its title bar
// area) lies on one of the screens.
func boundsVisible(bounds WindowBounds, screens []*application.Screen) bool {
	const margin = 50
	for _, screen := range screens {
		area := screen.WorkArea
		if area.Width == 0 {
			area = screen.Bounds
		}
		if bounds.X+bounds.Width-margin > area.X &&
			bounds.X+margin < area.X+area.Width &&
			bounds.Y >= area.Y-margin &&
			bounds.Y+margin < area.Y+area.Height {
			return true
		}
	}
	return false
}

// track saves the bounds shortly after the window stops moving or resizing.
func (s *windowState) track() {
	save := func(*application.WindowEvent) { s.scheduleSave() }
	s.desktop.window.OnWindowEvent(events.Common.WindowDidMove, save)
	s.desktop.window.OnWindowEvent(events.Common.WindowDidResize, save)
}

func (s *windowState) scheduleSave() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(500*time.Millisecond, s.save)
}

func (s *windowState) save() {
	window := s.desktop.window
	if window.IsMinimised() || window.IsFullscreen() {
		return
	}
	maximised := window.IsMaximised()
	bounds := window.Bounds()
	err := s.desktop.settings.Update(func(settings *Settings) {
		if maximised && settings.Window != nil {
			// Keep the size to return to when un-maximised
			settings.Window.Maximised = true
			return
		}
		settings.Window = &WindowBounds{
			X:         bounds.X,
			Y:         bounds.Y,
			Width:     bounds.Width,
			Height:    bounds.Height,
			Maximised: maximised,
		}
	})
	if err != nil {
		log.Printf("window: could not save its position: %v", err)
	}
}

// configureWindowBehaviour connects focus refreshes and, on macOS, keeps the
// app running when its window is closed, as Mac apps (and the Electron app)
// do: the window hides and comes back from the Dock.
func configureWindowBehaviour(d *Desktop) {
	d.window.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		// Lets the app sync and refresh after the user comes back to it
		d.window.ExecJS("window.__actionsForMenu && window.__actionsForMenu.appFocused()")
	})
	d.window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if runtime.GOOS == "darwin" && !d.quitting() {
			event.Cancel()
			d.window.Hide()
			return
		}
		if !d.shouldQuit() {
			event.Cancel()
		}
	})
	if runtime.GOOS == "darwin" {
		d.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
			d.window.Show()
			d.window.Focus()
		})
	}
}
