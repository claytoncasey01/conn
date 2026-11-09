package reviews

import (
	"testing"
)

// withGroups is pat with the group file text.
func withGroups(text string) func() Person {
	return func() Person {
		who := pat()
		who.groups = text

		return who
	}
}

// withPasswd is pat with the passwd file text.
func withPasswd(text string) func() Person {
	return func() Person {
		who := pat()
		who.passwd = text

		return who
	}
}

// TestAPrivateGroupIsTheirsAlone: the person's own group id, every line about it in the group
// file theirs alone, and no other account whose primary group it is; any doubt is shared.
func TestAPrivateGroupIsTheirsAlone(t *testing.T) {
	t.Parallel()

	const ownAccount = "pat:x:501:1001:Pat Smith:/home/pat:/bin/bash\n"

	tests := []struct {
		name string
		who  func() Person
		gid  uint32
		want bool
	}{
		{"the private group", pat, personGID, true},
		{"listing only the person", withGroups("pat:x:1001:pat\n"), personGID, true},
		{"macOS's staff", func() Person {
			who := pat()
			who.gid = staffGID

			return who
		}, staffGID, false},
		{"not the person's group id", pat, 1002, false},
		{"a second member", withGroups("pat:x:1001:pat,sam\n"), personGID, false},
		{"an unreadable group file", withGroups(""), personGID, false},
		{"a group the file does not list (NSS)", withGroups("root:x:0:\n"), personGID, false},
		{"no name for the person", func() Person {
			who := pat()
			who.name = ""

			return who
		}, personGID, false},
		// The group file, every line about the group.
		{
			"a duplicate id whose second group lists sam",
			withGroups("pat:x:1001:\nteam:x:1001:sam\n"), personGID, false,
		},
		{
			"a duplicate id, both lines the person's", withGroups("pat:x:1001:\npat:x:1001:pat\n"),
			personGID, true,
		},
		{
			"a comment naming another member", withGroups("#pat:x:1001:bob\npat:x:1001:\n"),
			personGID, true,
		},
		{"CRLF line ends", withGroups("pat:x:1001:\r\n"), personGID, false},
		{
			"a line about the group with 5 fields", withGroups("pat:x:1001::extra\n"),
			personGID, false,
		},
		{
			"a line with too few fields to name an id", withGroups("pat:x\npat:x:1001:\n"),
			personGID, true,
		},
		{
			"a line naming the id with a field missing", withGroups("pat:x:1001\n"),
			personGID, false,
		},
		{"a leading-zero id", withGroups("pat:x:1001:\nteam:x:01001:sam\n"), personGID, false},
		{"a padded id", withGroups("pat:x:1001:\nteam:x: 1001:sam\n"), personGID, false},
		// The passwd file: no other account with the group as its primary group.
		{
			"another account whose primary group it is (useradd -g pat sam)",
			withPasswd(ownAccount + "sam:x:502:1001::/home/sam:/bin/sh\n"), personGID, false,
		},
		{"the person alone has it", withPasswd(ownAccount), personGID, true},
		{
			"an account of the person's name with another uid",
			withPasswd(ownAccount + "pat:x:777:1001::/:/bin/sh\n"), personGID, false,
		},
		{
			"a malformed passwd line that may have the group",
			withPasswd(ownAccount + "sam:x:502:1001:/bin/sh\n"), personGID, false,
		},
		{
			"a malformed passwd line about another group",
			withPasswd(ownAccount + "sam:x:502:1002:/bin/sh\n"), personGID, true,
		},
		{
			"a passwd line too short to name a group", withPasswd(ownAccount + "sam:x:502\n"),
			personGID, true,
		},
		{"an unreadable passwd file", withPasswd(""), personGID, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := privateGroup(tt.who(), tt.gid); got != tt.want {
				t.Errorf("private %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWhoRunsReadsEachFileOnce: the person is read once per request, each file once.
func TestWhoRunsReadsEachFileOnce(t *testing.T) {
	t.Parallel()

	reads := map[string]int{}
	who := whoRunsWith(func(path string) string {
		reads[path]++

		return ""
	}, func() string { return "pat" })

	if reads[groupFile] != 1 {
		t.Errorf("read %s %d times", groupFile, reads[groupFile])
	}

	if reads[passwdFile] != 1 {
		t.Errorf("read %s %d times", passwdFile, reads[passwdFile])
	}

	if who.name != "pat" {
		t.Errorf("name %q", who.name)
	}
}

// TestAMissingFileIsUnreadable: a file that does not exist, as /etc/passwd lists no person on
// macOS, reads as nothing, so no group is private.
func TestAMissingFileIsUnreadable(t *testing.T) {
	t.Parallel()

	if readAccountFile(t.TempDir()+"/passwd") != "" {
		t.Error("a missing file read as text")
	}

	who := pat()
	who.passwd = readAccountFile(t.TempDir() + "/passwd")

	if privateGroup(who, personGID) {
		t.Error("private with no passwd file")
	}
}
