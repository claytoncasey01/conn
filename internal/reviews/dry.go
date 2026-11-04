package reviews

import "strconv"

// The dry view's PRs: their numbers, and how many hours ago each was opened or last changed.
const (
	dryRetryNumber      = 4101
	dryPaginateNumber   = 4098
	dryScriptNumber     = 4110
	dryRetryDMNumber    = 4115
	dryLinterNumber     = 12
	dryBumpNumber       = 4112
	dryCacheNumber      = 4105
	dryRenameNumber     = 4090
	dryImporterNumber   = 4095
	dryExportNumber     = 4080
	dryLegacyNumber     = 4070
	dryCreatedHours     = 6
	dryRetryHours       = 0.2
	dryScriptHours      = 0.1
	drySameNumberHours  = 0.15
	dryWaitingHours     = 5
	dryImporterHours    = 2
	dryDoneHours        = 8
	dryMergedHours      = 3
	dryAuthorAna        = "ana-p"
	dryRepoWidgets      = "acme/widgets"
	dryRepoGadgets      = "acme/gadgets"
	dryNoCloneRepo      = "jane/tools"
	dryNoCloneRepoWhy   = "No local clone of " + dryNoCloneRepo + ": set a folder in Settings"
	dryRetryDMSlackLink = "https://example.slack.com/archives/D0/p4115"
	// dryHourMs is an hour, in milliseconds.
	dryHourMs = 3_600_000
	// The dry view's two old records the migration could not place.
	dryLegacyWidgetsNumber = 3021
	dryLegacyOtherNumber   = 2990
)

// dryAt is now less some hours, as JavaScript computes it: the product rounded on its own.
func dryAt(now, hours, hour float64) float64 { return now - float64(hours*hour) }

func dryRow(now, hour float64, repo string, number int64, title string) *Row {
	digits := strconv.FormatInt(number, 10)
	slackURL := "https://example.slack.com/archives/C0/p" + digits

	return &Row{
		Key: repo + "#" + digits, Repo: repo, NoClone: nil, Number: number, Title: title,
		Author: "sam-dev", URL: "https://github.com/" + repo + "/pull/" + digits, BaseRef: "main",
		State: StateNew, ReAsked: nil, Updated: false, SlackURL: &slackURL, DMURL: nil,
		InChannel: true, Checks: "passing", CreatedAt: dryAt(now, dryCreatedHours, hour),
		UpdatedAt: dryAt(now, 1, hour), Ended: nil, Session: nil, Launches: 0, Stale: false,
	}
}

// dryRows are the dry view's rows, before they are sorted.
func dryRows(now, hour float64) []*Row {
	rereviewGitHub := dryRow(now, hour, dryRepoWidgets, dryRetryNumber, "Retry failed webhooks")
	rereviewGitHub.Author, rereviewGitHub.State = "maria-k", StateRereview
	rereviewGitHub.ReAsked, rereviewGitHub.Launches = new(askedGitHub), 1
	rereviewGitHub.UpdatedAt = dryAt(now, dryRetryHours, hour)

	rereviewSlack := dryRow(now, hour, dryRepoGadgets, dryPaginateNumber, "Paginate the audit log")
	rereviewSlack.Author, rereviewSlack.State = "li-wen", StateRereview
	rereviewSlack.ReAsked, rereviewSlack.Updated = new(askedSlack), true
	rereviewSlack.Checks, rereviewSlack.Launches = checksFailing, 1

	script := dryRow(now, hour, dryRepoWidgets, dryScriptNumber,
		"Fix <script>alert(1)</script> rendering in the header")
	script.Checks, script.UpdatedAt = checksPending, dryAt(now, dryScriptHours, hour)

	sameNumber := dryRow(now, hour, dryRepoGadgets, dryScriptNumber,
		"Same number, another repo: the gadgets list")
	sameNumber.Author, sameNumber.SlackURL, sameNumber.InChannel = dryAuthorAna, nil, false
	sameNumber.UpdatedAt = dryAt(now, drySameNumberHours, hour)

	byDM := dryRow(now, hour, dryRepoWidgets, dryRetryDMNumber,
		"Add a retry budget to the exporter")
	byDM.Author, byDM.SlackURL = dryAuthorAna, nil
	byDM.DMURL = new(dryRetryDMSlackLink)

	noClone := dryRow(now, hour, dryNoCloneRepo, dryLinterNumber, "Teach the linter a new rule")
	noClone.Author, noClone.SlackURL = "jane", nil
	noClone.NoClone = new(dryNoCloneRepoWhy)

	bot := dryRow(now, hour, dryRepoWidgets, dryBumpNumber, "Bump lodash from 4.17.20 to 4.17.21")
	bot.Author, bot.SlackURL, bot.Checks = "dependabot[bot]", nil, checksNone

	return append([]*Row{rereviewGitHub, rereviewSlack, script, sameNumber, byDM, noClone, bot},
		dryLater(now, hour)...)
}

// dryLater are the dry rows past new: reviewing, waiting and done.
func dryLater(now, hour float64) []*Row {
	reviewing := dryRow(now, hour, dryRepoWidgets, dryCacheNumber, "Cache the plan lookups")
	reviewing.Author, reviewing.State, reviewing.Launches = dryAuthorAna, StateReviewing, 1
	reviewing.Session = &RowSession{Name: "pr-4105-acme--widgets", Status: "busy"}

	waiting := dryRow(now, hour, dryRepoGadgets, dryRenameNumber, "Rename the billing flags")
	waiting.Author, waiting.State, waiting.Launches = "li-wen", StateWaiting, 1
	waiting.UpdatedAt = dryAt(now, dryWaitingHours, hour)

	updated := dryRow(now, hour, dryRepoWidgets, dryImporterNumber, "Move the importer to a queue")
	updated.Author, updated.State, updated.Updated = "maria-k", StateWaiting, true
	updated.Launches, updated.UpdatedAt = 1, dryAt(now, dryImporterHours, hour)

	done := dryRow(now, hour, dryRepoWidgets, dryExportNumber, "Add the export button")
	done.State, done.Launches, done.UpdatedAt = StateDone, 1, dryAt(now, dryDoneHours, hour)

	merged := dryRow(now, hour, dryRepoGadgets, dryLegacyNumber, "Drop the legacy endpoint")
	merged.Author, merged.State, merged.Ended = dryAuthorAna, StateDone, new(EndedMerged)
	merged.Launches, merged.UpdatedAt = 1, dryAt(now, dryMergedHours, hour)

	return []*Row{reviewing, waiting, updated, done, merged}
}
