// Package theme is the token contract and the judgment of a theme's manifest: which slots a
// theme may fill, what a value for each may be, and the contrast gate every theme passes. It is
// pure (no files, no network) and gives the same answers as src/lib/theme/validate.ts, which the
// page runs: testdata/theme-cases.json holds both to the same results.
package theme

// Kind is what a slot's value is.
type Kind int

// The kinds of slot.
const (
	KindColor Kind = iota
	KindLength
	KindNumber
	KindEnum
	KindShadow
	KindFont
)

// The size limits on a theme's files and on how many themes of the user's are listed.
const (
	MaxManifestBytes = 65536
	MaxCSSBytes      = 262144
	MaxUserThemes    = 100
)

// Slot is one token a theme may set and what a value for it may be.
type Slot struct {
	Name string
	Kind Kind
	// Bounded is whether Min and Max apply (lengths and numbers).
	Bounded  bool
	Min, Max float64
	// Unit is a length's unit, px or em.
	Unit string
	// PxOnly is a length that bounds a clip: a rem or em would grow with the reader's font past
	// its max.
	PxOnly bool
	// Values are an enum's choices; for a font slot, the generic families it allows besides End.
	Values []string
	// End is a font slot's required last family.
	End string
}

// Derived is a slot code paints from other tokens when a theme leaves it out: ac-soft and ac-line
// follow ac, edge is mu, and so on. Form is CSS with var(--name) references.
type Derived struct {
	Slot string
	Form string
}

func color(names ...string) []Slot {
	slots := make([]Slot, 0, len(names))
	for _, name := range names {
		slots = append(slots, Slot{Name: name, Kind: KindColor})
	}

	return slots
}

func enum(name string, values ...string) Slot {
	return Slot{Name: name, Kind: KindEnum, Values: values}
}

// span is a bounded slot's least and greatest value.
type span struct{ lowest, highest float64 }

func length(name, unit string, bounds span) Slot {
	return Slot{
		Name: name, Kind: KindLength, Unit: unit, Bounded: true, Min: bounds.lowest,
		Max: bounds.highest,
	}
}

func pxLength(name string, bounds span) Slot {
	slot := length(name, "px", bounds)
	slot.PxOnly = true

	return slot
}

func number(name string, bounds span) Slot {
	return Slot{
		Name: name, Kind: KindNumber, Bounded: true, Min: bounds.lowest, Max: bounds.highest,
	}
}

// unit is the 0 to 1 range of the look's strengths.
func unit() span { return span{lowest: 0, highest: 1} }

// Slots is every slot, in the order tokens.ts lists them.
func Slots() []Slot {
	slots := color("bg", "panel", "raise", "sunk", "hover", "line", "line2", "tx", "bright", "mu",
		"onac", "ac", "ac-soft", "ac-line")
	slots = append(slots, color("ok", "warn", "bad", "idle", "info")...)
	slots = append(slots, color("k-bug", "k-epic", "k-task", "k-story", "k-subtask", "teammate")...)
	slots = append(slots, color("role-lead", "role-agent", "role-advisor")...)
	slots = append(slots, color("edge", "stall")...)
	slots = append(slots, color("focus", "meter-track", "meter-lit", "meter-lead")...)
	slots = append(slots,
		Slot{
			Name: "font-ui", Kind: KindFont, End: "system-ui",
			Values: []string{"system-ui", "ui-sans-serif", "sans-serif"},
		},
		Slot{
			Name: "font-mono", Kind: KindFont, End: "ui-monospace",
			Values: []string{"ui-monospace", "monospace"},
		},
		enum("case", "none", "lowercase", "uppercase"),
		length("tracking", "em", span{-0.05, 0.3}),
		length("radius", "px", span{0, 24}),
		enum("led", "circle", "diamond", "square"),
		enum("border-style", "solid", "dashed", "dotted"),
		Slot{Name: "lift", Kind: KindShadow},
		number("glow", unit()),
		length("blur", "px", span{0, 16}),
		enum("motion", "calm", "power"),
	)

	return append(slots, lookSlots()...)
}

// lookSlotCount is how many slots lookSlots lists.
const lookSlotCount = 50

