package sim

// Engine runs the simulation for one World. It owns scratch buffers and
// caches of pure derived values, so it is not safe for concurrent use.
// Nothing in it affects results: two engines always produce the same states.
type Engine struct {
	w  *World
	np int
	nf int // number of factions including the "none" entry

	// pathfinding scratch
	dist []int64
	prev []ProvinceID
	heap []heapItem

	// occupancy of non-retreating divisions, rebuilt every Step
	cnt []int32 // [province*nf + faction]
	tot []int32 // [province]

	// supply cache: a pure function of the controller array
	supplyCtl   []FactionID
	supplyValid bool
	supplyLvl   []int32 // [faction*np + province]

	// combat scratch
	atkStart, atkItems []int32
	defStart, defItems []int32
	fill               []int32
	pendOrg, pendStr   []int64
	frontA, frontD     []int32
	dirs               []ProvinceID
}

func NewEngine(w *World) *Engine {
	np, nf := len(w.Provinces), len(w.Factions)
	return &Engine{
		w: w, np: np, nf: nf,
		dist:      make([]int64, np),
		prev:      make([]ProvinceID, np),
		cnt:       make([]int32, np*nf),
		tot:       make([]int32, np),
		supplyCtl: make([]FactionID, np),
		supplyLvl: make([]int32, np*nf),
		atkStart:  make([]int32, np+1),
		defStart:  make([]int32, np+1),
		fill:      make([]int32, np),
	}
}

func (e *Engine) World() *World { return e.w }

// NewState builds the initial state of the scenario.
func (e *Engine) NewState(seed uint64) *State {
	w := e.w
	s := &State{Seed: seed, NextDivisionID: 1}
	s.Provinces = make([]ProvinceState, e.np)
	for i := range s.Provinces {
		s.Provinces[i].Controller = w.InitControl[i]
	}
	for _, in := range w.InitDivisions {
		ut := &w.UnitTypes[in.Type]
		s.Divisions = append(s.Divisions, Division{
			ID: DivisionID(s.NextDivisionID), Faction: in.Faction, Type: in.Type, Name: in.Name,
			Loc: in.At, Strength: ut.MaxStrength, Org: ut.MaxOrg,
		})
		s.NextDivisionID++
	}
	e.updateSupplyLevels(s)
	for i := range s.Divisions {
		d := &s.Divisions[i]
		d.Supply = e.supplyLvl[int(d.Faction)*e.np+int(d.Loc)]
	}
	return s
}

// Validate checks a command against the current state without changing it.
func (e *Engine) Validate(s *State, c Command) RejectReason {
	_, _, r := e.check(s, c)
	return r
}

func (e *Engine) check(s *State, c Command) (*Division, []ProvinceID, RejectReason) {
	if c.Kind != CmdMove {
		return nil, nil, RejectMalformed
	}
	d := s.FindDivision(c.Division)
	if d == nil || d.Faction != c.Faction || d.Strength <= 0 {
		return nil, nil, RejectNotYourDivision
	}
	if d.Retreating {
		return nil, nil, RejectDivisionRetreating
	}
	if c.Dest < 0 || int(c.Dest) >= e.np {
		return nil, nil, RejectUnknownProvince
	}
	if c.Dest == d.Loc {
		return d, nil, RejectNone
	}
	path := e.FindPath(d.Loc, c.Dest)
	if path == nil {
		return nil, nil, RejectNoPath
	}
	return d, path, RejectNone
}

// Step advances the state by one tick. See docs/tick.md section 2.
func (e *Engine) Step(s *State, cmds []Command) StepResult {
	var res StepResult
	s.Tick++
	for i, c := range cmds {
		d, path, r := e.check(s, c)
		if r != RejectNone {
			res.Rejections = append(res.Rejections, Rejection{Index: i, Reason: r})
			continue
		}
		if len(path) == 0 || len(d.Path) == 0 || d.Path[0] != path[0] {
			d.Progress = 0
			d.AttackRole = RoleNone
		}
		d.Path = path
	}
	e.countOccupancy(s)
	e.move(s)
	e.supply(s)
	e.combat(s)
	e.recover(s)
	res.Deaths = compact(s, res.Deaths)
	return res
}

