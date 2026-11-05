package desk

// splashOpening is the splash's first line, there on every start.
const splashOpening = "Opening Conn…"

// SplashText is what the window shows while Conn starts: that it is opening and, when the login
// shell's environment could not be read, a line saying so, because the app then runs with the
// bare environment it was started with and tools may not be found. Lines are separated by "\n".
// The text is plain: the page that shows it escapes it.
func SplashText(loadErr error) string {
	if loadErr == nil {
		return splashOpening
	}

	return splashOpening + "\n" +
		"could not read your shell's environment: " + loadErr.Error() + "; using the app's own"
}
