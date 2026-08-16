// Package janitor is the background auto-clean service: on a configurable
// schedule it evicts expired download files, and when free disk space drops
// below a threshold it deletes this service's stale temp directories (and,
// if still low, all pending downloads) until space recovers.
//
// Defaults (all overridable):
//
//	Enabled            false  (opt-in: --auto-clean / ANTIAIMARK_AUTO_CLEAN=1)
//	Interval           15m    (--auto-clean-interval / ANTIAIMARK_AUTO_CLEAN_INTERVAL)
//	ThresholdPercent   11     (--auto-clean-threshold / ANTIAIMARK_AUTO_CLEAN_THRESHOLD)
//	DownloadTTL        24h    (--auto-clean-ttl / ANTIAIMARK_AUTO_CLEAN_TTL)
//	MinAge             1h     (stale temp dirs younger than this are never touched)
//
// Safety rules:
//   - only directories directly inside TempDir whose names start with this
//     service's prefixes (wm-inspect-, wm-clean-, wm-web-, wm-dl-) are ever
//     deleted — nothing else on the disk is touched;
//   - symlinks are skipped (no following links out of the temp dir);
//   - dirs younger than MinAge are left alone so in-flight requests keep
//     working;
//   - oldest-first deletion, re-checking free space after each removal.
package janitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"antiaimark/internal/i18n"
)

// DefaultPrefixes are the MkdirTemp prefixes this service creates.
var DefaultPrefixes = []string{"wm-inspect-", "wm-clean-", "wm-web-", "wm-dl-"}

// Config controls the janitor. Zero fields fall back to the documented
// defaults; EvictExpiredDownloads / PurgeDownloads are optional hooks (the
// HTTP facade passes its download store) and FreePercent / Log can be
// injected by tests and embedders.
type Config struct {
	Enabled     bool
	Interval    time.Duration // check period; default 15m
	Threshold   float64       // free-space % that triggers cleanup; default 11
	DownloadTTL time.Duration // downloads older than this are evicted every run; default 24h
	MinAge      time.Duration // temp dirs younger than this are never deleted; default 1h
	TempDir     string        // default os.TempDir()
	Prefixes    []string      // default DefaultPrefixes

	// EvictExpiredDownloads removes download entries older than ttl and
	// returns (freed bytes, count).
	EvictExpiredDownloads func(ttl time.Duration) (int64, int)
	// PurgeDownloads removes ALL pending download entries (last resort when
	// the disk is still low) and returns (freed bytes, count).
	PurgeDownloads func() (int64, int)

	// FreePercent returns free space as a percentage of volume capacity.
	// Defaults to the platform disk-free syscall over TempDir.
	FreePercent func(path string) (float64, error)
	// Log receives one localized line per notable event.
	Log func(msg string)
}

// Summary reports what one CleanOnce pass did.
type Summary struct {
	TTLFreed        int64
	TTLCount        int
	TempRemoved     int
	TempFreed       int64
	PurgedDownloads int
	PurgeFreed      int64
	FreeBefore      float64
	FreeAfter       float64
	Triggered       bool
}

// Janitor runs the auto-clean policy.
type Janitor struct {
	cfg Config
	log func(string)
}

// New builds a Janitor with defaults applied.
func New(cfg Config) *Janitor {
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Minute
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 11
	}
	if cfg.DownloadTTL <= 0 {
		cfg.DownloadTTL = 24 * time.Hour
	}
	if cfg.MinAge <= 0 {
		cfg.MinAge = time.Hour
	}
	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	if len(cfg.Prefixes) == 0 {
		cfg.Prefixes = DefaultPrefixes
	}
	if cfg.FreePercent == nil {
		cfg.FreePercent = diskFreePercent
	}
	j := &Janitor{cfg: cfg}
	j.log = cfg.Log
	if j.log == nil {
		j.log = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}
	return j
}

// Start launches the background loop and returns a stop function. The
// janitor logs its effective configuration once at startup. When cfg.Enabled
// is false Start is a no-op that returns a no-op stop.
func Start(ctx context.Context, cfg Config) (stop func()) {
	if !cfg.Enabled {
		return func() {}
	}
	j := New(cfg)
	j.log(i18n.T("janitor.start", cfg.Interval.String(), int(cfg.Threshold), cfg.DownloadTTL.String()))
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(j.cfg.Interval)
		defer ticker.Stop()
		// first pass immediately, then on every tick
		j.runPass()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				j.runPass()
			}
		}
	}()
	return cancel
}

