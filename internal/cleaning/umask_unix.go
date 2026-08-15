//go:build !windows

package cleaning

import "os"

func umask() os.FileMode {
	mask := syscallUmask(0)
	syscallUmask(mask)
	return os.FileMode(mask)
}
