package app

import (
	"testing"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/theme"
	"github.com/claytoncasey01/conn/internal/themes"
)

// TestTheBackgroundIsTheThemesInEachMode: Default's dark background is dark and its light one
// light, and a translucent background is laid over black, as the page's terminal draws it.
func TestTheBackgroundIsTheThemesInEachMode(t *testing.T) {
	t.Parallel()

	fallback := themes.BaseOf(themes.Embedded(conn.Themes())).Fallback

	dark := themeBackground(fallback, "dark")
	if brightness(dark) > 64 {
		t.Errorf("Default's dark background %v is not dark", dark)
	}

	if defaultBackground() != dark {
		t.Errorf("the background before a page says is %v, not Default's dark %v",
			defaultBackground(), dark)
	}

	light := themeBackground(fallback, "light")
	if brightness(light) < 192 {
		t.Errorf("Default's light background %v is not light", light)
	}

	translucent := theme.Manifest{Tokens: []theme.ModeTokens{
		{Mode: "light", Tokens: theme.Tokens{{Slot: "bg", Value: "rgba(255, 255, 255, 0.5)"}}},
	}}

	got := themeBackground(translucent, "light")
	if got != (pty.RGB{Red: 128, Green: 128, Blue: 128}) {
		t.Errorf("half white over black is %v", got)
	}

	if themeBackground(translucent, "dark") != (pty.RGB{Red: 0, Green: 0, Blue: 0}) {
		t.Error("a mode the theme does not have is not black")
	}
}

func brightness(colour pty.RGB) int {
	return (int(colour.Red) + int(colour.Green) + int(colour.Blue)) / 3
}
