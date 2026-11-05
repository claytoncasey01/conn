package main

import (
	_ "embed" // for the splash page below
	"html"
	"strings"
)

// splashTextSlot is where the splash page takes its text.
const splashTextSlot = "{{TEXT}}"

// splashFile is the page the window shows until a Conn answers. It holds splashTextSlot once.
// The embed directive can fill only a package-level variable, so this is the one, and it is read
// only by main, which passes it in as process.splash; no other code touches it.
//
//go:embed splash.html
var splashFile string

// splashPage is template (the splash page) with text in it, escaped: the text can carry a shell's
// error output.
func splashPage(template, text string) string {
	if strings.Count(template, splashTextSlot) != 1 {
		panic("splash.html must hold " + splashTextSlot + " exactly once")
	}

	return strings.Replace(template, splashTextSlot, html.EscapeString(text), 1)
}
