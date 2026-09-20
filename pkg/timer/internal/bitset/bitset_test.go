package bitset

import (
	"testing"
	"time"
)

func TestBit(t *testing.T) {
	var a BitSet = 0
	a = a.Set(7, true)
	t.Log(a.Print())
	t.Log(a.Get(7))
	t.Log(a.Get(6))
	a = a.Set(3, true)
	a = a.Set(4, true)
	a = a.Set(4, false)
	t.Log(a.Print())
	t.Log(a.Values(8))
}

func TestTime(t *testing.T) {
	testArr(t)
	testBitset(t)
}

func testArr(t *testing.T) {
	var a, b, c bool
	start := time.Now()
	for i := 0; i < 1000000; i++ {
		arr := [64]bool{}
		arr[3] = true
		arr[5] = true
		arr[6] = true
		a = arr[4]
		b = arr[5]
		c = arr[6]
	}
	end := time.Now()
	t.Log(a, b, c, end.Sub(start))
}

func testBitset(t *testing.T) {
	var a, b, c bool
	start := time.Now()
	for i := 0; i < 1000000; i++ {
		arr := BitSet(0)
		arr = arr.Set(3, true)
		arr = arr.Set(5, true)
		arr = arr.Set(6, true)
		a = arr.Get(4)
		b = arr.Get(5)
		c = arr.Get(6)
	}
	end := time.Now()
	t.Log(a, b, c, end.Sub(start))
}
