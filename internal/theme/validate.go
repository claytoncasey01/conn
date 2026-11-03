package theme

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Limits on a manifest's text fields, lists and values.
const (
	nameUnitsMax        = 80
	versionUnitsMax     = 40
	authorUnitsMax      = 200
	licenseUnitsMax     = 100
	descriptionUnitsMax = 1000
	valueUnitsMax       = 300
	variantNameUnitsMax = 40
	variantsMax         = 8
	fontsMax            = 8
	effectsMax          = 8
	optionUnitsMax      = 64
	shadowLayersMax     = 4
	shadowLengthsMin    = 2
	shadowLengthsMax    = 4
	shadowPxMax         = 64
	familiesMax         = 8
	modesMax            = 2

	// shownUnitsMax is how much of a name from the file a message quotes.
	shownUnitsMax = 40

	remPx = 16
)

// Newer is the line added after any unknown token: a theme made for a newer Conn names tokens
// this one does not have yet.
const Newer = "a token this conn does not know may come from a newer conn: " +
	"update conn to use this theme"

const (
	unknownTokenText = ": unknown token "
	shadowNone       = "none"
	unitPx           = "px"
	unitEm           = "em"
)

var (
	idForm = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	// forbidden is matched in ASCII case only, as JavaScript's /i without the u flag does.
	forbidden = regexp.MustCompile(`[uU][rR][lL]\(|[;{}]|@[iI][mM][pP][oO][rR][tT]` +
		`|[eE][xX][pP][rR][eE][sS][sS][iI][oO][nN]`)
	lengthForm = regexp.MustCompile(`^([+-]?(?:\d+(?:\.\d+)?|\.\d+))(px|rem|em)?$`)
	familyForm = regexp.MustCompile(`^(?:'([A-Za-z0-9 -]+)'|"([A-Za-z0-9 -]+)"` +
		`|([A-Za-z][A-Za-z0-9-]*(?: [A-Za-z0-9-]+)*))$`)
	// colorMixForm's "." is JavaScript's: anything but a line terminator.
	colorMixForm = regexp.MustCompile(`^color-mix\(in oklch, ([^\n\r\x{2028}\x{2029}]+) ` +
		`(\d+(?:\.\d+)?|\.\d+)%, transparent\)$`)
	optionName = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,31}$`)
)

// Context is what a manifest is judged in: where it comes from and the ids it may not take.
type Context struct {
	Source     Source
	BuiltinIDs map[string]bool
}

// show quotes a name from the file, clipped.
func show(text string) string { return js.QuoteClip(text, shownUnitsMax) }

// validator gathers a manifest's problems, in the order validateManifest finds them.
type validator struct {
	slots  map[string]Slot
	errors []string
}

func newValidator() *validator {
	slots := map[string]Slot{}
	for _, slot := range Slots() {
		slots[slot.Name] = slot
	}

	return &validator{slots: slots, errors: nil}
}

func (v *validator) fail(problem string) { v.errors = append(v.errors, problem) }

// ParseManifest reads theme.json text and validates it. The errors are empty when it passed.
func ParseManifest(text string, context Context) (Manifest, []string) {
	if len(text) > ManifestBytesMax {
		return Manifest{}, []string{
			"theme.json is larger than " + strconv.Itoa(ManifestBytesMax/kibibyte) + " KB",
		}
	}

	raw, err := js.Parse([]byte(text))
	if err != nil {
		return Manifest{}, []string{"theme.json is not valid JSON"}
	}

	return ValidateManifest(raw, context)
}

const kibibyte = 1024

// ValidateManifest judges a parsed manifest. The errors are empty when it passed.
func ValidateManifest(raw any, context Context) (Manifest, []string) {
	object, ok := raw.(*js.Object)
	if !ok {
		return Manifest{}, []string{"theme.json must be a JSON object"}
	}

	check := newValidator()
	manifest := Manifest{
		ID: check.id(object, context),
	}

	manifest.Name = check.text(object, "name", nameUnitsMax, true)
	manifest.Version = check.text(object, "version", versionUnitsMax, true)
	manifest.Author = check.text(object, "author", authorUnitsMax, false)
	manifest.License = check.text(object, "license", licenseUnitsMax, false)
	manifest.Description = check.text(object, "description", descriptionUnitsMax, false)
	manifest.Modes = check.modes(object)
	manifest.Accent = check.accent(object)
	manifest.Fonts = check.fonts(object)
	manifest.Tokens = check.modeTokens(object, manifest.Modes)
	manifest.Effects = check.effects(object)

	unknown := slices.ContainsFunc(check.errors, func(problem string) bool {
		return strings.Contains(problem, unknownTokenText)
	})
	if unknown {
		check.fail(Newer)
	}

	if len(check.errors) > 0 {
		return Manifest{}, check.errors
	}

	return manifest, nil
}

func (v *validator) id(object *js.Object, context Context) string {
	raw, _ := object.Get("id")

	given, _ := raw.(string)
	if !idForm.MatchString(given) {
		v.fail("id must be 1 to 40 characters of a-z, 0-9 and -, not starting with -")

		return given
	}

	if context.Source != SourceBuiltin {
		if context.BuiltinIDs[given] {
			v.fail("id " + show(given) + " belongs to a built-in theme")
		}
	}

	return given
}

func (v *validator) text(object *js.Object, key string, unitsMax int, required bool) string {
	raw, _ := object.Get(key)

	value, ok := raw.(string)
	if !ok {
		v.fail(key + " must be a string")

		return ""
	}

	if required {
		if js.Trim(value) == "" {
			v.fail(key + " must not be empty")
		}
	}

	if js.Len(value) > unitsMax {
		v.fail(key + " is longer than " + strconv.Itoa(unitsMax) + " characters")
	}

	return value
}

func (v *validator) modes(object *js.Object) []string {
	raw, _ := object.Get("modes")

	list, isList := raw.([]any)
	if !isList {
		v.fail(`modes must be ["light"], ["dark"] or both`)

		return []string{}
	}

	modes, valid := modeList(list)
	if !valid {
		v.fail(`modes must be ["light"], ["dark"] or both`)

		return []string{}
	}

	return modes
}

// modeList is a list of one or two different modes, or false.
func modeList(list []any) ([]string, bool) {
	if len(list) == 0 {
		return nil, false
	}

	if len(list) > modesMax {
		return nil, false
	}

	modes := []string{}

	for _, item := range list {
		mode, _ := item.(string)
		if !isMode(mode) {
			return nil, false
		}

		if slices.Contains(modes, mode) {
			return nil, false
		}

		modes = append(modes, mode)
	}

	return modes, true
}

func isMode(mode string) bool {
	return slices.Contains([]string{ModeLight, ModeDark}, mode)
}

func (v *validator) accent(object *js.Object) Accent {
	raw, _ := object.Get("accent")

	kind, _ := raw.(string)
	if kind == AccentFree {
		return Accent{Kind: kind, Variants: nil}
	}

	if kind == AccentFixed {
		return Accent{Kind: kind, Variants: nil}
	}

	list, ok := variantList(raw)
	if !ok {
		v.fail(`accent must be "free", "fixed" or { variants: [1 to 8 of { name, tokens }] }`)

		return Accent{Kind: AccentFixed, Variants: nil}
	}

	variants := []Variant{}

	for index, item := range list {
		variant, ok := v.variant(item, index, variants)
		if ok {
			variants = append(variants, variant)
		}
	}

	return Accent{Kind: "", Variants: variants}
}

// variantList is accent.variants when accent is an object holding 1 to 8 of them.
func variantList(raw any) ([]any, bool) {
	object, isObject := raw.(*js.Object)
	if !isObject {
		return nil, false
	}

	field, _ := object.Get("variants")

	list, isList := field.([]any)
	if !isList {
		return nil, false
	}

	if len(list) < 1 {
		return nil, false
	}

	return list, len(list) <= variantsMax
}

func (v *validator) variant(item any, index int, before []Variant) (Variant, bool) {
	name, ok := variantName(item)
	if !ok {
		v.fail("accent.variants[" + strconv.Itoa(index) + "] needs a name of 1 to 40 characters")

		return Variant{}, false
	}

	// The stored choice names a variant, so a second one of a name could never be picked.
	taken := slices.ContainsFunc(before, func(other Variant) bool { return other.Name == name })
	if taken {
		v.fail("accent.variants: name " + show(name) + " is used twice")

		return Variant{}, false
	}

	object, _ := item.(*js.Object)
	tokens, _ := object.Get("tokens")

	return Variant{
		Name:   name,
		Tokens: v.tokenSet(tokens, "accent variant "+show(name)+" tokens"),
	}, true
}

func variantName(item any) (string, bool) {
	object, isObject := item.(*js.Object)
	if !isObject {
		return "", false
	}

	raw, _ := object.Get("name")

	name, isText := raw.(string)
	if !isText {
		return "", false
	}

	if js.Trim(name) == "" {
		return "", false
	}

	return name, js.Len(name) <= variantNameUnitsMax
}

func (v *validator) fonts(object *js.Object) []string {
	raw, _ := object.Get("fonts")

	list, ok := raw.([]any)
	if !ok {
		v.fail("fonts must be a list of at most 8 names")

		return []string{}
	}

	if len(list) > fontsMax {
		v.fail("fonts must be a list of at most 8 names")

		return []string{}
	}

	fonts := []string{}
	allowed := AllowedFonts()

	for _, item := range list {
		font, ok := item.(string)
		if ok {
			if slices.Contains(allowed, font) {
				fonts = append(fonts, font)

				continue
			}
		}

		v.fail("fonts: " + show(js.String(item)) + " is not allowed (" +
			strings.Join(allowed, ", ") + ")")
	}

	return fonts
}

func (v *validator) modeTokens(object *js.Object, modes []string) []ModeTokens {
	raw, present := object.Get("tokens")
	if !present {
		return []ModeTokens{}
	}

	byMode, ok := raw.(*js.Object)
	if !ok {
		v.fail("tokens must be an object keyed by mode")

		return []ModeTokens{}
	}

	sets := []ModeTokens{}

	for _, mode := range byMode.Keys() {
		if !isMode(mode) {
			v.fail("tokens: " + show(mode) + " is not a mode")

			continue
		}

		if !slices.Contains(modes, mode) {
			v.fail("tokens." + mode + " is given but modes does not list " + mode)

			continue
		}

		value, _ := byMode.Get(mode)
		sets = append(sets, ModeTokens{Mode: mode, Tokens: v.tokenSet(value, "tokens."+mode)})
	}

	return sets
}

// tokenSet reads an object of tokens; an unknown slot or a bad value is reported and left out.
func (v *validator) tokenSet(raw any, path string) Tokens {
	tokens := Tokens{}

	object, isObject := raw.(*js.Object)
	if !isObject {
		v.fail(path + " must be an object of tokens")

		return tokens
	}

	for _, name := range object.Keys() {
		slot, known := v.slots[name]
		if !known {
			v.fail(path + unknownTokenText + show(name))

			continue
		}

		item, _ := object.Get(name)

		text, ok := item.(string)
		if !ok {
			v.fail(path + "." + name + " must be a string")

			continue
		}

		value := CSSTrim(text)

		problem := valueProblem(slot, value)
		if problem != "" {
			v.fail(path + "." + name + " " + problem)

			continue
		}

		tokens = append(tokens, Token{Slot: name, Value: value})
	}

	return tokens
}

func (v *validator) effects(object *js.Object) []Effect {
	raw, _ := object.Get("effects")
	if raw == nil {
		raw = []any{}
	}

	list, ok := raw.([]any)
	if !ok {
		v.fail("effects must be a list")

		return []Effect{}
	}

	if len(list) > effectsMax {
		v.fail("effects lists more than 8")
	}

	effects := []Effect{}

	for _, item := range list[:min(len(list), effectsMax)] {
		effect, ok := v.effect(item)
		if ok {
			effects = append(effects, effect)
		}
	}

	return effects
}

func (v *validator) effect(item any) (Effect, bool) {
	known := KnownEffects()

	object, isObject := item.(*js.Object)

	name, isKnown := effectName(object, isObject, known)
	if !isKnown {
		v.fail("effects: " + show(effectLabel(item, object, isObject)) +
			" is not a known effect (" + strings.Join(known, ", ") + ")")

		return Effect{}, false
	}

	raw, present := object.Get("options")
	if !present {
		return Effect{Name: name, Options: nil}, true
	}

	given, ok := raw.(*js.Object)
	if !ok {
		v.fail("effects." + name + ".options must be an object")

		return Effect{}, false
	}

	options := js.NewObject()

	for _, key := range given.Keys() {
		value, _ := given.Get(key)
		v.option(options, name, key, value)
	}

	return Effect{Name: name, Options: options}, true
}

func effectName(object *js.Object, isObject bool, known []string) (string, bool) {
	if !isObject {
		return "", false
	}

	raw, _ := object.Get("name")

	name, ok := raw.(string)
	if !ok {
		return "", false
	}

	return name, slices.Contains(known, name)
}

// effectLabel is String(isObject(e) ? e.name : e): an object's absent name is "undefined".
func effectLabel(item any, object *js.Object, isObject bool) string {
	if !isObject {
		return js.String(item)
	}

	name, present := object.Get("name")
	if !present {
		return "undefined"
	}

	return js.String(name)
}

func (v *validator) option(options *js.Object, effect, key string, value any) {
	if !optionName.MatchString(key) {
		v.fail("effects." + effect + ".options: bad name " + show(key))

		return
	}

	if optionValue(value) {
		options.Set(key, value)

		return
	}

	v.fail("effects." + effect + ".options." + key + " must be a number, boolean or plain string")
}

// optionValue is a finite number, a boolean, or a short string with nothing forbidden in it.
func optionValue(value any) bool {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) {
			return false
		}

		return !math.IsInf(typed, 0)
	case bool:
		return true
	case string:
		if js.Len(typed) > optionUnitsMax {
			return false
		}

		return !forbidden.MatchString(typed)
	}

	return false
}

// valueProblem is why value does not fit slot, or "".
func valueProblem(slot Slot, value string) string {
	bad := forbidden.FindString(value)
	if bad != "" {
		return "contains " + show(bad)
	}

	if js.Len(value) > valueUnitsMax {
		return "is too long"
	}

	switch slot.Kind {
	case KindColor:
		return colorProblem(value)
	case KindLength:
		return lengthProblem(slot, value)
	case KindNumber:
		return numberProblem(slot, value)
	case KindEnum:
		if slices.Contains(slot.Values, value) {
			return ""
		}

		return "must be one of " + strings.Join(slot.Values, ", ")
	case KindShadow:
		return shadowProblem(value)
	case KindFont:
		return fontProblem(slot, value)
	}

	return ""
}

func colorProblem(value string) string {
	_, ok := ParseColor(value)
	if ok {
		return ""
	}

	if colorMix(value) {
		return ""
	}

	return "is not a hex, rgb(), hsl(), oklch() or " +
		"color-mix(in oklch, <color> N%, transparent) color"
}

func lengthProblem(slot Slot, value string) string {
	if slot.PxOnly {
		if !strings.HasSuffix(value, unitPx) {
			return "is a length in px only"
		}
	}

	unit := slot.Unit
	if unit == "" {
		unit = unitPx
	}

	length, ok := lengthIn(value, unit)
	if !ok {
		return "is not a number with px, rem or em"
	}

	if outOfRange(slot, length) {
		return "is out of range (" + js.Number(slot.Min) + " to " + js.Number(slot.Max) +
			slot.Unit + ")"
	}

	return ""
}

func numberProblem(slot Slot, value string) string {
	if !numberOnly.MatchString(value) {
		return "is not a number"
	}

	parsed, _ := strconv.ParseFloat(value, 64)
	if outOfRange(slot, parsed) {
		return "is out of range (" + js.Number(slot.Min) + " to " + js.Number(slot.Max) + ")"
	}

	return ""
}

func outOfRange(slot Slot, value float64) bool {
	if !slot.Bounded {
		return false
	}

	if value < slot.Min {
		return true
	}

	return value > slot.Max
}

// lengthIn is a length as a number of unit (px or em); bare 0 is the only unitless length.
func lengthIn(value, unit string) (float64, bool) {
	match := lengthForm.FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}

	number, _ := strconv.ParseFloat(match[1], 64)
	given := match[2]

	if given == "" {
		return 0, number == 0
	}

	switch unit {
	case unitPx:
		if given == unitPx {
			return number, true
		}

		return number * remPx, true
	case unitEm:
		if given == unitPx {
			return number / remPx, true
		}

		return number, true
	}

	return 0, false
}

// splitTop splits text at each separator outside parentheses, trimmed, empty parts dropped.
func splitTop(text string, separator func(rune) bool) []string {
	parts := []string{}
	depth := 0

	var current strings.Builder

	for _, char := range text {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
		}

		if depth == 0 {
			if separator(char) {
				parts = append(parts, current.String())
				current.Reset()

				continue
			}
		}

		current.WriteRune(char)
	}

	parts = append(parts, current.String())

	kept := []string{}

	for _, part := range parts {
		part = CSSTrim(part)
		if part != "" {
			kept = append(kept, part)
		}
	}

	return kept
}

func isComma(char rune) bool { return char == ',' }

func isCSSSpace(char rune) bool { return strings.ContainsRune(cssSpace, char) }

func shadowProblem(value string) string {
	if value == shadowNone {
		return ""
	}

	layers := splitTop(value, isComma)
	if len(layers) == 0 {
		return "needs 1 to 4 shadows"
	}

	if len(layers) > shadowLayersMax {
		return "needs 1 to 4 shadows"
	}

	for _, layer := range layers {
		problem := layerProblem(layer)
		if problem != "" {
			return problem
		}
	}

	return ""
}

func layerProblem(layer string) string {
	inset, colors := 0, 0
	lengths := []float64{}

	for _, part := range splitTop(layer, isCSSSpace) {
		if part == "inset" {
			inset++

			continue
		}

		if lengthForm.MatchString(part) {
			px, ok := lengthIn(part, unitPx)
			if !ok {
				return "length " + show(part) + " needs px, rem or em"
			}

			lengths = append(lengths, px)

			continue
		}

		_, ok := ParseColor(part)
		if !ok {
			return show(part) + " is not a length or a color"
		}

		colors++
	}

	return layerShape(inset, colors, lengths)
}

func layerShape(inset, colors int, lengths []float64) string {
	if inset > 1 {
		return "each shadow needs exactly one color"
	}

	if colors != 1 {
		return "each shadow needs exactly one color"
	}

	if len(lengths) < shadowLengthsMin {
		return "each shadow needs 2 to 4 lengths"
	}

	if len(lengths) > shadowLengthsMax {
		return "each shadow needs 2 to 4 lengths"
	}

	beyond := slices.ContainsFunc(lengths, func(px float64) bool {
		return math.Abs(px) > shadowPxMax
	})
	if beyond {
		return "a length is beyond " + strconv.Itoa(shadowPxMax) + "px"
	}

	const blurIndex = 2
	if len(lengths) > blurIndex {
		if lengths[blurIndex] < 0 {
			return "blur cannot be negative"
		}
	}

	return ""
}

// fontProblem allows the allow-listed families and the generics the slot lists, and the stack
// must end in the slot's system family, so a font that fails to load never blocks rendering.
func fontProblem(slot Slot, value string) string {
	parts := splitTop(value, isComma)
	if len(parts) == 0 {
		return "needs 1 to 8 families"
	}

	if len(parts) > familiesMax {
		return "needs 1 to 8 families"
	}

	for _, part := range parts {
		if !familyAllowed(slot, part) {
			return "family " + show(part) + " is not on the allow-list for this slot"
		}
	}

	if parts[len(parts)-1] != slot.End {
		return "must end in " + slot.End
	}

	return ""
}

func familyAllowed(slot Slot, part string) bool {
	match := familyForm.FindStringSubmatch(part)
	if match == nil {
		return false
	}

	name := match[1] + match[2] + match[3]
	if slices.Contains(AllowedFonts(), name) {
		return true
	}

	// A generic family is a bare name, never a quoted one.
	if match[3] == "" {
		return false
	}

	return slices.Contains(slot.Values, name)
}

// colorMix is the one transparency form a theme may write: a color at N% over transparent.
func colorMix(value string) bool {
	match := colorMixForm.FindStringSubmatch(value)
	if match == nil {
		return false
	}

	share, _ := strconv.ParseFloat(match[2], 64)
	if share > percent {
		return false
	}

	_, ok := ParseColor(match[1])

	return ok
}

// paintOf is what a color value paints: the color, or for the transparency form, the color at
// that share of its alpha.
func paintOf(value string) (RGBA, bool) {
	match := colorMixForm.FindStringSubmatch(js.Trim(value))
	if match == nil {
		return ParseColor(value)
	}

	color, ok := ParseColor(match[1])
	if !ok {
		return RGBA{}, false
	}

	share, _ := strconv.ParseFloat(match[2], 64)
	if share > percent {
		return RGBA{}, false
	}

	color.A = color.A * share / percent

	return color, true
}
