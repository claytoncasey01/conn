package theme

// SlotProblem is why value does not fit slot (a slot name), or "": slotProblem in validate.ts.
func SlotProblem(slot, value string) string {
	spec, ok := newValidator().slots[slot]
	if !ok {
		return "unknown token " + show(slot)
	}

	return valueProblem(spec, CSSTrim(value))
}
