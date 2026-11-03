package history

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

const (
	limitDefault = 50
	limitMax     = 500
	safeInteger  = 1<<53 - 1
)

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

// Row is one line of the History list: everything the list shows, nothing that needs the
// stored detail.
type Row struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Project      string   `json:"project"`
	Repo         *string  `json:"repo"`
	Branch       string   `json:"branch"`
	StartedAt    float64  `json:"startedAt"`
	EndedAt      float64  `json:"endedAt"`
	Model        string   `json:"model"`
	CostUSD      float64  `json:"costUsd"`
	Agents       float64  `json:"agents"`
	AdvisorCalls float64  `json:"advisorCalls"`
	Errors       float64  `json:"errors"`
	Outcome      string   `json:"outcome"`
	PRNumber     *float64 `json:"prNumber"`
	TicketKey    *string  `json:"ticketKey"`
	Source       string   `json:"source"`
	Glance       string   `json:"glance"`
	SummaryState string   `json:"summaryState"`
}

const rowColumns = `e.id, e.name, e.kind, e.project, e.repo, e.branch, e.started_at, e.ended_at,
  e.model, e.cost_usd, e.agents, e.advisor_calls, e.errors, e.outcome, e.pr_number, e.ticket_key,
  e.source, e.glance, e.summary_state`

// scanner is a row or rows to scan from.
type scanner interface {
	Scan(dest ...any) error
}

func scanRow(from scanner, extra ...any) (Row, error) {
	var (
		row          Row
		repo, ticket sql.NullString
		prNumber     sql.NullFloat64
	)

	err := from.Scan(append([]any{
		&row.ID, &row.Name, &row.Kind, &row.Project, &repo, &row.Branch, &row.StartedAt,
		&row.EndedAt, &row.Model, &row.CostUSD, &row.Agents, &row.AdvisorCalls, &row.Errors,
		&row.Outcome, &prNumber, &ticket, &row.Source, &row.Glance, &row.SummaryState,
	}, extra...)...)
	if err != nil {
		return Row{}, fmt.Errorf("reading a row: %w", err)
	}

	if repo.Valid {
		row.Repo = &repo.String
	}

	if ticket.Valid {
		row.TicketKey = &ticket.String
	}

	if prNumber.Valid {
		row.PRNumber = &prNumber.Float64
	}

	return row, nil
}

// Query is what the History list is filtered by. A nil field filters nothing.
type Query struct {
	// From and To bound when sessions started (ms).
	From, To                          *float64
	Kinds, Outcomes, Models, Projects []string
	// Repos are lowercase owner/repo names.
	Repos []string
	// Allow is the allowlist: given (even empty), a row shows only when it is in no repo or in a
	// repo it allows, in the rows, the total, every count and every facet. Nil holds nothing back.
	Allow *[]string
	// Branch is a case-insensitive substring of the branch name.
	Branch string
	// Ref is a PR number (42 or #42) or a ticket key, any case.
	Ref     string
	MinCost *float64
	// Text is free text over the search text and the summary; blank is no text filter.
	Text          string
	Limit, Offset *float64
	// ExcludeIDs are left out of the rows, the total and every count: the sessions still running.
	ExcludeIDs []string
}

// List is one page of the History list.
type List struct {
	Rows   []Row      `json:"rows"`
	Total  float64    `json:"total"`
	Counts *js.Object `json:"counts"`
	Facets Facets     `json:"facets"`
}

// Facets are what the filter bar offers.
type Facets struct {
	Models   []string `json:"models"`
	Projects []string `json:"projects"`
	Repos    []string `json:"repos"`
}

// FTSPhrase is every whitespace-separated token as a quoted FTS5 phrase, with embedded quotes
// doubled, joined by spaces (an implicit AND). Quoting makes operators, NEAR(, a lone * or ^ and
// unbalanced quotes plain text. Blank input gives "" and the caller must then skip the MATCH.
func FTSPhrase(text string) string {
	words := strings.FieldsFunc(strings.ReplaceAll(text, "\x00", ""), js.IsSpace)
	quoted := make([]string, 0, len(words))

	for _, word := range words {
		quoted = append(quoted, `"`+strings.ReplaceAll(word, `"`, `""`)+`"`)
	}

	return strings.Join(quoted, " ")
}

