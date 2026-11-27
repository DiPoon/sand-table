package transport

import (
	"testing"

	"sandtable/server/internal/sim"
	"sandtable/server/internal/sim/simtest"
)

// Worlds built in code, without states or capitals, go on the wire as well,
// and a faction in its zero value has no capital.
func TestStaticOfAWorldWithoutStatesOrCapitals(t *testing.T) {
	for _, w := range []*sim.World{simtest.Grid(4, 3, 2, 4), {Factions: []sim.Faction{{}, {Key: "a"}}}} {
		s := staticToPB(w)
		if len(s.States) != 0 {
			t.Fatalf("%d states", len(s.States))
		}
		for _, f := range s.Factions {
			if f.Capital != 0 {
				t.Fatalf("faction %s: capital %d on the wire, want 0 (none)", f.Key, f.Capital)
			}
		}
	}
}

// A capital goes on the wire as province ID + 1, also for province 0.
func TestStaticCapital(t *testing.T) {
	w := simtest.Grid(4, 3, 2, 4)
	w.Factions[1].HasCapital, w.Factions[1].Capital = true, 0
	w.Factions[2].HasCapital, w.Factions[2].Capital = true, 7
	s := staticToPB(w)
	if s.Factions[0].Capital != 1 || s.Factions[1].Capital != 8 {
		t.Fatalf("capitals on the wire: %d, %d; want 1, 8", s.Factions[0].Capital, s.Factions[1].Capital)
	}
}
