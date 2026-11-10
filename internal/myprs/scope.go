package myprs

import "github.com/claytoncasey01/conn/internal/settings"

// Settings is the settings holder: read again on every call, never kept.
type Settings interface {
	Get() settings.Settings
	Setup() bool
	ModuleEnabled(name string) bool
}

// ScopeOf is the scope of current. Setup is true while there is no allowlist; the modules are
// the settings' own switches, all off in setup; the draft channel is the setting, else the
// first Slack channel, else nil (no draft); the advisor is the sessions' setting.
func ScopeOf(current settings.Settings, setup bool, enabled func(name string) bool) Scope {
	allow := current.Repos.Allow
	if allow == nil {
		allow = []string{}
	}

	channel := current.Slack.DraftChannel
	if channel == nil {
		if len(current.Slack.Channels) > 0 {
			first := current.Slack.Channels[0]
			channel = &first
		}
	}

	return Scope{
		Setup: setup, Allow: allow, GitHub: enabled(settings.ModuleGitHub),
		Slack: enabled(settings.ModuleSlack), DraftChannel: channel,
		Advisor: current.Sessions.Advisor,
	}
}

// ScopeNow is the scope of the holder now: answered fresh on every call, never kept.
func ScopeNow(holder Settings) func() Scope {
	return func() Scope { return ScopeOf(holder.Get(), holder.Setup(), holder.ModuleEnabled) }
}
