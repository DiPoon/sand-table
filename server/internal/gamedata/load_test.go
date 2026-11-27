package gamedata_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/sim"
)

// A line of four provinces a-b-c-d and a lake.
const baseMap = `
provinces:
  - {id: a, name: A, terrain: plains, pos: [0, 0], state: s1, vp: 10, hub: true}
  - {id: b, name: B, terrain: plains, pos: [40, 0], state: s1}
  - {id: c, name: C, terrain: forest, pos: [80, 0], state: s2, vp: 2}
  - {id: d, name: D, terrain: plains, pos: [120, 0], state: s2}
  - {id: x, name: X, terrain: lake, pos: [60, 40]}
edges:
  - [a, b, 40]
  - [b, c, 40]
  - [c, d, 40]
  - [c, x, 30]
states:
  - {id: s1, name: One, capital: a, manpower: 1000, factories: 2, infrastructure: 3}
  - {id: s2, name: Two, capital: d, manpower: 500, factories: 0, infrastructure: 1}
rivers:
  - [b, c, large]
railways:
  - [a, b, 1]
`

const baseScenario = `
map: m
factions:
  - {id: west, name: West, capital: a, states: [s1]}
  - {id: east, name: East, capital: d, states: [s2]}
divisions:
  - {faction: west, type: infantry, name: W1, at: b}
`

func load(t *testing.T, mapYAML, scenarioYAML string) (*sim.World, error) {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "maps"), 0o755)
	os.MkdirAll(filepath.Join(dir, "scenarios"), 0o755)
	for _, f := range []string{"rules.yaml", "units.yaml"} {
		data, err := os.ReadFile(filepath.Join("../../../data", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), data, 0o644)
	}
	os.WriteFile(filepath.Join(dir, "maps", "m.yaml"), []byte(mapYAML), 0o644)
	os.WriteFile(filepath.Join(dir, "scenarios", "s.yaml"), []byte(scenarioYAML), 0o644)
	return gamedata.Load(dir, "s")
}

func TestStatesRiversRailwaysAndCapitals(t *testing.T) {
	w, err := load(t, baseMap, baseScenario)
	if err != nil {
		t.Fatal(err)
	}
	id := func(key string) sim.ProvinceID {
		for i, p := range w.Provinces {
			if p.Key == key {
				return sim.ProvinceID(i)
			}
		}
		t.Fatalf("no province %s", key)
		return 0
	}
	a, b, c, d := id("a"), id("b"), id("c"), id("d")
	if len(w.Regions) != 3 || w.Regions[1].Key != "s1" || w.Regions[1].Capital != a || w.Regions[1].Factories != 2 {
		t.Fatalf("regions: %+v", w.Regions)
	}
	if w.Provinces[c].Region != 2 || w.Provinces[id("x")].Region != sim.NoRegion || w.Provinces[a].VP != 10 {
		t.Fatal("province region or victory points")
	}
	edge := func(p, q sim.ProvinceID) sim.Edge {
		for _, e := range w.Provinces[p].Adj {
			if e.To == q {
				return e
			}
		}
		t.Fatalf("no edge %d-%d", p, q)
		return sim.Edge{}
	}
	if edge(b, c).River != sim.RiverLarge || edge(c, b).River != sim.RiverLarge || edge(a, b).River != sim.RiverNone {
		t.Fatal("river must be on b-c both ways and nowhere else")
	}
	if edge(a, b).Rail != 1 || edge(b, a).Rail != 1 || edge(c, d).Rail != 0 {
		t.Fatal("railway must be on a-b both ways and nowhere else")
	}
	// Faction IDs follow key order: east is 1, west is 2.
	if w.Factions[1].Key != "east" || !w.Factions[1].HasCapital || w.Factions[1].Capital != d || w.Factions[2].Capital != a ||
		w.InitControl[c] != 1 || w.InitControl[b] != 2 || w.InitControl[id("x")] != sim.NoFaction {
		t.Fatal("capitals or control from states")
	}
}

func TestBadMapsAreRejected(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, want string
		scenario             bool
	}{
		// s1 becomes a, b and d; d touches only c.
		{"state in two pieces", "{id: d, name: D, terrain: plains, pos: [120, 0], state: s2}", "{id: d, name: D, terrain: plains, pos: [120, 0], state: s1}", "not connected", false},
		{"unknown state", "state: s2, vp: 2}", "state: s9, vp: 2}", "unknown state", false},
		{"water in a state", "{id: x, name: X, terrain: lake, pos: [60, 40]}", "{id: x, name: X, terrain: lake, pos: [60, 40], state: s1}", "impassable", false},
		{"capital outside its state", "capital: d, manpower: 500", "capital: a, manpower: 500", "another state", false},
		{"river where there is no border", "  - [b, c, large]", "  - [a, c, large]", "not adjacent", false},
		{"river of an unknown kind", "  - [b, c, large]", "  - [b, c, huge]", "small or large", false},
		{"railway level", "  - [a, b, 1]", "  - [a, b, 0]", "level", false},
		{"river along a lake shore", "  - [b, c, large]", "  - [c, x, small]", "passable", false},
		{"capital the faction does not hold", "{id: west, name: West, capital: a", "{id: west, name: West, capital: c", "capital", true},
		{"state given to two factions", "states: [s2]}", "states: [s2, s1]}", "two factions", true},
		{"state listed twice", "states: [s1]}", "states: [s1, s1]}", "listed twice", true},
		{"capital in a lake", "{id: west, name: West, capital: a, states: [s1]}", "{id: west, name: West, capital: x, provinces: [x], states: [s1]}", "passable", true},
	} {
		m, s := baseMap, baseScenario
		if tc.scenario {
			s = strings.Replace(s, tc.from, tc.to, 1)
		} else {
			m = strings.Replace(m, tc.from, tc.to, 1)
		}
		if m == baseMap && s == baseScenario {
			t.Fatalf("%s: the replacement did not apply", tc.name)
		}
		_, err := load(t, m, s)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}
