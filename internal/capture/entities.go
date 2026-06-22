//go:build windows

package capture

import (
	"strings"
	"sync"
)

// entityTracker locks onto the player's entity key by matching a configured
// character name against name bindings learned from the packet stream. Once
// locked, isMine(entityKey) tells skill packets apart by owner. Port of
// pingmaker's EntityTracker (single-character lock).
type entityTracker struct {
	mu      sync.Mutex
	names   map[string]string // lowercased -> original case
	key     uint64
	hasKey  bool
	keyName string // lowercased name of the current key
}

func newEntityTracker() *entityTracker {
	return &entityTracker{names: map[string]string{}}
}

// updateNames sets the character names to filter for and clears any locked key.
func (t *entityTracker) updateNames(names []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.names = map[string]string{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			t.names[strings.ToLower(n)] = n
		}
	}
	t.key = 0
	t.hasKey = false
	t.keyName = ""
}

// onBinding registers a learned name binding. Returns the matched character
// name (original case) and whether this locked/relocked the key.
func (t *entityTracker) onBinding(actorID uint64, name string) (matched string, locked bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	clean := strings.ToLower(strings.TrimSpace(name))
	orig, ok := t.names[clean]
	if !ok {
		return "", false
	}
	if t.hasKey && actorID == t.key {
		return orig, false
	}
	t.key = actorID
	t.hasKey = true
	t.keyName = clean
	return orig, true
}

func (t *entityTracker) isMine(ek uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hasKey && ek == t.key
}

func (t *entityTracker) isConfigured() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.names) > 0
}

func (t *entityTracker) hasAnyKeys() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hasKey
}
