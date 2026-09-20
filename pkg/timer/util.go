package timer

import (
	"fmt"
	"time"
)

// timerRecover handles a recovered panic value (logs it without re-panicking).
func timerRecover(r interface{}) {
	fmt.Printf("timer: recovered from panic: %v\n", r)
}

// getDaysOfMonth returns the number of days in the month of t.
func getDaysOfMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

// getNextMonthsStart returns the first instant of the month that is n months after t's month.
func getNextMonthsStart(t time.Time, n int) time.Time {
	return time.Date(t.Year(), t.Month()+time.Month(n), 1, 0, 0, 0, 0, t.Location())
}

// getNextYearStart returns midnight on January 1 of the year following t.
func getNextYearStart(t time.Time) time.Time {
	return time.Date(t.Year()+1, 1, 1, 0, 0, 0, 0, t.Location())
}

// getNextMonthStart returns midnight on the first day of the month following t.
func getNextMonthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
}

// getNextDayStart returns midnight at the start of the day following t.
func getNextDayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
}

// getMonthDayByLastWeekDay returns the day-of-month of the last occurrence
// of the given weekday in t's month.
func getMonthDayByLastWeekDay(t time.Time, weekday time.Weekday) int {
	// last calendar day of the month
	lastDay := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location())
	diff := int(lastDay.Weekday()) - int(weekday)
	if diff < 0 {
		diff += 7
	}
	return lastDay.Day() - diff
}

// getLastDaysOfMonth returns the time.Time for the nth-to-last day of t's month.
// n=1 → last day, n=2 → second-to-last day, etc.
func getLastDaysOfMonth(t time.Time, n int) time.Time {
	lastDay := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location())
	return lastDay.AddDate(0, 0, -(n - 1))
}
