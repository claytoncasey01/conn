package theme_test

import (
	"math"
	"os"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/theme"
)

// The cases src/lib/theme/validateCases.test.ts writes from validate.ts: the Go judgment must
// give every one of the same answers.
func readCases(t *testing.T) *js.Object {
	t.Helper()

	data, err := os.ReadFile("../../testdata/theme-cases.json")
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	cases, ok := parsed.(*js.Object)
	if !ok {
		t.Fatal("theme-cases.json is not an object")
	}

	return cases
}

func field[T any](t *testing.T, object *js.Object, key string) T {
	t.Helper()

	raw, _ := object.Get(key)

	value, ok := raw.(T)
	if !ok {
		t.Fatalf("theme-cases.json %s is a %T", key, raw)
	}

	return value
}

func rows(t *testing.T, cases *js.Object, key string) [][]any {
	t.Helper()

	list := field[[]any](t, cases, key)
	out := make([][]any, 0, len(list))

	for _, item := range list {
		row, ok := item.([]any)
		if !ok {
			t.Fatalf("theme-cases.json %s holds a %T", key, item)
		}

		out = append(out, row)
	}

	return out
}

func TestSlotsMatchTheTokenContract(t *testing.T) {
	t.Parallel()

	cases := readCases(t)
	want := rows(t, cases, "slots")
	got := theme.Slots()

	if len(got) != len(want) {
		t.Fatalf("Go has %d slots, tokens.ts %d", len(got), len(want))
	}

	for index, row := range want {
		name, _ := row[0].(string)
		spec, _ := row[1].(*js.Object)

		if got[index].Name != name {
			t.Errorf("slot %d is %s, want %s", index, got[index].Name, name)

			continue
		}

		if describe(got[index]) != js.Stringify(sortedSpec(spec)) {
			t.Errorf("slot %s = %s, want %s", name, describe(got[index]),
				js.Stringify(sortedSpec(spec)))
		}
	}
}

// describe writes a Go slot in the shape tokens.ts gives it, keys sorted.
func describe(slot theme.Slot) string {
	kinds := []string{"color", "length", "number", "enum", "shadow", "font"}
	spec := js.NewObject()

	if slot.End != "" {
		spec.Set("end", slot.End)
	}

	spec.Set("kind", kinds[slot.Kind])

	if slot.Bounded {
		spec.Set("max", slot.Max)
		spec.Set("min", slot.Min)
	}

	if slot.PxOnly {
		spec.Set("pxOnly", true)
	}

	if slot.Unit != "" {
		spec.Set("unit", slot.Unit)
	}

	if slot.Values != nil {
		values := make([]any, 0, len(slot.Values))
		for _, value := range slot.Values {
			values = append(values, value)
		}

		spec.Set("values", values)
	}

	return js.Stringify(spec)
}

func sortedSpec(spec *js.Object) *js.Object {
	keys := spec.Keys()
	slices.Sort(keys)

	sorted := js.NewObject()

	for _, key := range keys {
		value, _ := spec.Get(key)
		sorted.Set(key, value)
	}

	return sorted
}

func TestDerivedFontsEffectsAndLimitsMatch(t *testing.T) {
	t.Parallel()

	cases := readCases(t)

	derived := make([]any, 0, len(theme.DerivedSlots()))
	for _, slot := range theme.DerivedSlots() {
		derived = append(derived, []any{slot.Slot, slot.Form})
	}

	limits := js.NewObject()
	limits.Set("manifestBytes", theme.MaxManifestBytes)
	limits.Set("cssBytes", theme.MaxCSSBytes)
	limits.Set("userThemes", theme.MaxUserThemes)

	tests := []struct {
		key string
		got any
	}{
		{key: "derived", got: derived},
		{key: "fonts", got: theme.AllowedFonts()},
		{key: "effects", got: theme.KnownEffects()},
		{key: "limits", got: limits},
	}

	for _, tt := range tests {
		raw, _ := cases.Get(tt.key)
		if js.Stringify(tt.got) != js.Stringify(raw) {
			t.Errorf("%s = %s, want %s", tt.key, js.Stringify(tt.got), js.Stringify(raw))
		}
	}
}

