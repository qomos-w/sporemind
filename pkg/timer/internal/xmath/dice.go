package xmath

import (
	"math"
)

// Dice is a seedable pseudo-random number generator.
// All methods advance the internal state by one step, so successive calls
// without an explicit seed produce a deterministic but varied sequence.
type Dice struct {
	_seed uint64
}

// NewDice creates a Dice instance. An optional seed may be provided.
func NewDice(seed ...uint64) *Dice {
	ret := &Dice{}
	if len(seed) > 0 {
		ret.SetSeed(seed[0])
	}
	return ret
}

// SetSeed resets the internal seed and returns the receiver for chaining.
func (this *Dice) SetSeed(seed uint64) *Dice {
	this._seed = seed
	return this
}

// SRandom returns a pseudo-random float64 in [0, 1) derived from the current
// seed WITHOUT advancing state. Clamped so it never reaches 1.0.
func (this *Dice) SRandom() float64 {
	s1 := this._seed
	s1 = s1*(s1*s1*15731+789221) + 1376312589
	r := float64(s1&0x7fffffff) / float64(0x7fffffff)
	if r >= 1.0 {
		r = 0
	}
	return r
}

// Random advances the seed by one step and returns SRandom.
// If a seed argument is provided the internal seed is first replaced.
func (this *Dice) Random(seed ...uint64) float64 {
	if len(seed) > 0 {
		this.SetSeed(seed[0])
	}
	this._seed++
	return this.SRandom()
}

// RandomInt returns a random int in [math.MinInt, math.MaxInt].
func (this *Dice) RandomInt(seed ...uint64) int {
	return this.RngInt(math.MinInt, math.MaxInt, seed...)
}

// Rng returns a random float64 in [min, max).
func (this *Dice) Rng(min, max float64, seed ...uint64) float64 {
	return min + (max-min)*this.Random(seed...)
}

// RngFloat returns a random float32 in [min, max).
func (this *Dice) RngFloat(min, max float32, seed ...uint64) float32 {
	return float32(float64(min) + float64(max-min)*this.Random(seed...))
}

// RngInt returns a random int in [min, max).
func (this *Dice) RngInt(min, max int, seed ...uint64) int {
	return int(float64(min) + float64(max-min)*this.Random(seed...))
}

// RngInt32 returns a random int32 in [min, max).
func (this *Dice) RngInt32(min, max int32, seed ...uint64) int32 {
	return int32(float64(min) + float64(max-min)*this.Random(seed...))
}

// OneIn returns true with probability 1/chance (i.e. "one in N").
func (this *Dice) OneIn(chance float64, seed ...uint64) bool {
	return chance <= 1.0 || this.Rng(0, chance, seed...) < 1.0
}

// Polar returns +1.0 or -1.0 with equal probability.
func (this *Dice) Polar(seed ...uint64) float64 {
	if this.OneIn(2.0, seed...) {
		return -1.0
	}
	return 1.0
}

// XInY returns true with probability x/y.
func (this *Dice) XInY(x, y float64, seed ...uint64) bool {
	return this.Random(seed...) < x/y
}

// XInYFloat returns true with probability x/y (float32 variant).
func (this *Dice) XInYFloat(x, y float32, seed ...uint64) bool {
	return this.Random(seed...) < float64(x)/float64(y)
}

// XInYInt returns true with probability x/y (int variant).
func (this *Dice) XInYInt(x, y int, seed ...uint64) bool {
	return this.Random(seed...) < float64(x)/float64(y)
}

// ── package-level Dice helpers ────────────────────────────────────────────────

// DiceRng returns a random value of type T in [min, max) using dice.
func DiceRng[T Number](min, max T, dice *Dice, seed ...uint64) T {
	return min + T(float64(max-min)*dice.Random(seed...))
}

// DiceOneIn returns true with probability 1/chance using dice.
func DiceOneIn[T Number](chance T, dice *Dice, seed ...uint64) bool {
	if chance <= 1 {
		return true
	}
	return dice.Rng(0, float64(chance), seed...) < 1.0
}

