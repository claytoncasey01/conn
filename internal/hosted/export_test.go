package hosted

// ReadAs is Read for the person whose user id is uid, so a test can be another user.
func ReadAs(dir string, uid int) ([]Record, error) { return readAs(dir, uid) }
