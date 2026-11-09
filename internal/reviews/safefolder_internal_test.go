package reviews

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	personUID = 501
	otherUID  = 502
	// personGID is the person's private group, as a umask-002 Linux makes it; staffGID a shared
	// group, as macOS's staff is.
	personGID = 1001
	staffGID  = 20
)

// groupsFile is a group file with the person's private group, macOS's shared staff, and a group
// named for the person that has a second member.
const groupsFile = "root:x:0:\nstaff:*:20:root,pat\npat:x:1001:\nshared:x:1002:pat,sam\n"

// pat is the person, whose primary group is their private group.
func pat() person {
	return person{uid: personUID, gid: personGID, name: "pat", groups: groupsFile}
}

// ownFolder is a folder the person owns that only they may write.
func ownFolder() pathFacts {
	return pathFacts{
		exists: true, link: false, folder: true, owner: personUID, group: personGID,
		groupWrite: false, otherWrite: false, sticky: false,
	}
}

// rootFolder is a folder root owns that only root may write.
func rootFolder() pathFacts {
	facts := ownFolder()
	facts.owner = rootUID
	facts.group = 0

	return facts
}

// TestCheckSafeFolderJudgesEachPartOfThePath: the folder must be the person's own and theirs
// alone to write; each folder above it the person's or root's, not a link, and not writable by
// others unless sticky.
func TestCheckSafeFolderJudgesEachPartOfThePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		change   func(facts map[string]pathFacts)
		wantSays string
	}{
		{"every part safe", func(map[string]pathFacts) {}, ""},
		{
			"the folder a link", func(f map[string]pathFacts) { setLink(f, "/home/p/clone") },
			"/home/p/clone is a link",
		},
		{
			"an ancestor a link", func(f map[string]pathFacts) { setLink(f, "/home/p") },
			"/home/p is a link",
		},
		{"the folder missing", func(f map[string]pathFacts) {
			f["/home/p/clone"] = pathFacts{exists: false}
		}, "does not exist"},
		{"the folder a file", func(f map[string]pathFacts) {
			facts := f["/home/p/clone"]
			facts.folder = false
			f["/home/p/clone"] = facts
		}, "is not a folder"},
		{"the folder another user's", func(f map[string]pathFacts) {
			setOwner(f, "/home/p/clone", otherUID)
		}, "/home/p/clone belongs to another user"},
		{"the folder root's", func(f map[string]pathFacts) {
			setOwner(f, "/home/p/clone", rootUID)
		}, "/home/p/clone belongs to another user"},
		{"an ancestor another user's", func(f map[string]pathFacts) {
			setOwner(f, "/home/p", otherUID)
		}, "/home/p belongs to another user"},
		{"the folder writable by its private group (0775)", func(f map[string]pathFacts) {
			setGroupWrite(f, "/home/p/clone", personGID)
		}, ""},
		{"an ancestor writable by the private group", func(f map[string]pathFacts) {
			setGroupWrite(f, "/home/p", personGID)
		}, ""},
		{"the folder writable by a shared group (staff 0775)", func(f map[string]pathFacts) {
			setGroupWrite(f, "/home/p/clone", staffGID)
		}, "/home/p/clone may be written by its group (20)"},
		{"the folder writable by a group that is not theirs", func(f map[string]pathFacts) {
			setGroupWrite(f, "/home/p/clone", 1002)
		}, "chmod g-w"},
		{"the folder writable by others", func(f map[string]pathFacts) {
			setOtherWrite(f, "/home/p/clone", false)
		}, "other users may write /home/p/clone"},
		{"the folder sticky and writable by others", func(f map[string]pathFacts) {
			setOtherWrite(f, "/home/p/clone", true)
		}, "other users may write /home/p/clone"},
		{"an ancestor writable by others", func(f map[string]pathFacts) {
			setOtherWrite(f, "/home/p", false)
		}, "other users may write /home/p"},
		{"an ancestor sticky and writable by others, as /tmp", func(f map[string]pathFacts) {
			setOtherWrite(f, "/home", true)
		}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			facts := map[string]pathFacts{
				"/": rootFolder(), "/home": rootFolder(), "/home/p": ownFolder(),
				"/home/p/clone": ownFolder(),
			}
			tt.change(facts)

			look := func(path string) (pathFacts, error) { return facts[path], nil }
			err := checkSafeFolder("/home/p/clone", pat(), look)

			if tt.wantSays == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}

				return
			}

			if !errors.Is(err, ErrUnsafeFolder) {
				t.Fatalf("error %v, want ErrUnsafeFolder", err)
			}

			if !strings.Contains(err.Error(), tt.wantSays) {
				t.Errorf("says %q, want %q", err, tt.wantSays)
			}
		})
	}
}

