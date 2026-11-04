package reviews

// BForgetPids forgets which process each launch was last seen as, as a restart does.
func (r *Reviews) BForgetPids() {
	r.mu.Lock()
	defer r.mu.Unlock()

	clear(r.seenPid)
}

// BIdleCount is how many sessions Reviews remembers an idle time for.
func (r *Reviews) BIdleCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.idleSince)
}

// BLookCount is how many sessions Reviews remembers a next screen read for.
func (r *Reviews) BLookCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.nextLook)
}
