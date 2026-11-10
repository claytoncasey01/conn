package newsession_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/newsession"
)

const (
	conversationID = "0b6c4f7e-2d1a-4c3b-9e8f-112233445566"
	claudeProgram  = "/opt/claude/bin/claude"
	cloneFolder    = "/work/widgets"
	// idsTried is how many fresh ids TestNewIDIsAFreshUUID draws.
	idsTried = 100
)

// object is a JSON object's text read as a request body.
func object(t *testing.T, text string) *js.Object {
	t.Helper()

	parsed, err := js.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	body, isObject := parsed.(*js.Object)
	if !isObject {
		t.Fatalf("%s is not an object", text)
	}

	return body
}

func TestParseRequestReadsOnlyARepositoryANameAndAnAdvisor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body       string
		wantRepo         string
		wantName         string
		wantProblemsHave []string
	}{
		{"a repository", `{"repo":"acme/widgets"}`, "acme/widgets", "", nil},
		{
			"a name", `{"repo":"acme/widgets","name":"fix the widget"}`, "acme/widgets",
			"fix-the-widget", nil,
		},
		{"a null name", `{"repo":"acme/widgets","name":null}`, "acme/widgets", "", nil},
		{"an empty name", `{"repo":"acme/widgets","name":""}`, "acme/widgets", "", nil},
		{"no repository", `{}`, "", "", []string{"Choose a repository."}},
		{"a repository that is not text", `{"repo":7}`, "", "", []string{"owner/name"}},
		{"a path for a repository", `{"repo":"/etc"}`, "", "", []string{"owner/name"}},
		{"a flag in the repository", `{"repo":"acme/widgets --x"}`, "", "", []string{
			"owner/name",
		}},
		{"a climbing repository", `{"repo":"../widgets"}`, "", "", []string{"owner/name"}},
		{
			"a name that is not text", `{"repo":"acme/widgets","name":["x"]}`, "acme/widgets", "",
			[]string{"The name must be text."},
		},
		{
			"a folder", `{"repo":"acme/widgets","dir":"/tmp"}`, "acme/widgets", "",
			[]string{`"dir" is not a field`},
		},
		{
			"an argument list", `{"repo":"acme/widgets","argv":["sh"]}`, "acme/widgets", "",
			[]string{`"argv" is not a field`},
		},
		{"an advisor", `{"repo":"acme/widgets","advisor":"fable"}`, "acme/widgets", "", nil},
		{"a null advisor", `{"repo":"acme/widgets","advisor":null}`, "acme/widgets", "", nil},
		{
			"an advisor that is no choice", `{"repo":"acme/widgets","advisor":"--x"}`,
			"acme/widgets", "",
			[]string{"The advisor must be claude-code, fable, opus or sonnet."},
		},
		{"every problem at once", `{"repo":1,"name":2,"env":{},"advisor":7}`, "", "", []string{
			`"env" is not a field`, "owner/name", "The name must be text.", "The advisor must be",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request, problems := newsession.ParseRequest(object(t, tt.body))
			if len(problems) != len(tt.wantProblemsHave) {
				t.Fatalf("problems %q, want %d", problems, len(tt.wantProblemsHave))
			}

			for index, want := range tt.wantProblemsHave {
				if !strings.Contains(problems[index], want) {
					t.Errorf("problem %q does not say %q", problems[index], want)
				}
			}

			if request.Repo != tt.wantRepo {
				t.Errorf("repo %q, want %q", request.Repo, tt.wantRepo)
			}

			if request.Name != tt.wantName {
				t.Errorf("name %q, want %q", request.Name, tt.wantName)
			}
		})
	}
}

func TestSafeNameIsLaunchsRuleThenTheSafeAlphabet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, given, want string
		wantErr           error
	}{
		{"words", "fix the widget", "fix-the-widget", nil},
		{"a flag", "--dangerously-skip-permissions", "dangerously-skip-permissions", nil},
		{"a shell", "$(rm -rf ~); echo", "rm-rf-echo", nil},
		{"a hidden file", ".bashrc", "bashrc", nil},
		{"letters with no ASCII", "\u65e5\u672c", "", launch.ErrInvalidSession},
		{"only punctuation", "--", "", launch.ErrInvalidSession},
		{"an escape", "a\x1b[2Jb", "", launch.ErrInvalidSession},
		{"a line break", "a\nb", "", launch.ErrInvalidSession},
		{"a bidi override", "a\u202eb", "", launch.ErrInvalidSession},
		{"a zero-width space", "a\u200bb", "", launch.ErrInvalidSession},
		{"too long", strings.Repeat("a", 257), "", launch.ErrInvalidSession},
		{"the longest", strings.Repeat("a", 256), strings.Repeat("a", 64), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := newsession.SafeName(tt.given)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("name %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSpecIsExactlyClaudeOnTheChosenConversation: the argv is the program, --session-id and the
// id, and --name with the safe name only when one was given; nothing else.
func TestSpecIsExactlyClaudeOnTheChosenConversation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  newsession.Request
		wantArgv []string
		wantName string
	}{
		{
			"named",
			newsession.Request{
				Repo: "acme/widgets", Name: "fix-the-widget", Advisor: "claude-code",
			},
			[]string{claudeProgram, "--session-id", conversationID, "--name", "fix-the-widget"},
			"fix-the-widget",
		},
		{
			"unnamed",
			newsession.Request{Repo: "acme/widgets", Name: "", Advisor: "claude-code"},
			[]string{claudeProgram, "--session-id", conversationID},
			"widgets",
		},
		{
			"an advisor of its own",
			newsession.Request{Repo: "acme/widgets", Name: "fix", Advisor: "fable"},
			[]string{
				claudeProgram, "--session-id", conversationID, "--name", "fix",
				"--settings", `{"advisorModel":"fable"}`,
			},
			"fix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := newsession.Spec(claudeProgram, conversationID, cloneFolder, tt.request)
			if !slices.Equal(spec.Argv, tt.wantArgv) {
				t.Errorf("argv %q, want %q", spec.Argv, tt.wantArgv)
			}

			if spec.Dir != cloneFolder {
				t.Errorf("folder %q, want %q", spec.Dir, cloneFolder)
			}

			if spec.Name != tt.wantName {
				t.Errorf("name %q, want %q", spec.Name, tt.wantName)
			}

			if spec.Env != nil {
				t.Errorf("environment %q, want none", spec.Env)
			}

			if launch.Check(spec) != nil {
				t.Errorf("launch refuses it: %v", launch.Check(spec))
			}
		})
	}
}

func TestSpecPanicsOnAnIDThatIsNotAUUID(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()

	newsession.Spec(claudeProgram, "--continue", cloneFolder, newsession.Request{
		Repo: "acme/widgets", Name: "", Advisor: "claude-code",
	})
}

func TestNewIDIsAFreshUUID(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range idsTried {
		drawn, err := newsession.NewID()
		if err != nil {
			t.Fatal(err)
		}

		if !cmux.IsSessionID(drawn) {
			t.Fatalf("%q is not a UUID", drawn)
		}

		if drawn[14] != '4' {
			t.Errorf("%q is not version 4", drawn)
		}

		if !strings.ContainsRune("89ab", rune(drawn[19])) {
			t.Errorf("%q is not the RFC variant", drawn)
		}

		if seen[drawn] {
			t.Errorf("%q drawn twice", drawn)
		}

		seen[drawn] = true
	}
}
