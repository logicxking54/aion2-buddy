//go:build windows

package capture

import "testing"

func TestSkipSpeedEditForStabilityAllowsAllHellfireTiers(t *testing.T) {
	cases := []struct {
		name string
		id   uint32
		want bool
	}{
		{"Hellfire", 15063450, false},
		{"Hellfire - Level 1", 15063451, false},
		{"Hellfire - Level 2", 15063452, false},
		{"Hellfire - Max", 15063453, false},
		{"Flame Arrow", 15070453, false},
	}
	for _, tc := range cases {
		if got := skipSpeedEditForStability(tc.name, tc.id); got != tc.want {
			t.Fatalf("%s/%d: got %t, want %t", tc.name, tc.id, got, tc.want)
		}
	}
}