func likeEscape(text string) string {
	var builder strings.Builder

	for _, char := range text {
		if strings.ContainsRune(`\%_`, char) {
			builder.WriteByte('\\')
		}

		builder.WriteRune(char)
	}

	return builder.String()
}

// clause is SQL with its parameters.
type clause struct {
	sql    []string
	params []any
}

func (c *clause) add(sql string, params ...any) {
	c.sql = append(c.sql, sql)
	c.params = append(c.params, params...)
}

func (c *clause) in(column string, values []string) {
	if len(values) == 0 {
		return
	}

	marks := strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", ")
	params := make([]any, 0, len(values))

	for _, value := range values {
		params = append(params, value)
	}

	c.add(column+" IN ("+marks+")", params...)
}

// statement joins fixed SQL text: the columns, clauses and placeholders this package writes.
// Values never go into it; they go to the driver as parameters.
func statement(parts ...string) string {
	return strings.Join(parts, "")
}

func (c *clause) where() string {
	if len(c.sql) == 0 {
		return ""
	}

	return "WHERE " + strings.Join(c.sql, " AND ")
}

// visibleClause keeps the rows in no repo and those in one of visible: one JSON parameter, so the
// list's length never meets SQLite's variable limit.
func visibleClause(visible []string, alias string) (string, string) {
	return "(" + alias + "repo IS NULL OR " + alias + "repo IN (SELECT value FROM json_each(?)))",
		string(js.Marshal(visible))
}

// filters are a query's WHERE clauses, leaving out one group's own filter (for that group's
// counts). The allowlist is never left out: no count and no facet may speak of a row the page may
// not show.
func filters(query Query, visible []string, skip string) clause {
	var where clause

	if visible != nil {
		sql, param := visibleClause(visible, "e.")
		where.add(sql, param)
	}

	if query.From != nil {
		where.add("e.started_at >= ?", *query.From)
	}

	if query.To != nil {
		where.add("e.started_at <= ?", *query.To)
	}

	for _, group := range []struct {
		name, column string
		values       []string
	}{
		{"kind", "e.kind", query.Kinds},
		{"outcome", "e.outcome", query.Outcomes},
		{"model", "e.model", query.Models},
		{"project", "e.project", query.Projects},
		{repoColumn, "e.repo", lowered(query.Repos)},
	} {
		if group.name != skip {
			where.in(group.column, group.values)
		}
	}

	if len(query.ExcludeIDs) > 0 {
		where.add("e.id NOT IN (SELECT value FROM json_each(?))",
			string(js.Marshal(query.ExcludeIDs)))
	}

	refine(&where, query)

	return where
}

func lowered(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.ToLower(value))
	}

	return out
}

// refine adds the branch, ref, cost and text filters.
func refine(where *clause, query Query) {
	if branch := js.Trim(query.Branch); branch != "" {
		where.add(`e.branch LIKE ? ESCAPE '\'`, "%"+likeEscape(branch)+"%")
	}

	ref := strings.TrimPrefix(js.Trim(query.Ref), "#")
	if ref != "" {
		if digitsOnly.MatchString(ref) {
			value, _ := strconv.ParseFloat(ref, 64)
			where.add("(e.pr_number = ? OR e.ticket_key = ?)", number(value), ref)
		} else {
			where.add("e.ticket_key = ?", ref)
		}
	}

	if query.MinCost != nil {
		where.add("e.cost_usd >= ?", *query.MinCost)
	}

	if phrase := FTSPhrase(query.Text); phrase != "" {
		where.add("e.rowid IN (SELECT rowid FROM entries_fts WHERE entries_fts MATCH ?)", phrase)
	}
}

// whole is a number SQLite can take as an integer, else the default: 1e20 or NaN is junk.
func whole(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}

	if math.IsNaN(*value) {
		return fallback
	}

	truncated := math.Trunc(*value)
	if math.Abs(truncated) > safeInteger {
		return fallback
	}

	return truncated
}

