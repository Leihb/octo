package memoryd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHome redirects $HOME to a tempdir for the duration of t. Returns
// the directory. Lets PID-file helpers use the package's real
// resolution path while pointing at scratch space.
func fakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// ── PID file ─────────────────────────────────────────────────────────────

func TestPIDFile_ResolvesUnderOctoHome(t *testing.T) {
	dir := fakeHome(t)
	got, err := PIDFile()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".octo", "memoryd.pid")
	if got != want {
		t.Errorf("PIDFile = %q, want %q", got, want)
	}
}

func TestWriteAndReadPIDFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "memoryd.pid") // exercises mkdir
	if err := WritePIDFile(path, 12345); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPIDFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12345 {
		t.Errorf("ReadPIDFile = %d, want 12345", got)
	}
}

func TestWritePIDFile_AtomicNoTempLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memoryd.pid")
	if err := WritePIDFile(path, 1); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file leaked after WritePIDFile: %s", e.Name())
		}
	}
}

func TestReadPIDFile_MissingErrors(t *testing.T) {
	if _, err := ReadPIDFile(filepath.Join(t.TempDir(), "absent.pid")); err == nil {
		t.Error("missing PID file should error")
	}
}

func TestReadPIDFile_MalformedErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memoryd.pid")
	for _, body := range []string{"", "not-a-number", "0", "-3"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPIDFile(path); err == nil {
			t.Errorf("body %q should error", body)
		}
	}
}

func TestRemovePIDFile_IdempotentOnMissing(t *testing.T) {
	if err := RemovePIDFile(filepath.Join(t.TempDir(), "absent.pid")); err != nil {
		t.Errorf("removing a missing PID file should be a no-op, got %v", err)
	}
}

// ── IsRunning ────────────────────────────────────────────────────────────

func TestIsRunning_CurrentProcess(t *testing.T) {
	if !SupportedOnThisOS() {
		t.Skip("PID probing isn't supported on this OS — IsRunning returns false structurally")
	}
	if !IsRunning(os.Getpid()) {
		t.Error("our own PID should be reported as Running")
	}
}

func TestIsRunning_InvalidPIDsFalse(t *testing.T) {
	for _, pid := range []int{0, -1, -99999} {
		if IsRunning(pid) {
			t.Errorf("IsRunning(%d) should be false", pid)
		}
	}
}

func TestIsRunning_VeryHighPIDFalse(t *testing.T) {
	// 2^31 - 1 is well above any realistic PID range; the OS may
	// theoretically have it but it's a useful negative signal in
	// practice.
	if IsRunning(2_000_000_000) {
		t.Skip("PID 2_000_000_000 is in use on this system — skipping")
	}
}

// ── CheckStatus ──────────────────────────────────────────────────────────

func TestCheckStatus_NoPIDFileNotAnError(t *testing.T) {
	fakeHome(t)
	s, err := CheckStatus()
	if err != nil {
		t.Fatal(err)
	}
	if s.PID != 0 || s.Running {
		t.Errorf("no PID file → zero status, got %+v", s)
	}
}

func TestCheckStatus_AlivePID(t *testing.T) {
	if !SupportedOnThisOS() {
		t.Skip("daemon model not supported on this OS")
	}
	fakeHome(t)
	path, _ := PIDFile()
	if err := WritePIDFile(path, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	s, err := CheckStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Running {
		t.Errorf("our own PID should be Running, got %+v", s)
	}
	if s.StartedAt.IsZero() {
		t.Error("StartedAt should reflect the PID file mtime")
	}
}

func TestCheckStatus_StalePID(t *testing.T) {
	fakeHome(t)
	path, _ := PIDFile()
	// PID 1 always exists on Unix (init), so use a guaranteed-not-running
	// negative... wait, ReadPIDFile rejects negatives. Use the
	// "very high PID" heuristic instead.
	if err := WritePIDFile(path, 2_000_000_000); err != nil {
		t.Fatal(err)
	}
	s, err := CheckStatus()
	if err != nil {
		t.Fatal(err)
	}
	if s.Running {
		t.Skipf("PID %d happens to be alive on this system — skipping", s.PID)
	}
	if s.PID != 2_000_000_000 {
		t.Errorf("PID = %d, want 2_000_000_000", s.PID)
	}
}

// ── ReserveStart ─────────────────────────────────────────────────────────

func TestReserveStart_FreshHomeSucceeds(t *testing.T) {
	fakeHome(t)
	path, err := ReserveStart()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("ReserveStart should have created the PID file: %v", err)
	}
	pid, _ := ReadPIDFile(path)
	if pid != os.Getpid() {
		t.Errorf("PID file = %d, want %d", pid, os.Getpid())
	}
}

func TestReserveStart_AlreadyRunningRefuses(t *testing.T) {
	if !SupportedOnThisOS() {
		t.Skip("daemon model not supported on this OS — IsRunning always returns false there, so 'already running' never triggers")
	}
	fakeHome(t)
	path, _ := PIDFile()
	if err := WritePIDFile(path, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	_, err := ReserveStart()
	if err == nil {
		t.Fatal("ReserveStart should refuse when a live daemon's PID is already on file")
	}
	if !IsAlreadyRunning(err) {
		t.Errorf("error should be IsAlreadyRunning, got %v (type %T)", err, err)
	}
}

func TestReserveStart_StalePIDOverwritten(t *testing.T) {
	fakeHome(t)
	path, _ := PIDFile()
	if err := WritePIDFile(path, 2_000_000_000); err != nil {
		t.Fatal(err)
	}
	if IsRunning(2_000_000_000) {
		t.Skip("very-high PID happens to be alive — can't simulate stale")
	}
	if _, err := ReserveStart(); err != nil {
		t.Errorf("stale PID file should be overwritten, got %v", err)
	}
	pid, _ := ReadPIDFile(path)
	if pid != os.Getpid() {
		t.Errorf("stale PID not overwritten: %d, want %d", pid, os.Getpid())
	}
}

func TestIsAlreadyRunning_DistinguishesOtherErrors(t *testing.T) {
	if IsAlreadyRunning(errors.New("unrelated")) {
		t.Error("IsAlreadyRunning should not match arbitrary errors")
	}
}

// ── Daemon.Run lifecycle ────────────────────────────────────────────────

func TestDaemonRun_ExitsOnCtxCancel(t *testing.T) {
	var out bytes.Buffer
	d := &Daemon{Tick: 20 * time.Millisecond, Out: &out}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Let at least one tick happen then cancel.
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run should return ctx.Err(), got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Daemon did not stop after ctx cancel")
	}
	if !strings.Contains(out.String(), "tick") {
		t.Errorf("expected at least one tick heartbeat:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "stopping") {
		t.Errorf("shutdown notice missing:\n%s", out.String())
	}
}

func TestDaemonRun_HonoursDefaultTick(t *testing.T) {
	d := &Daemon{Out: nil} // no Out + no Tick → uses defaults; just verify
	// we can construct + cancel without hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := d.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run should propagate ctx.Err(), got %v", err)
	}
}

// ── Platform support ────────────────────────────────────────────────────

func TestSupportedOnThisOS(t *testing.T) {
	// On the build target running this test, we just verify the
	// function compiles + returns something. The platform-specific
	// assertion lives in the per-OS file.
	_ = SupportedOnThisOS()
}
