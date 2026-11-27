package transport

import (
	"testing"

	"sandtable/server/internal/sim"
	"sandtable/server/internal/sim/simtest"
)

// Worlds built in code, without states or capitals, go on the wire as well.
func TestStaticOfAWorldWithoutStatesOrCapitals(t *testing.T) {
	for _, w := range []*sim.World{simtest.Grid(4, 3, 2, 4), {Factions: []sim.Faction{{}, {Key: "a"}}}} {
		s := staticToPB(w)
		if len(s.States) != 0 {
			t.Fatalf("%d states", len(s.States))
		}
		for _, f := range s.Factions {
			if w.Factions[1].Capital == sim.NoProvince && f.Capital != 0 {
				t.Fatalf("faction %s: capital %d on the wire, want 0 (none)", f.Key, f.Capital)
			}
		}
	}
}
