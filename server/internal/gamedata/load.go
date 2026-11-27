// Package gamedata loads and validates the YAML files under data/ into a sim.World.
package gamedata

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"go.yaml.in/yaml/v3"

	"sandtable/server/internal/sim"
)

type rulesFile struct {
	Combat struct {
		RollMin             int64 `yaml:"roll_min"`
		RollMax             int64 `yaml:"roll_max"`
		OrgDamage           int64 `yaml:"org_damage"`
		StrengthDamage      int64 `yaml:"strength_damage"`
		MaxReduction        int64 `yaml:"max_reduction"`
		MinSupplyEffect     int64 `yaml:"min_supply_effect"`
		ExtraDirectionWidth int64 `yaml:"extra_direction_width"`
	} `yaml:"combat"`
	Supply struct {
		FullRange int64 `yaml:"full_range"`
		MaxRange  int64 `yaml:"max_range"`
		Attrition int64 `yaml:"attrition"`
	} `yaml:"supply"`
	Vision struct {
		Range int32 `yaml:"range"`
	} `yaml:"vision"`
	Terrain []struct {
		ID           string `yaml:"id"`
		Name         string `yaml:"name"`
		MoveCost     int64  `yaml:"move_cost"`
		DefenseBonus int64  `yaml:"defense_bonus"`
		CombatWidth  int64  `yaml:"combat_width"`
		Passable     *bool  `yaml:"passable"`
	} `yaml:"terrain"`
}

type unitFile struct {
	ID              string `yaml:"id"`
	Name            string `yaml:"name"`
	MaxStrength     int32  `yaml:"max_strength"`
	MaxOrg          int32  `yaml:"max_org"`
	Attack          int64  `yaml:"attack"`
	Defense         int64  `yaml:"defense"`
	Speed           int64  `yaml:"speed"`
	OrgRecovery     int64  `yaml:"org_recovery"`
	Width           int64  `yaml:"width"`
	ReinforceChance int64  `yaml:"reinforce_chance"`
}

type mapFile struct {
	Provinces []struct {
		ID      string  `yaml:"id"`
		Name    string  `yaml:"name"`
		Terrain string  `yaml:"terrain"`
		Pos     []int32 `yaml:"pos"`
		Hub     bool    `yaml:"hub"`
		State   string  `yaml:"state"`
		VP      int32   `yaml:"vp"`
	} `yaml:"provinces"`
	Edges  [][]any `yaml:"edges"` // [a, b, distance in km]
	States []struct {
		ID             string `yaml:"id"`
		Name           string `yaml:"name"`
		Capital        string `yaml:"capital"`
		Manpower       int64  `yaml:"manpower"`
		Factories      int32  `yaml:"factories"`
		Infrastructure int32  `yaml:"infrastructure"`
	} `yaml:"states"`
	Rivers   [][]any `yaml:"rivers"`   // [a, b, small | large]: a river along the border of a and b
	Railways [][]any `yaml:"railways"` // [a, b, level]: a railway along the edge a-b
}

type scenarioFile struct {
	Map      string `yaml:"map"`
	Factions []struct {
		ID        string   `yaml:"id"`
		Name      string   `yaml:"name"`
		Capital   string   `yaml:"capital"`
		Provinces []string `yaml:"provinces"`
		States    []string `yaml:"states"` // every province of these states
	} `yaml:"factions"`
	Divisions []struct {
		Faction string `yaml:"faction"`
		Type    string `yaml:"type"`
		Name    string `yaml:"name"`
		At      string `yaml:"at"`
	} `yaml:"divisions"`
}

func readYAML(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// index sorts keys and returns a lookup from key to position.
// IDs are assigned in lexical key order so they do not depend on file order.
func index(kind string, keys []string) (map[string]int, error) {
	sorted := slices.Clone(keys)
	sort.Strings(sorted)
	m := make(map[string]int, len(sorted))
	for i, k := range sorted {
		if k == "" {
			return nil, fmt.Errorf("%s: empty id", kind)
		}
		if _, dup := m[k]; dup {
			return nil, fmt.Errorf("%s: duplicate id %q", kind, k)
		}
		m[k] = i
	}
	return m, nil
}

func tooMany(kind string, n, limit int) error {
	if n > limit {
		return fmt.Errorf("too many %s: %d, at most %d", kind, n, limit)
	}
	return nil
}

func inRange(name string, v, lo, hi int64) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s = %d out of range [%d, %d]", name, v, lo, hi)
	}
	return nil
}

