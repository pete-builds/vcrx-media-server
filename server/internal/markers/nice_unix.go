//go:build linux || darwin

package markers

import "syscall"

// lowPriority sets a child process to nice 19, so it only uses CPU that
// playback and everything else on the box leave idle.
func lowPriority(pid int) {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 19)
}
