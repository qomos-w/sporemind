package xmath

import (
	"math"
	"math/rand"

	"github.com/qomos-w/sporemind/pkg/timer/internal/slice"
)

// SRandom returns a pseudo-random float64 in [0, 1).
// Without a seed it delegates to math/rand. With a seed it uses a fast
// deterministic hash; the result is clamped to strictly less than 1.0 to
// prevent index-out-of-bounds in callers that use it to index slices.
func SRandom(seed ...uint64) float64 {
	if len(seed) == 0 {
		return rand.Float64()
	}
	s1 := seed[0]
	s1 = s1*(s1*s1*15731+789221) + 1376312589
	r := float64(s1&0x7fffffff) / float64(0x7fffffff)
	// Guard: the hash can produce exactly 1.0 (when all 31 low bits are set),
	// which would cause off-by-one panics in PickOne / Shuffle.
	if r >= 1.0 {
		r = 0
	}
	return r
}

func Rng[T Number](val1, val2 T, seed ...uint64) T {
	var minVal, maxVal T
	if val1 < val2 {
		minVal = val1
		maxVal = val2
	} else {
		minVal = val2
		maxVal = val1
	}
	return minVal + T(float64(maxVal-minVal)*SRandom(seed...))
}

func Polar(seed ...uint64) float64 {
	if OneIn(2.0, seed...) {
		return -1.0
	}
	return 1.0
}

// PolarInt returns +1 or -1 with equal probability.
func PolarInt(seed ...uint64) int {
	if OneIn(2.0, seed...) {
		return -1
	}
	return 1
}

func OneIn[T Number](chance T, seed ...uint64) bool {
	return chance <= 1 || Rng(0, float64(chance), seed...) < 1.0
}

func XInY[T Number](x T, y T, seed ...uint64) bool {
	return SRandom(seed...) < float64(x)/float64(y)
}

type WeightAble interface {
	Weight() float64
}

// removeAt removes element at index from slice
func removeAt[T any](slice []T, index int) []T {
	if index < 0 || index >= len(slice) {
		return slice
	}
	result := make([]T, 0, len(slice)-1)
	result = append(result, slice[:index]...)
	result = append(result, slice[index+1:]...)
	return result
}

func PickOneWeightSelect[T WeightAble](weightList []T, seed ...uint64) (int, T, []T) {
	if len(weightList) == 0 {
		panic("weight list must > 0")
	}
	sum := 0.0
	for i := 0; i < len(weightList); i++ {
		sum += weightList[i].Weight()
	}
	r := SRandom(seed...) * sum
	var i int
	for i = 0; i < len(weightList); i++ {
		weight := weightList[i].Weight()
		if weight == 0 {
			continue
		}
		if weight >= r {
			break
		} else {
			r -= weight
		}
	}
	ret := weightList[i]
	arr := removeAt(weightList, i)
	return i, ret, arr
}
func PickSomeWeightSelect[T WeightAble](weightList []T, num int, seed ...uint64) (results []T, remain []T) {
	results = []T{}
	remain = weightList
	var ret T
	seeds := make([]uint64, 0)
	if len(seed) > 0 {
		for i := 0; i < len(seed); i++ {
			seeds = append(seeds, seed[i]+uint64(i))
		}
	} else {
	}
	for i := 0; i < num; i++ {
		_, ret, remain = PickOneWeightSelect(remain, seeds...)
		if len(seed) != 0 {
			seed[0]++
		}
		results = append(results, ret)
	}
	return results, remain
}

func WeightSelect[T WeightAble](weightList []T, seed ...uint64) (int, T) {
	if len(weightList) == 0 {
		panic("weight list must > 0")
	}
	sum := 0.0
	for i := 0; i < len(weightList); i++ {
		sum += weightList[i].Weight()
	}

	if sum == 0 {
		// 所有权重都是0，返回第一个
		return 0, weightList[0]
	}

	r := SRandom(seed...) * sum

	for i := 0; i < len(weightList); i++ {
		weight := weightList[i].Weight()
		if weight == 0 {
			continue
		}
		if weight >= r {
			return i, weightList[i]
		}
		r -= weight
	}

	// 理论上不应该到这里，但为了安全返回最后一个非零权重的元素
	for i := len(weightList) - 1; i >= 0; i-- {
		if weightList[i].Weight() > 0 {
			return i, weightList[i]
		}
	}

	return 0, weightList[0]
}

func Shuffle[T any](arr []T, seed ...uint64) []T {
	sr := true
	if len(seed) == 0 {
		sr = false
	}
	leng := len(arr)
	var temp T
	var random int
	seedMod := 0
	collection := slice.Concat(arr)
	for leng > 0 {
		seedMod++
		if sr {
			random = int(math.Floor(SRandom(seed[0]+uint64(seedMod)) * float64(leng)))
		} else {
			random = int(math.Floor(rand.Float64() * float64(leng)))
		}
		if random >= leng {
			random = leng - 1
		}
		leng -= 1
		temp = collection[leng]
		collection[leng] = collection[random]
		collection[random] = temp
	}
	return collection
}

func GetOne[T any](arr []T, seed ...uint64) T {
	if len(arr) == 0 {
		var zero T
		return zero
	}
	index := Rng(0, len(arr)-1, seed...)
	return arr[index]
}

func PickOne[T any](arr []T, seed ...uint64) (T, []T) {
	leng := len(arr)
	if leng == 0 {
		var zero T
		return zero, nil
	}
	index := int(math.Floor(float64(leng) * SRandom(seed...)))
	if index >= leng {
		index = leng - 1
	}
	var ret T
	retOrigin := make([]T, 0, leng-1)
	for i := 0; i < leng; i++ {
		if i == index {
			ret = arr[i]
			continue
		}
		retOrigin = append(retOrigin, arr[i])
	}
	return ret, retOrigin
}

func PickSome[T any](arr []T, num int, seed ...uint64) ([]T, []T) {
	if num > len(arr) {
		num = len(arr)
	}

	ret := make([]T, 0, num)
	remain := make([]T, len(arr))
	copy(remain, arr)

	for i := 0; i < num; i++ {
		if len(remain) == 0 {
			break
		}
		var picked T
		picked, remain = PickOne(remain, seed...)
		if len(seed) != 0 {
			seed[0]++
		}
		ret = append(ret, picked)
	}
	return ret, remain
}
