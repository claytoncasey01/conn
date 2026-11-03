package theme

import (
	"math"
	"regexp"
	"slices"

	"github.com/claytoncasey01/conn/internal/js"
)

// The look can lay color under text: a card's wash, a glass card over the page, the backdrop's
// pattern and orbs. tx and mu are measured over the worst of each: the strongest wash any card
// paints (WashMax of its state color) and the page where the pattern's line and the strongest
// orb meet (an orb is at most OrbMax of its hue).
const (
	WashMax = 0.16
	OrbMax  = 0.22

	glassAlpha  = 0.55
	bloomShare  = 0.2
	fixedPlaces = 2

	// The least contrast text, muted text and the strong line need.
	textContrastMin  = 4.5
	mutedContrastMin = 3
	lineContrastMin  = 1.5

	// How strong an orb is, and how strongly the backdrop's pattern draws its line.
	orbSoft      = 0.5
	orbBright    = 1
	patternLines = 0.09
	patternDots  = 0.3
)

// The slots the gate measures by name.
const (
	slotBg    = "bg"
	slotPanel = "panel"
	slotRaise = "raise"
	slotAc    = "ac"
	slotTx    = "tx"
	slotMu    = "mu"
	slotEdge  = "edge"
	slotLine2 = "line2"
)

var varReference = regexp.MustCompile(`var\(--([a-z-]+)\)`)

// minimum is the least contrast each measured token needs.
func minimum(name string) float64 {
	switch name {
	case slotLine2:
		return lineContrastMin
	case slotMu:
		return mutedContrastMin
	}

	return textContrastMin
}

// pair is a token measured on a surface.
type pair struct{ fg, on string }

func pairs() []pair {
	return []pair{
		{fg: slotTx, on: slotBg},
		{fg: slotTx, on: slotPanel},
		{fg: slotTx, on: slotRaise},
		{fg: "onac", on: slotAc},
		{fg: slotLine2, on: slotPanel},
		{fg: slotMu, on: slotBg},
		{fg: slotMu, on: slotPanel},
	}
}

func washHues() []string {
	return []string{
		"warn", "bad", "ok", slotAc, slotEdge, "role-lead", "role-advisor", "role-agent", slotTx,
	}
}

func orbHues(tint string) []string {
	switch tint {
	case "accent":
		return []string{slotAc}
	case slotEdge:
		return []string{slotEdge}
	}

	return []string{slotEdge, "role-advisor", "role-lead", slotAc, "warn"}
}

func orbAmount(orbs string) float64 {
	switch orbs {
	case "soft":
		return orbSoft
	case "bright":
		return orbBright
	}

	return 0
}

func patternAlpha(backdrop string) float64 {
	switch backdrop {
	case "grid", "lines":
		return patternLines
	case "dots":
		return patternDots
	}

	return 0
}

func withAlpha(color RGBA, share float64) RGBA {
	color.A *= share

	return color
}

// CheckTheme parses and validates theme.json text and holds it to the contrast gate against
// fallback (Default), which fills the slots it leaves out. The errors are empty when it passed.
func CheckTheme(text string, context Context, fallback *Manifest) (Manifest, []string) {
	manifest, errors := ParseManifest(text, context)
	if len(errors) > 0 {
		return manifest, errors
	}

	errors = CheckContrast(&manifest, fallback)
	if len(errors) > 0 {
		return Manifest{}, errors
	}

	return manifest, nil
}

// CheckContrast is the gate: one message per failing pair, naming the tokens. No waiver.
func CheckContrast(manifest, fallback *Manifest) []string {
	variants := make([]*Variant, 0, 1+len(manifest.Accent.Variants))

	variants = append(variants, nil)
	for index := range manifest.Accent.Variants {
		variants = append(variants, &manifest.Accent.Variants[index])
	}

	out := make([]string, 0, len(manifest.Modes))

	for _, mode := range manifest.Modes {
		base := fallbackTokens(fallback, mode)
		own, _ := manifest.ModeTokens(mode)

		for _, variant := range variants {
			where := mode
			if variant != nil {
				where += " (" + variant.Name + ")"
			}

			tokens := merged(base, own, variant)
			out = append(out, gate(tokens, mode, where)...)
		}
	}

	return out
}

// fallbackTokens are the fallback's tokens for mode; a missing mode reads its other one, so a
// one-mode Default still backs a theme.
func fallbackTokens(fallback *Manifest, mode string) Tokens {
	for _, name := range []string{mode, ModeDark, ModeLight} {
		tokens, ok := fallback.ModeTokens(name)
		if ok {
			return tokens
		}
	}

	return nil
}

