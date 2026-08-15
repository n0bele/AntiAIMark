//go:build windows

package cleaning

import "os"

func umask() os.FileMode {
	// Windows has no POSIX umask; default file mode bits are a no-op.
	return 0
}
