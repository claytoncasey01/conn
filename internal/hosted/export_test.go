package hosted

// ReadAs is Read for the person whose user id is uid, so a test can be another user.
func ReadAs(dir string, uid int) ([]Record, error) { return readAs(dir, uid) }

// The bounds of a record's name and folder.
const (
	NameBytesMax = nameBytesMax
	DirBytesMax  = dirBytesMax
)
