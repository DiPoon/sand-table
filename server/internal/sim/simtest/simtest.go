// Package simtest builds worlds and command scripts for tests and benchmarks.
package simtest

import (
	"fmt"

	"sandtable/server/internal/sim"
)

// Rules are the sample rule constants.
var Rules = sim.Rules{
	RollMin: 800, RollMax: 1200, OrgDamage: 1000, StrengthDamage: 400,
	MaxReduction: 800, MinSupplyEffect: 400, ExtraDirectionWidth: 500,
	SupplyFullRange: 80, SupplyMaxRange: 240, Attrition: 20, VisionRange: 1,
}

var Infantry = sim.UnitType{Key: "infantry", Name: "Infantry", MaxStrength: 10000, MaxOrg: 1000,
	Attack: 60, Defense: 200, Speed: 4, OrgRecovery: 10, Width: 20, ReinforceChance: 20}

var Plains = sim.Terrain{Key: "plains", Name: "Plains", MoveCost: 1000, CombatWidth: 80, Passable: true}

// Grid builds a width x height grid of plains, 40 km apart, split into
// vertical stripes for each faction. Each faction gets one hub and
// divisions spread over its provinces.
func Grid(width, height, factions, divisionsPerFaction int) *sim.World {
	w := &sim.World{Scenario: fmt.Sprintf("grid-%dx%d", width, height), Rules: Rules}
	w.Terrains = []sim.Terrain{Plains}
	w.UnitTypes = []sim.UnitType{Infantry}
	w.Factions = make([]sim.Faction, factions+1)
	w.Factions[0].Capital = sim.NoProvince
	w.Regions = []sim.Region{{Capital: sim.NoProvince}} // the "none" entry; a grid has no states
	for f := 1; f <= factions; f++ {
		w.Factions[f] = sim.Faction{Key: fmt.Sprintf("f%d", f), Name: fmt.Sprintf("Faction %d", f), Capital: sim.NoProvince}
	}
	n := width * height
	w.Provinces = make([]sim.Province, n)
	w.InitControl = make([]sim.FactionID, n)
	owned := make([][]sim.ProvinceID, factions+1)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			id := y*width + x
			p := &w.Provinces[id]
			p.Key = fmt.Sprintf("g%05d", id)
			p.Name = p.Key
			p.X, p.Y = int32(x*100), int32(y*100)
			f := x*factions/width + 1
			w.InitControl[id] = sim.FactionID(f)
			owned[f] = append(owned[f], sim.ProvinceID(id))
			// Neighbours in ascending ID order.
			if y > 0 {
				p.Adj = append(p.Adj, sim.Edge{To: sim.ProvinceID(id - width), Distance: 40})
			}
			if x > 0 {
				p.Adj = append(p.Adj, sim.Edge{To: sim.ProvinceID(id - 1), Distance: 40})
			}
			if x < width-1 {
				p.Adj = append(p.Adj, sim.Edge{To: sim.ProvinceID(id + 1), Distance: 40})
			}
			if y < height-1 {
				p.Adj = append(p.Adj, sim.Edge{To: sim.ProvinceID(id + width), Distance: 40})
			}
		}
	}
	for f := 1; f <= factions; f++ {
		ps := owned[f]
		// Hubs every few provinces so most of the stripe is supplied.
		for i := 0; i < len(ps); i += 7 {
			w.Provinces[ps[i]].Hub = true
		}
		for i := 0; i < divisionsPerFaction; i++ {
			w.InitDivisions = append(w.InitDivisions, sim.InitDivision{
				Faction: sim.FactionID(f), Type: 0, Name: fmt.Sprintf("D%d-%d", f, i),
				At: ps[(i*len(ps))/divisionsPerFaction],
			})
		}
	}
	return w
}

// Script is a deterministic stream of commands for tests.
type Script struct {
	s uint64
	w *sim.World
}

func NewScript(w *sim.World, seed uint64) *Script { return &Script{s: seed, w: w} }

func (sc *Script) next() uint64 {
	sc.s += 0x9E3779B97F4A7C15
	z := sc.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Commands returns the commands for the next tick: on average `rate` permille
// of the divisions get a move order to a random province. Some orders are
// deliberately issued by the wrong faction.
func (sc *Script) Commands(s *sim.State, rate uint64) []sim.Command {
	var out []sim.Command
	for i := range s.Divisions {
		if sc.next()%1000 >= rate {
			continue
		}
		d := &s.Divisions[i]
		f := d.Faction
		if sc.next()%20 == 0 {
			f = f%sim.FactionID(len(sc.w.Factions)-1) + 1
		}
		out = append(out, sim.Command{Faction: f, Kind: sim.CmdMove, Division: d.ID,
			Dest: sim.ProvinceID(sc.next() % uint64(len(sc.w.Provinces)))})
	}
	return out
}
