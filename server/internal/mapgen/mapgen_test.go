package mapgen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/sim"
	"sandtable/server/internal/sim/simtest"
	"sandtable/server/internal/view"
)

func TestSameSeedSameFiles(t *testing.T) {
	a, err := Generate(Options{Seed: 7, Land: 300})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(Options{Seed: 7, Land: 300})
	if !bytes.Equal(a.MapYAML(), b.MapYAML()) || !bytes.Equal(a.ScenarioYAML(), b.ScenarioYAML()) {
		t.Fatal("the same seed gave different files")
	}
	c, _ := Generate(Options{Seed: 8, Land: 300})
	if bytes.Equal(a.MapYAML(), c.MapYAML()) {
		t.Fatal("a different seed gave the same map")
	}
}

// load writes the generated map next to the real rules and units and loads it like the server does.
func load(t testing.TB, m *Map) *sim.World {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"maps", "scenarios"} {
		os.MkdirAll(filepath.Join(dir, sub), 0o755)
	}
	for _, f := range []string{"rules.yaml", "units.yaml"} {
		data, err := os.ReadFile(filepath.Join("../../../data", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), data, 0o644)
	}
	if err := m.Write(dir); err != nil {
		t.Fatal(err)
	}
	w, err := gamedata.Load(dir, m.Opt.Name)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// connected reports whether the provinces where in is true form one piece.
func connected(w *sim.World, in func(sim.ProvinceID) bool) bool {
	start, total := sim.NoProvince, 0
	for p := range w.Provinces {
		if in(sim.ProvinceID(p)) {
			total++
			if start == sim.NoProvince {
				start = sim.ProvinceID(p)
			}
		}
	}
	seen := map[sim.ProvinceID]bool{start: true}
	for queue := []sim.ProvinceID{start}; len(queue) > 0; queue = queue[1:] {
		for _, e := range w.Provinces[queue[0]].Adj {
			if !seen[e.To] && in(e.To) {
				seen[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
	return len(seen) == total
}

func TestGeneratedMapIsPlayable(t *testing.T) {
	for _, opt := range []Options{{Seed: 1}, {Seed: 2, Land: 300, Factions: 3}} {
		m, err := Generate(opt)
		if err != nil {
			t.Fatal(err)
		}
		w := load(t, m)
		passable := func(p sim.ProvinceID) bool { return w.Terrains[w.Provinces[p].Terrain].Passable }

		land, rivers, rails, hubs, vp := 0, 0, 0, 0, int32(0)
		for p := range w.Provinces {
			pr := &w.Provinces[p]
			if !passable(sim.ProvinceID(p)) {
				if len(pr.Adj) > 0 || pr.Region != sim.NoRegion {
					t.Fatalf("water province %s has edges or a state", pr.Key)
				}
				continue
			}
			land++
			if pr.Region == sim.NoRegion || w.InitControl[p] == sim.NoFaction {
				t.Fatalf("land province %s has no state or no controller", pr.Key)
			}
			if pr.Hub {
				hubs++
			}
			vp += pr.VP
			for _, e := range pr.Adj {
				if e.River > 0 {
					rivers++
				}
				if e.Rail > 0 {
					rails++
				}
			}
		}
		if land < opt.Land*9/10 && opt.Land > 0 {
			t.Errorf("%d land provinces, asked for %d", land, opt.Land)
		}
		if !connected(w, passable) {
			t.Fatal("the land is not one piece")
		}
		nf := len(w.Factions) - 1
		for f := 1; f <= nf; f++ {
			own := func(p sim.ProvinceID) bool { return w.InitControl[p] == sim.FactionID(f) }
			n := 0
			for p := range w.Provinces {
				if own(sim.ProvinceID(p)) {
					n++
				}
			}
			if !connected(w, own) {
				t.Errorf("faction %d is not one piece", f)
			}
			if share := float64(n) / float64(land); share < 0.5/float64(nf) || share > 1.6/float64(nf) {
				t.Errorf("faction %d holds %.0f%% of the land", f, share*100)
			}
			if c := w.Factions[f].Capital; c == sim.NoProvince || w.Provinces[c].VP < 10 || !w.Provinces[c].Hub {
				t.Errorf("faction %d: capital missing, small or without a hub", f)
			}
		}
		if rivers == 0 || rails == 0 || hubs < nf || vp == 0 {
			t.Errorf("rivers %d, railway edge ends %d, hubs %d, victory points %d", rivers/2, rails, hubs, vp)
		}
		// Hubs stay a minority: a few per faction, not one in every corner.
		if hubs > land/8 {
			t.Errorf("%d hubs for %d land provinces", hubs, land)
		}

		// The match runs on it.
		e := sim.NewEngine(w)
		s := e.NewState(1)
		sc := simtest.NewScript(w, 3)
		for i := 0; i < 300; i++ {
			e.Step(s, sc.Commands(s, 20))
		}
		if len(s.Divisions) == 0 {
			t.Fatal("no divisions left")
		}
		t.Logf("seed %d: %d provinces, %d land, %d states, %d hubs, %d river borders, %d divisions after 300 ticks",
			opt.Seed, len(w.Provinces), land, len(w.Regions)-1, hubs, rivers/2, len(s.Divisions))
	}
}

// One tick of the simulation, and the views of every faction, on the default generated map.
func BenchmarkTickOnGeneratedMap(b *testing.B) {
	m, err := Generate(Options{Seed: 1})
	if err != nil {
		b.Fatal(err)
	}
	w := load(b, m)
	e := sim.NewEngine(w)
	s := e.NewState(1)
	sc := simtest.NewScript(w, 3)
	for i := 0; i < 50; i++ {
		e.Step(s, sc.Commands(s, 20))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := e.Step(s, sc.Commands(s, 20))
		for f := 1; f < len(w.Factions); f++ {
			cur := view.Build(w, s, sim.FactionID(f))
			_ = view.Diff(cur, cur, res.Deaths)
		}
	}
}
