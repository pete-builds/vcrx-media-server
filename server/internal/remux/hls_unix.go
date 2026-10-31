//go:build unix

package remux

import (
	"os"
	"syscall"
)

// pauseProcess stops ffmpeg where it is; resumeProcess lets it carry on. A
// stopped process still dies at once when its stream is stopped.
func pauseProcess(p *os.Process) error  { return p.Signal(syscall.SIGSTOP) }
func resumeProcess(p *os.Process) error { return p.Signal(syscall.SIGCONT) }
