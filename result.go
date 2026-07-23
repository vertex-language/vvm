// result.go
package vvm

// RunResult is what Run/RunModule hand back once the built binary has
// finished executing.
type RunResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}