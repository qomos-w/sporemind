//go:build !windows

package persist

import (
	"errors"
	"syscall"
)

// ldbLockConflict reports whether err is a goleveldb directory-lock conflict:
// another open file description already holds the exclusive flock on the DB
// directory's LOCK file.
func ldbLockConflict(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
