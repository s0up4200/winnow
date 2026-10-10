//go:build prototype

package winnow

// PROTOTYPE, throwaway. Do not merge. Issue #12.

// PrototypeExcerpt runs a body through the real clean and cutWords of winnow.
func PrototypeExcerpt(body, repoURL string, n int) string {
	return cutWords(clean(body, repoURL), n)
}
