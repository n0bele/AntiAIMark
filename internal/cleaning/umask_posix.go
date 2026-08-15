//go:build unix

package cleaning

import "syscall"

func syscallUmask(mask int) int {
	return syscall.Umask(mask)
}