func TestColorsParseAsValidateTsParsesThem(t *testing.T) {
	t.Parallel()

	for _, row := range rows(t, readCases(t), "colors") {
		input, _ := row[0].(string)
		got, parsed := theme.ParseColor(input)

		if row[1] == nil {
			if parsed {
				t.Errorf("ParseColor(%q) = %v, want no color", input, got)
			}

			continue
		}

		want, _ := row[1].(*js.Object)
		if !parsed {
			t.Errorf("ParseColor(%q) found no color, want %s", input, js.Stringify(want))

			continue
		}

		if colorText(got) != js.Stringify(want) {
			t.Errorf("ParseColor(%q) = %s, want %s", input, colorText(got), js.Stringify(want))
		}
	}
}

func colorText(color theme.RGBA) string {
	object := js.NewObject()
	object.Set("r", color.R)
	object.Set("g", color.G)
	object.Set("b", color.B)
	object.Set("a", color.A)

	return js.Stringify(object)
}

// Go's math.Pow and the pow JavaScriptCore calls can round the last bit differently, so a ratio
// may be an ulp or two off. What a theme author sees cannot be: the ratio as a message prints it
// and which side of each threshold it falls on.
func TestContrastMatchesWhereItCounts(t *testing.T) {
	t.Parallel()

	const maxUlps = 4

	for _, row := range rows(t, readCases(t), "pairs") {
		front, _ := row[0].(string)
		back, _ := row[1].(string)
		want, _ := row[2].(float64)

		fg, _ := theme.ParseColor(front)
		bg, _ := theme.ParseColor(back)

		got := theme.Contrast(fg, bg)
		if ulps(got, want) > maxUlps {
			t.Errorf("contrast(%s, %s) = %v, want %v", front, back, got, want)
		}

		if js.Fixed(got, 2) != js.Fixed(want, 2) {
			t.Errorf("contrast(%s, %s) prints %s, want %s", front, back, js.Fixed(got, 2),
				js.Fixed(want, 2))
		}

		for _, threshold := range []float64{1.5, 3, 4.5} {
			if (got < threshold) != (want < threshold) {
				t.Errorf("contrast(%s, %s) = %v falls the other side of %v", front, back, got,
					threshold)
			}
		}
	}
}

func ulps(left, right float64) uint64 {
	a, b := math.Float64bits(left), math.Float64bits(right)
	if a > b {
		return a - b
	}

	return b - a
}

func TestSlotValuesAreJudgedAsValidateTsJudgesThem(t *testing.T) {
	t.Parallel()

	for _, row := range rows(t, readCases(t), "values") {
		slot, _ := row[0].(string)
		value, _ := row[1].(string)
		want, _ := row[2].(string)

		got := theme.SlotProblem(slot, value)
		if got != want {
			t.Errorf("%s %q: %q, want %q", slot, value, got, want)
		}
	}
}

func TestManifestsAreJudgedAsValidateTsJudgesThem(t *testing.T) {
	t.Parallel()

	cases := readCases(t)

	builtin := map[string]bool{}

	for _, id := range field[[]any](t, cases, "builtinIds") {
		name, _ := id.(string)
		builtin[name] = true
	}

	fallback, problems := theme.ParseManifest(field[string](t, cases, "default"),
		theme.Context{Source: theme.SourceBuiltin, BuiltinIDs: builtin})
	if len(problems) > 0 {
		t.Fatalf("Default: %q", problems)
	}

	for index, row := range rows(t, cases, "manifests") {
		source, _ := row[0].(string)
		text, _ := row[1].(string)
		want := js.Stringify(row[2])

		context := theme.Context{Source: theme.Source(source), BuiltinIDs: builtin}
		manifest, errors := theme.CheckTheme(text, context, &fallback)

		result := js.NewObject()
		result.Set("ok", len(errors) == 0)

		if len(errors) == 0 {
			result.Set("manifest", &manifest)
		} else {
			result.Set("errors", errors)
		}

		got := js.Stringify(result)
		if got != want {
			t.Errorf("case %d (%s) %s\n got %s\nwant %s", index, source, js.QuoteClip(text, 200),
				got, want)
		}
	}
}
