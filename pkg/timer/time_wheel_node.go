package timer

import (
	"github.com/qomos-w/sporemind/pkg/timer/internal/bitset"
	"github.com/qomos-w/sporemind/pkg/timer/internal/slice"
	"github.com/qomos-w/sporemind/pkg/timer/internal/xmath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	haveStop = uint32(1)
)

// 先使用sync.Mutex实现功能
// 后面使用cas优化
type Time struct {
	timeNode
	sync.Mutex

	// |---16bit---|---16bit---|------32bit-----|
	// |---level---|---index---|-------seq------|
	// level 在near盘子里就是1, 在T2ToTt[0]盘子里就是2起步
	// index 就是各自盘子的索引值
	// seq   自增id
	version uint64
}

func newTimeHead(level uint64, index uint64) *Time {
	head := &Time{}
	head.version = genVersionHeight(level, index)
	head.Init()
	return head
}

func genVersionHeight(level uint64, index uint64) uint64 {
	return level<<(32+16) | index<<32
}

func (t *Time) lockPushBack(node *timeNode, level uint64, index uint64) {
	t.Lock()
	defer t.Unlock()
	if atomic.LoadUint32(&node.stop) == haveStop {
		return
	}

	t.AddTail(&node.Head)
	atomic.StorePointer(&node.list, unsafe.Pointer(t))
	//更新节点的version信息
	atomic.StoreUint64(&node.version, atomic.LoadUint64(&t.version))
}

var _ TimeNoder = (*timeNode)(nil)

const ALL_WEEK bitset.BitSet = 0b1111111
const ALL_MONTH bitset.BitSet = 0b111111111111
const ALL_DAY bitset.BitSet = 0b1111111111111111111111111111111
const ALL_HOUR bitset.BitSet = 0b111111111111111111111111
const ALL_MINUTE bitset.BitSet = 0b111111111111111111111111111111111111111111111111111111111111
const ALL_SECOND bitset.BitSet = ALL_MINUTE

type timeNode struct {
	expire uint64
	// userExpire time.Duration
	callback func(TimeNoder)
	stop     uint32
	list     unsafe.Pointer //存放表头信息
	version  uint64         //保存节点版本信息
	// isSchedule bool
	delay        uint64
	interval     uint64
	loopCur      uint64
	loopMax      uint64
	isCron       bool
	loc          *time.Location // cron timezone (nil → time.Local)
	month        bitset.BitSet
	weekday      bitset.BitSet
	monthday     bitset.BitSet
	hour         bitset.BitSet
	minute       bitset.BitSet
	second       bitset.BitSet
	lastMonthDay int  //在MonthDay模式代表每个月的倒数第几天,在WeekDay模式代表每个月的最后一个星期几
	useWeekDay   bool //切换MonthDay,WeekDay模式

	handler *timeHandler

	Head
}

// 一个timeNode节点有4个状态
// 1.存在于初始化链表中
// 2.被移动到tmp链表
// 3.1 和 3.2是if else的状态
//
//	3.1被移动到new链表
//	3.2直接执行
//
// 1和3.1状态是没有问题的
// 2和3.2状态会是没有锁保护下的操作,会有数据竞争
func (this *timeNode) Stop() {

	// Idempotent: only the first Stop may unlink the node. A second Stop would
	// otherwise decrement the slot length again and can corrupt a non-empty
	// slot so moveAndExec skips it (#9).
	if !atomic.CompareAndSwapUint32(&this.stop, 0, haveStop) {
		return
	}

	// 使用版本号算法让timeNode知道自己是否被移动了
	// timeNode的version和表头的version一样表示没有被移动可以直接删除
	// 如果不一样，可能在第2或者3.2状态，使用惰性删除
	cpyList := (*Time)(atomic.LoadPointer(&this.list))
	if cpyList == nil {
		// Node was created but never added to the wheel (or already unlinked).
		return
	}
	cpyList.Lock()
	defer cpyList.Unlock()
	if atomic.LoadUint64(&this.version) != atomic.LoadUint64(&cpyList.version) {
		return
	}

	cpyList.Del(&this.Head)
	atomic.StorePointer(&this.list, nil)

	this.handler.noders.Delete(this)
}

func (this *timeNode) GetDelay() time.Duration {
	return time.Duration(this.delay)
}

func (this *timeNode) GetInterval() time.Duration {
	return time.Duration(this.interval)
}

func (this *timeNode) GetCallback() func(TimeNoder) {
	return this.callback
}