// List is one page of rows under a query, with the total, the counts and the facets.
func (d *DB) List(ctx context.Context, query Query) (List, error) {
	var visible []string

	if query.Allow != nil {
		all, err := d.Repos(ctx)
		if err != nil {
			return List{}, err
		}

		visible = []string{}

		for _, repo := range all {
			if repos.Allowed(repo, *query.Allow) {
				visible = append(visible, repo)
			}
		}
	}

	page, total, err := d.page(ctx, query, visible)
	if err != nil {
		return List{}, err
	}

	counts, err := d.counts(ctx, query, visible)
	if err != nil {
		return List{}, err
	}

	facets, err := d.facets(ctx, visible)
	if err != nil {
		return List{}, err
	}

	return List{Rows: page, Total: total, Counts: counts, Facets: facets}, nil
}

func (d *DB) page(ctx context.Context, query Query, visible []string) ([]Row, float64, error) {
	where := filters(query, visible, "")
	limit := min(max(whole(query.Limit, limitDefault), 0), limitMax)
	offset := max(whole(query.Offset, 0), 0)

	rows, err := d.db.QueryContext(ctx, statement("SELECT ", rowColumns, " FROM entries e ",
		where.where(), " ORDER BY e.started_at DESC, e.id DESC LIMIT ? OFFSET ?"),
		append(where.params, int64(limit), int64(offset))...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing: %w", err)
	}

	defer func() { _ = rows.Close() }()

	page := []Row{}

	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, 0, err
		}

		page = append(page, row)
	}

	var total float64

	err = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entries e "+where.where(),
		where.params...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("counting: %w", err)
	}

	err = rows.Err()
	if err != nil {
		return nil, 0, fmt.Errorf("listing: %w", err)
	}

	return page, total, nil
}

// counts are, per group, the count of each value under every filter but that group's own. A row
// in no repo has no chip: it is counted in no repo's number.
func (d *DB) counts(ctx context.Context, query Query, visible []string) (*js.Object, error) {
	counts := js.NewObject()

	for _, group := range []string{"kind", "outcome", "model", "project", repoColumn} {
		values, err := d.countBy(ctx, group, filters(query, visible, group))
		if err != nil {
			return nil, err
		}

		counts.Set(group, values)
	}

	return counts, nil
}

// countBy is the count of each value of one column under a clause.
func (d *DB) countBy(ctx context.Context, group string, where clause) (*js.Object, error) {
	values := js.NewObject()

	rows, err := d.db.QueryContext(ctx, statement("SELECT e.", group, ", COUNT(*) FROM entries e ",
		where.where(), " GROUP BY e.", group), where.params...)
	if err != nil {
		return nil, fmt.Errorf("counting %s: %w", group, err)
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			value sql.NullString
			count float64
		)

		err = rows.Scan(&value, &count)
		if err != nil {
			return nil, fmt.Errorf("reading a count: %w", err)
		}

		if value.Valid {
			values.Set(value.String, count)
		}
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("counting %s: %w", group, err)
	}

	return values, nil
}

func (d *DB) facets(ctx context.Context, visible []string) (Facets, error) {
	distinct := func(column string) ([]string, error) {
		query := "SELECT DISTINCT " + column + " FROM entries WHERE " + column + " <> ''"
		params := []any{}

		if visible != nil {
			sql, param := visibleClause(visible, "")
			query += " AND " + sql

			params = append(params, param)
		}

		return strings1(ctx, d.db, query+" ORDER BY "+column, params...)
	}

	models, err := distinct("model")
	if err != nil {
		return Facets{}, err
	}

	projects, err := distinct("project")
	if err != nil {
		return Facets{}, err
	}

	repoFacet := visible
	if repoFacet == nil {
		repoFacet, err = d.Repos(ctx)
		if err != nil {
			return Facets{}, err
		}
	}

	return Facets{Models: models, Projects: projects, Repos: repoFacet}, nil
}

// Repos are every repo any row holds, for the Settings page's suggestions: it must offer a repo
// that is hidden now, so it can be added back.
func (d *DB) Repos(ctx context.Context) ([]string, error) {
	return strings1(ctx, d.db,
		"SELECT DISTINCT repo FROM entries WHERE repo IS NOT NULL ORDER BY repo")
}
