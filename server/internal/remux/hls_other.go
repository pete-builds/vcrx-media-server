//go:build !unix

package remux

import (
	"errors"
	"os"
)

// Without SIGSTOP, ffmpeg cannot be paused; old segments are still deleted.
func pauseProcess(*os.Process) error  { return errors.ErrUnsupported }
func resumeProcess(*os.Process) error { return errors.ErrUnsupported }
