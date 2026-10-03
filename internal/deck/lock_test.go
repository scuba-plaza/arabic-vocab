//go:build unix

package deck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockAudioAllowsOneHolderAtATime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "audio.lock")
	first, err := LockAudio(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockAudio(context.Background(), path, 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second holder: %v", err)
	}
	first()
	second, err := LockAudio(context.Background(), path, 0)
	if err != nil {
		t.Fatalf("the lock should be free after it was released: %v", err)
	}
	second()
}

func TestLockAudioWaitsForTheHolderToLetGo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.lock")
	first, err := LockAudio(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		first()
	}()
	start := time.Now()
	second, err := LockAudio(context.Background(), path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	if waited := time.Since(start); waited < 100*time.Millisecond || waited > 3*time.Second {
		t.Errorf("waited %v", waited)
	}
}

func TestLockAudioGivesUpAfterTheWaitAndWhenCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.lock")
	held, err := LockAudio(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	start := time.Now()
	if _, err := LockAudio(context.Background(), path, 120*time.Millisecond); !errors.Is(err, ErrBusy) || time.Since(start) < 100*time.Millisecond {
		t.Errorf("err = %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LockAudio(ctx, path, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestLockAudioReportsAnUnusablePath(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LockAudio(context.Background(), filepath.Join(blocked, "x", "audio.lock"), 0); err == nil || errors.Is(err, ErrBusy) {
		t.Errorf("a lock file that cannot be created is an error of its own: %v", err)
	}
}
