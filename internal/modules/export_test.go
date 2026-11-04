package modules

// Waiting is how many presses wait for the module's probe in flight.
func (s *Statuses) Waiting(name string) int { return s.running.Waiting(name) }