func (j *Janitor) runPass() {
	defer func() {
		if r := recover(); r != nil {
			j.log(i18n.T("janitor.error", fmt.Sprintf("%v", r)))
		}
	}()
	if _, err := j.CleanOnce(); err != nil {
		j.log(i18n.T("janitor.error", err.Error()))
	}
}

// CleanOnce runs one scheduled pass. It is exported for tests and for
// embedders that prefer manual triggering over Start's loop.
func (j *Janitor) CleanOnce() (Summary, error) {
	var s Summary

	// 1. Always evict expired downloads (bounded growth, disk space aside).
	if j.cfg.EvictExpiredDownloads != nil {
		freed, n := j.cfg.EvictExpiredDownloads(j.cfg.DownloadTTL)
		s.TTLFreed, s.TTLCount = freed, n
		if n > 0 {
			j.log(i18n.T("janitor.evicted", n, humanBytes(freed)))
		}
	}

	// 2. Disk-space-triggered cleanup.
	pct, err := j.cfg.FreePercent(j.cfg.TempDir)
	if err != nil {
		return s, err
	}
	s.FreeBefore = pct
	if pct >= j.cfg.Threshold {
		return s, nil
	}
	s.Triggered = true
	j.log(i18n.T("janitor.low_disk", pct, int(j.cfg.Threshold)))

	// Delete this service's stale temp dirs, oldest first, until recovered.
	// The pass was already triggered by a low initial reading, so each
	// deletion is followed by a re-check rather than preceded by one.
	dirs := j.staleDirs()
	for _, d := range dirs {
		size := dirSize(d.path)
		if err := os.RemoveAll(d.path); err != nil {
			continue // busy or in use; try the next one
		}
		s.TempRemoved++
		s.TempFreed += size
		j.log(i18n.T("janitor.removed_dir", filepath.Base(d.path), humanBytes(size)))
		cur, err := j.cfg.FreePercent(j.cfg.TempDir)
		if err == nil && cur >= j.cfg.Threshold {
			break
		}
	}

	// Still low? Downloads are re-generatable — purge them all.
	cur, err := j.cfg.FreePercent(j.cfg.TempDir)
	if err != nil {
		return s, err
	}
	if cur < j.cfg.Threshold && j.cfg.PurgeDownloads != nil {
		freed, n := j.cfg.PurgeDownloads()
		s.PurgedDownloads, s.PurgeFreed = n, freed
		if n > 0 {
			j.log(i18n.T("janitor.purged_downloads", n, humanBytes(freed)))
		}
		cur, err = j.cfg.FreePercent(j.cfg.TempDir)
		if err != nil {
			return s, err
		}
	}
	s.FreeAfter = cur
	if cur >= j.cfg.Threshold {
		j.log(i18n.T("janitor.recovered", cur))
	} else {
		j.log(i18n.T("janitor.still_low", cur))
	}
	return s, nil
}

type staleDir struct {
	path    string
	modTime time.Time
}

// staleDirs lists eligible temp dirs (prefix-matched, real directories, no
// symlinks, older than MinAge), oldest first.
func (j *Janitor) staleDirs() []staleDir {
	entries, err := os.ReadDir(j.cfg.TempDir)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-j.cfg.MinAge)
	var dirs []staleDir
	for _, e := range entries {
		name := e.Name()
		matched := false
		for _, p := range j.cfg.Prefixes {
			if strings.HasPrefix(name, p) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue // too young — may belong to an in-flight request
		}
		dirs = append(dirs, staleDir{filepath.Join(j.cfg.TempDir, name), info.ModTime()})
	}
	sort.Slice(dirs, func(i, k int) bool { return dirs[i].modTime.Before(dirs[k].modTime) })
	return dirs
}

func dirSize(path string) int64 {
	var total int64
	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// humanBytes renders a byte count for logs (B / KB / MB / GB).
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