// DiceXInY returns true with probability min/max using dice.
func DiceXInY[T Number](x, y T, dice *Dice, seed ...uint64) bool {
	return dice.Random(seed...) < float64(x)/float64(y)
}

// DiceShuffle returns a new shuffled copy of list using the Fisher-Yates
// algorithm with dice as the random source.
// If a seed is supplied it initialises dice once before the loop; subsequent
// steps advance dice's internal state naturally.
func DiceShuffle[T any](list []T, dice *Dice, seed ...uint64) []T {
	if len(seed) > 0 {
		dice.SetSeed(seed[0])
	}
	n := len(list)
	out := make([]T, n)
	copy(out, list)
	for i := n - 1; i > 0; i-- {
		// random ∈ [0, i]
		j := int(dice.Random() * float64(i+1))
		if j > i {
			j = i
		}
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// DiceGetOne returns a random element from list using dice.
func DiceGetOne[T any](list []T, dice *Dice, seed ...uint64) T {
	if len(list) == 0 {
		var zero T
		return zero
	}
	if len(seed) > 0 {
		dice.SetSeed(seed[0])
	}
	idx := int(dice.Random() * float64(len(list)))
	if idx >= len(list) {
		idx = len(list) - 1
	}
	return list[idx]
}

// DiceWeightSelect picks one element proportional to its weight using dice.
func DiceWeightSelect[T WeightAble](weightList []T, dice *Dice, seed ...uint64) (int, T) {
	if len(weightList) == 0 {
		panic("xmath: DiceWeightSelect requires a non-empty list")
	}
	sum := 0.0
	for i := range weightList {
		sum += weightList[i].Weight()
	}
	r := dice.Random(seed...) * sum
	for i := range weightList {
		w := weightList[i].Weight()
		if w == 0 {
			continue
		}
		if w >= r {
			return i, weightList[i]
		}
		r -= w
	}
	// Floating-point remainder: return last non-zero element.
	for i := len(weightList) - 1; i >= 0; i-- {
		if weightList[i].Weight() > 0 {
			return i, weightList[i]
		}
	}
	return 0, weightList[0]
}

// DicePickOneWeightSelect removes and returns one element chosen by weight.
// The returned remainder is a new slice; the original is not modified.
func DicePickOneWeightSelect[T WeightAble](weightList []T, dice *Dice, seed ...uint64) (int, T, []T) {
	if len(weightList) == 0 {
		panic("xmath: DicePickOneWeightSelect requires a non-empty list")
	}
	sum := 0.0
	for i := range weightList {
		sum += weightList[i].Weight()
	}
	r := dice.Random(seed...) * sum
	for i := range weightList {
		w := weightList[i].Weight()
		if w == 0 {
			continue
		}
		if w >= r {
			ret := weightList[i]
			return i, ret, removeAt(weightList, i) // new slice, caller's data safe
		}
		r -= w
	}
	// Floating-point remainder: pick last non-zero element.
	for i := len(weightList) - 1; i >= 0; i-- {
		if weightList[i].Weight() > 0 {
			ret := weightList[i]
			return i, ret, removeAt(weightList, i)
		}
	}
	ret := weightList[0]
	return 0, ret, removeAt(weightList, 0)
}

// DicePickSomeWeightSelect picks num elements by weight without replacement.
func DicePickSomeWeightSelect[T WeightAble](weightList []T, num int, dice *Dice, seed ...uint64) (results []T, remain []T) {
	if len(seed) > 0 {
		dice.SetSeed(seed[0])
	}
	remain = weightList
	results = make([]T, 0, num)
	for i := 0; i < num && len(remain) > 0; i++ {
		var ret T
		_, ret, remain = DicePickOneWeightSelect(remain, dice)
		results = append(results, ret)
	}
	return results, remain
}
