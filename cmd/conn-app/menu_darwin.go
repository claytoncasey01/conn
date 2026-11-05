package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/claytoncasey01/conn/internal/update"
)

// menuBar is the menu bar and the one item in it that changes while the app runs.
type menuBar struct {
	menu *application.Menu
	// updateItem is Check for Updates…, relabelled when a release is found and hidden when the
	// check is off.
	updateItem *application.MenuItem
}

// appMenu is the menu bar: the app menu (About, Check for Updates…, Services, Hide, Hide Others,
// Show All, Quit), Edit (Undo, Redo, Cut, Copy, Paste, Select All), so the keys for them work in
// the page, and Window (Minimize, Zoom). The app menu is built from its roles, as Wails builds
// its own, because Wails cannot add an item to that one. Check for Updates… sits in About's
// group, with no separator of its own, because a separator cannot be hidden with it. Choosing
// it sends on asks without waiting: a full buffer means a check is already asked for.
func appMenu(running *application.App, asks chan<- struct{}) menuBar {
	menu := running.Menu.New()

	conn := menu.AddSubmenu(appName)
	conn.AddRole(application.About)

	updateItem := conn.Add(update.MenuLabel).OnClick(func(*application.Context) {
		select {
		case asks <- struct{}{}:
		default:
		}
	})

	conn.AddSeparator()
	conn.AddRole(application.ServicesMenu)
	conn.AddSeparator()
	conn.AddRole(application.Hide)
	conn.AddRole(application.HideOthers)
	conn.AddRole(application.UnHide)
	conn.AddSeparator()
	conn.AddRole(application.Quit)

	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)

	return menuBar{menu: menu, updateItem: updateItem}
}