// Next returns the next scheduled fire time. It returns the zero Time when the
// node has been stopped or when no wheel is attached.
func (this *timeNode) Next() time.Time {
	if this.handler == nil || this.handler.wheel == nil {
		return time.Time{}
	}
	if atomic.LoadUint32(&this.stop) == haveStop {
		return time.Time{}
	}
	wheel := this.handler.wheel
	if this.isCron {
		d, _ := this.cronExpireFunc(wheel)
		return wheel.Now().Add(time.Duration(d))
	}
	return wheel.Now().Add(time.Duration(this.interval))
}

// 基础打点更新函数
func (this *timeNode) intervalExpireFunc() (uint64, bool) {
	if this.loopMax > 0 && this.loopCur >= this.loopMax {
		return this.interval, false
	}
	return this.interval, true
}

func parseWeekWords(s string) (time.Weekday, error) {
	switch s {
	case "SUN":
		return time.Sunday, nil
	case "MON":
		return time.Monday, nil
	case "TUE":
		return time.Tuesday, nil
	case "WED":
		return time.Wednesday, nil
	case "THU":
		return time.Thursday, nil
	case "FRI":
		return time.Friday, nil
	case "SAT":
		return time.Saturday, nil
	}
	return -1, logger.Error("unrecognized week word:" + s)
}

// 忽略检测,仅检测星期和月天
func ignoreCheck(s string) bool {
	return regexp.MustCompile(`^\s*\?\s*$`).MatchString(s)
}

// 通配符检测
func everyCheck(s string) bool {
	return regexp.MustCompile(`^\s*\*\s*$`).MatchString(s)
}

// 如果是星期模式,返回下一个星期几,如果是月天模式,返回下一个月天
func lastCheck(s string) (bool, int) {
	r := regexp.MustCompile(`\s*([0-9]+)\s*L\s*`)
	if r.FindString(s) != s {
		return false, -1
	}
	ret, err := strconv.Atoi(r.ReplaceAllString(s, "$1"))
	if err != nil {
		logger.Error(err.Error())
		return false, -1
	}
	return true, ret
}
func divideCheck(s string, period int) (bool, []int) {
	r := regexp.MustCompile(`\s*(\d+)\s*\/\s*(\d+)`)
	res := r.FindString(s)
	if res == "" {
		return false, nil
	}
	splits := strings.Split(s, "/")
	if len(splits) != 2 {
		return false, nil
	}
	d1, err := strconv.Atoi(strings.TrimSpace(splits[0]))
	if err != nil {
		return false, nil
	}
	d2, err := strconv.Atoi(strings.TrimSpace(splits[1]))
	if err != nil {
		return false, nil
	}
	if d2 == 0 {
		return false, nil
	}
	ret := []int{}
	for i := 0; i < period; i++ {
		if (i+d1)%d2 == 0 {
			ret = append(ret, i)
		}
	}
	return true, ret
}

// 检测分隔符
func splitCheck(s string) (bool, []int) {
	r := regexp.MustCompile(`\s*(\d+)\s*(\-\s*\d+)?(\s*\,\s*(\d+)\s*(\-\s*\d+)?)*`)
	res := r.FindString(s)
	if res == "" {
		return false, nil
	}
	ret := []int{}
	splits := strings.Split(s, ",")
	for _, v := range splits {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		splits1 := strings.Split(v, "-")
		results := []int{}
		for _, v1 := range splits1 {
			v1 = strings.TrimSpace(v1)
			if v1 == "" {
				continue
			}
			d, err := strconv.Atoi(v1)
			if err != nil {
				logger.Error(err.Error())
				return false, ret
			}
			results = append(results, d)
		}
		if len(results) == 2 {
			min := results[0]
			max := results[1]
			if min > max {
				logger.Error("timewheel:parse error,range min must <= max " + s)
				return false, nil
			}
			for i := min; i <= max; i++ {
				ret = slice.AppendOnce(ret, i)
			}
		} else if len(results) == 1 {
			ret = slice.AppendOnce(ret, results[0])
		} else {
			logger.Error("timewheel:parse error")
			return false, nil
		}
	}
	return true, ret
}

func checkMaxMin(period int, entry []int, offset int) bool {
	var max = xmath.MaxArr[int](entry) + offset
	var min = xmath.MinArr[int](entry) + offset
	if min < 0 {
		return false
	}
	if max > period-1 {
		return false
	}
	return true
}

