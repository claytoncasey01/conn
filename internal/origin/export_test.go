package origin

// NamedLikeWithin is namedLike reading at most maxEntries entries of parent.
func NamedLikeWithin(parent, name string, maxEntries int) []string {
	return namedLikeWithin(parent, name, maxEntries)
}
