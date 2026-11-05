package main

import "github.com/wailsapp/wails/v3/pkg/application"

// appMenu is the menu bar: the app menu (About, Hide, Hide Others, Show All, Quit), Edit (Undo,
// Redo, Cut, Copy, Paste, Select All), so the keys for them work in the page, and Window
// (Minimize, Zoom).
func appMenu(running *application.App) *application.Menu {
	menu := running.Menu.New()
	menu.AddRole(application.AppMenu)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)

	return menu
}
