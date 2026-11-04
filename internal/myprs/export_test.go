package myprs

// What the tests in package myprs_test reach that is not exported. A group of tests that needs
// more adds its own export_<group>_test.go.

// SetDeps changes the dependencies of a running MyPrs, so a test can swap a fake mid-test.
func (m *MyPrs) SetDeps(change func(deps *Deps)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	change(&m.deps)
}

// Seen is whether the launch's session has been seen live.
func (m *MyPrs) Seen(launch *Launch) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.seen[launch]
}

// WithFile runs change on the file in memory (nil before the first poll), holding the mutex.
func (m *MyPrs) WithFile(change func(file *File)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	change(m.file)
}

// NoCut is the cut that keeps a text whole.
const NoCut = noCut
