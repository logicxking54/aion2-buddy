package capture

// Player skill ids are eight digits whose first two name the class (11 Gladiator,
// 12 Templar, 13 Assassin, 14 Ranger, 15 Sorcerer, 16 Spiritmaster, 17 Cleric,
// 18 Chanter), and every class has its own Dodge at <class>000101 — see the
// "Dodge" entry in skills.data.json. Masking a cast with the caster's own
// class's dodge keeps the client's animation lookup valid; the single fixed id
// used before (Cleric's) was wrong for seven of the eight classes.
const (
	firstPlayerClass = 11
	lastPlayerClass  = 18
	dodgeSuffix      = 101
	classIDSpan      = 1_000_000
)

// dodgeFor returns the dodge skill id of the class that owns skillID, or 0 when
// the id is not a player-class skill (monster and system ids never match, so
// boss casts are never rewritten).
func dodgeFor(skillID uint32) uint32 {
	class := skillID / classIDSpan
	if class < firstPlayerClass || class > lastPlayerClass {
		return 0
	}
	return class*classIDSpan + dodgeSuffix
}
