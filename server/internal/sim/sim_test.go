package sim_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/sim"
	"sandtable/server/internal/sim/simtest"
)

func sampleWorld(t testing.TB) *sim.World {
	t.Helper()
	w, err := gamedata.Load("../../../data", "sample")
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// run simulates ticks with a scripted command stream and returns the hash after every tick.
func run(w *sim.World, seed, scriptSeed uint64, ticks int) (*sim.State, []sim.Hash) {
	e := sim.NewEngine(w)
	s := e.NewState(seed)
	sc := simtest.NewScript(w, scriptSeed)
	hashes := make([]sim.Hash, 0, ticks)
	for i := 0; i < ticks; i++ {
		e.Step(s, sc.Commands(s, 30))
		hashes = append(hashes, sim.HashState(s))
	}
	return s, hashes
}

func TestDeterminism(t *testing.T) {
	for _, w := range []*sim.World{sampleWorld(t), simtest.Grid(12, 8, 3, 20)} {
		_, a := run(w, 42, 7, 1500)
		_, b := run(w, 42, 7, 1500)
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%s: hash differs at tick %d", w.Scenario, i+1)
			}
		}
	}
}

func TestSeedChangesResult(t *testing.T) {
	w := sampleWorld(t)
	_, a := run(w, 1, 7, 1500)
	_, b := run(w, 2, 7, 1500)
	if a[len(a)-1] == b[len(b)-1] {
		t.Fatal("different seeds gave the same final hash: randomness is not used")
	}
}

