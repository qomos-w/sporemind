package timer

import (
	"time"
)

// type TypeEventChan func(...interface{})
type TypeEventChan interface{}

// 定时器接口
type Timer interface {
	// 一次性定时器
	// AfterFunc(expire time.Duration, callback func()) TimeNoder

	// 周期性定时器
	// ScheduleFunc(expire time.Duration, callback func()) TimeNoder

	// 运行
	// Run()

	// After(delay time.Duration, value interface{}) TimeNoder

	// Schedule(interal time.Duration, value interface{}, delay time.Duration, loop uint64) TimeNoder
	// Schedule(interal time.Duration, value interface{}) TimeNoder

	// 停止所有定时器
	Stop()

	Start()

	// TimeEventChan() <-chan interface{}

	NewHandler() TimeHandler
}

type ITimerData interface {
	Restore()
}

// 停止单个定时器
type TimeNoder interface {
	Stop()

	GetCallback() func(TimeNoder)

	GetDelay() time.Duration

	GetInterval() time.Duration

	Next() time.Time
}

type TimeHandler interface {
	EventChan() <-chan TypeEventChan

	After(delay time.Duration, cb func(TimeNoder)) TimeNoder

	Schedule(interval time.Duration, cb func(TimeNoder), opts ...TimerOption) TimeNoder

	//expresion:1-2,3,4,5,6-12
	Cron(second, minute, hour, day, month, weekday interface{}, cb func(TimeNoder), opts ...TimerOption) (TimeNoder, error)

	At(t time.Time, cb func(TimeNoder)) TimeNoder

	// 停止所有定时器
	StopTimer()

	// 删除handler
	DelTimer()

	PrintDebug()
}

type TimeEventMsg struct {
	Callback  func(TimeNoder)
	TimeNoder TimeNoder
}

func NewTimer() Timer {
	return newTimeWheel()
}

func Instance() Timer {
	once.Do(func() {
		if ins == nil {
			ins = newTimeWheel()
			// go ins.Run()
			ins.Start()
		}
	})
	return ins
}

func NewHandler() TimeHandler {
	return Instance().NewHandler()
}

func Stop() {
	Instance().Stop()
}
