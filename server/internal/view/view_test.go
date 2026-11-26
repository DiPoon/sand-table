package view_test

import (
	"testing"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/sim"
	"sandtable/server/internal/sim/simtest"
	"sandtable/server/internal/view"
)

// Applying every delta to the first snapshot must reproduce the server-side
// view of every tick, for every faction.
func TestSnapshotPlusDeltasEqualsView(t *testing.T) {
	sample, err := gamedata.Load("../../../data", "sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []*sim.World{sample, simtest.Grid(12, 8, 3, 20)} {
		e := sim.NewEngine(w)
		s := e.NewState(42)
		sc := simtest.NewScript(w, 7)
		nf := len(w.Factions)
		client := make([]*view.View, nf)
		last := make([]*view.View, nf)
		for f := 1; f < nf; f++ {
			last[f] = view.Build(w, s, sim.FactionID(f))
			client[f] = view.Build(w, s, sim.FactionID(f))
		}
		sawDestroyed, sawOutOfSight := false, false
		for i := 0; i < 1500; i++ {
			e.Step(s, sc.Commands(s, 30))
			for f := 1; f < nf; f++ {
				cur := view.Build(w, s, sim.FactionID(f))
				d := view.Diff(last[f], cur, s)
				for _, r := range d.Removed {
					if r.Destroyed {
						sawDestroyed = true
					} else {
						sawOutOfSight = true
					}
				}
				view.Apply(client[f], d)
				if !view.Equal(client[f], cur) {
					t.Fatalf("%s: faction %d diverged at tick %d", w.Scenario, f, s.Tick)
				}
				last[f] = cur
			}
		}
		if !sawDestroyed || !sawOutOfSight {
			t.Errorf("%s: run did not cover both removal reasons (destroyed %v, out of sight %v)",
				w.Scenario, sawDestroyed, sawOutOfSight)
		}
	}
}

// A faction never receives a foreign division outside its vision, nor the
// private fields of a foreign division inside it.
func TestFogOfWar(t *testing.T) {
	w := simtest.Grid(12, 8, 3, 20)
	e := sim.NewEngine(w)
	s := e.NewState(42)
	sc := simtest.NewScript(w, 7)
	hidden := 0
	for i := 0; i < 600; i++ {
		e.Step(s, sc.Commands(s, 30))
		for f := 1; f < len(w.Factions); f++ {
			fid := sim.FactionID(f)
			vis := view.Visible(w, s, fid)
			v := view.Build(w, s, fid)
			seen := 0
			for _, d := range v.Divisions {
				if d.Faction == fid {
					continue
				}
				seen++
				if !vis[d.Province] {
					t.Fatalf("tick %d: faction %d sees division %d outside its vision", s.Tick, f, d.ID)
				}
				if d.Own || d.Path != nil || d.Progress != 0 || d.Supply != 0 {
					t.Fatalf("tick %d: faction %d sees private fields of division %d", s.Tick, f, d.ID)
				}
			}
			foreign := 0
			for _, d := range s.Divisions {
				if d.Faction != fid {
					foreign++
				}
			}
			hidden += foreign - seen
		}
	}
	if hidden == 0 {
		t.Error("nothing was ever hidden: the test does not exercise the fog")
	}
}

func BenchmarkDelta(b *testing.B) {
	w := simtest.Grid(125, 80, 4, 1250)
	e := sim.NewEngine(w)
	s := e.NewState(42)
	sc := simtest.NewScript(w, 7)
	for i := 0; i < 100; i++ {
		e.Step(s, sc.Commands(s, 30))
	}
	prev := view.Build(w, s, 1)
	e.Step(s, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		view.Diff(prev, view.Build(w, s, 1), s)
	}
}
