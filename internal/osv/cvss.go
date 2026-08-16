package osv

import (
	"fmt"
	"math"
	"strings"
)

// BaseScoreV3 computes a CVSS v3.0/v3.1 base score from its vector string
// (e.g. "CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:C/C:H/I:N/A:N"), per the FIRST.org
// v3.1 specification §7.1. ok is false for anything other than a
// well-formed v3.x vector — including CVSS v4.0, which uses an entirely
// different macrovector scoring system this package does not implement;
// database_specific.severity is dep-risk's fallback signal for those.
func BaseScoreV3(vector string) (score float64, ok bool) {
	metrics, err := parseCVSSVector(vector)
	if err != nil {
		return 0, false
	}

	av, ok1 := cvssAV[metrics["AV"]]
	ac, ok2 := cvssAC[metrics["AC"]]
	ui, ok3 := cvssUI[metrics["UI"]]
	c, ok4 := cvssCIA[metrics["C"]]
	i, ok5 := cvssCIA[metrics["I"]]
	a, ok6 := cvssCIA[metrics["A"]]
	scopeChanged, ok7 := cvssScope[metrics["S"]]
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 {
		return 0, false
	}
	pr, ok8 := prValue(metrics["PR"], scopeChanged)
	if !ok8 {
		return 0, false
	}

	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if scopeChanged {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	} else {
		impact = 6.42 * iss
	}
	if impact <= 0 {
		return 0, true
	}

	exploitability := 8.22 * av * ac * pr * ui

	sum := impact + exploitability
	if scopeChanged {
		sum *= 1.08
	}
	return cvssRoundup(math.Min(sum, 10)), true
}

var cvssAV = map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}
var cvssAC = map[string]float64{"L": 0.77, "H": 0.44}
var cvssUI = map[string]float64{"N": 0.85, "R": 0.62}
var cvssCIA = map[string]float64{"H": 0.56, "L": 0.22, "N": 0}
var cvssScope = map[string]bool{"U": false, "C": true}

func prValue(v string, scopeChanged bool) (float64, bool) {
	switch v {
	case "N":
		return 0.85, true
	case "L":
		if scopeChanged {
			return 0.68, true
		}
		return 0.62, true
	case "H":
		if scopeChanged {
			return 0.5, true
		}
		return 0.27, true
	default:
		return 0, false
	}
}

// parseCVSSVector parses "CVSS:3.1/AV:N/AC:H/..." into a metric->value map.
// It requires a CVSS 3.x prefix and every Base metric to be present;
// Temporal/Environmental metrics (if any) are ignored.
func parseCVSSVector(vector string) (map[string]string, error) {
	parts := strings.Split(vector, "/")
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "CVSS:3.") {
		return nil, fmt.Errorf("osv: not a CVSS 3.x vector: %q", vector)
	}
	metrics := make(map[string]string, len(parts)-1)
	for _, p := range parts[1:] {
		kv := strings.SplitN(p, ":", 2)
		if len(kv) != 2 {
			continue
		}
		metrics[kv[0]] = kv[1]
	}
	for _, required := range []string{"AV", "AC", "PR", "UI", "S", "C", "I", "A"} {
		if _, ok := metrics[required]; !ok {
			return nil, fmt.Errorf("osv: CVSS vector missing %s: %q", required, vector)
		}
	}
	return metrics, nil
}

// cvssRoundup implements the spec's integer-arithmetic rounding, which
// avoids floating-point inaccuracies a naive math.Ceil(x*10)/10 can hit.
func cvssRoundup(input float64) float64 {
	intInput := int64(math.Round(input * 100000))
	if intInput%10000 == 0 {
		return float64(intInput) / 100000
	}
	return float64(intInput/10000+1) / 10
}
