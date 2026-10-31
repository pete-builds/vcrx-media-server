package markers

import (
	"runtime"
	"syscall"
)

// lowerThreadPriority pins the calling goroutine to its OS thread and sets
// that thread to nice 19, so the fingerprinting and matching done in Go yield
// to everything else too. On Linux a priority set on a thread id applies to
// that thread only. The goroutine must never unlock the thread: when it ends
// locked, the runtime retires the thread rather than reuse it at nice 19.
func lowerThreadPriority() {
	runtime.LockOSThread()
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, syscall.Gettid(), 19)
}
