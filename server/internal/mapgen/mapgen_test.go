package mapgen

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/sim"
	"sandtable/server/internal/sim/simtest"
	"sandtable/server/internal/view"
)

// withRules fills in the rules of the real data directory.
func withRules(t testing.TB, o Options) Options {
	t.Helper()
	r, err := ReadRules("../../../data")
	if err != nil {
		t.Fatal(err)
	}
	o.Rules = r
	return o
}

// must stops the test on a setup error, so that it shows instead of a later, confusing failure.
func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSameSeedSameFiles(t *testing.T) {
	a, err := Generate(withRules(t, Options{Seed: 7, Land: 300}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(withRules(t, Options{Seed: 7, Land: 300}))
	must(t, err)
	if !bytes.Equal(a.MapYAML(), b.MapYAML()) || !bytes.Equal(a.ScenarioYAML(), b.ScenarioYAML()) {
		t.Fatal("the same seed gave different files")
	}
	c, err := Generate(withRules(t, Options{Seed: 8, Land: 300}))
	must(t, err)
	if bytes.Equal(a.MapYAML(), c.MapYAML()) {
		t.Fatal("a different seed gave the same map")
	}
}

// load writes the generated map next to the real rules and units and loads it like the server does.
func load(t testing.TB, m *Map) *sim.World {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"maps", "scenarios"} {
		must(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
	}
	for _, f := range []string{"rules.yaml", "units.yaml"} {
		data, err := os.ReadFile(filepath.Join("../../../data", f))
		if err != nil {
			t.Fatal(err)
		}
		must(t, os.WriteFile(filepath.Join(dir, f), data, 0o644))
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
	if total == 0 {
		return true // nothing to connect; callers check sizes separately
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
		m, err := Generate(withRules(t, opt))
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
		if land < m.Opt.Land*9/10 {
			t.Errorf("%d land provinces, asked for %d", land, m.Opt.Land)
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
			if c := w.Factions[f].Capital; !w.Factions[f].HasCapital || w.Provinces[c].VP < 10 || !w.Provinces[c].Hub {
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
	m, err := Generate(withRules(b, Options{Seed: 1}))
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

// The committed generated map, with the rules as they are now, supplies every
// land province of its controller from the start. Hubs are placed from the
// rules; this catches a rule change that was not followed by regenerating.
func TestCommittedMapIsSupplied(t *testing.T) {
	w, err := gamedata.Load("../../../data", "generated")
	if err != nil {
		t.Fatal(err)
	}
	e := sim.NewEngine(w)
	s := e.NewState(1)
	worst := int32(1000)
	for p := range w.Provinces {
		f := w.InitControl[p]
		if f == sim.NoFaction {
			continue
		}
		lv := e.SupplyLevel(s, f, sim.ProvinceID(p))
		if lv <= 0 {
			t.Errorf("%s has no supply for its controller at the start; regenerate the map after changing rules.yaml", w.Provinces[p].Key)
		}
		worst = min(worst, lv)
	}
	t.Logf("lowest supply at the start: %d‰", worst)
}

func TestNamesAndRules(t *testing.T) {
	for _, name := range []string{"../x", "Sample", "a/b", ""} {
		if name == "" {
			continue // the default name
		}
		if _, err := Generate(withRules(t, Options{Seed: 1, Land: 60, Name: name})); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
	if _, err := Generate(Options{Seed: 1, Land: 60}); err == nil {
		t.Error("generating without rules worked")
	}
	noArmor := withRules(t, Options{Seed: 1, Land: 60})
	noArmor.Rules.UnitTypes = map[string]bool{"infantry": true, "tank": true}
	if _, err := Generate(noArmor); err == nil {
		t.Error("generating with units.yaml lacking armor worked")
	}
}

func TestWriteKeepsHandWrittenFiles(t *testing.T) {
	m, err := Generate(withRules(t, Options{Seed: 1, Land: 60, Name: "mine"}))
	if err != nil {
		t.Fatal(err)
	}
	// A data directory without maps/ and scenarios/ yet.
	dir := t.TempDir()
	if err := m.Write(dir); err != nil {
		t.Fatal(err)
	}
	// Writing again over its own files is fine.
	if err := m.Write(dir); err != nil {
		t.Fatal(err)
	}
	// A hand-written scenario of that name stops it, before the map is touched.
	hand := []byte("map: mine\nfactions: []\n")
	must(t, os.WriteFile(filepath.Join(dir, "scenarios", "mine.yaml"), hand, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "maps", "mine.yaml"), []byte(generatedMark+" earlier\n"), 0o644))
	if err := m.Write(dir); err == nil {
		t.Fatal("overwrote a hand-written scenario")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "maps", "mine.yaml")); string(got) != generatedMark+" earlier\n" {
		t.Fatal("wrote the map before refusing the scenario")
	}
}

func TestSpacingLimits(t *testing.T) {
	for _, sp := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 4, maxSpacing + 1, 60000} {
		if _, err := Generate(withRules(t, Options{Seed: 1, Land: 60, Spacing: sp})); err == nil {
			t.Errorf("spacing %v was accepted", sp)
		}
	}
	// The largest spacing allowed still makes a map the server loads.
	m, err := Generate(withRules(t, Options{Seed: 1, Land: 60, Spacing: maxSpacing}))
	must(t, err)
	load(t, m)
}

func TestWaterTerrainsMustBeImpassable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(r *Rules)
	}{
		{"no sea", func(r *Rules) { delete(r.Impassable, "sea") }},
		{"a passable lake", func(r *Rules) { delete(r.Impassable, "lake"); r.MoveCost["lake"] = 1 }},
	} {
		o := withRules(t, Options{Seed: 1, Land: 60})
		tc.break_(&o.Rules)
		if _, err := Generate(o); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

// A write that fails half way leaves the old map and scenario as they were.
func TestFailedWriteKeepsTheOldPair(t *testing.T) {
	m, err := Generate(withRules(t, Options{Seed: 1, Land: 60, Name: "pair"}))
	must(t, err)
	dir := t.TempDir()
	must(t, m.Write(dir))
	before, err := os.ReadFile(filepath.Join(dir, "maps", "pair.yaml"))
	must(t, err)

	other, err := Generate(withRules(t, Options{Seed: 2, Land: 60, Name: "pair"}))
	must(t, err)
	// Neither the scenario nor its directory can be written: replacing the
	// file in place fails, and so does creating a new one next to it.
	scen := filepath.Join(dir, "scenarios")
	must(t, os.Chmod(filepath.Join(scen, "pair.yaml"), 0o444))
	must(t, os.Chmod(scen, 0o555))
	defer os.Chmod(scen, 0o755)
	if err := other.Write(dir); err == nil {
		t.Skip("the directory is writable anyway (running as root?)")
	}
	after, err := os.ReadFile(filepath.Join(dir, "maps", "pair.yaml"))
	must(t, err)
	if string(after) != string(before) {
		t.Fatal("the map was replaced although the scenario could not be")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "maps"))
	must(t, err)
	if len(entries) != 1 {
		t.Fatalf("left behind in maps/: %d files", len(entries))
	}
}
