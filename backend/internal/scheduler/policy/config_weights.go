package policy

import "math"

// config_weights.go validates global scoring coefficients and calculates their
// base and total sums.
// Global score configuration needs at least one positive base contribution.
// Affinity bonuses extend that base when weighted stickiness is active, but
// they cannot alone make process defaults valid. IsValid checks individual
// coefficients before evaluating the sums, so a negative or nonfinite field is
// rejected even when another field would offset it.
//
// Finite fields can still overflow when added. The base sum covers seven
// ordinary score factors, while the full sum also includes previous-response
// and session affinity. Both sums must remain finite for configuration
// admission. Group overrides use a separate validator that permits a zero
// base, which gives deliberate zero group weights a different meaning from
// unusable global defaults.

// ConfigScoreWeights holds process configuration coefficients before
// conversion to scoring weights. IsValid enforces finite nonnegative values
// and a positive base sum for global defaults.
type ConfigScoreWeights struct {
	Priority  float64 `yaml:"priority"`   // global priority coefficient
	Load      float64 `yaml:"load"`       // provider load coefficient
	Queue     float64 `yaml:"queue"`      // waiting count coefficient
	ErrorRate float64 `yaml:"error_rate"` // feedback health coefficient
	TTFT      float64 `yaml:"ttft"`       // token latency coefficient
	Reset     float64 `yaml:"reset"`      // window reset coefficient
	// platform quota coefficient
	QuotaHeadroom float64 `yaml:"quota_headroom"`
	// PreviousResponse and SessionSticky contribute bonuses only when
	// advanced weighted stickiness is enabled. They are included in the
	// total sum checked by IsValid but excluded from the required positive
	// base sum.
	// previous response bonus
	PreviousResponse float64 `yaml:"previous_response"`
	SessionSticky    float64 `yaml:"session_sticky"` // session provider bonus
}

// BaseWeightSum adds the seven coefficients for ordinary candidate score
// factors.
func (w ConfigScoreWeights) BaseWeightSum() float64 {
	return w.Priority + w.Load + w.Queue + w.ErrorRate + w.TTFT + w.Reset + w.QuotaHeadroom
}

// TotalWeightSum adds the base score coefficients and both affinity bonuses
// together.
func (w ConfigScoreWeights) TotalWeightSum() float64 {
	return w.BaseWeightSum() + w.PreviousResponse + w.SessionSticky
}

// IsValid reports whether global coefficients can be used as scheduling
// defaults. Each coefficient must be finite and nonnegative, and the base sum
// must be positive.
//
// The base and full sums are checked separately because adding finite
// coefficients can overflow, including when the affinity bonuses extend a
// valid base. Group overrides use ValidateEffectiveWeights instead, which
// permits a zero base and still checks both sums.
func (w ConfigScoreWeights) IsValid() bool {
	for _, weight := range []float64{
		w.Priority, w.Load, w.Queue, w.ErrorRate, w.TTFT, w.Reset,
		w.QuotaHeadroom, w.PreviousResponse, w.SessionSticky,
	} {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return false
		}
	}
	baseSum := w.BaseWeightSum()
	return baseSum > 0 && !math.IsNaN(baseSum) && !math.IsInf(baseSum, 0) &&
		!math.IsNaN(w.TotalWeightSum()) && !math.IsInf(w.TotalWeightSum(), 0)
}
