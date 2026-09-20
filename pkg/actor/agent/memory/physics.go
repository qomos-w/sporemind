package memory

// Default constants matching Config defaults so pure functions remain
// deterministic without a Config instance.
const (
	defaultOntologyImmunityFrac = 0.0
)

// LayerTemp returns the layer temperature used as a decay multiplier.
// baseTemp is the per-node temperature when the layer is empty; growthK
// adds incremental pressure per existing node in the layer.
//
//   session:     baseTemp highest, growthK lowest  (individual memory is "hot" but layer pressure grows slowly)
//   ontology:    baseTemp lowest,  growthK highest (individual memory is "cool" but layer pressure grows fast)
func LayerTemp(nodeCount int, baseTemp, growthK float64) float64 {
	return baseTemp + growthK*float64(nodeCount)
}

// DecayNode returns the node energy after one tick of decay.
// If isAwake is false the weight is returned unchanged.
// The result is clamped to [minEnergy, weight] so a node never drops
// below its configured floor in a single tick. baseEnergy is retained
// in the signature for compatibility but no longer clamps the result:
// decay penetrates below baseEnergy so nodes can reach the death line.
func DecayNode(weight, rate, baseEnergy, minEnergy float64, isAwake bool) float64 {
	if !isAwake {
		return weight
	}
	loss := weight * rate
	if loss > weight {
		loss = weight
	}
	result := weight - loss
	if result < minEnergy {
		result = minEnergy
	}
	return result
}

// AntagonistDrain returns the energy that should be transferred from the
// loser to the winner when an antagonist siphon fires:
// min(SiphonEnergy, loserWeight).  The winnerActivity parameter is reserved
// for future scaling.
func AntagonistDrain(winnerActivity, loserWeight float64) float64 {
	if winnerActivity <= 0 || loserWeight <= 0 {
		return 0
	}
	if winnerActivity < loserWeight {
		return winnerActivity
	}
	return loserWeight
}

// ShouldEvaporate returns true when weight has fallen below the death
// threshold (minEnergy), indicating the node should be marked for death.
func ShouldEvaporate(weight, minEnergy float64) bool {
	return weight < minEnergy
}

// LayerPressure grows continuously from zero at the stable target to one at
// the high-pressure count. Counts beyond high remain fully pressured.
func LayerPressure(count, target, high int) float64 {
	if target < 0 || high <= target || count <= target {
		return 0
	}
	if count >= high {
		return 1
	}
	return float64(count-target) / float64(high-target)
}

// RecallScore computes the ranking score for a node at the given tick:
// energy * recency where recency = 1 / (1 + ticksSinceLastAccess * tickDecayK).
// This is step-based: recency decays with tick progression (token-driven),
// not wall-clock time.
func RecallScore(node *Node, currentTick int, tickDecayK float64) float64 {
	delta := currentTick - node.LastAccessTick
	if delta < 0 {
		delta = 0
	}
	recency := 1.0 / (1.0 + float64(delta)*tickDecayK)
	return node.Energy * recency
}
