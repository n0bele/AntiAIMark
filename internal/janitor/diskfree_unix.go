//go:build !windows

package janitor

import "syscall"

// diskFreePercent returns free space as a percentage of volume capacity for
// the volume containing path.
func diskFreePercent(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	total := uint64(st.Blocks) * uint64(st.Bsize)
	if total == 0 {
		return 100, nil
	}
	free := uint64(st.Bavail) * uint64(st.Bsize)
	return float64(free) * 100 / float64(total), nil
}