// Load reads data/rules.yaml, data/units.yaml, the scenario and its map.
func Load(dir, scenario string) (*sim.World, error) {
	var rf rulesFile
	var uf []unitFile
	var sf scenarioFile
	var mf mapFile
	if err := readYAML(filepath.Join(dir, "rules.yaml"), &rf); err != nil {
		return nil, err
	}
	if err := readYAML(filepath.Join(dir, "units.yaml"), &uf); err != nil {
		return nil, err
	}
	if err := readYAML(filepath.Join(dir, "scenarios", scenario+".yaml"), &sf); err != nil {
		return nil, err
	}
	if err := readYAML(filepath.Join(dir, "maps", sf.Map+".yaml"), &mf); err != nil {
		return nil, err
	}

	w := &sim.World{Scenario: scenario}
	w.Rules = sim.Rules{
		RollMin: rf.Combat.RollMin, RollMax: rf.Combat.RollMax,
		OrgDamage: rf.Combat.OrgDamage, StrengthDamage: rf.Combat.StrengthDamage,
		MaxReduction: rf.Combat.MaxReduction, MinSupplyEffect: rf.Combat.MinSupplyEffect,
		ExtraDirectionWidth: rf.Combat.ExtraDirectionWidth,
		SupplyFullRange:     rf.Supply.FullRange, SupplyMaxRange: rf.Supply.MaxRange,
		Attrition: rf.Supply.Attrition, VisionRange: rf.Vision.Range,
	}
	r := &w.Rules
	if err := errors.Join(
		inRange("combat.roll_min", r.RollMin, 1, 10000),
		inRange("combat.roll_max", r.RollMax, r.RollMin, 10000),
		inRange("combat.org_damage", r.OrgDamage, 0, 100000),
		inRange("combat.strength_damage", r.StrengthDamage, 0, 100000),
		inRange("combat.max_reduction", r.MaxReduction, 0, 1000),
		inRange("combat.min_supply_effect", r.MinSupplyEffect, 0, 1000),
		inRange("combat.extra_direction_width", r.ExtraDirectionWidth, 0, 10000),
		inRange("supply.full_range", r.SupplyFullRange, 0, 100000),
		inRange("supply.max_range", r.SupplyMaxRange, r.SupplyFullRange+1, 1000000),
		inRange("supply.attrition", r.Attrition, 0, 1000000),
		inRange("vision.range", int64(r.VisionRange), 0, 100),
	); err != nil {
		return nil, fmt.Errorf("rules.yaml: %w", err)
	}

	// IDs are small integers in the simulation: refuse more entries than they can number.
	if err := errors.Join(
		tooMany("terrain types", len(rf.Terrain), math.MaxUint16+1),
		tooMany("unit types", len(uf), math.MaxUint16+1),
		tooMany("states", len(mf.States), math.MaxUint16), // state ID 0 is "none"
		tooMany("provinces", len(mf.Provinces), math.MaxInt32),
	); err != nil {
		return nil, err
	}

	// Terrain.
	keys := make([]string, len(rf.Terrain))
	for i, t := range rf.Terrain {
		keys[i] = t.ID
	}
	terrainIdx, err := index("terrain", keys)
	if err != nil {
		return nil, err
	}
	w.Terrains = make([]sim.Terrain, len(keys))
	for _, t := range rf.Terrain {
		pass := t.Passable == nil || *t.Passable
		if pass {
			if err := errors.Join(
				inRange("move_cost", t.MoveCost, 1, 100000),
				inRange("defense_bonus", t.DefenseBonus, 0, 1000),
				inRange("combat_width", t.CombatWidth, 1, 100000),
			); err != nil {
				return nil, fmt.Errorf("terrain %q: %w", t.ID, err)
			}
		}
		w.Terrains[terrainIdx[t.ID]] = sim.Terrain{Key: t.ID, Name: t.Name, MoveCost: t.MoveCost,
			DefenseBonus: t.DefenseBonus, CombatWidth: t.CombatWidth, Passable: pass}
	}

	// Unit types.
	keys = make([]string, len(uf))
	for i, u := range uf {
		keys[i] = u.ID
	}
	unitIdx, err := index("unit type", keys)
	if err != nil {
		return nil, err
	}
	w.UnitTypes = make([]sim.UnitType, len(keys))
	for _, u := range uf {
		if err := errors.Join(
			inRange("max_strength", int64(u.MaxStrength), 1, 1_000_000_000),
			inRange("max_org", int64(u.MaxOrg), 1, 1_000_000_000),
			inRange("attack", u.Attack, 0, 1_000_000),
			inRange("defense", u.Defense, 0, 1000),
			inRange("speed", u.Speed, 1, 100000),
			inRange("org_recovery", u.OrgRecovery, 0, 1_000_000),
			inRange("width", u.Width, 1, 100000),
			inRange("reinforce_chance", u.ReinforceChance, 0, 1000),
		); err != nil {
			return nil, fmt.Errorf("unit type %q: %w", u.ID, err)
		}
		w.UnitTypes[unitIdx[u.ID]] = sim.UnitType{Key: u.ID, Name: u.Name, MaxStrength: u.MaxStrength,
			MaxOrg: u.MaxOrg, Attack: u.Attack, Defense: u.Defense, Speed: u.Speed,
			OrgRecovery: u.OrgRecovery, Width: u.Width, ReinforceChance: u.ReinforceChance}
	}

	// States (regions in sim), before provinces refer to them.
	keys = make([]string, len(mf.States))
	for i, st := range mf.States {
		keys[i] = st.ID
	}
	stateIdx, err := index("state", keys)
	if err != nil {
		return nil, err
	}

	// Provinces and edges.
	keys = make([]string, len(mf.Provinces))
	for i, p := range mf.Provinces {
		keys[i] = p.ID
	}
	provIdx, err := index("province", keys)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errors.New("map has no provinces")
	}
	w.Provinces = make([]sim.Province, len(keys))
	for _, p := range mf.Provinces {
		t, ok := terrainIdx[p.Terrain]
		if !ok {
			return nil, fmt.Errorf("province %q: unknown terrain %q", p.ID, p.Terrain)
		}
		if len(p.Pos) != 2 {
			return nil, fmt.Errorf("province %q: pos must be [x, y]", p.ID)
		}
		if p.Hub && !w.Terrains[t].Passable {
			return nil, fmt.Errorf("province %q: a hub must be passable", p.ID)
		}
		region := sim.NoRegion
		if p.State != "" {
			si, ok := stateIdx[p.State]
			if !ok {
				return nil, fmt.Errorf("province %q: unknown state %q", p.ID, p.State)
			}
			if !w.Terrains[t].Passable {
				return nil, fmt.Errorf("province %q: an impassable province cannot belong to a state", p.ID)
			}
			region = sim.RegionID(si + 1)
		}
		if err := inRange("vp", int64(p.VP), 0, 1_000_000); err != nil {
			return nil, fmt.Errorf("province %q: %w", p.ID, err)
		}
		if p.VP > 0 && !w.Terrains[t].Passable {
			return nil, fmt.Errorf("province %q: victory points need a passable province", p.ID)
		}
		w.Provinces[provIdx[p.ID]] = sim.Province{Key: p.ID, Name: p.Name, Terrain: sim.TerrainID(t),
			X: p.Pos[0], Y: p.Pos[1], Hub: p.Hub, Region: region, VP: p.VP}
	}
	for n, e := range mf.Edges {
		if len(e) != 3 {
			return nil, fmt.Errorf("edge %d: want [a, b, distance]", n)
		}
		ak, _ := e[0].(string)
		bk, _ := e[1].(string)
		dist, okd := e[2].(int)
		a, oka := provIdx[ak]
		b, okb := provIdx[bk]
		if !oka || !okb || !okd {
			return nil, fmt.Errorf("edge %d: unknown province or bad distance: %v", n, e)
		}
		if a == b {
			return nil, fmt.Errorf("edge %d: self edge on %q", n, ak)
		}
		if err := inRange("distance", int64(dist), 1, 100000); err != nil {
			return nil, fmt.Errorf("edge %d: %w", n, err)
		}
		for _, x := range w.Provinces[a].Adj {
			if int(x.To) == b {
				return nil, fmt.Errorf("edge %d: duplicate edge %s-%s", n, ak, bk)
			}
		}
		w.Provinces[a].Adj = append(w.Provinces[a].Adj, sim.Edge{To: sim.ProvinceID(b), Distance: int64(dist)})
		w.Provinces[b].Adj = append(w.Provinces[b].Adj, sim.Edge{To: sim.ProvinceID(a), Distance: int64(dist)})
	}
	for i := range w.Provinces {
		adj := w.Provinces[i].Adj
		sort.Slice(adj, func(x, y int) bool { return adj[x].To < adj[y].To })
	}
	if err := loadEdgeTags(w, provIdx, mf.Rivers, mf.Railways); err != nil {
		return nil, err
	}
	if err := loadRegions(w, provIdx, mf); err != nil {
		return nil, err
	}

	// Factions, initial control, divisions.
	keys = make([]string, len(sf.Factions))
	for i, f := range sf.Factions {
		keys[i] = f.ID
	}
	facIdx, err := index("faction", keys)
	if err != nil {
		return nil, err
	}
	if len(keys) < 2 || len(keys) > 200 {
		return nil, errors.New("scenario needs between 2 and 200 factions")
	}
	w.Factions = make([]sim.Faction, len(keys)+1)
	w.InitControl = make([]sim.FactionID, len(w.Provinces))
	for _, f := range sf.Factions {
		id := sim.FactionID(facIdx[f.ID] + 1)
		w.Factions[id] = sim.Faction{Key: f.ID, Name: f.Name}
		assign := func(pk string) error {
			p, ok := provIdx[pk]
			if !ok {
				return fmt.Errorf("faction %q: unknown province %q", f.ID, pk)
			}
			switch w.InitControl[p] {
			case sim.NoFaction:
			case id:
				return fmt.Errorf("faction %q: province %q is listed twice (in provinces or in two of its states)", f.ID, pk)
			default:
				return fmt.Errorf("province %q is assigned to two factions", pk)
			}
			w.InitControl[p] = id
			return nil
		}
		for _, pk := range f.Provinces {
			if err := assign(pk); err != nil {
				return nil, err
			}
		}
		for _, sk := range f.States {
			si, ok := stateIdx[sk]
			if !ok {
				return nil, fmt.Errorf("faction %q: unknown state %q", f.ID, sk)
			}
			for _, p := range w.Regions[si+1].Provinces {
				if err := assign(w.Provinces[p].Key); err != nil {
					return nil, err
				}
			}
		}
		if f.Capital != "" {
			p, ok := provIdx[f.Capital]
			if !ok || w.InitControl[p] != id {
				return nil, fmt.Errorf("faction %q: capital %q is not a province the faction controls", f.ID, f.Capital)
			}
			if !w.Terrains[w.Provinces[p].Terrain].Passable {
				return nil, fmt.Errorf("faction %q: capital %q must be passable", f.ID, f.Capital)
			}
			w.Factions[id].HasCapital, w.Factions[id].Capital = true, sim.ProvinceID(p)
		}
	}
	for n, d := range sf.Divisions {
		f, okf := facIdx[d.Faction]
		t, okt := unitIdx[d.Type]
		p, okp := provIdx[d.At]
		if !okf || !okt || !okp {
			return nil, fmt.Errorf("division %d (%q): unknown faction, type or province", n, d.Name)
		}
		fid := sim.FactionID(f + 1)
		if !w.Terrains[w.Provinces[p].Terrain].Passable {
			return nil, fmt.Errorf("division %q starts in an impassable province", d.Name)
		}
		if w.InitControl[p] != fid {
			return nil, fmt.Errorf("division %q must start in a province its faction controls", d.Name)
		}
		w.InitDivisions = append(w.InitDivisions, sim.InitDivision{
			Faction: fid, Type: sim.UnitTypeID(t), Name: d.Name, At: sim.ProvinceID(p)})
	}
	return w, nil
}

