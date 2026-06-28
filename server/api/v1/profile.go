package v1

import (
	"math"
)

// The profile / signal / delta types describe a forward-looking
// "threat model" view derived from classification metrics. They are
// not wired into any live route yet; kept here so the executor can
// pick them up when the corresponding viz is added.

// MetricAgg is the per-label aggregate returned by classification's
// metric query: count, mean confidence, and population variance.
type MetricAgg struct {
	Label    string  `db:"label"`
	Count    int     `db:"n"`
	Mean     float64 `db:"mean"`
	Variance float64 `db:"var"`
}

// Signal collapses one label's MetricAgg into a single weighted score
// plus its component dimensions (frequency, mean confidence, variance,
// derived entropy).
type Signal struct {
	Frequency float64 `json:"frequency"`
	Mean      float64 `json:"mean"`
	Variance  float64 `json:"variance"`
	Weight    float64 `json:"weight"`
	Entropy   float64 `json:"entropy"`
}

// Profile is one period's set of Signals grouped by classification
// dimension (vector / severity / actor / sector).
type Profile struct {
	Start    string            `json:"start"`
	End      string            `json:"end"`
	Vector   map[string]Signal `json:"vector"`
	Severity map[string]Signal `json:"severity"`
	Actor    map[string]Signal `json:"actor"`
	Sector   map[string]Signal `json:"sector"`
}

// DeltaPoint is per-label change between two Profiles: weight drift,
// entropy shift, and a human label summarising the transition.
type DeltaPoint struct {
	WeightDrift  float64 `json:"weight_drift"`
	EntropyShift float64 `json:"entropy_shift"`
	Status       string  `json:"status"`
}

// Delta is the full set of DeltaPoints across every dimension of a
// Profile pair.
type Delta struct {
	Vector   map[string]DeltaPoint `json:"vector"`
	Severity map[string]DeltaPoint `json:"severity"`
	Actor    map[string]DeltaPoint `json:"actor"`
	Sector   map[string]DeltaPoint `json:"sector"`
}

// ThreatModel pairs two Profiles (A = baseline, B = compare) with the
// Delta between them.
type ThreatModel struct {
	A Profile
	B Profile
	Delta
}

// IntoSignal collapses a MetricAgg into a Signal. Entropy uses a
// binary-entropy approximation; weight = frequency * mean * (1-variance).
func IntoSignal(met *MetricAgg, total int) *Signal {
	freq := float64(met.Count) / float64(total)

	var entropy float64
	if met.Mean > 0 && met.Mean < 1 {
		entropy = -(met.Mean*math.Log2(met.Mean) + (1-met.Mean)*math.Log2(1-met.Mean))
	}

	weight := freq * met.Mean * (1 - met.Variance)

	return &Signal{
		Frequency: freq,
		Mean:      met.Mean,
		Variance:  met.Variance,
		Weight:    weight,
		Entropy:   entropy,
	}
}

// IntoMappedSignal applies IntoSignal across a slice and keys the
// result by label so callers can look up signals by name.
func IntoMappedSignal(mets []*MetricAgg, total int) map[string]*Signal {
	out := make(map[string]*Signal)

	for _, met := range mets {
		out[met.Label] = IntoSignal(met, total)
	}

	return out
}
