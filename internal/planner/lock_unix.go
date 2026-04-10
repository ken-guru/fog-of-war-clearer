//go:build linux || darwin

package planner

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// acquireOllamaLock acquires a host-level exclusive advisory flock on a
// well-known lock file.  This prevents concurrent fog processes from
// simultaneously operating on the shared fog-ollama-models Docker volume,
// which would cause model-file corruption when two Ollama server processes
// write to the same content-addressed blob directory at the same time.
//
// The returned function releases the lock and must always be called (use
// defer).  The call blocks until the lock is available.
func acquireOllamaLock() (func(), error) {
	lockPath := filepath.Join(os.TempDir(), "fog-ollama.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open ollama lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire ollama lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
