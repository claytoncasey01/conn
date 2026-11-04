package theme

import (
	"math"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Source is where a theme comes from.
type Source string

// The sources: shipped with Conn, the user's own folder, or installed from the gallery.
const (
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
	SourceGallery Source = "gallery"
)

// The modes a theme can have.
const (
	ModeLight = "light"
	ModeDark  = "dark"
)

// The accent kinds besides variants.
const (
	AccentFree  = "free"
	AccentFixed = "fixed"
)

// Token is one slot's value.
type Token struct {
	Slot  string
	Value string
}

// Tokens are slot values in the order the theme wrote them.
type Tokens []Token

// Get is the value of slot, and whether it is set.
func (t Tokens) Get(slot string) (string, bool) {
	for _, token := range t {
		if token.Slot == slot {
			return token.Value, true
		}
	}

	return "", false
}

// MarshalJSON writes the tokens as an object in order.
func (t Tokens) MarshalJSON() ([]byte, error) {
	object := js.NewObject()
	for _, token := range t {
		object.Set(token.Slot, token.Value)
	}

	return []byte(js.Stringify(object)), nil
}

// ModeTokens are one mode's tokens.
type ModeTokens struct {
	Mode   string
	Tokens Tokens
}

// Variant is one accent variant: a name the user picks and the tokens it sets.
type Variant struct {
	Name   string `json:"name"`
	Tokens Tokens `json:"tokens"`
}

// Accent is "free", "fixed", or a list of variants (Kind empty).
type Accent struct {
	Kind     string
	Variants []Variant
}

// MarshalJSON writes "free", "fixed" or {"variants": [...]}.
func (a Accent) MarshalJSON() ([]byte, error) {
	if a.Kind != "" {
		return []byte(js.Stringify(a.Kind)), nil
	}

	variants := make([]any, 0, len(a.Variants))
	for _, variant := range a.Variants {
		object := js.NewObject()
		object.Set("name", variant.Name)
		object.Set("tokens", variant.Tokens)
		variants = append(variants, object)
	}

	object := js.NewObject()
	object.Set("variants", variants)

	return []byte(js.Stringify(object)), nil
}

// Effect is a named effect and its options (nil when the theme gave none).
type Effect struct {
	Name    string
	Options *js.Object
}

// Manifest is a theme's theme.json as it passed validation.
type Manifest struct {
	ID          string
	Name        string
	Version     string
	Author      string
	License     string
	Description string
	Modes       []string
	Accent      Accent
	Fonts       []string
	Tokens      []ModeTokens
	Effects     []Effect
}

// ModeTokens are the tokens the theme sets for mode, and whether it sets any.
func (m *Manifest) ModeTokens(mode string) (Tokens, bool) {
	for _, set := range m.Tokens {
		if set.Mode == mode {
			return set.Tokens, true
		}
	}

	return nil, false
}

// MarshalJSON writes the manifest with its keys in validateManifest's order.
func (m *Manifest) MarshalJSON() ([]byte, error) {
	return []byte(js.Stringify(m.object())), nil
}

func (m *Manifest) object() *js.Object {
	tokens := js.NewObject()
	for _, set := range m.Tokens {
		tokens.Set(set.Mode, set.Tokens)
	}

	effects := make([]any, 0, len(m.Effects))
	for _, effect := range m.Effects {
		object := js.NewObject()
		object.Set("name", effect.Name)

		if effect.Options != nil {
			object.Set("options", effect.Options)
		}

		effects = append(effects, object)
	}

	object := js.NewObject()
	object.Set("id", m.ID)
	object.Set("name", m.Name)
	object.Set("version", m.Version)
	object.Set("author", m.Author)
	object.Set("license", m.License)
	object.Set("description", m.Description)
	object.Set("modes", slices.Clone(m.Modes))
	object.Set("accent", m.Accent)
	object.Set("fonts", slices.Clone(m.Fonts))
	object.Set("tokens", tokens)
	object.Set("effects", effects)

	return object
}

// Same is whether two manifests hold the same values, the order of an object's keys aside, as
// node's isDeepStrictEqual judges them (0 and -0 differ).
func Same(left, right *Manifest) bool {
	return canonical(left.object()) == canonical(right.object())
}

// canonical is a value as JSON-like text with every object's keys sorted, so key order does not
// count, and -0 written apart from 0.
func canonical(value any) string {
	var builder strings.Builder

	writeCanonical(&builder, value)

	return builder.String()
}

func writeCanonical(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case *js.Object:
		writeCanonicalObject(builder, typed)
	case Tokens:
		object := js.NewObject()
		for _, token := range typed {
			object.Set(token.Slot, token.Value)
		}

		writeCanonicalObject(builder, object)
	case Accent:
		writeCanonicalAccent(builder, typed)
	case []any:
		writeCanonicalList(builder, typed)
	case []string:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}

		writeCanonicalList(builder, items)
	case float64:
		builder.WriteString(canonicalNumber(typed))
	default:
		builder.WriteString(js.Stringify(typed))
	}
}

func writeCanonicalAccent(builder *strings.Builder, accent Accent) {
	if accent.Kind != "" {
		builder.WriteString(js.Stringify(accent.Kind))

		return
	}

	variants := make([]any, 0, len(accent.Variants))
	for _, variant := range accent.Variants {
		object := js.NewObject()
		object.Set("name", variant.Name)
		object.Set("tokens", variant.Tokens)
		variants = append(variants, object)
	}

	object := js.NewObject()
	object.Set("variants", variants)
	writeCanonicalObject(builder, object)
}

func writeCanonicalObject(builder *strings.Builder, object *js.Object) {
	keys := object.Keys()
	slices.Sort(keys)
	builder.WriteByte('{')

	for index, key := range keys {
		if index > 0 {
			builder.WriteByte(',')
		}

		item, _ := object.Get(key)

		builder.WriteString(js.Stringify(key) + ":")
		writeCanonical(builder, item)
	}

	builder.WriteByte('}')
}

func writeCanonicalList(builder *strings.Builder, items []any) {
	builder.WriteByte('[')

	for index, item := range items {
		if index > 0 {
			builder.WriteByte(',')
		}

		writeCanonical(builder, item)
	}

	builder.WriteByte(']')
}

func canonicalNumber(value float64) string {
	if value == 0 {
		if math.Signbit(value) {
			return "-0"
		}
	}

	return js.Number(value)
}
