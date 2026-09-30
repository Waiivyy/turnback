package ui

// SetMaxDiff changes the diff size limit and returns a function that
// restores it.
func SetMaxDiff(n int) func() {
	old := maxDiff
	maxDiff = n
	return func() { maxDiff = old }
}
