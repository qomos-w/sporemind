//go:build windows

package winx

// COM in this package runs in a dedicated apartment owned by a single
// pinned goroutine ("apartment thread"). Every IUIAutomation call must be
// proxied to that goroutine — making a vtable call from an arbitrary
// goroutine would cross apartments and either fail or marshal silently.
//
// We use Multi-Threaded Apartment (COINIT_MULTITHREADED) so we don't have
// to pump a message loop. UIA tolerates MTA for read operations and works
// well enough for the synchronous Invoke/Value patterns we drive.

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	ole "github.com/go-ole/go-ole"
)

type comTask struct {
	fn   func()
	done chan struct{}
	err  *error
}

var (
	comOnce      sync.Once
	comStartErr  error
	comTaskQueue chan comTask
)

// initCOM ensures the apartment goroutine is running. Safe to call from
// any goroutine; only the first caller actually performs OLE init.
func initCOM() error {
	comOnce.Do(func() {
		comTaskQueue = make(chan comTask, 32)
		ready := make(chan error, 1)
		go apartmentLoop(ready)
		comStartErr = <-ready
	})
	return comStartErr
}

func apartmentLoop(ready chan<- error) {
	runtime.LockOSThread()
	// Deliberately do NOT unlock — this goroutine owns the apartment for
	// the lifetime of the process.
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// S_FALSE (already initialised on this thread) is non-fatal.
		oerr, ok := err.(*ole.OleError)
		if !ok || oerr.Code() != 0x00000001 {
			ready <- err
			return
		}
	}
	ready <- nil
	for t := range comTaskQueue {
		func() {
			defer func() {
				if r := recover(); r != nil && t.err != nil {
					*t.err = fmt.Errorf("COM task panic recovered: %v", r)
				}
			}()
			t.fn()
		}()
		close(t.done)
	}
}

// onCOMThread runs fn on the apartment goroutine and waits for it to
// return. Use this for every IUIAutomation vtable call. It refuses to run
// outside the helper subprocess so the host process can never execute COM.
func onCOMThread(fn func()) error {
	if !uiaHelperMode.Load() {
		return errors.New("COM calls are only allowed in the UIA helper process")
	}
	if err := initCOM(); err != nil {
		return err
	}
	var taskErr error
	t := comTask{fn: fn, done: make(chan struct{}), err: &taskErr}
	comTaskQueue <- t
	<-t.done
	return taskErr
}
