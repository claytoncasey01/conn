package origin

// NamedLikeWithin is namedLike reading at most entriesMax entries of parent.
func NamedLikeWithin(parent, name string, entriesMax int) []string {
	return namedLikeWithin(parent, name, entriesMax)
}
