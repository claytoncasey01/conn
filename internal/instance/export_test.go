package instance

// AcquireAs is Acquire for a process of user uid, so a test can see what another user's files
// look like to it.
func AcquireAs(folder string, uid int) (*Lock, error) { return acquire(folder, uid) }

// RunningAs is Running for a process of user uid.
func RunningAs(folder string, uid int) (Holder, bool, error) { return running(folder, uid) }
