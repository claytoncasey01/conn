package transcript_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/transcript"
)

// reply is an assistant line whose message is messageID: its own advisorModel key when advisor
// is not "", and an advisor call on the model called when called is not "".
func reply(messageID, advisor, called string) string {
	iterations := `[]`
	if called != "" {
		iterations = `[{"type":"advisor_message","model":"` + called + `","input_tokens":10,` +
			`"output_tokens":5}]`
	}

	line := `{"type":"assistant","timestamp":"2026-11-09T12:00:00Z",`
	if advisor != "" {
		line += `"advisorModel":"` + advisor + `",`
	}

	return line + `"message":{"id":"` + messageID + `","model":"claude-sonnet-5-5","content":[],` +
		`"usage":{"input_tokens":1,"output_tokens":1,"iterations":` + iterations + `}}}`
}

func TestTheAdvisorModelIsTheOneTheSessionRuns(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "no reply yet", lines: nil, want: ""},
		{
			name:  "a reply that names no advisor and no call",
			lines: []string{reply("m1", "", "")},
			want:  "",
		},
		{
			name:  "a reply names the advisor before any call",
			lines: []string{reply("m1", "claude-fable-5-2", "")},
			want:  "claude-fable-5-2",
		},
		{
			name:  "an older Claude Code: only the call names it",
			lines: []string{reply("m1", "", "claude-opus-5-5")},
			want:  "claude-opus-5-5",
		},
		{
			name: "the replies' advisor wins over a call's",
			lines: []string{
				reply("m1", "claude-fable-5-2", "claude-opus-5-5"),
			},
			want: "claude-fable-5-2",
		},
		{
			name:  "a synthetic advisor is no model",
			lines: []string{reply("m1", "<synthetic>", "<synthetic>")},
			want:  "",
		},
		{
			name: "a synthetic advisor leaves the real one",
			lines: []string{
				reply("m1", "claude-opus-5-5", ""), reply("m2", "<synthetic>", "<synthetic>"),
			},
			want: "claude-opus-5-5",
		},
		{
			name: "the latest reply that names one",
			lines: []string{
				reply("m1", "claude-opus-5-5", ""), reply("m2", "claude-fable-5-2", ""),
				reply("m3", "", ""),
			},
			want: "claude-fable-5-2",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			lead := transcript.NewLeadState(0)

			for _, line := range row.lines {
				parsed, err := js.Parse([]byte(line))
				if err != nil {
					t.Fatal(err)
				}

				lead.Feed(parsed)
			}

			got := lead.AdvisorModel()
			if got != row.want {
				t.Errorf("AdvisorModel() = %q, want %q", got, row.want)
			}
		})
	}
}
