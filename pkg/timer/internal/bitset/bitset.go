package bitset

import (
	"strconv"
)

type BitSet uint64

func (this BitSet) Values(len int) []int {
	if len > 64 {
		len = 64
	}
	ret := make([]int, 0, len)
	b := this
	for i := 0; i < len; i++ {
		if b&1 == 1 {
			ret = append(ret, i)
		}
		b = b >> 1
	}
	return ret
}

func (this BitSet) Get(index int) bool {
	return (this>>index)&1 == 1
}

func (this BitSet) Print() string {
	return strconv.FormatInt(int64(this), 2)
}

func (this BitSet) Set(index int, v bool) BitSet {
	if v {
		return this | (1 << index)
	} else {
		return this & (^(1 << index))
	}
}

type BitSetInt32 int32

func (this BitSetInt32) Values(len int) []int {
	if len > 32 {
		len = 32
	}
	ret := make([]int, 0, len)
	b := this
	for i := 0; i < len; i++ {
		if b&1 == 1 {
			ret = append(ret, i)
		}
		b = b >> 1
	}
	return ret
}

func (this BitSetInt32) Get(index int) bool {
	return (this>>index)&1 == 1
}

func (this BitSetInt32) Print() string {
	return strconv.FormatInt(int64(this), 2)
}

func (this BitSetInt32) Set(index int, v bool) BitSetInt32 {
	if v {
		return this | (1 << index)
	} else {
		return this & (^(1 << index))
	}
}

type BitSetInt int

func (this BitSetInt) Values(len int) []int {
	if len > 32 {
		len = 32
	}
	ret := make([]int, 0, len)
	b := this
	for i := 0; i < len; i++ {
		if b&1 == 1 {
			ret = append(ret, i)
		}
		b = b >> 1
	}
	return ret
}

func (this BitSetInt) Get(index int) bool {
	return (this>>index)&1 == 1
}

func (this BitSetInt) Print() string {
	return strconv.FormatInt(int64(this), 2)
}

func (this BitSetInt) Set(index int, v bool) BitSetInt {
	if v {
		return this | (1 << index)
	} else {
		return this & (^(1 << index))
	}
}

type BitSetInt64 int64

func (this BitSetInt64) Values(len int) []int {
	if len > 64 {
		len = 64
	}
	ret := make([]int, 0, len)
	b := this
	for i := 0; i < len; i++ {
		if b&1 == 1 {
			ret = append(ret, i)
		}
		b = b >> 1
	}
	return ret
}

func (this BitSetInt64) Get(index int) bool {
	return (this>>index)&1 == 1
}

func (this BitSetInt64) Print() string {
	return strconv.FormatInt(int64(this), 2)
}

func (this BitSetInt64) Set(index int, v bool) BitSetInt64 {
	if v {
		return this | (1 << index)
	} else {
		return this & (^(1 << index))
	}
}

type BitSetMapEnum[T comparable] struct {
	sets map[T]bool
}

func NewBitSetEnum[T comparable]() *BitSetMapEnum[T] {
	return &BitSetMapEnum[T]{
		sets: make(map[T]bool),
	}
}

func (this *BitSetMapEnum[T]) Set(index T, v bool) {
	if v {
		this.sets[index] = true
	} else {
		delete(this.sets, index)
	}
}

func (this *BitSetMapEnum[T]) Get(index T) bool {
	return this.sets[index]
}

func (this *BitSetMapEnum[T]) Values() []T {
	ret := make([]T, 0, len(this.sets))
	for k := range this.sets {
		ret = append(ret, k)
	}
	return ret
}
