package theme

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The CSS grammar the colors are read with, not JavaScript's: whitespace is only these five
// (JavaScript's \s also matches a no-break space), a decimal point must be followed by digits,
// and the function names match in ASCII case only (Go's (?i) would also fold ſ to s).
const (
	cssSpace = " \t\n\r\f"
	numberRE = `[+-]?(?:\d+(?:\.\d+)?|\.\d+)`
	partRE   = `[^ \t\n\r\f/,]+`
	spaceRE  = `[ \t\n\r\f]`
)

// The color functions' scales: a percentage of a channel, of OKLCH chroma and of a hue turn.
const (
	maxChannel   = 255
	maxChroma    = 0.4
	percent      = 100
	degreesHalf  = 180
	degreesTurn  = 360
	hueSector    = 30
	hueSectors   = 12
	hexPairShort = 4
)

// OKLCH to linear sRGB, by the standard matrices (Björn Ottosson's).
const (
	labL1, labL2 = 0.3963377774, 0.2158037573
	labM1, labM2 = 0.1055613458, 0.0638541728
	labS1, labS2 = 0.0894841775, 1.291485548

	rgbR1, rgbR2, rgbR3 = 4.0767416621, 3.3077115913, 0.2309699292
	rgbG1, rgbG2, rgbG3 = 1.2684380046, 2.6097574011, 0.3413193965
	rgbB1, rgbB2, rgbB3 = 0.0041960863, 0.7034186147, 1.707614701

	cube = 3
)

// sRGB's transfer function and WCAG's relative luminance.
const (
	gammaKnee    = 0.0031308
	gammaSlope   = 12.92
	gammaScale   = 1.055
	gammaOffset  = 0.055
	gammaPower   = 2.4
	linearKnee   = 0.04045
	lumaRed      = 0.2126
	lumaGreen    = 0.7152
	lumaBlue     = 0.0722
	contrastPad  = 0.05
	hslKneeLow   = 3
	hslKneeHigh  = 9
	hslRedOffset = 0
	hslGreenOff  = 8
	hslBlueOff   = 4
)