func setLink(facts map[string]pathFacts, path string) {
	changed := facts[path]
	changed.link = true
	changed.folder = false
	facts[path] = changed
}

func setOwner(facts map[string]pathFacts, path string, owner uint32) {
	changed := facts[path]
	changed.owner = owner
	facts[path] = changed
}

func setGroupWrite(facts map[string]pathFacts, path string, group uint32) {
	changed := facts[path]
	changed.groupWrite = true
	changed.group = group
	facts[path] = changed
}

func setOtherWrite(facts map[string]pathFacts, path string, sticky bool) {
	changed := facts[path]
	changed.otherWrite = true
	changed.sticky = sticky
	facts[path] = changed
}

// TestAPrivateGroupIsTheirsAloneByNameIDAndMembers: the person's own group id, listed under
// their name, with no other member; anything else, or a group file that could not be read, is a
// shared group.
func TestAPrivateGroupIsTheirsAloneByNameIDAndMembers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		who  func() person
		gid  uint32
		want bool
	}{
		{"the private group", pat, personGID, true},
		{"the private group listing only the person", func() person {
			who := pat()
			who.groups = "pat:x:1001:pat\n"

			return who
		}, personGID, true},
		{"macOS's staff", func() person {
			who := pat()
			who.gid = staffGID

			return who
		}, staffGID, false},
		{"a group that is not the person's group id", pat, 1002, false},
		{"a group named for the person with a second member", func() person {
			who := pat()
			who.groups = "pat:x:1001:pat,sam\n"

			return who
		}, personGID, false},
		{"a group file that could not be read", func() person {
			who := pat()
			who.groups = ""

			return who
		}, personGID, false},
		{"a group the file does not list (NSS or LDAP)", func() person {
			who := pat()
			who.groups = "root:x:0:\n"

			return who
		}, personGID, false},
		{"no name for the person", func() person {
			who := pat()
			who.name = ""

			return who
		}, personGID, false},
		{"a line with too few fields", func() person {
			who := pat()
			who.groups = "pat:x:1001\n"

			return who
		}, personGID, false},
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

func TestCheckSafeFolderRefusesAPathItWouldHaveToClean(t *testing.T) {
	t.Parallel()

	for _, folder := range []string{"relative/clone", "/home/p/../p/clone", "/home/p/clone/"} {
		err := checkSafeFolder(folder, pat(), func(string) (pathFacts, error) {
			return ownFolder(), nil
		})
		if !errors.Is(err, ErrUnsafeFolder) {
			t.Errorf("%s: error %v, want ErrUnsafeFolder", folder, err)
		}
	}
}

// realTemp is a new folder of the person's own, its links resolved.
func realTemp(t *testing.T) string {
	t.Helper()

	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Private whatever the umask, as Conn's own folders are: a test folder another user could
	// write would rightly be refused.
	err = os.Chmod(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	return folder
}

func makeFolder(t *testing.T, path string) {
	t.Helper()

	err := os.Mkdir(path, 0o700)
	if err != nil {
		t.Fatal(err)
	}
}

// TestCheckSafeFolderOnTheDisk: the same rules read from real folders.
func TestCheckSafeFolderOnTheDisk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		arrange  func(t *testing.T, base string) string
		wantSays string
	}{
		{"an own folder", func(t *testing.T, base string) string {
			t.Helper()

			folder := filepath.Join(base, "clone")
			makeFolder(t, folder)

			return folder
		}, ""},
		{"an ancestor others may write", func(t *testing.T, base string) string {
			t.Helper()

			parent := filepath.Join(base, "shared")
			makeFolder(t, parent)
			makeFolder(t, filepath.Join(parent, "clone"))

			err := os.Chmod(parent, 0o777)
			if err != nil {
				t.Fatal(err)
			}

			return filepath.Join(parent, "clone")
		}, "other users may write"},
		{"a link in an ancestor", func(t *testing.T, base string) string {
			t.Helper()

			projects := filepath.Join(base, "projects")
			makeFolder(t, projects)
			makeFolder(t, filepath.Join(projects, "clone"))

			err := os.Symlink(projects, filepath.Join(base, "linked"))
			if err != nil {
				t.Fatal(err)
			}

			return filepath.Join(base, "linked", "clone")
		}, "is a link"},
		{"a link as the folder", func(t *testing.T, base string) string {
			t.Helper()

			makeFolder(t, filepath.Join(base, "real"))

			err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "clone"))
			if err != nil {
				t.Fatal(err)
			}

			return filepath.Join(base, "clone")
		}, "is a link"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckSafeFolder(tt.arrange(t, realTemp(t)))
			if tt.wantSays == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}

				return
			}

			if !strings.Contains(err.Error(), tt.wantSays) {
				t.Errorf("says %v, want %q", err, tt.wantSays)
			}
		})
	}
}
