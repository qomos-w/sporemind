package timer

import (
	"context"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	nearShift = 8

	nearSize = 1 << nearShift

	levelShift = 6

	levelSize = 1 << levelShift

	nearMask = nearSize - 1

	levelMask = levelSize - 1
)

type timeWheel struct {
	// 单调递增累加值, 走过一个时间片就+1
	jiffies uint64

	// 256个槽位
	t1 [nearSize]*Time

	// 4个64槽位, 代表不同的刻度
	t2Tot5 [4][levelSize]*Time

	// 时间只精确到10ms
	// curTimePoint 为1就是10ms 为2就是20ms
	// 由 ticker goroutine 写、Now()/调度方读，必须原子访问。
	curTimePoint atomic.Int64

	// 上下文
	ctx context.Context

	// 取消函数
	cancel context.CancelFunc
}

func newTimeWheel() *timeWheel {

	ctx, cancel := context.WithCancel(context.Background())

	t := &timeWheel{
		ctx:    ctx,
		cancel: cancel,
	}

	t.init()

	return t
}

func (t *timeWheel) Now() time.Time {
	return time.UnixMilli(t.curTimePoint.Load() * 10).Local()
}

func (t *timeWheel) init() {

	// t.timeEvent = make(chan interface{})

	for i := 0; i < nearSize; i++ {
		t.t1[i] = newTimeHead(1, uint64(i))

	}

	// 4个64槽位, 代表不同的刻度
	for i := 0; i < 4; i++ {
		for j := 0; j < levelSize; j++ {
			t.t2Tot5[i][j] = newTimeHead(uint64(i+2), uint64(j))
		}
	}

	t.curTimePoint.Store(int64(get10Ms()))
}

func maxVal() uint64 {
	return (1 << (nearShift + 4*levelShift)) - 1
}

func levelMax(index int) uint64 {
	return 1 << (nearShift + index*levelShift)
}

func (t *timeWheel) index(n int) uint64 {
	return (t.jiffies >> (nearShift + levelShift*n)) & levelMask
}

func (t *timeWheel) add(node *timeNode, jiffies uint64) *timeNode {

	var head *Time
	expire := node.expire
	// expire must be strictly in the future: a past/zero expire would make the
	// unsigned subtraction below wrap around (~497 days at 10ms resolution),
	// and an expire equal to the current jiffies would be processed in a slot
	// that has already been drained this tick. Clamp to the next tick, which is
	// also the minimum resolution handle for sub-10ms delays.
	if expire <= jiffies {
		expire = jiffies + 1
	}
	node.expire = expire
	// 获取到期时间
	idx := expire - jiffies

	// 从t1中找到对应的槽位
	level, index := uint64(1), uint64(0)
	// 从t1中找到对应的槽位
	if idx < nearSize {

		index = uint64(expire) & nearMask
		head = t.t1[index]

	} else {
		max := maxVal()
		for i := 0; i <= 3; i++ {

			if idx > max {
				idx = max
				expire = idx + jiffies
			}

			if uint64(idx) < levelMax(i+1) {
				index = uint64(expire >> (nearShift + i*levelShift) & levelMask)
				head = t.t2Tot5[i][index]
				level = uint64(i) + 2
				break
			}
		}
	}

	if head == nil {
		panic("not found head")
	}

	head.lockPushBack(node, level, index)

	return node
}

// func (t *timeWheel) AfterFunc(expire time.Duration, callback func()) TimeNoder {

// 	jiffies := atomic.LoadUint64(&t.jiffies)

// 	expire = expire/(time.Millisecond*10) + time.Duration(jiffies)

// 	node := &timeNode{
// 		expire:   uint64(expire),
// 		callback: callback,
// 	}

// 	return t.add(node, jiffies)
// }

func getExpire(expire time.Duration, jiffies uint64) time.Duration {
	return expire/(time.Millisecond*10) + time.Duration(jiffies)
}

// func (t *timeWheel) ScheduleFunc(userExpire time.Duration, callback func()) TimeNoder {

// 	jiffies := atomic.LoadUint64(&t.jiffies)

// 	expire := getExpire(userExpire, jiffies)

// 	node := &timeNode{
// 		userExpire: userExpire,
// 		expire:     uint64(expire),
// 		callback:   callback,
// 		isSchedule: true,
// 	}

// 	return t.add(node, jiffies)
// }

// func (t *timeWheel) After(delay time.Duration, cb func()) TimeNoder {
// 	jiffies := atomic.LoadUint64(&t.jiffies)

// 	expire := delay/(time.Millisecond*10) + time.Duration(jiffies)

// 	node := &timeNode{
// 		expire: uint64(expire),

// 		callback: cb,
// 	}

// 	return t.add(node, jiffies)
// }

// func (t *timeWheel) Schedule(interval time.Duration, cb func()) TimeNoder {
// 	jiffies := atomic.LoadUint64(&t.jiffies)

// 	expire := getExpire(interval, jiffies)

// 	node := &timeNode{
// 		userExpire: interval,
// 		expire:     uint64(expire),
// 		callback:   cb,
// 		isSchedule: true,

// 		delay:    uint64(0),
// 		interval: uint64(interval),
// 		loop:     0,
// 	}

