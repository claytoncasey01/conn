package desk_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/desk"
)

func TestLinkTarget(t *testing.T) {
	t.Parallel()

	const server = "http://127.0.0.1:7777"

	tests := []struct {
		name      string
		rawURL    string
		serverURL string
		want      desk.Link
	}{
		{
			"a page of the server stays in the window",
			server + "/#/history", server, desk.LinkInApp,
		},
		{
			"the server's origin in another case stays",
			"HTTP://127.0.0.1:7777/x", server, desk.LinkInApp,
		},
		{
			"a pull request opens in the browser",
			"https://github.com/o/r/pull/1", server, desk.LinkBrowser,
		},
		{
			"a Slack thread opens in the browser",
			"https://x.slack.com/archives/C1/p1", server, desk.LinkBrowser,
		},
		{
			"another port on loopback is another site",
			"http://127.0.0.1:3000/", server, desk.LinkBrowser,
		},
		{
			"the server's host by name is another origin",
			"http://localhost:7777/", server, desk.LinkBrowser,
		},
		{
			"https on the server's port is another origin",
			"https://127.0.0.1:7777/", server, desk.LinkBrowser,
		},
		{
			"any web page opens in the browser on the splash",
			"https://github.com/", "", desk.LinkBrowser,
		},
		{
			"a server address that cannot be read sends web pages out",
			"https://github.com/", "%zz", desk.LinkBrowser,
		},
		{
			"a file is refused",
			"file:///etc/passwd", server, desk.LinkRefused,
		},
		{
			"a mail link is refused",
			"mailto:someone@example.com", server, desk.LinkRefused,
		},
		{
			"another app's scheme is refused",
			"slack://channel?id=C1", server, desk.LinkRefused,
		},
		{
			"javascript is refused",
			"javascript:alert(1)", server, desk.LinkRefused,
		},
		{
			"an http address with no host is refused",
			"http:///path", server, desk.LinkRefused,
		},
		{
			"text that is not an address is refused",
			"http://[::1", server, desk.LinkRefused,
		},
		{
			"nothing is refused",
			"", server, desk.LinkRefused,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := desk.LinkTarget(tt.rawURL, tt.serverURL)
			if got != tt.want {
				t.Errorf("LinkTarget(%q, %q) = %v, want %v", tt.rawURL, tt.serverURL, got, tt.want)
			}
		})
	}
}