// merged is the fallback's tokens under the theme's own and the variant's, with each derived
// slot the theme leaves unset painted from the theme's own values (edge is its mu), not the
// fallback's literal.
func merged(base, own Tokens, variant *Variant) map[string]string {
	tokens := map[string]string{}

	for _, layer := range []Tokens{base, own, variantTokens(variant)} {
		for _, token := range layer {
			tokens[token.Slot] = token.Value
		}
	}

	for _, derived := range DerivedSlots() {
		if setBy(own, variant, derived.Slot) {
			continue
		}

		value, ok := resolve(derived.Form, tokens)
		if ok {
			tokens[derived.Slot] = value

			continue
		}

		delete(tokens, derived.Slot)
	}

	return tokens
}

func variantTokens(variant *Variant) Tokens {
	if variant == nil {
		return nil
	}

	return variant.Tokens
}

func setBy(own Tokens, variant *Variant, slot string) bool {
	_, ownSets := own.Get(slot)
	if ownSets {
		return true
	}

	_, variantSets := variantTokens(variant).Get(slot)

	return variantSets
}

// resolve fills each var(--name) in form from tokens, and is false when one is unset.
func resolve(form string, tokens map[string]string) (string, bool) {
	for _, match := range varReference.FindAllStringSubmatch(form, -1) {
		_, ok := tokens[match[1]]
		if !ok {
			return "", false
		}
	}

	return varReference.ReplaceAllStringFunc(form, func(reference string) string {
		name := varReference.FindStringSubmatch(reference)[1]

		return tokens[name]
	}), true
}

// surfaces are the opaque colors text sits on: bg over the mode's floor, panel on bg, raise on
// panel, ac on bg. Each is false when it cannot be painted.
type surfaces struct {
	bg, panel, raise, ac             RGBA
	hasBg, hasPanel, hasRaise, hasAc bool
}

func gate(tokens map[string]string, mode, where string) []string {
	read := func(slot string) (RGBA, bool) {
		value := tokens[slot]
		if value == "" {
			return RGBA{}, false
		}

		return paintOf(value)
	}

	surface := paint(read, mode)

	out := lookContrast(tokens, where, read, surface)

	for _, measured := range pairs() {
		out = append(out, pairProblem(measured, where, read, surface)...)
	}

	return out
}

func paint(read func(string) (RGBA, bool), mode string) surfaces {
	floor := RGBA{R: 0, G: 0, B: 0, A: 1}
	if mode == ModeLight {
		floor = RGBA{R: channelMax, G: channelMax, B: channelMax, A: 1}
	}

	var surface surfaces

	page, painted := read(slotBg)
	if !painted {
		return surface
	}

	surface.bg, surface.hasBg = Composite(page, floor), true

	accent, painted := read(slotAc)
	if painted {
		surface.ac, surface.hasAc = Composite(accent, surface.bg), true
	}

	panel, painted := read(slotPanel)
	if !painted {
		return surface
	}

	surface.panel, surface.hasPanel = Composite(panel, surface.bg), true

	raise, painted := read(slotRaise)
	if painted {
		surface.raise, surface.hasRaise = Composite(raise, surface.panel), true
	}

	return surface
}

func (s surfaces) named(name string) (RGBA, bool) {
	switch name {
	case slotBg:
		return s.bg, s.hasBg
	case slotPanel:
		return s.panel, s.hasPanel
	case slotRaise:
		return s.raise, s.hasRaise
	}

	return s.ac, s.hasAc
}

func pairProblem(
	measured pair, where string, read func(string) (RGBA, bool), surface surfaces,
) []string {
	front, hasFg := read(measured.fg)
	back, hasOn := surface.named(measured.on)

	if !hasFg {
		return []string{where + ": " + measured.fg + " on " + measured.on +
			" cannot be measured, " + measured.fg + " has no usable value"}
	}

	if !hasOn {
		return []string{where + ": " + measured.fg + " on " + measured.on +
			" cannot be measured, " + measured.on + " has no usable value"}
	}

	ratio := Contrast(front, back)
	if ratio < minimum(measured.fg) {
		return []string{ratioProblem(where, measured.fg, measured.on, ratio)}
	}

	return nil
}

func ratioProblem(where, fg, on string, ratio float64) string {
	return where + ": " + fg + " on " + on + " is " + js.Fixed(ratio, fixedPlaces) +
		":1, needs at least " + js.Number(minimum(fg)) + ":1"
}

