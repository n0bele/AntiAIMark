package janitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkTempDir(t *testing.T, tempDir, prefix string, age time.Duration, sizeBytes int) string {
	t.Helper()
	dir, err := os.MkdirTemp(tempDir, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.bin"), make([]byte, sizeBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "payload.bin"), old, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCleanOnceLowDiskRemovesStaleDirsOnly(t *testing.T) {
	tempDir := t.TempDir()
	stale := mkTempDir(t, tempDir, "wm-dl-", 3*time.Hour, 1024)    // old -> eligible
	fresh := mkTempDir(t, tempDir, "wm-web-", 2*time.Minute, 1024) // young -> protected
	foreign := mkTempDir(t, tempDir, "other-", 3*time.Hour, 1024)  // wrong prefix -> never touched

	free := 5.0 // below the 11% threshold
	var logs []string
	j := New(Config{
		Enabled:     true,
		Interval:    time.Minute,
		Threshold:   11,
		MinAge:      time.Hour,
		TempDir:     tempDir,
		FreePercent: func(string) (float64, error) { return free, nil },
		Log:         func(msg string) { logs = append(logs, msg) },
	})
	summary, err := j.CleanOnce()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale dir should be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh dir must survive: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("foreign dir must never be touched: %v", err)
	}
	if summary.TempRemoved != 1 || summary.Triggered != true {
		t.Errorf("summary = %+v", summary)
	}
	if len(logs) == 0 {
		t.Error("expected log lines")
	}
}

func TestCleanOnceStopsWhenSpaceRecovers(t *testing.T) {
	tempDir := t.TempDir()
	oldest := mkTempDir(t, tempDir, "wm-clean-", 5*time.Hour, 512)
	newer := mkTempDir(t, tempDir, "wm-inspect-", 3*time.Hour, 512)

	// free space flips to healthy after the first deletion
	calls := 0
	freeFn := func(string) (float64, error) {
		calls++
		if calls <= 1 {
			return 5, nil // initial check: low
		}
		return 50, nil // after removing one dir: recovered
	}
	j := New(Config{
		Threshold:   11,
		MinAge:      time.Hour,
		TempDir:     tempDir,
		FreePercent: freeFn,
		Log:         func(string) {},
	})
	if _, err := j.CleanOnce(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Error("oldest dir should have been removed first")
	}
	if _, err := os.Stat(newer); err != nil {
		t.Error("newer dir should survive once space recovered")
	}
}

func TestCleanOnceHealthyDiskOnlyRunsTTL(t *testing.T) {
	tempDir := t.TempDir()
	stale := mkTempDir(t, tempDir, "wm-dl-", 3*time.Hour, 512)

	ttlCalled := false
	j := New(Config{
		Threshold: 11,
		TempDir:   tempDir,
		FreePercent: func(string) (float64, error) {
			return 60, nil
		},
		EvictExpiredDownloads: func(ttl time.Duration) (int64, int) {
			ttlCalled = true
			return 0, 0
		},
		Log: func(string) {},
	})
	if _, err := j.CleanOnce(); err != nil {
		t.Fatal(err)
	}
	if !ttlCalled {
		t.Error("TTL eviction must run on every pass")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Error("healthy disk must not trigger temp deletion")
	}
}

func TestCleanOncePurgesDownloadsAsLastResort(t *testing.T) {
	tempDir := t.TempDir()
	purged := false
	free := 3.0
	j := New(Config{
		Threshold: 11,
		MinAge:    time.Hour,
		TempDir:   tempDir,
		FreePercent: func(string) (float64, error) {
			return free, nil // stays low
		},
		EvictExpiredDownloads: func(time.Duration) (int64, int) { return 0, 0 },
		PurgeDownloads: func() (int64, int) {
			purged = true
			return 4096, 2
		},
		Log: func(string) {},
	})
	summary, err := j.CleanOnce()
	if err != nil {
		t.Fatal(err)
	}
	if !purged {
		t.Error("still-low disk must purge pending downloads")
	}
	if summary.PurgedDownloads != 2 {
		t.Errorf("summary = %+v", summary)
	}
}

func TestStartDisabledIsNoop(t *testing.T) {
	stop := Start(nil, Config{Enabled: false})
	if stop == nil {
		t.Fatal("stop must be non-nil")
	}
	stop() // must not panic
}

func TestDiskFreePercentReal(t *testing.T) {
	pct, err := diskFreePercent(t.TempDir())
	if err != nil {
		t.Skipf("platform disk-free unavailable: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Fatalf("free percent = %v", pct)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:     "512B",
		2048:    "2.0KB",
		5 << 20: "5.0MB",
		3 << 30: "3.0GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
