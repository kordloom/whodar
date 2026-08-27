package util

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestLockFileSerializesHolders proves the lock actually excludes: a second
// taker blocks until the first releases, so two writers can never interleave
// on the state file the lock guards.
func TestLockFileSerializesHolders(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.lock")

	release, err := LockFile(path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	var second atomic.Bool
	acquired := make(chan struct{})
	go func() {
		rel, err := LockFile(path)
		if err != nil {
			t.Errorf("second lock: %v", err)
			close(acquired)
			return
		}
		second.Store(true)
		close(acquired)
		rel()
	}()

	// The second taker must still be waiting while the first holds.
	time.Sleep(50 * time.Millisecond)
	if second.Load() {
		t.Fatal("second lock acquired while the first was held")
	}

	release()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second lock never acquired after release")
	}
	if !second.Load() {
		t.Fatal("second taker finished without holding the lock")
	}
}

// TestLockFileReacquireAfterRelease proves release really lets go: the same
// goroutine can take the lock again immediately.
func TestLockFileReacquireAfterRelease(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.lock")
	release, err := LockFile(path)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	release()
	again, err := LockFile(path)
	if err != nil {
		t.Fatalf("relock: %v", err)
	}
	again()
}

// TestLockFileBadPath covers the open failure: a path whose directory does not
// exist is an error, not a panic or a silent no-op lock.
func TestLockFileBadPath(t *testing.T) {
	t.Parallel()
	if _, err := LockFile(filepath.Join(t.TempDir(), "missing", "state.lock")); err == nil {
		t.Fatal("no error for a lock path in a missing directory")
	}
}
