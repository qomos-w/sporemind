package xmath

import (
	"fmt"
	"math"
)

const PI = math.Pi

type Number interface {
	Signed | UnSignedInt
}

type SignedInt interface {
	int8 | int16 | int32 | int64 | int
}

type Integer interface {
	SignedInt
	UnSignedInt
}

type Signed interface {
	SignedInt | Float
}

type UnSignedInt interface {
	uint8 | uint16 | uint32 | uint64 | uint
}

type Float interface {
	float32 | float64
}

func Ternary[T any](cond bool, a T, b T) T {
	if cond {
		return a
	}
	return b
}

func Sin[T Number](a T) T {
	return T(math.Sin(float64(a)))
}

func Cos[T Number](a T) T {
	return T(math.Cos(float64(a)))
}

func Tan[T Number](a T) T {
	return T(math.Tan(float64(a)))
}

func Asin[T Number](a T) T {
	return T(math.Asin(float64(a)))
}

func Acos[T Number](a T) T {
	return T(math.Acos(float64(a)))
}

func Atan[T Number](a T) T {
	return T(math.Atan(float64(a)))
}

func Abs[T Signed](a T) T {
	if a < 0 {
		return -a
	}
	return a
}

func Sum[T Number](a []T) T {
	var sum T
	for i := 0; i < len(a); i++ {
		sum += a[i]
	}
	return sum
}

// NumberMax returns the maximum representable value of type T.
// Each constant is explicitly typed before boxing so the interface assertion
// succeeds for all numeric types.
func NumberMax[T Number]() T {
	var ret T
	switch any(ret).(type) {
	case int8:
		return any(int8(math.MaxInt8)).(T)
	case int16:
		return any(int16(math.MaxInt16)).(T)
	case int32:
		return any(int32(math.MaxInt32)).(T)
	case int64:
		return any(int64(math.MaxInt64)).(T)
	case int:
		return any(int(math.MaxInt)).(T)
	case uint8:
		return any(uint8(math.MaxUint8)).(T)
	case uint16:
		return any(uint16(math.MaxUint16)).(T)
	case uint32:
		return any(uint32(math.MaxUint32)).(T)
	case uint64:
		return any(uint64(math.MaxUint64)).(T)
	case uint:
		return any(uint(math.MaxUint)).(T)
	case float32:
		return any(float32(math.MaxFloat32)).(T)
	case float64:
		return any(float64(math.MaxFloat64)).(T)
	default:
		panic(fmt.Sprintf("xmath: unsupported type %T", ret))
	}
}

// NumberMin returns the minimum representable value of type T.
func NumberMin[T Number]() T {
	var ret T
	switch any(ret).(type) {
	case int8:
		return any(int8(math.MinInt8)).(T)
	case int16:
		return any(int16(math.MinInt16)).(T)
	case int32:
		return any(int32(math.MinInt32)).(T)
	case int64:
		return any(int64(math.MinInt64)).(T)
	case int:
		return any(int(math.MinInt)).(T)
	case uint8:
		return any(uint8(0)).(T)
	case uint16:
		return any(uint16(0)).(T)
	case uint32:
		return any(uint32(0)).(T)
	case uint64:
		return any(uint64(0)).(T)
	case uint:
		return any(uint(0)).(T)
	case float32:
		return any(float32(-math.MaxFloat32)).(T)
	case float64:
		return any(float64(-math.MaxFloat64)).(T)
	default:
		panic(fmt.Sprintf("xmath: unsupported type %T", ret))
	}
}

func Sqrt[T Number](a T) T {
	return T(math.Sqrt(float64(a)))
}

func Dist[T Number](x1, y1, x2, y2 T) T {
	return Sqrt(Pow(x2-x1, 2) + Pow(y2-y1, 2))
}

func Pow[T Number](x, y T) T {
	return T(math.Pow(float64(x), float64(y)))
}

func Log[T Number](x T) T {
	return T(math.Log(float64(x)))
}

func Exp[T Number](x T) T {
	return T(math.Exp(float64(x)))
}

func MaxArr[T Number](v []T) T {
	var max T
	for i := 0; i < len(v); i++ {
		if i == 0 {
			max = v[i]
		} else {
			if v[i] > max {
				max = v[i]
			}
		}
	}
	return max
}

func MinArr[T Number](v []T) T {
	var min T
	for i := 0; i < len(v); i++ {
		if i == 0 {
			min = v[i]
		} else {
			if v[i] < min {
				min = v[i]
			}
		}
	}
	return min
}

func Max[T Number](x ...T) T {
	return MaxArr(x)
}

func Min[T Number](x ...T) T {

	return MinArr(x)
}

func Lerp[T Number, T1 Float](t T1, a, b T) T {
	return T(float64(a) + float64(t)*float64(b-a))
}

func Range[T Number](start, end T) []T {
	var s1, e1 T
	if start > end {
		s1 = end
		e1 = start
	} else {
		s1 = start
		e1 = end
	}
	ret := make([]T, 0)
	for i := s1; i < e1; i++ {
		ret = append(ret, i)
	}
	return ret
}

func Step[T Number](edge T, x T) T {
	if x < edge {
		return 0
	}
	return 1
}

func Clamp[T Number](v T, min T, max T) T {
	return Min(Max(v, min), max)
}

func Sign[T Signed](v T) T {
	if v > 0 {
		return 1
	}
	if v < 0 {
		return -1
	}
	return 0
}

func Floor[T Float](v T) T {
	return T(math.Floor(float64(v)))
}

func Round[T Float](v T) T {
	return T(math.Round(float64(v)))
}

func Ceil[T Float](v T) T {
	return T(math.Ceil(float64(v)))
}

func Fract[T Float](v T) T {
	return v - Floor(v)
}
