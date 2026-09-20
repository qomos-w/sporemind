package timer

import (
	"time"
)

type ITimeTestHandler interface {
	Now() time.Time
	Get10Ms() time.Duration
}

func SetTimeTestHandler(handler ITimeTestHandler) {
	_timeTestHandler = handler
	get10Ms = _timeTestHandler.Get10Ms
}

func _get10Ms() time.Duration {
	return time.Duration(time.Now().UnixNano() / int64(time.Millisecond) / 10)
}
