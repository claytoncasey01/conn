package settingsapi

// BrowseWaiting is how many presses wait for the browse in flight.
func (a *API) BrowseWaiting() int { return a.browsing.Waiting(struct{}{}) }