// 	return t.add(node, jiffies)
// }

func (t *timeWheel) Stop() {
	t.cancel()
}

func (t *timeWheel) NewHandler() TimeHandler {

	return &timeHandler{
		wheel: t,
	}
}

func (t *timeWheel) cascade(levelIndex int, index int) {

	tmp := newTimeHead(0, 0)

	l := t.t2Tot5[levelIndex][index]
	l.Lock()
	if l.Len() == 0 {
		l.Unlock()
		return
	}

	l.ReplaceInit(&tmp.Head)

	atomic.AddUint64(&l.version, 1)
	l.Unlock()

	offset := unsafe.Offsetof(tmp.Head)
	tmp.ForEachSafe(func(pos *Head) {
		node := (*timeNode)(pos.Entry(offset))
		t.add(node, atomic.LoadUint64(&t.jiffies))
	})

}

func (t *timeWheel) moveAndExec() {

	//如果本层的盘子没有定时器，这时候从上层的盘子移动一些过来
	index := t.jiffies & nearMask
	if index == 0 {
		for i := 0; i <= 3; i++ {
			index2 := t.index(i)
			t.cascade(i, int(index2))
			if index2 != 0 {
				break
			}
		}
	}

	atomic.AddUint64(&t.jiffies, 1)
	t.t1[index].Lock()
	if t.t1[index].Len() == 0 {
		t.t1[index].Unlock()
		return
	}

	head := newTimeHead(0, 0)
	t1 := t.t1[index]
	t1.ReplaceInit(&head.Head)
	atomic.AddUint64(&t1.version, 1)
	t.t1[index].Unlock()

	// 执行,链表中的定时器
	offset := unsafe.Offsetof(head.Head)

	head.ForEachSafe(func(pos *Head) {
		val := (*timeNode)(pos.Entry(offset))
		head.Del(pos)
		val.loopCur++
		var interval uint64
		var isLoop bool
		if !val.isCron {
			interval, isLoop = val.intervalExpireFunc()
		} else {
			interval, isLoop = val.cronExpireFunc(t)
		}
		val.interval = interval
		if !isLoop {
			val.handler.noders.Delete(val)
		}

		if atomic.LoadUint32(&val.stop) == haveStop {
			return
		}

		if isLoop {
			jiffies := t.jiffies
			// 这里的jiffies必须要减去1
			// 当前的callback被调用，已经包含一个时间片,如果不把这个时间片减去，
			// 每次多一个时间片，就变成累加器, 最后周期定时器慢慢会变得不准

			// val.expire = uint64(getExpire(val.userExpire, jiffies-1))
			val.expire = val.interval/(uint64(time.Millisecond)*10) + jiffies
			t.add(val, jiffies)
		}

		// Dispatch the callback and the event message on an independent
		// goroutine. The single wheel goroutine must never block on a slow
		// callback or on a full event channel, otherwise every timer in the
		// wheel stalls behind the slow one (#2). Re-arming happens above,
		// before dispatch: lockPushBack re-checks the stop flag under the slot
		// lock, so a Stop() called from inside the callback still cancels the
		// next fire (#1).
		cb := val.callback
		h := val.handler
		msg := &TimeEventMsg{
			Callback:  cb,
			TimeNoder: val,
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					timerRecover(r)
				}
			}()
			if cb != nil {
				cb(val)
			}
			h.deliver(msg)
		}()

	})

}

// get10Ms函数通过参数传递，为了方便测试
func (t *timeWheel) run(get10Ms func() time.Duration) {
	// 先判断是否需要更新
	// 内核里面实现使用了全局jiffies和本地的jiffies比较,应用层没有jiffies，直接使用时间比较
	// 这也是skynet里面的做法

	ms10 := get10Ms()
	cur := time.Duration(t.curTimePoint.Load())
	if ms10 < cur {
		// Wall clock jumped backwards (e.g. an NTP correction). Do NOT rebase
		// the wheel onto the earlier instant: rebasing would silently delay
		// every outstanding timer by the size of the jump. Skip this tick and
		// let the wall clock catch back up to the wheel's clock.
		return
	}

	diff := ms10 - cur
	// A large forward jump (suspend/resume, VM pause) must not be replayed in
	// one burst: cap how many 10ms ticks a single tick can process. The wheel
	// then catches up at maxCatchUpTicks per real tick instead of freezing or
	// spinning through hours of backlog.
	const maxCatchUpTicks = 100 // 1s of wheel time per 10ms real tick
	if diff > maxCatchUpTicks {
		diff = maxCatchUpTicks
	}
	t.curTimePoint.Store(int64(cur + diff))

	for i := 0; i < int(diff); i++ {
		t.moveAndExec()
	}

}

func (t *timeWheel) Run() {

	// 10ms精度
	tk := time.NewTicker(time.Millisecond * 10)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			t.run(get10Ms)
		case <-t.ctx.Done():
			return
		}
	}
}

func (t *timeWheel) Start() {

	go func() {
		tk := time.NewTicker(time.Millisecond * 10)
		defer tk.Stop()

		for {
			select {
			case <-tk.C:
				t.run(get10Ms)

			case <-t.ctx.Done():
				return

			}
		}
	}()
}
