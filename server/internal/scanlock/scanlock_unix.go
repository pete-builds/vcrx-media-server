//go:build unix

package scanlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Library takes an exclusive, non-blocking lock on dir/scan-<id>.lock. held
// reports that another holder (in this or another process) has it. The lock
// goes with the process, so a scanner that crashed or was killed never leaves
// it behind.
func Library(dir string, id int64) (release func(), held bool, err error) {
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("scan-%d.lock", id)), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return func() {}, false, err
	}
	fd := int(f.Fd()) //nolint:gosec // a file descriptor fits in an int
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return func() {}, true, nil
		}
		return func() {}, false, err
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, false, nil
}