func checkCronString(s string, period int) (bitset.BitSet, error) {
	if everyCheck(s) {
		switch period {
		case 60:
			return ALL_MINUTE, nil
		case 12:
			return ALL_MONTH, nil
		case 7:
			return ALL_WEEK, nil
		case 24:
			return ALL_HOUR, nil
		case 31:
			return ALL_DAY, nil
		default:
			return 0, logger.Error("timewheel:period not exist")
		}
	}
	offset := 0
	if period == 31 || period == 12 {
		offset = -1
	}
	if ok, entry := divideCheck(s, period); ok {
		if !checkMaxMin(period, entry, offset) {
			return 0, logger.Error("timewheel:range error")
		}
		var ret bitset.BitSet = 0
		for _, v := range entry {
			ret = ret.Set(v+offset, true)
		}
		return ret, nil
	}
	if ok, entry := splitCheck(s); ok {
		if !checkMaxMin(period, entry, offset) {
			return 0, logger.Error("timewheel:range error")
		}
		var ret bitset.BitSet = 0
		for _, v := range entry {
			ret = ret.Set(v+offset, true)
		}
		return ret, nil
	}
	return 0, logger.Error("timewheel:parse error")
}

func (this *timeNode) parseCron(second, minute, hour, day, month, weekday string) error {
	ignore_day := ignoreCheck(day)
	ignore_weekday := ignoreCheck(weekday)
	if ignore_day && ignore_weekday {
		return logger.Error("timewheel:cant have 2 ?")
	}
	if !ignore_day && !ignore_weekday {
		return logger.Error("timewheel:must have 1 ? in day or week day")
	}
	if ignore_day {
		this.useWeekDay = true
		if ok, d := lastCheck(weekday); ok {
			if d < 0 || d > 6 {
				return logger.Error("timewheel:last weekday error,must be 0-6")
			}
			this.lastMonthDay = d
		} else {
			b, err := checkCronString(weekday, 7)
			if err != nil {
				return err
			}
			this.weekday = b
		}

	} else {
		this.useWeekDay = false
		if ok, d := lastCheck(day); ok {
			if d < 1 || d > 27 {
				return logger.Error("timewheel:last day error,must be 1-27")
			}
			this.lastMonthDay = d
		} else {
			b, err := checkCronString(day, 31)
			if err != nil {
				return err
			}
			this.monthday = b
		}
	}
	b, err := checkCronString(second, 60)
	if err != nil {
		return err
	}
	this.second = b
	b, err = checkCronString(minute, 60)
	if err != nil {
		return err
	}
	this.minute = b
	b, err = checkCronString(hour, 24)
	if err != nil {
		return err
	}
	this.hour = b
	b, err = checkCronString(month, 12)
	if err != nil {
		return err
	}
	this.month = b

	return nil
}

func (this timeNode) parseDebug() {
	logger.Infof("month", this.month.Values(12))
	logger.Infof("weekday", this.weekday.Values(7))
	logger.Infof("day", this.monthday.Values(31))
	logger.Infof("hour", this.hour.Values(24))
	logger.Infof("minute", this.minute.Values(60))
	logger.Infof("second", this.second.Values(60))
}

func (this timeNode) cronExpireFunc(t *timeWheel) (uint64, bool) {
	loc := this.loc
	if loc == nil {
		loc = time.Local
	}
	now := t.Now().In(loc)
	t1 := now
	year := now.Year()
	monthday := now.Day()
	month := int(now.Month())
	hour := now.Hour()
	minute := now.Minute()
	second := now.Second()
	move_month := false
	force_month := false // current month not in schedule → must advance to next valid month
	move_day := false
	move_hour := false
	move_minute := false
	move_next_day := false
	if this.useWeekDay {
		if !this.weekday.Get(int(now.Weekday())) {
			move_next_day = true
			hour = 0
			minute = 0
			second = 0
		}
	} else {
		if !this.monthday.Get(monthday - 1) {
			move_next_day = true
			hour = 0
			minute = 0
			second = 0
		}
	}

	if !this.month.Get(month - 1) {
		hour = 0
		minute = 0
		second = 0
		force_month = true // getNextDayMonthTime must seek the next valid month
	} else if !this.hour.Get(hour) {
		minute = 0
		second = 0
	} else if !this.minute.Get(minute) {
		second = 0
	}
	defer func() {
		if r := recover(); r != nil {
			timerRecover(r)
		}
	}()
	second, move_minute = this.getNextSecond(second, now.Nanosecond() != 0)
	minute, move_hour = this.getNextMinute(minute, move_minute)
	hour, move_day = this.getNextHour(hour, move_hour)
	move_day = move_day || move_next_day
	if move_day {
		t1, move_month = this.getNextDayStart(now, month)
	}
	move_month = move_month || force_month
	monthday, month, year = this.getNextDayMonthTime(t1, this.useWeekDay, this.lastMonthDay, move_month, false)
	next_time := time.Date(year, time.Month(month), monthday, hour, minute, second, 0, loc)
	d := next_time.Sub(now)
	if d <= 0 {
		// Computation landed on a past instant (e.g. rounding artefact); retry on the next tick.
		d = time.Millisecond * 10
	}
	return uint64(d), true
}