// edgeOf returns both directions of the edge a-b, or nils if they are not adjacent.
func edgeOf(w *sim.World, a, b int) (*sim.Edge, *sim.Edge) {
	var ab, ba *sim.Edge
	for i := range w.Provinces[a].Adj {
		if int(w.Provinces[a].Adj[i].To) == b {
			ab = &w.Provinces[a].Adj[i]
		}
	}
	for i := range w.Provinces[b].Adj {
		if int(w.Provinces[b].Adj[i].To) == a {
			ba = &w.Provinces[b].Adj[i]
		}
	}
	return ab, ba
}

// loadEdgeTags puts rivers and railways on existing edges.
func loadEdgeTags(w *sim.World, provIdx map[string]int, rivers, railways [][]any) error {
	edge := func(what string, n int, e []any) (*sim.Edge, *sim.Edge, error) {
		if len(e) != 3 {
			return nil, nil, fmt.Errorf("%s %d: want [a, b, value]", what, n)
		}
		ak, _ := e[0].(string)
		bk, _ := e[1].(string)
		a, oka := provIdx[ak]
		b, okb := provIdx[bk]
		if !oka || !okb {
			return nil, nil, fmt.Errorf("%s %d: unknown province in %v", what, n, e)
		}
		ab, ba := edgeOf(w, a, b)
		if ab == nil {
			return nil, nil, fmt.Errorf("%s %d: %s and %s are not adjacent", what, n, ak, bk)
		}
		return ab, ba, nil
	}
	for n, e := range rivers {
		ab, ba, err := edge("river", n, e)
		if err != nil {
			return err
		}
		kind := sim.RiverNone
		switch e[2] {
		case "small":
			kind = sim.RiverSmall
		case "large":
			kind = sim.RiverLarge
		default:
			return fmt.Errorf("river %d: kind must be small or large, not %v", n, e[2])
		}
		if ab.River != sim.RiverNone {
			return fmt.Errorf("river %d: duplicate river on %v", n, e[:2])
		}
		if !w.Terrains[w.Provinces[ab.To].Terrain].Passable || !w.Terrains[w.Provinces[ba.To].Terrain].Passable {
			return fmt.Errorf("river %d: both sides must be passable", n)
		}
		ab.River, ba.River = kind, kind
	}
	for n, e := range railways {
		ab, ba, err := edge("railway", n, e)
		if err != nil {
			return err
		}
		level, ok := e[2].(int)
		if !ok || level < 1 || level > 10 {
			return fmt.Errorf("railway %d: level must be 1..10, not %v", n, e[2])
		}
		if ab.Rail != 0 {
			return fmt.Errorf("railway %d: duplicate railway on %v", n, e[:2])
		}
		if !w.Terrains[w.Provinces[ab.To].Terrain].Passable || !w.Terrains[w.Provinces[ba.To].Terrain].Passable {
			return fmt.Errorf("railway %d: both ends must be passable", n)
		}
		ab.Rail, ba.Rail = uint8(level), uint8(level)
	}
	return nil
}

