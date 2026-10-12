//go:build windows

package main

import (
	"os"
	"reflect"
	"slices"
	"testing"

	"aion2tmp/internal/capture"
)

func TestPingMakerOnlyBindings(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  reflect.Type
		want []string
	}{
		// SetGameBoost is the one non-capture capability: it only touches process
		// priority/affinity for Aion2.exe and this app, nothing in the game files.
		{"Capture", reflect.TypeOf((*Capture)(nil)), []string{"IsCapturing", "SetCasterFilter", "SetCatalog", "SetCharacterNames", "SetGameBoost", "StartCapture", "StopCapture", "UpdateCapture"}},
		{"App", reflect.TypeOf((*App)(nil)), []string{"LoadConfig", "SaveConfig"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for i := 0; i < tc.typ.NumMethod(); i++ {
				got = append(got, tc.typ.Method(i).Name)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("public app capabilities = %v; want only %v", got, tc.want)
			}
		})
	}
}

func TestPingMakerEngineHasNoLabsOrOptimizer(t *testing.T) {
	typ := reflect.TypeOf((*capture.Engine)(nil))
	for _, name := range []string{"StartLatencyLab", "StopLatencyLab", "LatencySnapshot", "StartRotationRecord", "StopRotationRecord", "RotationSnapshot", "StartRotationOptimizer", "StopRotationOptimizer", "RotationOptimizerSnapshot", "ApplyBestRotationOptimizerCandidate", "RestoreRotationOptimizerMacro"} {
		if _, exists := typ.MethodByName(name); exists {
			t.Errorf("removed feature is still in capture engine: %s", name)
		}
	}
}

func TestPingMakerConfigRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	a := NewApp()
	data := `{"language":"th","character":"Maliwan","defaultSpeed":250,"rows":[{"id":"hellfire-max","speedPct":145,"overridden":true,"brk":false}],"legacy":{"keep":"for rollback"}}`
	if err := a.SaveConfig(data); err != nil {
		t.Fatal(err)
	}
	if got := a.LoadConfig(); got != data {
		t.Fatal("config persistence changed the user's settings")
	}
	p, err := a.configPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}
