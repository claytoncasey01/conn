package safefolder

// Whether a group is the person's private group, read from /etc/group and /etc/passwd. Every
// doubt counts as shared: a file that cannot be read, is too large or too long, a line about the
// group that cannot be read cleanly, a group listed under another name or with another member, or
// another account whose primary group it is (an account is never listed among the members of its
// own primary group, so /etc/group alone cannot show that). Members added only through NSS (sssd,
// LDAP) are not seen: "no other member" means none in /etc/group or /etc/passwd.

import (
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"
)

const (
	// groupFile and passwdFile are the system's lists of groups and of accounts.
	groupFile  = "/etc/group"
	passwdFile = "/etc/passwd"
	// accountFileBytesMax and accountLinesMax bound the read of either file: one larger, or
	// longer, is not read, and every group counts as shared.
	accountFileBytesMax = 1 << 20
	accountLinesMax     = 10_000
	// groupFields is how many fields a line of the group file has: name, password, id, members.
	groupFields = 4
	// passwdFields is how many a line of the passwd file has: name, password, uid, gid, gecos,
	// home, shell.
	passwdFields = 7
	// The group file's fields, in the order of a line: name, password, id, members.
	groupNameField     = 0
	groupPasswordField = 1
	groupIDField       = 2
	groupMembersField  = 3
	// The passwd file's fields that are used: name, user id, and the account's group id.
	passwdNameField  = 0
	passwdUIDField   = 2
	passwdGroupField = 3
	// commentMark starts a line the files' readers skip.
	commentMark = "#"
)

// Person is who runs Conn, as the folder check needs it, read once per request (WhoRuns): their
// user id, their group id and name, and the text of the group and passwd files ("" for one that
// could not be read, so no group is private).
type Person struct {
	uid    int
	gid    int
	name   string
	groups string
	passwd string
}

// WhoRuns is the person running Conn, its two files read now.
func WhoRuns() Person {
	return whoRunsWith(readAccountFile, currentName)
}

// whoRunsWith is WhoRuns with the files read by read and the name from name.
func whoRunsWith(read func(path string) string, name func() string) Person {
	return Person{
		uid: os.Getuid(), gid: os.Getgid(), name: name(), groups: read(groupFile),
		passwd: read(passwdFile),
	}
}

// currentName is the user name of the person running Conn, or "" when it cannot be told.
func currentName() string {
	current, err := user.Current()
	if err != nil {
		return ""
	}

	return current.Username
}

// readAccountFile is path's text, or "" when it cannot be read or is larger than
// accountFileBytesMax.
func readAccountFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}

	// Only read: nothing useful can be done with a failed close.
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, accountFileBytesMax+1))
	if err != nil {
		return ""
	}

	if len(data) > accountFileBytesMax {
		return ""
	}

	return string(data)
}

// privateGroup is whether gid is the person's private group: their own group id; at least one
// line of the group file has it, and every line that has it names the person and lists no other
// member; and no other account in the passwd file has it as its primary group.
func privateGroup(who Person, gid uint32) bool {
	if int64(gid) != int64(who.gid) {
		return false
	}

	if who.name == "" {
		return false
	}

	wanted := strconv.FormatUint(uint64(gid), 10)

	listed, alone := groupLinesAlone(who.groups, wanted, who.name)
	if !listed {
		return false
	}

	if !alone {
		return false
	}

	return passwdAlone(who.passwd, wanted, who)
}

// accountLines are the lines of an account file that are not blank or comments, and false when
// the file is empty (it could not be read) or longer than accountLinesMax.
func accountLines(text string) ([]string, bool) {
	if text == "" {
		return nil, false
	}

	lines := strings.Split(text, "\n")
	if len(lines) > accountLinesMax {
		return nil, false
	}

	kept := make([]string, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, commentMark) {
			continue
		}

		kept = append(kept, line)
	}

	return kept, true
}

// mayBeID is whether field could be read as the id wanted by a reader more lenient than this
// one: with spaces, tabs or carriage returns around it, a sign (glibc reads +20 as 20), or
// leading zeros. Such a line is about the group, and since it is not written cleanly it makes the
// group shared.
func mayBeID(field, wanted string) bool {
	trimmed := strings.Trim(field, " \t\r")
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "+"), "-")

	unpadded := strings.TrimLeft(trimmed, "0")
	if unpadded == "" {
		unpadded = "0"
	}

	return unpadded == wanted
}

// clean is whether a line of an account file, split into fields, has exactly fieldsWanted of
// them, no carriage return anywhere (a file with CRLF line ends), no space or tab in the fields
// at strict (the names and ids: a passwd line's comment field may hold spaces), and its id field
// exactly wanted.
func clean(fields []string, fieldsWanted int, strict []int, idField int, wanted string) bool {
	if len(fields) != fieldsWanted {
		return false
	}

	if strings.Contains(strings.Join(fields, ":"), "\r") {
		return false
	}

	for _, index := range strict {
		if strings.ContainsAny(fields[index], " \t") {
			return false
		}
	}

	return fields[idField] == wanted
}

// groupStrict and passwdStrict are the fields of each file's lines that hold no space or tab.
func groupStrict() []int {
	return []int{groupNameField, groupPasswordField, groupIDField, groupMembersField}
}

func passwdStrict() []int { return []int{passwdNameField, passwdUIDField, passwdGroupField} }

// groupLinesAlone is whether the group file lists the group wanted at all, and whether every line
// about it is clean, named name and lists no member but name. A line too short to have an id is
// about no group; a file that cannot be read lists none.
func groupLinesAlone(text, wanted, name string) (bool, bool) {
	lines, readable := accountLines(text)
	if !readable {
		return false, false
	}

	listed := false

	for _, line := range lines {
		fields := strings.Split(line, ":")
		if len(fields) <= groupIDField {
			continue
		}

		if !mayBeID(fields[groupIDField], wanted) {
			continue
		}

		if !clean(fields, groupFields, groupStrict(), groupIDField, wanted) {
			return true, false
		}

		if !groupIsOnly(fields, name) {
			return true, false
		}

		listed = true
	}

	return listed, true
}

// groupIsOnly is whether a group file line's fields name the group name and list no member but
// name.
func groupIsOnly(fields []string, name string) bool {
	if fields[groupNameField] != name {
		return false
	}

	for member := range strings.SplitSeq(fields[groupMembersField], ",") {
		if member == "" {
			continue
		}

		if member != name {
			return false
		}
	}

	return true
}

// passwdAlone is whether no account but who has the group wanted as its primary group: every
// line about it is clean and is who's (their name and user id). A line too short to have a
// group is about no group; a file that cannot be read shows nothing, so the group is shared.
func passwdAlone(text, wanted string, who Person) bool {
	lines, readable := accountLines(text)
	if !readable {
		return false
	}

	for _, line := range lines {
		fields := strings.Split(line, ":")
		if len(fields) <= passwdGroupField {
			continue
		}

		if !mayBeID(fields[passwdGroupField], wanted) {
			continue
		}

		if !clean(fields, passwdFields, passwdStrict(), passwdGroupField, wanted) {
			return false
		}

		if fields[passwdNameField] != who.name {
			return false
		}

		if fields[passwdUIDField] != strconv.Itoa(who.uid) {
			return false
		}
	}

	return true
}
