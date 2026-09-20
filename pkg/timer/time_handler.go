package timer

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type TimerOption func(node *timeNode)

type timeHandler struct {
	// eventMu guards eventChan/eventClosed. Delivery and DelTimer both take it
	// so a close can never race a send (send-on-closed-channel panic, #10).
	eventMu     sync.Mutex
	eventChan   chan TypeEventChan
	eventClosed bool

	wheel  *timeWheel
	noders sync.Map
}

func WithDelay(delay time.Duration) TimerOption {
	return func(node *timeNode) {
		node.delay = uint64(delay)
	}
}

func WithLoop(loop uint64) TimerOption {
	return func(node *timeNode) {
		node.loopMax = loop
	}
}

// WithLocation sets the timezone a cron schedule is evaluated in. Without it
// the wheel evaluates cron in the server's time.Local. Pass nil to keep the
// default.
func WithLocation(loc *time.Location) TimerOption {
	return func(node *timeNode) {
		node.loc = loc
	}
}

func (this *timeHandler) At(t time.Time, cb func(TimeNoder)) TimeNoder {
	delay := t.Sub(this.wheel.Now())
	if delay < time.Millisecond*10 {
		// A past (or sub-tick) target must fire on the next tick, not wrap to a
		// ~497-day timeout through the uint64 conversion of a negative duration
		// (#4 timer audit).
		delay = time.Millisecond * 10
	}
	return this.Schedule(delay, cb, WithLoop(1))
}

func (this *timeHandler) After(delay time.Duration, cb func(TimeNoder)) TimeNoder {
	return this.Schedule(delay, cb, WithLoop(1))
}

func convertIntToString(i interface{}) (string, error) {
	if isString(i) {
		return i.(string), nil
	}
	if isInt(i) {
		if i == -1 {
			return "*", nil
		}
		return strconv.Itoa(i.(int)), nil
	}
	if isNil(i) {
		return "?", nil
	}
	if isBool(i) {
		if i.(bool) {
			return "*", nil
		} else {
			return "?", nil
		}
	}
	return "", logger.Error("type is not correct")
}

func (this *timeHandler) Cron(i_second, i_minute, i_hour, i_day, i_month, i_weekday interface{}, cb func(TimeNoder), opts ...TimerOption) (TimeNoder, error) {
	second, err := convertIntToString(i_second)
	if err != nil {
		return nil, err
	}
	minute, err := convertIntToString(i_minute)
	if err != nil {
		return nil, err
	}
	hour, err := convertIntToString(i_hour)
	if err != nil {
		return nil, err
	}
	day, err := convertIntToString(i_day)
	if err != nil {
		return nil, err
	}
	month, err := convertIntToString(i_month)
	if err != nil {
		return nil, err
	}
	weekday, err := convertIntToString(i_weekday)
	if err != nil {
		return nil, err
	}

	node := &timeNode{
		expire:       0,
		callback:     cb,
		delay:        uint64(0),
		loopCur:      0,
		loopMax:      0,
		handler:      this,
		isCron:       true,
		lastMonthDay: -1,
	}
	for _, opt := range opts {
		opt(node)
	}

	if err := node.parseCron(second, minute, hour, day, month, weekday); err != nil {
		// Parsing failures are returned, never fatal: a bad expression must not
		// be able to take down the caller (previously logger.Panic killed the
		// scheduler actor and triggered a supervisor restart loop).
		return nil, err
	}

	jiffies := atomic.LoadUint64(&this.wheel.jiffies)
	expire, _ := node.cronExpireFunc(this.wheel)
	ticks := expire / (uint64(time.Millisecond) * 10)
	if ticks < 1 {
		// A sub-10ms first fire (a match on the current second boundary) must
		// still land on a real tick, never on the already-drained slot (#5).
		ticks = 1
	}
	node.expire = ticks + jiffies
	tn := this.wheel.add(node, jiffies)

	this.noders.Store(tn, 1)

	return tn, nil
}

func (this *timeHandler) Schedule(interval time.Duration, cb func(TimeNoder), opts ...TimerOption) TimeNoder {
	if interval <= 0 {
		// Guard against a negative duration wrapping to a huge uint64 tick count
		// (~497 days at 10ms resolution) when converted below.
		interval = time.Millisecond * 10
	}
	jiffies := atomic.LoadUint64(&this.wheel.jiffies)

	node := &timeNode{
		expire:   0,
		callback: cb,
		delay:    uint64(0),
		interval: uint64(interval),
		loopCur:  0,
		loopMax:  0,
		handler:  this,
		isCron:   false,
	}

	for _, v := range opts {
		v(node)
	}

	// Compute the delay in 10ms ticks. A sub-10ms delay/interval truncates to
	// zero ticks, which would place the timer on the already-drained current
	// slot; clamp to the minimum resolution of one tick (#5). add() also guards
	// against a past/zero expire, this makes the intent explicit here.
	ticks := node.interval / (uint64(time.Millisecond) * 10)
	if node.delay > 0 {
		ticks = node.delay / (uint64(time.Millisecond) * 10)
	}
	if ticks < 1 {
		ticks = 1
	}
	node.expire = ticks + jiffies

	tn := this.wheel.add(node, jiffies)

	this.noders.Store(tn, 1)

	return tn
}

func (this *timeHandler) EventChan() <-chan TypeEventChan {
	this.eventMu.Lock()
	defer this.eventMu.Unlock()
	if this.eventChan == nil && !this.eventClosed {
		this.eventChan = make(chan TypeEventChan, 100)
	}
	return this.eventChan
}

// deliver hands an event to the handler's consumer. It is safe to call from
// the wheel's dispatch goroutine: the mutex plus closed flag make a concurrent
// DelTimer close impossible while a send is in flight, and the non-blocking
// send keeps a slow/stalled consumer from parking the delivery goroutine (and
// the wheel) forever (#2/#10). Events are dropped when the buffer is full.
func (this *timeHandler) deliver(msg *TimeEventMsg) {
	if msg == nil {
		return
	}
	this.eventMu.Lock()
	defer this.eventMu.Unlock()
	if this.eventClosed || this.eventChan == nil {
		return
	}
	select {
	case this.eventChan <- msg:
	default:
	}
}

func (this *timeHandler) DelTimer() {

	// delete time event
	this.StopTimer()

	// close channel under the lock so a concurrent delivery can never send on
	// the closed channel (send-on-closed-channel panic, #10).
	this.eventMu.Lock()
	this.eventClosed = true
	ch := this.eventChan
	this.eventChan = nil
	this.eventMu.Unlock()
	if ch != nil {
		close(ch)
	}

	this.wheel = nil

}

func (this *timeHandler) StopTimer() {
	this.noders.Range(func(key, value any) bool {
		node := key.(*timeNode)
		node.Stop()
		return true
	})
}

func (this *timeHandler) PrintDebug() {

	i := 0

	this.noders.Range(func(key, value any) bool {
		i++
		return true
	})

	logger.Infof("handler info, nodeCnt:", i)
}
