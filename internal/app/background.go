package app

import (
	"math"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/theme"
	"github.com/claytoncasey01/conn/internal/themes"
)

// The background a hosted program is told its terminal has (OSC 11), until a page says the one
// its theme draws the terminal on: Default's, in dark mode, as the page draws it before it knows
// the person's choice. The page then tells the server its own, light themes included
// (/api/term/background), and the host answers with that.

// modeDark is the theme mode the background is read in before a page says.
const modeDark = "dark"

// defaultBackground is the built-in Default theme's background in dark mode.
func defaultBackground() pty.RGB {
	return themeBackground(themes.BaseOf(themes.Embedded(conn.Themes())).Fallback, modeDark)
}

// themeBackground is manifest's background (its bg token) in mode, as the page's terminal draws
// it: laid over black when it is translucent. Black when the theme has no background that reads
// as a colour.
func themeBackground(manifest theme.Manifest, mode string) pty.RGB {
	for _, tokens := range manifest.Tokens {
		if tokens.Mode != mode {
			continue
		}

		value, found := tokens.Tokens.Get("bg")
		if !found {
			continue
		}

		colour, read := theme.ParseColor(value)
		if !read {
			continue
		}

		return pty.RGB{
			Red: channel(colour.R, colour.A), Green: channel(colour.G, colour.A),
			Blue: channel(colour.B, colour.A),
		}
	}

	return pty.RGB{Red: 0, Green: 0, Blue: 0}
}

// channel is one channel of a colour of opacity alpha laid over black, rounded and kept to
// 0..255.
func channel(value, alpha float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(math.MaxUint8, value*alpha))))
}
