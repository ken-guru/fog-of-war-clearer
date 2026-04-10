//go:build !linux && !darwin

package planner

// acquireOllamaLock is a no-op on platforms that don't support flock (e.g.
// Windows).  On such platforms concurrent Ollama instances sharing the same
// volume may still interfere, but since the primary supported platforms are
// Linux and macOS the real implementation lives in lock_unix.go.
func acquireOllamaLock() (func(), error) {
	return func() {}, nil
}
