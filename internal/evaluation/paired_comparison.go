package evaluation

import (
	"errors"
	"math"
	"math/rand"
	"sort"
)

const (
	PairedComparisonVersion         = "paired-comparison-v1"
	PairedBootstrapIterations       = 2000
	PairedBootstrapSeed       int64 = 20260906
)

type PairedObservation struct {
	BaselineScore  float64
	CandidateScore float64
}

type Fraction struct {
	Numerator   int     `json:"numerator"`
	Denominator int     `json:"denominator"`
	Rate        float64 `json:"rate"`
}

type PairedComparison struct {
	MethodVersion              string   `json:"method_version"`
	PairCount                  int      `json:"pair_count"`
	SuccessThreshold           float64  `json:"success_threshold"`
	BaselineSuccess            Fraction `json:"baseline_success"`
	CandidateSuccess           Fraction `json:"candidate_success"`
	Wins                       int      `json:"wins"`
	Losses                     int      `json:"losses"`
	Ties                       int      `json:"ties"`
	BaselineMean               float64  `json:"baseline_mean"`
	CandidateMean              float64  `json:"candidate_mean"`
	MeanDelta                  float64  `json:"mean_delta"`
	DeltaCI95Lower             float64  `json:"delta_ci95_lower"`
	DeltaCI95Upper             float64  `json:"delta_ci95_upper"`
	BootstrapIterations        int      `json:"bootstrap_iterations"`
	BootstrapSeed              int64    `json:"bootstrap_seed"`
	CandidateOnlySuccesses     int      `json:"candidate_only_successes"`
	BaselineOnlySuccesses      int      `json:"baseline_only_successes"`
	DiscordantPairs            int      `json:"discordant_pairs"`
	McNemarExactTwoSidedPValue float64  `json:"mcnemar_exact_two_sided_p_value"`
	Conclusion                 string   `json:"conclusion"`
}

func AnalyzePairedObservations(observations []PairedObservation, successThreshold float64) (PairedComparison, error) {
	if len(observations) < 2 {
		return PairedComparison{}, errors.New("paired comparison requires at least two observations")
	}
	if !finiteUnitInterval(successThreshold) {
		return PairedComparison{}, errors.New("success threshold must be finite and within [0,1]")
	}
	result := PairedComparison{
		MethodVersion: PairedComparisonVersion, PairCount: len(observations), SuccessThreshold: successThreshold,
		BootstrapIterations: PairedBootstrapIterations, BootstrapSeed: PairedBootstrapSeed,
	}
	deltas := make([]float64, 0, len(observations))
	for _, observation := range observations {
		if !finiteUnitInterval(observation.BaselineScore) || !finiteUnitInterval(observation.CandidateScore) {
			return PairedComparison{}, errors.New("paired scores must be finite and within [0,1]")
		}
		result.BaselineMean += observation.BaselineScore
		result.CandidateMean += observation.CandidateScore
		delta := observation.CandidateScore - observation.BaselineScore
		deltas = append(deltas, delta)
		switch {
		case delta > 1e-12:
			result.Wins++
		case delta < -1e-12:
			result.Losses++
		default:
			result.Ties++
		}
		baselineSuccess := observation.BaselineScore >= successThreshold
		candidateSuccess := observation.CandidateScore >= successThreshold
		if baselineSuccess {
			result.BaselineSuccess.Numerator++
		}
		if candidateSuccess {
			result.CandidateSuccess.Numerator++
		}
		if candidateSuccess && !baselineSuccess {
			result.CandidateOnlySuccesses++
		}
		if baselineSuccess && !candidateSuccess {
			result.BaselineOnlySuccesses++
		}
	}
	denominator := len(observations)
	result.BaselineSuccess.Denominator = denominator
	result.CandidateSuccess.Denominator = denominator
	result.BaselineSuccess.Rate = float64(result.BaselineSuccess.Numerator) / float64(denominator)
	result.CandidateSuccess.Rate = float64(result.CandidateSuccess.Numerator) / float64(denominator)
	result.BaselineMean /= float64(denominator)
	result.CandidateMean /= float64(denominator)
	result.MeanDelta = result.CandidateMean - result.BaselineMean
	result.DeltaCI95Lower, result.DeltaCI95Upper = deterministicPairedBootstrapCI(deltas, PairedBootstrapIterations, PairedBootstrapSeed)
	result.DiscordantPairs = result.CandidateOnlySuccesses + result.BaselineOnlySuccesses
	result.McNemarExactTwoSidedPValue = exactMcNemarTwoSided(result.CandidateOnlySuccesses, result.BaselineOnlySuccesses)
	result.Conclusion = "inconclusive"
	if result.DeltaCI95Lower > 0 && result.McNemarExactTwoSidedPValue < .05 {
		result.Conclusion = "candidate_better"
	} else if result.DeltaCI95Upper < 0 && result.McNemarExactTwoSidedPValue < .05 {
		result.Conclusion = "candidate_worse"
	}
	return result, nil
}

func finiteUnitInterval(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func deterministicPairedBootstrapCI(deltas []float64, iterations int, seed int64) (float64, float64) {
	random := rand.New(rand.NewSource(seed))
	means := make([]float64, iterations)
	for iteration := range means {
		for range deltas {
			means[iteration] += deltas[random.Intn(len(deltas))]
		}
		means[iteration] /= float64(len(deltas))
	}
	sort.Float64s(means)
	lower := int(math.Floor(.025 * float64(iterations)))
	upper := int(math.Ceil(.975*float64(iterations))) - 1
	return means[lower], means[upper]
}

func exactMcNemarTwoSided(candidateOnly int, baselineOnly int) float64 {
	discordant := candidateOnly + baselineOnly
	if discordant == 0 {
		return 1
	}
	extreme := candidateOnly
	if baselineOnly < extreme {
		extreme = baselineOnly
	}
	probability := 0.0
	for successes := 0; successes <= extreme; successes++ {
		probability += binomialCoefficient(discordant, successes) * math.Pow(.5, float64(discordant))
	}
	return math.Min(1, 2*probability)
}

func binomialCoefficient(n int, k int) float64 {
	if k > n-k {
		k = n - k
	}
	coefficient := 1.0
	for index := 1; index <= k; index++ {
		coefficient *= float64(n-k+index) / float64(index)
	}
	return coefficient
}