// look measures tx and mu over the layers the look lays under text.
type look struct {
	tokens map[string]string
	where  string
	read   func(string) (RGBA, bool)
	text   RGBA
	muted  RGBA
	out    []string
}

// check measures tx and mu over the worst of ons.
func (l *look) check(ons []RGBA, what string) {
	for _, measured := range []struct {
		name string
		fg   RGBA
	}{{name: slotTx, fg: l.text}, {name: slotMu, fg: l.muted}} {
		ratio := math.Inf(1)
		for _, on := range ons {
			ratio = math.Min(ratio, Contrast(measured.fg, on))
		}

		if ratio < minimum(measured.name) {
			l.out = append(l.out, ratioProblem(l.where, measured.name, what, ratio))
		}
	}
}

// hue reads a layer's hue, saying so when it is set but cannot be painted.
func (l *look) hue(name string) (RGBA, bool) {
	color, ok := l.read(name)
	if ok {
		return color, true
	}

	_, set := l.tokens[name]
	if set {
		l.out = append(l.out, l.where+": the look's layers cannot be measured, "+name+
			" has no usable value")
	}

	return RGBA{}, false
}

// share is a 0 to 1 token as Math.min(1, Math.max(0, Number(t[slot] ?? 0) || 0)) reads it.
func (l *look) share(slot string) float64 {
	value, ok := l.tokens[slot]
	if !ok {
		return 0
	}

	number := js.ToNumber(value)
	if math.IsNaN(number) {
		return 0
	}

	return math.Min(1, math.Max(0, number))
}

func lookContrast(tokens map[string]string, where string, read func(string) (RGBA, bool),
	surface surfaces,
) []string {
	text, hasText := read(slotTx)
	muted, hasMuted := read(slotMu)

	ready := []bool{hasText, hasMuted, surface.hasBg, surface.hasPanel}
	if slices.Contains(ready, false) {
		return nil
	}

	measure := &look{
		tokens: tokens, where: where, read: read, text: text, muted: muted, out: nil,
	}
	pages := measure.pages(surface.bg)

	switch tokens["card-fill"] {
	case "wash":
		ons := append([]RGBA{surface.panel}, pages...)

		for _, name := range washHues() {
			color, ok := measure.hue(name)
			if !ok {
				continue
			}

			washed := make([]RGBA, 0, len(ons))
			for _, on := range ons {
				washed = append(washed, Composite(withAlpha(color, WashMax), on))
			}

			measure.check(washed, "the strongest card wash ("+name+")")
		}
	case "glass":
		measure.glass(pages)
	}

	bloom := measure.share("card-bloom")
	if bloom > 0 {
		edge := Composite(withAlpha(text, bloomShare*bloom), surface.panel)
		measure.check([]RGBA{edge}, "a card's edge under its bloom")
	}

	return measure.out
}

func (l *look) glass(pages []RGBA) {
	panel, painted := l.read(slotPanel)
	if !painted {
		return
	}

	glassed := make([]RGBA, 0, len(pages))
	for _, on := range pages {
		glassed = append(glassed, Composite(withAlpha(panel, glassAlpha), on))
	}

	l.check(glassed, "a glass card over the page")
}

// pages is the page as text and see-through layers find it: plain, and where the pattern's line
// and each orb are strongest.
func (l *look) pages(page RGBA) []RGBA {
	backdrop, hasBackdrop := l.tokens["backdrop"]
	pattern := patternAlpha(backdrop)
	strength := l.share("backdrop-strength")
	orb := orbAmount(l.tokens["orbs"])

	edge, painted := l.read(slotEdge)
	if !painted {
		edge = l.muted
	}

	pages := []RGBA{page}

	if pattern*strength <= 0 {
		if orb <= 0 {
			return pages
		}
	}

	hues := []string{slotEdge}
	if orb > 0 {
		hues = orbHues(l.tokens["orbs-tint"])
	}

	if !hasBackdrop {
		backdrop = shadowNone
	}

	for _, name := range hues {
		color, painted := l.hue(name)
		if !painted {
			continue
		}

		lit := page
		label := backdrop

		if orb > 0 {
			lit = Composite(withAlpha(color, OrbMax*orb), page)
			label += ", " + name + " orb"
		}

		strongest := Composite(withAlpha(edge, pattern*strength), lit)
		pages = append(pages, strongest)
		l.check([]RGBA{strongest}, "the page's backdrop at its strongest ("+label+")")
	}

	return pages
}
