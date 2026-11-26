// Package view computes what one faction can see of a state and the
// difference between two such views. It only reads the simulation state.
package view

import (
	"cmp"
	"slices"

	"sandtable/server/internal/sim"
)

type Province struct {
	Controller sim.FactionID
	Visible    bool
	InBattle   bool // only meaningful when Visible
}

type Division struct {
	ID         sim.DivisionID
	Faction    sim.FactionID
	Type       sim.UnitTypeID
	Name       string
	Province   sim.ProvinceID
	Strength   int32
	Org        int32
	Retreating bool

	// Own divisions only.
	Own      bool
	Path     []sim.ProvinceID
	Progress int64
	Supply   int32
}

func (d *Division) equal(o *Division) bool {
	return d.ID == o.ID && d.Faction == o.Faction && d.Type == o.Type && d.Name == o.Name &&
		d.Province == o.Province && d.Strength == o.Strength && d.Org == o.Org &&
		d.Retreating == o.Retreating && d.Own == o.Own && d.Progress == o.Progress &&
		d.Supply == o.Supply && slices.Equal(d.Path, o.Path)
}

// View is everything one faction can see at one tick.
type View struct {
	Tick      uint64
	Provinces []Province // indexed by province ID
	Divisions []Division // ascending ID
}

// Visible returns the provinces faction f can see: the ones it controls or
// occupies, plus everything within the vision range of those.
func Visible(w *sim.World, s *sim.State, f sim.FactionID) []bool {
	n := len(w.Provinces)
	vis := make([]bool, n)
	frontier := make([]sim.ProvinceID, 0, n)
	mark := func(p sim.ProvinceID) {
		if !vis[p] {
			vis[p] = true
			frontier = append(frontier, p)
		}
	}
	for p := range s.Provinces {
		if s.Provinces[p].Controller == f {
			mark(sim.ProvinceID(p))
		}
	}
	for i := range s.Divisions {
		if s.Divisions[i].Faction == f {
			mark(s.Divisions[i].Loc)
		}
	}
	for r := int32(0); r < w.Rules.VisionRange; r++ {
		cur := frontier
		frontier = nil
		for _, p := range cur {
			for _, e := range w.Provinces[p].Adj {
				mark(e.To)
			}
		}
	}
	return vis
}

// Build computes the view of faction f.
func Build(w *sim.World, s *sim.State, f sim.FactionID) *View {
	vis := Visible(w, s, f)
	v := &View{Tick: s.Tick, Provinces: make([]Province, len(s.Provinces))}
	for p := range s.Provinces {
		v.Provinces[p] = Province{
			Controller: s.Provinces[p].Controller, // public
			Visible:    vis[p],
			InBattle:   vis[p] && s.Provinces[p].InBattle,
		}
	}
	for i := range s.Divisions {
		d := &s.Divisions[i]
		own := d.Faction == f
		if !own && !vis[d.Loc] {
			continue
		}
		dv := Division{ID: d.ID, Faction: d.Faction, Type: d.Type, Name: d.Name, Province: d.Loc,
			Strength: d.Strength, Org: d.Org, Retreating: d.Retreating}
		if own {
			dv.Own = true
			dv.Path = slices.Clone(d.Path)
			dv.Progress = d.Progress
			dv.Supply = d.Supply
		}
		v.Divisions = append(v.Divisions, dv)
	}
	return v
}

type ProvinceUpdate struct {
	ID sim.ProvinceID
	Province
}

type Removed struct {
	ID sim.DivisionID
	// Destroyed is true when the division died where the viewer could see it.
	// Otherwise it merely left the viewer's sight.
	Destroyed bool
}

// Delta turns the view of one tick into the view of the next.
type Delta struct {
	Tick      uint64
	Provinces []ProvinceUpdate
	Divisions []Division // new or changed, complete records
	Removed   []Removed
}

// Diff returns the delta from prev to cur. deaths are the divisions that died
// in the step from prev to cur (sim.StepResult.Deaths).
func Diff(prev, cur *View, deaths []sim.Death) *Delta {
	d := &Delta{Tick: cur.Tick}
	for p := range cur.Provinces {
		if cur.Provinces[p] != prev.Provinces[p] {
			d.Provinces = append(d.Provinces, ProvinceUpdate{ID: sim.ProvinceID(p), Province: cur.Provinces[p]})
		}
	}
	i, j := 0, 0
	for i < len(prev.Divisions) || j < len(cur.Divisions) {
		switch {
		case j == len(cur.Divisions) || (i < len(prev.Divisions) && prev.Divisions[i].ID < cur.Divisions[j].ID):
			old := &prev.Divisions[i]
			// Do not reveal a death the viewer could not have seen. Where it died
			// counts, not where it was last seen: it may have left sight and died
			// in the same tick.
			destroyed := false
			if k, dead := slices.BinarySearchFunc(deaths, old.ID, func(x sim.Death, id sim.DivisionID) int {
				return cmp.Compare(x.ID, id)
			}); dead {
				destroyed = old.Own || cur.Provinces[deaths[k].Loc].Visible
			}
			d.Removed = append(d.Removed, Removed{ID: old.ID, Destroyed: destroyed})
			i++
		case i == len(prev.Divisions) || cur.Divisions[j].ID < prev.Divisions[i].ID:
			d.Divisions = append(d.Divisions, cur.Divisions[j])
			j++
		default:
			if !prev.Divisions[i].equal(&cur.Divisions[j]) {
				d.Divisions = append(d.Divisions, cur.Divisions[j])
			}
			i++
			j++
		}
	}
	return d
}

// Apply updates v in place with d. Clients use this to follow the server.
func Apply(v *View, d *Delta) {
	v.Tick = d.Tick
	for _, u := range d.Provinces {
		v.Provinces[u.ID] = u.Province
	}
	if len(d.Removed) > 0 {
		v.Divisions = slices.DeleteFunc(v.Divisions, func(x Division) bool {
			return slices.ContainsFunc(d.Removed, func(r Removed) bool { return r.ID == x.ID })
		})
	}
	for _, nd := range d.Divisions {
		i, found := slices.BinarySearchFunc(v.Divisions, nd.ID, func(x Division, id sim.DivisionID) int {
			switch {
			case x.ID < id:
				return -1
			case x.ID > id:
				return 1
			}
			return 0
		})
		if found {
			v.Divisions[i] = nd
		} else {
			v.Divisions = slices.Insert(v.Divisions, i, nd)
		}
	}
}

// Equal reports whether two views are identical.
func Equal(a, b *View) bool {
	return a.Tick == b.Tick && slices.Equal(a.Provinces, b.Provinces) &&
		slices.EqualFunc(a.Divisions, b.Divisions, func(x, y Division) bool { return x.equal(&y) })
}
