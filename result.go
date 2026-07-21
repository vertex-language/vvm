// result.go
package vvm

// RunResult is what Run/RunModule hand back once the built binary has
// finished executing — the same three things `go run` reports on exit.
type RunResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}