var (
	numberOnly = regexp.MustCompile(`^` + numberRE + `$`)
	hueForm    = regexp.MustCompile(`^(` + numberRE + `)(deg)?$`)
	hexForm    = regexp.MustCompile(
		`^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	functionForm = regexp.MustCompile(
		`^([rR][gG][bB][aA]?|[hH][sS][lL][aA]?|[oO][kK][lL][cC][hH])\(` +
			spaceRE + `*([^()]*?)` + spaceRE + `*\)$`)
	commaForm = regexp.MustCompile(`^(` + partRE + `)` + spaceRE + `*,` + spaceRE + `*(` +
		partRE + `)` + spaceRE + `*,` + spaceRE + `*(` + partRE + `)(?:` + spaceRE + `*,` +
		spaceRE + `*(` + partRE + `))?$`)
	spaceForm = regexp.MustCompile(`^(` + partRE + `)` + spaceRE + `+(` + partRE + `)` +
		spaceRE + `+(` + partRE + `)(?:` + spaceRE + `*/` + spaceRE + `*(` + partRE + `))?$`)
)

// Every product that is added to something is converted to float64 on its own: Go may otherwise
// fuse a*b + c into one instruction that skips the rounding JavaScript does after a*b, and a
// channel then rounds the other way.

// RGBA is a color: red, green and blue 0 to 255 (whole numbers), alpha 0 to 1.
type RGBA struct {
	R, G, B, A float64
}

// CSSTrim strips CSS whitespace from both ends.
func CSSTrim(text string) string {
	return strings.Trim(text, cssSpace)
}

// ParseColor reads a hex, rgb(), rgba(), hsl(), hsla() or oklch() color, and is false for
// anything else.
func ParseColor(value string) (RGBA, bool) {
	text := CSSTrim(value)

	hex := hexForm.FindStringSubmatch(text)
	if hex != nil {
		return hexColor(hex[1]), true
	}

	function := functionForm.FindStringSubmatch(text)
	if function == nil {
		return RGBA{}, false
	}

	name := strings.ToLower(function[1])

	parts, split := colorParts(name, function[2])
	if !split {
		return RGBA{}, false
	}

	opacity, read := alphaOf(parts)
	if !read {
		return RGBA{}, false
	}

	switch name[:3] {
	case "rgb":
		return rgbColor(parts, opacity)
	case "hsl":
		return hslColor(parts, opacity)
	}

	return oklchColor(parts, opacity)
}

func hexColor(digits string) RGBA {
	if len(digits) <= hexPairShort {
		var doubled strings.Builder
		for _, digit := range digits {
			doubled.WriteRune(digit)
			doubled.WriteRune(digit)
		}

		digits = doubled.String()
	}

	byteAt := func(index int) float64 {
		value, _ := strconv.ParseUint(digits[index:index+2], 16, 8)

		return float64(value)
	}

	// Where each channel's pair of digits starts, and the length that carries an alpha.
	const (
		redAt, greenAt, blueAt, alphaAt = 0, 2, 4, 6
		withAlphaDigits                 = 8
	)

	opacity := 1.0
	if len(digits) == withAlphaDigits {
		opacity = byteAt(alphaAt) / maxChannel
	}

	return RGBA{R: byteAt(redAt), G: byteAt(greenAt), B: byteAt(blueAt), A: opacity}
}

// colorParts splits a function's arguments: the comma form (rgb and hsl only, legacy CSS) or the
// space form, three parts and an optional alpha.
func colorParts(name, arguments string) ([]string, bool) {
	var match []int
	if name != "oklch" {
		match = commaForm.FindStringSubmatchIndex(arguments)
	}

	if match == nil {
		match = spaceForm.FindStringSubmatchIndex(arguments)
	}

	if match == nil {
		return nil, false
	}

	parts := []string{}

	for group := 1; group*2 < len(match); group++ {
		start := match[group*2]
		if start < 0 {
			continue
		}

		parts = append(parts, arguments[start:match[group*2+1]])
	}

	return parts, true
}

func alphaOf(parts []string) (float64, bool) {
	const alphaPart = 3
	if len(parts) <= alphaPart {
		return 1, true
	}

	value, ok := component(parts[alphaPart], 1)
	if !ok {
		return 0, false
	}

	return clampTo(value, 1), true
}

// component reads "50%" as 0.5 of scale, and a bare number as it is.
func component(text string, scale float64) (float64, bool) {
	number, isPercent := strings.CutSuffix(text, "%")
	if !numberOnly.MatchString(number) {
		return 0, false
	}

	value, _ := strconv.ParseFloat(number, 64)
	if isPercent {
		return value / percent * scale, true
	}

	return value, true
}

func hueOf(text string) (float64, bool) {
	match := hueForm.FindStringSubmatch(text)
	if match == nil {
		return 0, false
	}

	value, _ := strconv.ParseFloat(match[1], 64)

	return value, true
}

func rgbColor(parts []string, opacity float64) (RGBA, bool) {
	channels := [3]float64{}

	for index := range channels {
		value, ok := component(parts[index], maxChannel)
		if !ok {
			return RGBA{}, false
		}

		channels[index] = round(clampTo(value, maxChannel))
	}

	return RGBA{R: channels[0], G: channels[1], B: channels[2], A: opacity}, true
}

func hslColor(parts []string, opacity float64) (RGBA, bool) {
	hue, read := hueOf(parts[0])
	if !read {
		return RGBA{}, false
	}

	saturation, read := percentOnly(parts[1])
	if !read {
		return RGBA{}, false
	}

	lightness, read := percentOnly(parts[2])
	if !read {
		return RGBA{}, false
	}

	hue = math.Mod(math.Mod(hue, degreesTurn)+degreesTurn, degreesTurn)

	return hsl(hue, clampTo(saturation, 1), clampTo(lightness, 1), opacity), true
}

// percentOnly reads a percentage as a fraction; hsl's saturation and lightness take nothing else.
func percentOnly(text string) (float64, bool) {
	if !strings.HasSuffix(text, "%") {
		return 0, false
	}

	return component(text, 1)
}

func hsl(hue, saturation, lightness, opacity float64) RGBA {
	sector := func(offset float64) float64 { return math.Mod(offset+hue/hueSector, hueSectors) }
	chroma := saturation * math.Min(lightness, 1-lightness)
	channel := func(offset float64) float64 {
		at := sector(offset)
		step := math.Max(-1, math.Min(at-hslKneeLow, math.Min(hslKneeHigh-at, 1)))

		return round((lightness - float64(chroma*step)) * maxChannel)
	}

	return RGBA{
		R: channel(hslRedOffset), G: channel(hslGreenOff), B: channel(hslBlueOff), A: opacity,
	}
}

func oklchColor(parts []string, opacity float64) (RGBA, bool) {
	lightness, read := component(parts[0], 1)
	if !read {
		return RGBA{}, false
	}

	chroma, read := component(parts[1], maxChroma)
	if !read {
		return RGBA{}, false
	}

	hue, read := hueOf(parts[2])
	if !read {
		return RGBA{}, false
	}

	return oklch(clampTo(lightness, 1), math.Max(0, chroma), hue, opacity), true
}

// oklch converts by way of OKLab and linear sRGB, clamped, with no gamut mapping.
func oklch(lightness, chroma, hue, opacity float64) RGBA {
	radians := hue * math.Pi / degreesHalf
	labA := chroma * math.Cos(radians)
	labB := chroma * math.Sin(radians)
	long := math.Pow(lightness+float64(labL1*labA)+float64(labL2*labB), cube)
	medium := math.Pow(lightness-float64(labM1*labA)-float64(labM2*labB), cube)
	short := math.Pow(lightness-float64(labS1*labA)-float64(labS2*labB), cube)

	channel := func(linear float64) float64 {
		return round(gamma(clampTo(linear, 1)) * maxChannel)
	}

	return RGBA{
		R: channel(float64(rgbR1*long) - float64(rgbR2*medium) + float64(rgbR3*short)),
		G: channel(float64(-rgbG1*long) + float64(rgbG2*medium) - float64(rgbG3*short)),
		B: channel(float64(-rgbB1*long) - float64(rgbB2*medium) + float64(rgbB3*short)),
		A: opacity,
	}
}

func gamma(linear float64) float64 {
	if linear <= gammaKnee {
		return gammaSlope * linear
	}

	// The exponent is worked out in float64 as JavaScript's 1 / 2.4 is, not as an exact constant.
	power := gammaPower

	return float64(gammaScale*math.Pow(linear, 1/power)) - gammaOffset
}

// Composite lays fg over bg; the result is opaque, as bg is taken to be.
func Composite(fg, bg RGBA) RGBA {
	mix := func(front, back float64) float64 {
		return round(float64(front*fg.A) + float64(back*(1-fg.A)))
	}

	return RGBA{R: mix(fg.R, bg.R), G: mix(fg.G, bg.G), B: mix(fg.B, bg.B), A: 1}
}

func luminance(color RGBA) float64 {
	linear := func(channel float64) float64 {
		value := channel / maxChannel
		if value <= linearKnee {
			return value / gammaSlope
		}

		return math.Pow((value+gammaOffset)/gammaScale, gammaPower)
	}

	return float64(lumaRed*linear(color.R)) + float64(lumaGreen*linear(color.G)) +
		float64(lumaBlue*linear(color.B))
}

// Contrast is the WCAG 2 ratio, 1 to 21; a translucent fg is laid over bg first.
func Contrast(fg, bg RGBA) float64 {
	front := luminance(Composite(fg, bg))
	back := luminance(bg)

	return (math.Max(front, back) + contrastPad) / (math.Min(front, back) + contrastPad)
}

// clampTo is JavaScript's Math.min(highest, Math.max(0, value)).
func clampTo(value, highest float64) float64 {
	return math.Min(highest, math.Max(0, value))
}

// round is JavaScript's Math.round: to the nearest whole number, a half going up.
func round(value float64) float64 {
	const half = 0.5

	whole := math.Floor(value)
	if value-whole >= half {
		return whole + 1
	}

	return whole
}

// opaqueWhite is the white the look's light is drawn in.
func opaqueWhite() RGBA { return RGBA{R: maxChannel, G: maxChannel, B: maxChannel, A: 1} }

// channel is a filtered channel as the browser keeps it: a whole value from 0 to 255.
func channel(value float64) float64 { return math.Min(maxChannel, math.Max(0, round(value))) }

// saturate is CSS saturate(amount) on an opaque color, by the Filter Effects matrix, each channel
// kept to a whole 8-bit value; 1 changes nothing.
func saturate(color RGBA, amount float64) RGBA {
	if amount == 1 {
		return color
	}

	rows := saturateRows(color, amount)

	return RGBA{R: channel(rows[0]), G: channel(rows[1]), B: channel(rows[2]), A: 1}
}

// saturateRows is saturate before its channels are kept to 8 bits. Each product is converted on
// its own so no platform fuses it, and the answer matches validate.ts's to the bit.
func saturateRows(color RGBA, amount float64) [3]float64 {
	gains := saturateMatrix()
	row := func(entries [3][2]float64) float64 {
		// gain is an entry, base plus slope times the amount, its product rounded on its own.
		gain := func(entry [2]float64) float64 { return entry[0] + float64(entry[1]*amount) }

		red := float64(gain(entries[0]) * color.R)
		green := float64(gain(entries[1]) * color.G)
		blue := float64(gain(entries[2]) * color.B)

		return red + green + blue
	}

	return [3]float64{row(gains[0]), row(gains[1]), row(gains[2])}
}

// glassFilter is the page behind the glass after its filter, saturate then brightness: kept to 8
// bits between the steps, as Chrome does, and, when both steps change it, with saturate's
// overshoot carried into brightness, which lands brighter where brightness deepens (dark mode).
// The gate measures each (validate.ts glassFilter).
func glassFilter(color RGBA, boost, tone float64) []RGBA {
	clamped := brightness(saturate(color, boost), tone)
	if boost == 1 || tone == 1 {
		return []RGBA{clamped}
	}

	rows := saturateRows(color, boost)
	carried := RGBA{
		R: channel(float64(rows[0] * tone)), G: channel(float64(rows[1] * tone)),
		B: channel(float64(rows[2] * tone)), A: 1,
	}

	return []RGBA{clamped, carried}
}

// saturateMatrix is the Filter Effects saturate matrix, row by row, each entry its base and its
// slope: the entry is base plus slope times the amount.
func saturateMatrix() [3][3][2]float64 {
	return [3][3][2]float64{
		{{0.213, 0.787}, {0.715, -0.715}, {0.072, -0.072}},
		{{0.213, -0.213}, {0.715, 0.285}, {0.072, -0.072}},
		{{0.213, -0.213}, {0.715, -0.715}, {0.072, 0.928}},
	}
}

// brightness is CSS brightness(amount) on an opaque color; 1 changes nothing.
func brightness(color RGBA, amount float64) RGBA {
	if amount == 1 {
		return color
	}

	return RGBA{
		R: channel(float64(color.R * amount)), G: channel(float64(color.G * amount)),
		B: channel(float64(color.B * amount)),
		A: 1,
	}
}

// wallpaperAt is the page at point (shares of the window's width and height) under the
// wallpaper: each field present, from the bottom layer up, fading from its center to nothing at
// wallpaperFade of its radii.
func wallpaperAt(point [2]float64, hues []RGBA, present []bool, strength float64, page RGBA) RGBA {
	out := page
	fields := wallpaperFields()

	for index, field := range slices.Backward(fields) {
		if !present[index] {
			continue
		}

		across := (point[0] - field.x) / field.rx
		down := (point[1] - field.y) / field.ry
		reach := math.Sqrt(float64(across*across) + float64(down*down))

		share := float64(strength * math.Max(0, 1-reach/wallpaperFade))
		if share > 0 {
			out = Composite(withAlpha(hues[index], share), out)
		}
	}

	return out
}