// lookSlots are the look: what the page is drawn on, how a card is shaped and marked, the glyphs
// of the chrome and the Overview agent tree. Keywords and bounded numbers only.
func lookSlots() []Slot {
	slots := make([]Slot, 0, lookSlotCount)
	slots = append(slots,
		enum("font-body", "ui", "mono"),
		enum("chrome", "styled", "authored"),
		number("text-glow", unit()),
		enum("cursor", "bar", "block"),
		enum("hover-lift", "off", "on"),
		enum("meter", "smooth", "segmented"),
		enum("backdrop", "none", "grid", "lines", "dots"),
		length("backdrop-size", "px", span{12, 96}),
		number("backdrop-strength", unit()),
		enum("orbs", "none", "soft", "bright"),
		enum("orbs-tint", "roles", "accent", "edge"),
		number("backdrop-scan", unit()),
		enum("header", "flat", "frost", "rule", "glint"),
		enum("stage", "plain", "dots", "hud"),
		number("stage-backing", unit()),
		enum("card-fill", "plain", "wash", "glass"),
		number("glass-tint", span{0, 0.6}),
		pxLength("glass-blur", span{0, 40}),
		number("glass-saturate", span{1, 2.4}),
		number("glass-edge", unit()),
		number("card-sheen", unit()),
		number("wallpaper-strength", unit()),
	)
	slots = append(slots, color(wallpaperSlots()...)...)
	slots = append(slots,
		enum("card-corner", "round", "square", "chamfer", "notch"),
		pxLength("card-cut", span{4, 20}),
		enum("card-frame", "none", "hairline", "bold", "double", "rule"),
		enum("card-marks", "none", "brackets", "ticks", "gutter"),
		enum("card-stripe", "off", "on"),
		number("card-rim", unit()),
		number("card-bloom", unit()),
		number("card-glow", unit()),
		enum("card-shadow", "none", "lift", "halo"),
		enum("glyph-arrow", "triangle", "chevron", "arrow", "none"),
		enum("glyph-bullet", "triangle", "dot", "dash", "prompt", "none"),
		enum("glyph-sep", "slash", "dot", "bar", "dash"),
		enum("glyph-brand", "conn", "conn-tube", "conn-pixel", "conn-phosphor", "blocks", "prompt",
			"diamond", "none"),
	)

	return append(slots,
		enum("wire-style", "line", "cable", "rail", "dash"),
		pxLength("wire-width", span{1, 6}),
		enum("wire-head", "arrow", "plug", "dot", "bar", "none"),
		enum("wire-crawl", "off", "on"),
		enum("wire-flow", "packets", "blocks", "pulse", "glyphs", "none"),
		enum("wire-glyphs", "binary", "hex", "kana", "ascii"),
		enum("wire-signal", "roles", "lead", "bright", "tx"),
		number("wire-speed", span{0.25, 4}),
		enum("tree-orbits", "off", "on"),
		enum("node-beat", "off", "on"),
	)
}

// DerivedSlots are the slots code paints from a theme's own tokens when it leaves them out, in
// the order they are applied.
func DerivedSlots() []Derived {
	return []Derived{
		{Slot: "ac-soft", Form: "color-mix(in oklch, var(--ac) 14%, transparent)"},
		{Slot: "ac-line", Form: "color-mix(in oklch, var(--ac) 40%, transparent)"},
		{Slot: "edge", Form: "var(--mu)"},
		{Slot: "stall", Form: "color-mix(in oklch, var(--warn) 50%, var(--bad))"},
		{Slot: "card-glow", Form: "var(--glow)"},
		{Slot: "focus", Form: "var(--ac)"},
		{Slot: "meter-track", Form: "var(--line)"},
		{Slot: "meter-lit", Form: "var(--ac)"},
		{Slot: "meter-lead", Form: "var(--role-lead)"},
	}
}

// AllowedFonts are the font families a theme may name.
func AllowedFonts() []string {
	return []string{
		"Chakra Petch", "JetBrains Mono", "IBM Plex Sans", "IBM Plex Mono", "Share Tech Mono",
	}
}

// KnownEffects are the effects a theme may list.
func KnownEffects() []string {
	return []string{"rain", "scanlines", "vignette", "flicker", "glow", "roll", "grain"}
}

// The wallpaper: its color slots, its fields from the top layer down (tokens.ts WALLPAPER), each
// fading to nothing at wallpaperFade of its radii, and the samples the gate takes of it.
const (
	wallpaperFade  = 0.72
	wallpaperSteps = 24
)

// field is one of the wallpaper's fields: its center and radii, as shares of the window.
type field struct{ x, y, rx, ry float64 }

func wallpaperSlots() []string {
	return []string{"wallpaper-1", "wallpaper-2", "wallpaper-3", "wallpaper-4", "wallpaper-5"}
}

func wallpaperFields() []field {
	// Each row is a field's center (x, y) and radii (rx, ry), as shares of the window.
	return []field{
		{0.12, 0.18, 0.38, 0.46},
		{0.86, 0.12, 0.42, 0.5},
		{0.7, 0.92, 0.46, 0.52},
		{0.22, 0.96, 0.34, 0.4},
		{0.5, 0.48, 0.3, 0.34},
	}
}

// wallpaperPoints is a grid of wallpaperSteps by wallpaperSteps over the window, row by row,
// then each field's center, where it is strongest.
func wallpaperPoints() [][2]float64 {
	side := wallpaperSteps + 1
	fields := wallpaperFields()
	points := make([][2]float64, 0, side*side+len(fields))

	for index := range side * side {
		points = append(points, [2]float64{
			float64(index%side) / wallpaperSteps, float64(index/side) / wallpaperSteps,
		})
	}

	for _, center := range fields {
		points = append(points, [2]float64{center.x, center.y})
	}

	return points
}
