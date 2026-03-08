package convergence

// MatchesDependencyFilter checks if a bead's metadata satisfies a
// depends_on_filter. Returns true if all filter keys match their
// expected values in the metadata. An empty filter always matches.
//
// The filter requires the key to be present in the metadata. A filter
// value of "" matches only when the key exists and is explicitly set
// to ""; a missing key does not match.
func MatchesDependencyFilter(meta map[string]string, filter map[string]string) bool {
	for k, expected := range filter {
		actual, ok := meta[k]
		if !ok || actual != expected {
			return false
		}
	}
	return true
}