func alive(d *Division) bool { return d.Strength > 0 }

func (e *Engine) countOccupancy(s *State) {
	clear(e.cnt)
	clear(e.tot)
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if alive(d) && !d.Retreating {
			e.occupy(d.Loc, d.Faction, 1)
		}
	}
}

func (e *Engine) occupy(p ProvinceID, f FactionID, delta int32) {
	e.cnt[int(p)*e.nf+int(f)] += delta
	e.tot[p] += delta
}

// hostile reports whether p holds a non-retreating division hostile to f.
func (e *Engine) hostile(p ProvinceID, f FactionID) bool {
	return e.tot[p]-e.cnt[int(p)*e.nf+int(f)] > 0
}

func (e *Engine) kill(d *Division) {
	if !d.Retreating {
		e.occupy(d.Loc, d.Faction, -1)
	}
	d.Strength = 0
}

func (e *Engine) retreatTarget(s *State, d *Division) (ProvinceID, bool) {
	best, bestCost, found := ProvinceID(0), int64(0), false
	for _, a := range e.w.Provinces[d.Loc].Adj {
		cost, ok := e.w.EdgeCost(d.Loc, a.To)
		if !ok || s.Provinces[a.To].Controller != d.Faction || e.hostile(a.To, d.Faction) {
			continue
		}
		if !found || cost < bestCost {
			best, bestCost, found = a.To, cost, true
		}
	}
	return best, found
}

func (e *Engine) move(s *State) {
	w := e.w
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !alive(d) || len(d.Path) == 0 {
			continue
		}
		if d.Retreating {
			t := d.Path[0]
			if s.Provinces[t].Controller != d.Faction || e.hostile(t, d.Faction) {
				nt, ok := e.retreatTarget(s, d)
				if !ok {
					e.kill(d)
					continue
				}
				d.Path = []ProvinceID{nt}
				d.Progress = 0
			}
		}
		next := d.Path[0]
		need, _ := w.EdgeCost(d.Loc, next)
		d.Progress += w.UnitTypes[d.Type].Speed * 1000
		if d.Progress > need {
			d.Progress = need
		}
		if d.Progress < need || e.hostile(next, d.Faction) {
			continue
		}
		if !d.Retreating {
			e.occupy(d.Loc, d.Faction, -1)
		}
		d.Loc = next
		d.Path = d.Path[1:]
		if len(d.Path) == 0 {
			d.Path = nil
			d.Retreating = false
		}
		d.Progress = 0
		d.AttackRole = RoleNone
		d.DefenseRole = RoleNone
		if !d.Retreating {
			e.occupy(d.Loc, d.Faction, 1)
			s.Provinces[d.Loc].Controller = d.Faction
		}
	}
}

func (e *Engine) recover(s *State) {
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !alive(d) || d.Retreating || d.AttackRole != RoleNone || d.DefenseRole != RoleNone {
			continue
		}
		ut := &e.w.UnitTypes[d.Type]
		org := int64(d.Org) + ut.OrgRecovery*int64(d.Supply)/1000
		if org > int64(ut.MaxOrg) {
			org = int64(ut.MaxOrg)
		}
		d.Org = int32(org)
	}
}

// compact removes dead divisions and appends them to deaths.
func compact(s *State, deaths []Death) []Death {
	n := 0
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !alive(d) {
			deaths = append(deaths, Death{ID: d.ID, Loc: d.Loc})
			continue
		}
		if n != i {
			s.Divisions[n] = *d
		}
		n++
	}
	for i := n; i < len(s.Divisions); i++ {
		s.Divisions[i] = Division{}
	}
	s.Divisions = s.Divisions[:n]
	return deaths
}