// loadRegions fills World.Regions from the states of the map: index 0 is
// "none", state i in key order is region i+1.
func loadRegions(w *sim.World, provIdx map[string]int, mf mapFile) error {
	keys := make([]string, len(mf.States))
	for i, st := range mf.States {
		keys[i] = st.ID
	}
	idx, err := index("state", keys)
	if err != nil {
		return err
	}
	w.Regions = make([]sim.Region, len(keys)+1)
	w.Regions[0].Capital = sim.NoProvince
	for _, st := range mf.States {
		id := idx[st.ID] + 1
		c, ok := provIdx[st.Capital]
		if !ok {
			return fmt.Errorf("state %q: unknown capital %q", st.ID, st.Capital)
		}
		if err := errors.Join(
			inRange("manpower", st.Manpower, 0, 1_000_000_000_000),
			inRange("factories", int64(st.Factories), 0, 100_000),
			inRange("infrastructure", int64(st.Infrastructure), 0, 100),
		); err != nil {
			return fmt.Errorf("state %q: %w", st.ID, err)
		}
		w.Regions[id] = sim.Region{Key: st.ID, Name: st.Name, Capital: sim.ProvinceID(c), Manpower: st.Manpower,
			Factories: st.Factories, Infrastructure: st.Infrastructure}
	}
	for p := range w.Provinces {
		if r := w.Provinces[p].Region; r != sim.NoRegion {
			w.Regions[r].Provinces = append(w.Regions[r].Provinces, sim.ProvinceID(p))
		}
	}
	for r := 1; r < len(w.Regions); r++ {
		reg := &w.Regions[r]
		if len(reg.Provinces) == 0 {
			return fmt.Errorf("state %q has no provinces", reg.Key)
		}
		if w.Provinces[reg.Capital].Region != sim.RegionID(r) {
			return fmt.Errorf("state %q: capital %q lies in another state", reg.Key, w.Provinces[reg.Capital].Key)
		}
		// A state is one piece: every province is reachable from the capital inside the state.
		seen := map[sim.ProvinceID]bool{reg.Capital: true}
		queue := []sim.ProvinceID{reg.Capital}
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			for _, e := range w.Provinces[p].Adj {
				if !seen[e.To] && w.Provinces[e.To].Region == sim.RegionID(r) {
					seen[e.To] = true
					queue = append(queue, e.To)
				}
			}
		}
		if len(seen) != len(reg.Provinces) {
			return fmt.Errorf("state %q is not connected", reg.Key)
		}
	}
	return nil
}