func (this *timeNode) getNextDayMonthTime(now time.Time, is_weekday bool, last_day int, move_month, move_year bool) (next_day, next_month, next_year int) {

	days := getDaysOfMonth(now)
	monthday := now.Day()
	weekday := int(now.Weekday())
	month := int(now.Month())
	year := now.Year()
	if move_month {
		for i := 0; i < 12; i++ {
			if this.month.Get(month - 1) {
				if i > 0 {
					now = getNextMonthsStart(now, i)
					return this.getNextDayMonthTime(now, is_weekday, last_day, true, false)
				} else {
					break
				}
			}
			month += 1
			if month > 12 {
				now = getNextYearStart(now)
				return this.getNextDayMonthTime(now, is_weekday, last_day, true, true)
			}
		}
	}

	if is_weekday {
		if last_day >= 0 {
			next_day = getMonthDayByLastWeekDay(now, time.Weekday(last_day))
			if monthday <= next_day {
				return next_day, month, year
			} else {
				next_month_time, is_move_year_or_not := this.getNextMonthStart(now, year)
				return this.getNextDayMonthTime(next_month_time, is_weekday, last_day, true, move_year || is_move_year_or_not)
			}
		}
		for i := 0; i < 7; i++ {
			if this.weekday.Get(weekday) {
				return monthday, month, year
			}
			weekday += 1
			monthday += 1
			if weekday > 6 {
				weekday = 0
			}
			if monthday > days {
				next_month_time, is_move_year_or_not := this.getNextMonthStart(now, year)
				return this.getNextDayMonthTime(next_month_time, is_weekday, last_day, true, move_year || is_move_year_or_not)
			}
		}
		logger.Panic("iterator out of range")
	}
	if last_day >= 0 {
		next_day = getLastDaysOfMonth(now, last_day).Day()
		if monthday <= next_day {
			return next_day, month, year
		} else {
			next_month_time, is_move_year_or_not := this.getNextMonthStart(now, year)
			return this.getNextDayMonthTime(next_month_time, is_weekday, last_day, true, move_year || is_move_year_or_not)
		}
	}
	for i := 0; i < days; i++ {
		if this.monthday.Get(monthday - 1) {
			return monthday, month, year
		}
		monthday += 1
		if monthday > days {
			next_month_time, is_move_year_or_not := this.getNextMonthStart(now, year)
			return this.getNextDayMonthTime(next_month_time, is_weekday, last_day, true, move_year || is_move_year_or_not)
		}
	}
	logger.Panic("iterator out of range")
	return
}

func (this *timeNode) getNextMonthStart(t time.Time, current_year int) (next_month_start time.Time, is_next_year bool) {
	ret := getNextMonthStart(t)
	return ret, ret.Year() > current_year
}

func (this *timeNode) getNextDayStart(t time.Time, current_month int) (next_day_start time.Time, is_next_month bool) {
	ret := getNextDayStart(t)
	return ret, int(ret.Month()) != current_month
}

func (this *timeNode) getNextHour(hour int, next bool) (next_hour int, move_day bool) {
	if !next && this.hour.Get(hour) {
		next_hour = hour
		return
	}
	for i := 0; i < 24; i++ {
		hour += 1
		if hour > 23 {
			hour = 0
			move_day = true
		}
		if this.hour.Get(hour) {
			next_hour = hour
			return
		}
	}
	logger.Panic("timewheel:hour not found")
	return
}

func (this *timeNode) getNextMinute(minute int, next bool) (next_minute int, move_hour bool) {
	if !next && this.minute.Get(minute) {
		next_minute = minute
		return
	}
	for i := 0; i < 60; i++ {
		minute += 1
		if minute > 59 {
			minute = 0
			move_hour = true
		}
		if this.minute.Get(minute) {
			next_minute = minute
			return
		}
	}
	logger.Panic("timewheel:minute not found")
	return
}

// getNextSecond returns the next matching second. When next is false the
// current second is kept if it already matches — this is the fast path that
// lets a schedule landing exactly on a second boundary fire on :00 instead of
// always skipping to :01 (#11).
func (this *timeNode) getNextSecond(second int, next bool) (next_second int, move_minute bool) {
	if !next && this.second.Get(second) {
		next_second = second
		return
	}
	for i := 0; i < 60; i++ {
		second += 1
		if second > 59 {
			second = 0
			move_minute = true
		}
		if this.second.Get(second) {
			next_second = second
			return
		}
	}
	logger.Panic("timewheel:second not found")
	return
}
