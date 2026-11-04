package transcript

import (
	"regexp"

	"github.com/claytoncasey01/conn/internal/js"
)

// Session cost, estimated from the token counts in the transcripts at Anthropic's list prices
// (https://platform.claude.com/docs/en/about-claude/pricing, read 2026-10-28). It is an
// estimate: plans with included usage, discounts and batch pricing are not modelled.

const (
	tokensPerMillion = 1_000_000
	// haikuLongPrompt is where Haiku 5.5's per-request price steps up: a prompt over 100K tokens.
	haikuLongPrompt = 100_000
	fastMultiplier  = 2

	advisorMessage = "advisor_message"
)

var haiku55 = regexp.MustCompile(`^claude-haiku-5-5`)

// price is dollars per million tokens.
type price struct {
	input, write5m, write1h, read, output float64
}

type priceRow struct {
	model *regexp.Regexp
	price price
}

// prices are matched first to last, so a model id comes before any id it is a prefix of
// (opus-5-5 before opus-5).
func prices() []priceRow {
	row := func(pattern string, values price) priceRow {
		return priceRow{model: regexp.MustCompile(pattern), price: values}
	}

	return []priceRow{
		row(`^claude-(fable|mythos)-5-1`, price{10, 12.5, 20, 0.25, 50}),
		row(`^claude-(fable|mythos)-5`, price{10, 12.5, 20, 1, 50}),
		row(`^claude-opus-5-5`, price{4, 5, 8, 0.2, 20}),
		row(`^claude-opus-(5|4-8|4-7|4-6|4-5)(-|$)`, price{5, 6.25, 10, 0.5, 25}),
		row(`^claude-opus-4(-1)?(-|$)`, price{15, 18.75, 30, 1.5, 75}),
		row(`^claude-sonnet-5-5`, price{2, 2.5, 4, 0.1, 10}),
		row(`^claude-sonnet-5`, price{2, 2.5, 4, 0.2, 10}),
		row(`^claude-sonnet-4`, price{3, 3.75, 6, 0.3, 15}),
		row(`^claude-haiku-4-5`, price{1, 1.25, 2, 0.1, 5}),
		row(`^claude-haiku-3-5`, price{0.8, 1, 1.6, 0.08, 4}),
	}
}

// Haiku 5.5 is priced by prompt size, per request: up to 100K tokens, and over.
func haikuPrice(promptTokens float64) price {
	if promptTokens > haikuLongPrompt {
		return price{0.5, 0.625, 1, 0.05, 2.5}
	}

	return price{0.1, 0.125, 0.2, 0.01, 0.5}
}

// number is a usage field as `?? 0` reads it: a number, else 0.
func number(object *js.Object, key string) float64 {
	raw, _ := object.Get(key)
	value, _ := raw.(float64)

	return value
}

// UsageCost is dollars for one request's usage block, and false when the model has no known
// price.
func UsageCost(model string, usage *js.Object) (float64, bool) {
	input := number(usage, "input_tokens")
	read := number(usage, "cache_read_input_tokens")
	total := number(usage, "cache_creation_input_tokens")

	// Without the split, every cache write counts as the cheaper 5-minute kind.
	split, _ := usage.Get("cache_creation")
	splitObject, _ := split.(*js.Object)
	writes1h := number(splitObject, "ephemeral_1h_input_tokens")
	writes5m := total

	if js.Truthy(split) {
		writes5m = number(splitObject, "ephemeral_5m_input_tokens")
	}

	rates, priced := priceOf(model, input+read+total)
	if !priced {
		return 0, false
	}

	usd := (float64(input*rates.input) + float64(read*rates.read) +
		float64(writes5m*rates.write5m) + float64(writes1h*rates.write1h) +
		float64(number(usage, "output_tokens")*rates.output)) / tokensPerMillion

	// Fast mode runs the same model at twice the price.
	speed, _ := usage.Get("speed")
	if speed == "fast" {
		return usd * fastMultiplier, true
	}

	return usd, true
}

func priceOf(model string, promptTokens float64) (price, bool) {
	if haiku55.MatchString(model) {
		return haikuPrice(promptTokens), true
	}

	for _, row := range prices() {
		if row.model.MatchString(model) {
			return row.price, true
		}
	}

	return price{}, false
}

// meterEntry is one response's share of a meter.
type meterEntry struct {
	usd, output, unpriced float64
}

// UsageMeter totals one transcript. A transcript has one line per content block of a response,
// and every line repeats that response's message.id and the same usage block, so adding up
// lines would count a response once per block. Each response is counted once, keyed by its id,
// a later line replacing an earlier one.
type UsageMeter struct {
	// Output is the output tokens of the main model, a rough measure of work.
	Output float64
	USD    float64
	// Unpriced counts responses from a model with no known price: USD leaves them out.
	Unpriced float64
	seen     map[string]meterEntry
}

// NewUsageMeter is an empty meter.
func NewUsageMeter() *UsageMeter {
	return &UsageMeter{Output: 0, USD: 0, Unpriced: 0, seen: map[string]meterEntry{}}
}

// Add counts an assistant message's usage (its message object: id, model, usage).
func (m *UsageMeter) Add(message *js.Object) {
	raw, _ := message.Get("usage")
	if !js.Truthy(raw) {
		return
	}

	usage, _ := raw.(*js.Object)
	model := modelName(message)
	entry := meterEntry{usd: 0, output: number(usage, "output_tokens"), unpriced: 0}

	main, priced := UsageCost(model, usage)
	if priced {
		entry.usd += main
	} else {
		entry.unpriced++
	}

	// The advisor runs inside the request on its own model and is billed on its own line.
	iterations, _ := usage.Get("iterations")
	list, _ := iterations.([]any)

	for _, item := range list {
		entry = withAdvisor(entry, item)
	}

	m.replace(message, entry)
}

// modelName is String(m?.model ?? ”).
func modelName(message *js.Object) string {
	raw, present := message.Get("model")
	if !present {
		return ""
	}

	if raw == nil {
		return ""
	}

	return js.String(raw)
}

func withAdvisor(entry meterEntry, item any) meterEntry {
	iteration, _ := item.(*js.Object)

	kind, _ := iteration.Get("type")
	if kind != advisorMessage {
		return entry
	}

	model, _ := iteration.Get("model")
	if !js.Truthy(model) {
		entry.unpriced++

		return entry
	}

	cost, priced := UsageCost(js.String(model), iteration)
	if !priced {
		entry.unpriced++

		return entry
	}

	entry.usd += cost

	return entry
}

// replace takes back the earlier count of the same response, if any, and adds this one.
func (m *UsageMeter) replace(message *js.Object, entry meterEntry) {
	rawID, _ := message.Get("id")
	identified := js.Truthy(rawID)
	messageID := js.String(rawID)

	if identified {
		old, seen := m.seen[messageID]
		if seen {
			m.USD -= old.usd
			m.Output -= old.output
			m.Unpriced -= old.unpriced
		}

		m.seen[messageID] = entry
	}

	m.USD += entry.usd
	m.Output += entry.output
	m.Unpriced += entry.unpriced
}