// The scripted run must actually exercise the rules, or the tests above prove little.
func TestScriptedRunHasAction(t *testing.T) {
	w := sampleWorld(t)
	s, _ := run(w, 42, 7, 1500)
	if len(s.Divisions) == len(w.InitDivisions) {
		t.Error("no division was destroyed")
	}
	changed := 0
	for i := range s.Provinces {
		if s.Provinces[i].Controller != w.InitControl[i] {
			changed++
		}
	}
	if changed == 0 {
		t.Error("no province changed hands")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	w := simtest.Grid(12, 8, 3, 20)
	_, want := run(w, 42, 7, 600)

	e := sim.NewEngine(w)
	s := e.NewState(42)
	sc := simtest.NewScript(w, 7)
	for i := 0; i < 600; i++ {
		if i%97 == 0 {
			// Encode, decode and continue on a fresh engine.
			enc := sim.AppendState(nil, s)
			dec, err := sim.DecodeState(enc)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(enc, sim.AppendState(nil, dec)) {
				t.Fatalf("tick %d: re-encoding a decoded state changed the bytes", i)
			}
			s = dec
			e = sim.NewEngine(w)
		}
		e.Step(s, sc.Commands(s, 30))
		if sim.HashState(s) != want[i] {
			t.Fatalf("hash differs at tick %d after snapshot round trip", i+1)
		}
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	w := sampleWorld(t)
	s, _ := run(w, 42, 7, 50)
	enc := sim.AppendState(nil, s)
	for _, n := range []int{0, 3, len(enc) / 2, len(enc) - 1} {
		if _, err := sim.DecodeState(enc[:n]); err == nil {
			t.Errorf("truncated to %d bytes: no error", n)
		}
	}
	if _, err := sim.DecodeState(append(enc, 0)); err == nil {
		t.Error("trailing byte: no error")
	}
}

// Golden hash of a fixed run. It must be identical on every CPU architecture.
// Update it (and bump sim.Version) only when rules change on purpose.
const goldenHash = "670d1d8a914d5f2c5ebd52184ff3bb484178e1a16ae6a2bce3950627e5a7603b"

func TestGoldenHash(t *testing.T) {
	w := sampleWorld(t)
	_, h := run(w, 42, 7, 1500)
	got := hex.EncodeToString(h[len(h)-1][:])
	if got != goldenHash {
		t.Fatalf("golden hash changed:\n got  %s\n want %s", got, goldenHash)
	}
}

// ---- Rule tests on a small hand-built world ----

// line builds provinces 0-1-2-...-(n-1) in a row, 40 km apart.
// Faction 1 controls the first `split` provinces, faction 2 the rest.
// Province 0 and n-1 are hubs.
func line(n, split int) *sim.World {
	w := &sim.World{Scenario: "line", Rules: simtest.Rules}
	w.Terrains = []sim.Terrain{simtest.Plains}
	w.UnitTypes = []sim.UnitType{simtest.Infantry}
	w.Factions = []sim.Faction{{Capital: sim.NoProvince}, {Key: "a", Name: "A", Capital: sim.NoProvince},
		{Key: "b", Name: "B", Capital: sim.NoProvince}}
	w.Regions = []sim.Region{{Capital: sim.NoProvince}}
	w.Provinces = make([]sim.Province, n)
	w.InitControl = make([]sim.FactionID, n)
	for i := 0; i < n; i++ {
		p := &w.Provinces[i]
		p.Key = fmt.Sprintf("l%02d", i)
		if i > 0 {
			p.Adj = append(p.Adj, sim.Edge{To: sim.ProvinceID(i - 1), Distance: 40})
		}
		if i < n-1 {
			p.Adj = append(p.Adj, sim.Edge{To: sim.ProvinceID(i + 1), Distance: 40})
		}
		w.InitControl[i] = 1
		if i >= split {
			w.InitControl[i] = 2
		}
	}
	w.Provinces[0].Hub = true
	w.Provinces[n-1].Hub = true
	return w
}

func place(w *sim.World, f sim.FactionID, at sim.ProvinceID) {
	w.InitDivisions = append(w.InitDivisions, sim.InitDivision{Faction: f, Type: 0, Name: "d", At: at})
}

func move(f sim.FactionID, d sim.DivisionID, to sim.ProvinceID) []sim.Command {
	return []sim.Command{{Faction: f, Kind: sim.CmdMove, Division: d, Dest: to}}
}

func TestMovementTiming(t *testing.T) {
	w := line(4, 4)
	place(w, 1, 0)
	e := sim.NewEngine(w)
	s := e.NewState(1)
	e.Step(s, move(1, 1, 2))
	// Infantry: 4 km per tick, 40 km on plains = 10 ticks per province.
	for s.Tick < 10 {
		if s.Divisions[0].Loc != 0 {
			t.Fatalf("tick %d: left too early", s.Tick)
		}
		e.Step(s, nil)
	}
	if s.Divisions[0].Loc != 1 {
		t.Fatalf("tick %d: want province 1, got %d", s.Tick, s.Divisions[0].Loc)
	}
	for s.Tick < 20 {
		e.Step(s, nil)
	}
	if d := s.Divisions[0]; d.Loc != 2 || len(d.Path) != 0 {
		t.Fatalf("tick %d: want arrival at 2, got loc %d path %v", s.Tick, d.Loc, d.Path)
	}
}

func TestValidation(t *testing.T) {
	w := line(4, 2)
	w.Terrains = append(w.Terrains, sim.Terrain{Key: "lake"})
	w.Provinces[3].Terrain = 1
	w.Provinces[3].Hub = false
	place(w, 1, 0)
	place(w, 2, 2)
	e := sim.NewEngine(w)
	s := e.NewState(1)
	cases := []struct {
		c    sim.Command
		want sim.RejectReason
	}{
		{sim.Command{Faction: 1, Kind: sim.CmdMove, Division: 1, Dest: 1}, sim.RejectNone},
		{sim.Command{Faction: 1, Kind: sim.CmdMove, Division: 1, Dest: 0}, sim.RejectNone},
		{sim.Command{Faction: 1, Kind: 9, Division: 1, Dest: 1}, sim.RejectMalformed},
		{sim.Command{Faction: 1, Kind: sim.CmdMove, Division: 2, Dest: 1}, sim.RejectNotYourDivision},
		{sim.Command{Faction: 1, Kind: sim.CmdMove, Division: 99, Dest: 1}, sim.RejectNotYourDivision},
		{sim.Command{Faction: 1, Kind: sim.CmdMove, Division: 1, Dest: 4}, sim.RejectUnknownProvince},
		{sim.Command{Faction: 1, Kind: sim.CmdMove, Division: 1, Dest: -1}, sim.RejectUnknownProvince},
		{sim.Command{Faction: 1, Kind: sim.CmdMove, Division: 1, Dest: 3}, sim.RejectNoPath},
	}
	before := sim.HashState(s)
	for i, c := range cases {
		if got := e.Validate(s, c.c); got != c.want {
			t.Errorf("case %d: got %d, want %d", i, got, c.want)
		}
	}
	if sim.HashState(s) != before {
		t.Error("Validate changed the state")
	}
	cmds := make([]sim.Command, len(cases))
	for i, c := range cases {
		cmds[i] = c.c
	}
	res := e.Step(s, cmds)
	if len(res.Rejections) != 6 {
		t.Errorf("Step rejected %d commands, want 6", len(res.Rejections))
	}
}

// An attacker stays in its province while it fights, the defender retreats
// when its organization breaks, and the attacker then moves in and captures.
func TestAttackRetreatCapture(t *testing.T) {
	w := line(5, 2)
	place(w, 1, 1) // attackers: divisions 1-3
	place(w, 1, 1)
	place(w, 1, 1)
	place(w, 2, 2) // defender: division 4
	e := sim.NewEngine(w)
	s := e.NewState(1)
	cmds := append(move(1, 1, 2), move(1, 2, 2)...)
	cmds = append(cmds, move(1, 3, 2)...)
	e.Step(s, cmds)
	if !s.Provinces[2].InBattle {
		t.Fatal("no battle after the attack order")
	}
	sawRetreat := false
	for i := 0; i < 400 && s.Provinces[2].Controller != 1; i++ {
		for _, d := range s.Divisions {
			if d.Faction == 1 && d.Loc != 1 && s.Provinces[2].InBattle {
				t.Fatalf("tick %d: attacker entered a defended province", s.Tick)
			}
		}
		if d := s.FindDivision(4); d != nil && d.Retreating {
			sawRetreat = true
			if len(d.Path) != 1 || d.Path[0] != 3 {
				t.Fatalf("defender retreats along %v, want [3]", d.Path)
			}
		}
		e.Step(s, nil)
	}
	if !sawRetreat {
		t.Error("defender never retreated")
	}
	if s.Provinces[2].Controller != 1 {
		t.Fatal("attackers never captured the province")
	}
	if s.Provinces[2].InBattle {
		t.Error("battle flag still set after capture")
	}
}

// A defender with no friendly province to fall back to is destroyed.
func TestSurroundedDefenderIsDestroyed(t *testing.T) {
	w := line(3, 2)
	w.Provinces[2].Hub = false
	w.InitControl[2] = 2
	for i := 0; i < 3; i++ {
		place(w, 1, 1)
	}
	place(w, 2, 2)
	e := sim.NewEngine(w)
	s := e.NewState(1)
	e.Step(s, append(append(move(1, 1, 2), move(1, 2, 2)...), move(1, 3, 2)...))
	for i := 0; i < 400 && s.FindDivision(4) != nil; i++ {
		e.Step(s, nil)
	}
	if s.FindDivision(4) != nil {
		t.Fatal("surrounded defender survived")
	}
}

// An attacker whose organization breaks stops attacking and stays put.
func TestBrokenAttackStops(t *testing.T) {
	w := line(5, 2)
	place(w, 1, 1)
	for i := 0; i < 3; i++ {
		place(w, 2, 2)
	}
	e := sim.NewEngine(w)
	s := e.NewState(1)
	e.Step(s, move(1, 1, 2))
	for i := 0; i < 400 && s.Provinces[2].InBattle; i++ {
		e.Step(s, nil)
	}
	d := s.FindDivision(1)
	if d == nil || d.Loc != 1 || len(d.Path) != 0 || d.Retreating {
		t.Fatalf("attacker should hold at province 1 with no orders, got %+v", d)
	}
}

// Only as many divisions as the combat width allows stand in the front line.
func TestCombatWidth(t *testing.T) {
	w := line(5, 2)
	for i := 0; i < 6; i++ {
		place(w, 1, 1) // width 20 each, limit 80: four in front, two in reserve
	}
	place(w, 2, 2)
	e := sim.NewEngine(w)
	s := e.NewState(1)
	var cmds []sim.Command
	for id := sim.DivisionID(1); id <= 6; id++ {
		cmds = append(cmds, move(1, id, 2)...)
	}
	e.Step(s, cmds)
	front, reserve := 0, 0
	for _, d := range s.Divisions {
		switch {
		case d.Faction == 1 && d.AttackRole == sim.RoleFront:
			front++
		case d.Faction == 1 && d.AttackRole == sim.RoleReserve:
			reserve++
		}
	}
	if front != 4 || reserve != 2 {
		t.Fatalf("front %d reserve %d, want 4 and 2", front, reserve)
	}
	// Reserves take no damage.
	e.Step(s, nil)
	for _, d := range s.Divisions {
		if d.AttackRole == sim.RoleReserve && d.Org != simtest.Infantry.MaxOrg {
			t.Errorf("reserve division %d lost organization", d.ID)
		}
	}
}

// Supply only flows through provinces the faction controls.
func TestSupplyCutOff(t *testing.T) {
	w := line(7, 6)
	place(w, 1, 4)
	e := sim.NewEngine(w)
	s := e.NewState(1)
	// 160 km from the hub: halfway between full (80) and zero (240).
	if got := s.Divisions[0].Supply; got != 500 {
		t.Fatalf("supply at 160 km = %d, want 500", got)
	}
	s.Provinces[2].Controller = 2 // cut the line behind the division
	e.Step(s, nil)
	d := s.Divisions[0]
	if d.Supply != 0 {
		t.Fatalf("supply after cut = %d, want 0", d.Supply)
	}
	if d.Strength != simtest.Infantry.MaxStrength-int32(simtest.Rules.Attrition) {
		t.Fatalf("strength %d: attrition not applied", d.Strength)
	}
}

// ---- Benchmarks: time per tick ----

func benchStep(b *testing.B, w *sim.World) {
	e := sim.NewEngine(w)
	s := e.NewState(42)
	sc := simtest.NewScript(w, 7)
	// Warm up so the benchmark measures a world with movement and battles.
	for i := 0; i < 200; i++ {
		e.Step(s, sc.Commands(s, 30))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cmds := sc.Commands(s, 5)
		b.StartTimer()
		e.Step(s, cmds)
	}
}

func BenchmarkStep(b *testing.B) {
	b.Run("sample", func(b *testing.B) { benchStep(b, sampleWorld(b)) })
	b.Run("1k", func(b *testing.B) { benchStep(b, simtest.Grid(40, 25, 4, 125)) })
	b.Run("10k", func(b *testing.B) { benchStep(b, simtest.Grid(125, 80, 4, 1250)) })
}

func benchState(b *testing.B) *sim.State {
	w := simtest.Grid(125, 80, 4, 1250)
	s, _ := run(w, 42, 7, 100)
	return s
}

func BenchmarkHash(b *testing.B) {
	s := benchState(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sim.HashState(s)
	}
}

func BenchmarkSnapshot(b *testing.B) {
	s := benchState(b)
	var buf []byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = sim.AppendState(buf[:0], s)
	}
}

// Step reports every division it removed, in ID order, with the province it
// died in: the one it stood in before the step, or the next one if it moved.
func TestStepReportsDeaths(t *testing.T) {
	w := simtest.Grid(12, 8, 3, 20)
	e := sim.NewEngine(w)
	s := e.NewState(42)
	sc := simtest.NewScript(w, 7)
	total := 0
	for i := 0; i < 1500; i++ {
		before := map[sim.DivisionID]sim.ProvinceID{}
		for _, d := range s.Divisions {
			before[d.ID] = d.Loc
		}
		res := e.Step(s, sc.Commands(s, 30))
		var gone []sim.DivisionID
		for _, d := range s.Divisions {
			delete(before, d.ID)
		}
		for id := range before {
			gone = append(gone, id)
		}
		if len(gone) != len(res.Deaths) {
			t.Fatalf("tick %d: %d divisions removed, %d deaths reported", s.Tick, len(gone), len(res.Deaths))
		}
		for k, dd := range res.Deaths {
			if k > 0 && res.Deaths[k-1].ID >= dd.ID {
				t.Fatalf("tick %d: deaths not in ID order", s.Tick)
			}
			from, ok := before[dd.ID]
			if !ok {
				t.Fatalf("tick %d: division %d reported dead but still there", s.Tick, dd.ID)
			}
			if _, adjacent := w.EdgeCost(from, dd.Loc); dd.Loc != from && !adjacent {
				t.Fatalf("tick %d: division %d died in province %d, far from %d", s.Tick, dd.ID, dd.Loc, from)
			}
		}
		total += len(res.Deaths)
	}
	if total == 0 {
		t.Fatal("nobody died: the test does not exercise deaths")
	}
}
