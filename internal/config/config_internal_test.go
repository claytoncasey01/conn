package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// updateGolden rewrites the golden files from the code: CONN_UPDATE_GOLDEN=1 go test ./... after
// a deliberate change, so the review shows the change's diff.
const updateGolden = "CONN_UPDATE_GOLDEN"

func lookupFrom(env map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		value, ok := env[key]

		return value, ok
	}
}

func TestDataDirDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no XDG_CONFIG_HOME", map[string]string{"HOME": "/home/jane"}, "/home/jane/.config/conn"},
		{
			"XDG_CONFIG_HOME set",
			map[string]string{"HOME": "/home/jane", "XDG_CONFIG_HOME": "/srv/cfg"},
			"/srv/cfg/conn",
		},
		{
			"XDG_CONFIG_HOME empty",
			map[string]string{"HOME": "/home/jane", "XDG_CONFIG_HOME": ""},
			"/home/jane/.config/conn",
		},
		{
			"XDG_CONFIG_HOME relative",
			map[string]string{"HOME": "/home/jane", "XDG_CONFIG_HOME": "cfg"},
			"/home/jane/.config/conn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FromEnv(lookupFrom(tt.env))
			if got.DataDir != tt.want {
				t.Errorf("DataDir = %q, want %q", got.DataDir, tt.want)
			}

			if got.DataDirDefault != tt.want {
				t.Errorf("DataDirDefault = %q, want %q", got.DataDirDefault, tt.want)
			}
		})
	}
}

