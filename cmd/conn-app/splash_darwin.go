package main

import (
	_ "embed" // for the splash page below
	"html"
	"strings"
)

// splashTextSlot is where the splash page takes its text.
const splashTextSlot = "{{TEXT}}"

// The page the window shows until a Conn answers. It holds splashTextSlot once.
//
//go:embed splash.html
var splashTemplate string

// splashPage is the splash with text in it, escaped: the text can carry a shell's error output.
func splashPage(text string) string {
	if strings.Count(splashTemplate, splashTextSlot) != 1 {
		panic("splash.html must hold " + splashTextSlot + " exactly once")
	}

	return strings.Replace(splashTemplate, splashTextSlot, html.EscapeString(text), 1)
}
