package historyapi

import (
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/js"
)

const (
	// twoDigitYears are the years JavaScript's Date(y, m, d) reads as 19yy.
	twoDigitYears = 100
	weekDays      = 7
	monthDays     = 30
)

func text(params url.Values, key string) string {
	return js.Cut(js.Trim(params.Get(key)), maxText)
}

func list(params url.Values, key string) []string {
	out := []string{}

	for item := range strings.SplitSeq(params.Get(key), ",") {
		trimmed := js.Cut(js.Trim(item), maxText)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out[:min(len(out), maxList)]
}

// number is a finite number from a non-blank parameter, else nil: Number(”) is 0 and must not
// pass for one.
func number(params url.Values, key string) *float64 {
	raw, present := params[key]
	if !present {
		return nil
	}

	if js.Trim(raw[0]) == "" {
		return nil
	}

	value := js.ToNumber(raw[0])
	if math.IsNaN(value) {
		return nil
	}

	if math.IsInf(value, 0) {
		return nil
	}

	return &value
}

// local is the machine's own zone, as JavaScript's Date reads a day.
func local() *time.Location { return time.Now().Location() }

// dateParam is a date from the range picker. A bare YYYY-MM-DD is a day on this machine's clock
// (as today is), and as to it runs to that day's last millisecond so the day is included;
// anything else goes to Date.parse. Invalid is nil.
func dateParam(params url.Values, key string, endOfDay bool) *float64 {
	raw := js.Trim(params.Get(key))
	if raw == "" {
		return nil
	}

	match := bareDay.FindStringSubmatch(raw)
	if match == nil {
		parsed := js.DateParse(raw)
		if math.IsNaN(parsed) {
			return nil
		}

		return &parsed
	}

	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])

	// Date(y, m, d) reads a year under 100 as 19yy, which then fails the check below.
	if year < twoDigitYears {
		return nil
	}

	dayStart := time.Date(year, time.Month(month), day, 0, 0, 0, 0, local())
	if dayStart.Year() != year {
		return nil
	}

	if int(dayStart.Month()) != month {
		return nil
	}

	if dayStart.Day() != day {
		return nil
	}

	moment := float64(dayStart.UnixMilli())

	if endOfDay {
		nextDay := time.Date(year, time.Month(month), day+1, 0, 0, 0, 0, local())
		moment = float64(nextDay.UnixMilli()) - 1
	}

	return &moment
}

// ParseQuery reads the History list's query string.
func ParseQuery(params url.Values, now time.Time) history.Query {
	query := history.Query{
		From: nil, To: nil, Kinds: list(params, "kind"), Outcomes: list(params, "outcome"),
		Models: list(params, "model"), Projects: list(params, "project"), Repos: nil,
		Allow: nil, Branch: text(params, "branch"), Ref: text(params, "ref"), MinCost: nil,
		Text: text(params, "q"), Limit: nil, Offset: nil, ExcludeIDs: nil,
	}

	query.From, query.To = dateRange(params, now)
	query.Repos = reposParam(params)
	query.MinCost = minCostParam(params)
	query.Limit = limitParam(params)
	query.Offset = offsetParam(params)

	return query
}

// dateRange is when the listed sessions started: from, to, either nil for no bound.
func dateRange(params url.Values, now time.Time) (*float64, *float64) {
	nowMs := float64(now.UnixMilli())

	switch params.Get("range") {
	case "today":
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		from := float64(midnight.UnixMilli())

		return &from, nil
	case "7d":
		from := nowMs - weekDays*dayMs

		return &from, nil
	case "30d":
		from := nowMs - monthDays*dayMs

		return &from, nil
	case "custom":
		return dateParam(params, "from", false), dateParam(params, "to", true)
	}

	return nil, nil
}

// reposParam is repos as the spec names it; repo too, the way the other groups are spelled.
// Lowercase; nil for none.
func reposParam(params url.Values) []string {
	named := list(params, "repos")
	if len(named) == 0 {
		named = list(params, "repo")
	}

	if len(named) == 0 {
		return nil
	}

	out := make([]string, 0, len(named))

	for _, repo := range named {
		out = append(out, strings.ToLower(repo))
	}

	return out
}

func minCostParam(params url.Values) *float64 {
	if minCost := number(params, "minCost"); minCost != nil {
		if *minCost >= 0 {
			return minCost
		}
	}

	return nil
}

func limitParam(params url.Values) *float64 {
	limit := float64(defaultLimit)

	if asked := number(params, "limit"); asked != nil {
		if *asked >= 1 {
			limit = math.Min(math.Trunc(*asked), maxLimit)
		}
	}

	return &limit
}

func offsetParam(params url.Values) *float64 {
	if offset := number(params, "offset"); offset != nil {
		if *offset >= 0 {
			if *offset <= safeInteger {
				truncated := math.Trunc(*offset)

				return &truncated
			}
		}
	}

	return nil
}