func TestConnDataOverridesTheDefault(t *testing.T) {
	t.Parallel()

	got := FromEnv(lookupFrom(map[string]string{"HOME": "/home/jane", "CONN_DATA": "/scratch"}))
	if got.DataDir != "/scratch" {
		t.Errorf("DataDir = %q, want /scratch", got.DataDir)
	}

	if got.DataDirDefault != "/home/jane/.config/conn" {
		t.Errorf("DataDirDefault = %q, want the default folder", got.DataDirDefault)
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	got := FromEnv(lookupFrom(map[string]string{"HOME": "/home/jane"}))

	tests := []struct {
		name      string
		got, want any
	}{
		{"data", got.DataDir, "/home/jane/.config/conn"},
		{"themes", got.ThemesUser, "/home/jane/.config/conn/themes"},
		{"transcripts", got.TranscriptsRoot, "/home/jane/.claude/projects"},
		{"summary model", got.SummaryModel, Haiku},
		{"theme index", got.ThemeIndex, themeIndexDefault},
		{"review poll", got.ReviewPoll, 180 * time.Second},
		{"first window", got.ReviewFirstWindowDays, 2},
		{"scan model", got.ReviewScanModel, Haiku},
		{"scan cost", got.ReviewScanCostUSDMax, 0.5},
		{"scan timeout", got.ReviewScanTimeout, 90 * time.Second},
		{"dm scan", got.ReviewDMScanInterval, 600 * time.Second},
		{"close after", got.ReviewCloseAfter, 10 * time.Minute},
		{"close after max", got.ReviewCloseAfterMax, 60 * time.Minute},
		{"greptile bot", got.GreptileBot, "greptile-apps"},
		{"finding bots", strings.Join(got.FindingBots, ","), "greptile-apps,devin-ai-integration"},
		{"heads", strings.Join(got.ProtectedHeads, ","), "main,master,develop,staging,release/*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Fatalf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestEnvironment(t *testing.T) {
	t.Parallel()

	got := FromEnv(lookupFrom(map[string]string{
		"HOME": "/home/jane", "CONN_DATA": "/d", "CONN_TRANSCRIPTS": "/t",
		"CONN_SUMMARY_MODEL": "m", "CONN_THEME_INDEX": "off", "CONN_REVIEW_CLOSE_MIN": "5",
		"CONN_FINDING_BOTS": "x", "CONN_PROTECTED_HEADS": "y", "CONN_GREPTILE_BOT": "g",
	}))

	tests := []struct {
		name      string
		got, want any
	}{
		{"data", got.DataDir, "/d"},
		{"themes", got.ThemesUser, "/d/themes"},
		{"transcripts", got.TranscriptsRoot, "/t"},
		{"summary model", got.SummaryModel, "m"},
		{"theme index", got.ThemeIndex, ""},
		{"close after", got.ReviewCloseAfter, 5 * time.Minute},
		{"finding bots", strings.Join(got.FindingBots, ","), "x"},
		{"heads", strings.Join(got.ProtectedHeads, ","), "y"},
		{"greptile bot", got.GreptileBot, "g"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Fatalf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestASetButEmptyPathIsKept(t *testing.T) {
	t.Parallel()

	got := FromEnv(lookupFrom(map[string]string{"HOME": "/home/jane", "CONN_DATA": ""}))
	if got.DataDir != "" {
		t.Fatalf("got %q", got.DataDir)
	}
}

func TestNamePatterns(t *testing.T) {
	t.Parallel()

	settings := FromEnv(lookupFrom(nil))

	tests := []struct {
		name   string
		review bool
		self   bool
	}{
		{"pr-1234", true, false},
		{"pr-1234-2", true, false},
		{"pr-12ab", false, false},
		{"my-pr-1", false, false},
		{"self-widgets-core-12", false, true},
		{"self-widgets-core-12-2", false, true},
		{"self-x", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := settings.ReviewNamePattern.MatchString(tt.name); got != tt.review {
				t.Fatalf("review: got %v", got)
			}

			if got := settings.SelfNamePattern.MatchString(tt.name); got != tt.self {
				t.Fatalf("self: got %v", got)
			}
		})
	}
}

func TestCloseMinutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want int
	}{
		{"", 10},
		{"abc", 10},
		{"NaN", 10},
		{"0", 1},
		{"-5", 1},
		{"999", 60},
		{"Infinity", 10},
		{"25", 25},
		{"2.7", 2},
		{" 7 ", 7},
		{"  ", 10},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.raw), func(t *testing.T) {
			t.Parallel()

			if got := parseCloseMinutes(tt.raw); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parse func(string) []string
		raw   string
		want  []string
	}{
		{"bots", parseBots, " a , b,,c ", []string{"a", "b", "c"}},
		{"bots unset", parseBots, "", findingBotsDefault()},
		{"bots blank", parseBots, " , ", findingBotsDefault()},
		{"heads unset", parseHeads, "", protectedHeadsDefault()},
		{"heads blank", parseHeads, " , ", protectedHeadsDefault()},
		{"heads", parseHeads, " trunk , hotfix/* ,,", []string{"trunk", "hotfix/*"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.parse(tt.raw); !slices.Equal(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGreptileBot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw, want string
	}{
		{"", "greptile-apps"},
		{"  ", "greptile-apps"},
		{" my-greptile ", "my-greptile"},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.raw), func(t *testing.T) {
			t.Parallel()

			if got := parseGreptileBot(tt.raw); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestThemeIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw, want string
	}{
		{"off", ""},
		{" off ", ""},
		{"OFF", ""},
		{"Off", ""},
		{" off\n", ""},
		{"false", ""},
		{"FALSE", ""},
		{"False", ""},
		{"0", ""},
		{" 0 ", ""},
		{"none", ""},
		{"None", ""},
		{"no", ""},
		{"NO", ""},
		{"disabled", ""},
		{"Disabled", ""},
		{"\tdisabled\n", ""},
		{"", themeIndexDefault},
		{"   ", themeIndexDefault},
		{"me/mine", "me/mine"},
		{" me/my.repo_1 ", "me/my.repo_1"},
		{"none/no", "none/no"},
		{"0/false", "0/false"},
		// Only the whole value counts: these are not off-values, so they give the default.
		{"nonsense", themeIndexDefault},
		{"a/b/c", themeIndexDefault},
		{"../x", themeIndexDefault},
		{"me/..", themeIndexDefault},
		{"offline", themeIndexDefault},
		{"nope", themeIndexDefault},
		{"00", themeIndexDefault},
		{"on", themeIndexDefault},
		{"true", themeIndexDefault},
		{"1", themeIndexDefault},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.raw), func(t *testing.T) {
			t.Parallel()

			if got := parseThemeIndex(tt.raw); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The docs check reads the off-values from testdata/theme-index-off.json: the server's list is
// that list.
func TestThemeIndexOffIsTheSharedList(t *testing.T) {
	t.Parallel()

	text, err := os.ReadFile(filepath.Join("..", "..", "testdata", "theme-index-off.json"))
	if err != nil {
		t.Fatal(err)
	}

	var want []string

	err = json.Unmarshal(text, &want)
	if err != nil {
		t.Fatal(err)
	}

	if got := ThemeIndexOff(); !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The prompts are held by golden files, so a change to one shows in review as a diff.
func TestPromptsMatchTheirGoldenFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file, prompt string
	}{
		{"review-prompt.txt", reviewPrompt},
		{"re-review-line.txt", reReviewLine},
		{"self-review-prompt.txt", selfReviewPrompt},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("testdata", tt.file)
			if os.Getenv(updateGolden) != "" {
				err := os.WriteFile(path, []byte(tt.prompt), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if tt.prompt != string(want) {
				t.Fatalf("%s differs from the prompt; after a deliberate change run "+
					"%s=1 go test ./internal/config", path, updateGolden)
			}
		})
	}
}

func TestPromptsHaveTheirSlots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prompt, slot string
	}{
		{reviewPrompt, "pull request #{pr} in the GitHub repository {repo}"},
		{reReviewLine, "{sha}"},
		{selfReviewPrompt, "{worktree}"},
		{selfReviewPrompt, "{branch}:{headRef}"},
		{selfReviewPrompt, "round {round}"},
	}
	for _, tt := range tests {
		t.Run(tt.slot, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(tt.prompt, tt.slot) {
				t.Fatal("missing")
			}
		})
	}
}
