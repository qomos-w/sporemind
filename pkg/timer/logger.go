package timer

import (
	"errors"
	"fmt"
)

type timerLoggerT struct{}

func (l timerLoggerT) Error(msg string) error {
	return errors.New(msg)
}

func (l timerLoggerT) Infof(args ...interface{}) {
	fmt.Println(args...)
}

func (l timerLoggerT) Panic(msg string) {
	panic(msg)
}
