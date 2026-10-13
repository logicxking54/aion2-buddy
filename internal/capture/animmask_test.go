package capture

import "testing"

func TestDodgeForMapsEachPlayerClassToItsOwnDodge(t *testing.T) {
	cases := map[uint32]uint32{
		11360031: 11000101, // Gladiator Rush Strike - Max
		12430000: 12000101, // Templar Shield Rush
		13050000: 13000101, // Assassin Flash Slice
		14270000: 14000101, // Ranger Arrow Storm
		15061453: 15000101, // Sorcerer Hellfire - Max
		16100001: 16000101, // Spiritmaster Fire Spirit
		17000101: 17000101, // Cleric dodge stays itself (caller skips the no-op write)
		18160000: 18000101, // Chanter Sprint Mantra
	}
	for id, want := range cases {
		if got := dodgeFor(id); got != want {
			t.Errorf("dodgeFor(%d) = %d, want %d", id, got, want)
		}
	}
}

// Anything outside the player-class range must come back 0 so the engine leaves
// it alone: monster and boss casts, system skills, basic attacks.
func TestDodgeForIgnoresNonPlayerSkills(t *testing.T) {
	for _, id := range []uint32{0, 4001, 9951, 100011, 109330, 10999999, 19000000, 25000123, 4294967295} {
		if got := dodgeFor(id); got != 0 {
			t.Errorf("dodgeFor(%d) = %d, want 0", id, got)
		}
	}
}
