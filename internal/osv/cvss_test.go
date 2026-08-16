package osv

import "testing"

func TestBaseScoreV3KnownVectors(t *testing.T) {
	cases := []struct {
		vector string
		want   float64
	}{
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8},
		{"CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:C/C:H/I:N/A:N", 6.8},
		{"CVSS:3.1/AV:N/AC:H/PR:H/UI:R/S:U/C:H/I:H/A:N", 5.7},
		{"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:L/I:L/A:L", 5.3},
		{"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8},
	}
	for _, tc := range cases {
		got, ok := BaseScoreV3(tc.vector)
		if !ok {
			t.Errorf("BaseScoreV3(%q) ok = false, want true", tc.vector)
			continue
		}
		if got != tc.want {
			t.Errorf("BaseScoreV3(%q) = %v, want %v", tc.vector, got, tc.want)
		}
	}
}

func TestBaseScoreV3NoImpactIsZero(t *testing.T) {
	got, ok := BaseScoreV3("CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N")
	if !ok || got != 0 {
		t.Errorf("BaseScoreV3 = (%v, %v), want (0, true)", got, ok)
	}
}

func TestBaseScoreV3RejectsV4(t *testing.T) {
	_, ok := BaseScoreV3("CVSS:4.0/AV:N/AC:L/AT:N/PR:H/UI:N/VC:L/VI:L/VA:N/SC:N/SI:N/SA:N")
	if ok {
		t.Errorf("BaseScoreV3 accepted a CVSS v4.0 vector, want ok=false")
	}
}

func TestBaseScoreV3RejectsMalformed(t *testing.T) {
	_, ok := BaseScoreV3("not a vector")
	if ok {
		t.Errorf("BaseScoreV3 accepted garbage input")
	}
	_, ok = BaseScoreV3("CVSS:3.1/AV:N/AC:L")
	if ok {
		t.Errorf("BaseScoreV3 accepted an incomplete vector")
	}
}
