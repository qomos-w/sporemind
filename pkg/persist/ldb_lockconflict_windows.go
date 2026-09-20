//go:build windows

package persist

import (
	"errors"
	"syscall"
)

// ldbLockConflict reports whether err is a goleveldb directory-lock conflict:
// another process (or handle) already holds the exclusive LOCK on the DB
// directory. On Windows goleveldb opens LOCK with share mode 0, so a second
// CreateFile fails with ERROR_SHARING_VIOLATION.
func ldbLockConflict(err error) bool {
	// ERROR_SHARING_VIOLATION / ERROR_LOCK_VIOLATION are not in stdlib
	// syscall; the raw errno values come from winerror.h.
	const (
		errnoSharingViolation = syscall.Errno(32)
		errnoLockViolation    = syscall.Errno(33)
	)
	return errors.Is(err, errnoSharingViolation) || errors.Is(err, errnoLockViolation)
}
