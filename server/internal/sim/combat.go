package sim

// isAttacker reports whether d is attacking the next province on its path.
func (e *Engine) isAttacker(d *Division) bool {
	return alive(d) && !d.Retreating && len(d.Path) > 0 && e.hostile(d.Path[0], d.Faction)
}

// bucket groups division indices by province, keeping ID order inside a group.
func (e *Engine) bucket(s *State, start []int32, items []int32, key func(d *Division) (ProvinceID, bool)) []int32 {
	clear(start)
	n := 0
	for i := range s.Divisions {
		if p, ok := key(&s.Divisions[i]); ok {
			start[p+1]++
			n++
		}
	}
	for p := 0; p < e.np; p++ {
		start[p+1] += start[p]
	}
	if cap(items) < n {
		items = make([]int32, n)
	}
	items = items[:n]
	copy(e.fill, start[:e.np])
	for i := range s.Divisions {
		if p, ok := key(&s.Divisions[i]); ok {
			items[e.fill[p]] = int32(i)
			e.fill[p]++
		}
	}
	return items
}

func role(d *Division, attack bool) *uint8 {
	if attack {
		return &d.AttackRole
	}
	return &d.DefenseRole
}

// assignRoles decides which divisions of one side stand in the front line.
func (e *Engine) assignRoles(s *State, side []int32, attack, newBattle bool, limit int64) {
	w := e.w
	var used int64
	if newBattle {
		for _, i := range side {
			d := &s.Divisions[i]
			wd := w.UnitTypes[d.Type].Width
			if used == 0 || used+wd <= limit {
				*role(d, attack) = RoleFront
				used += wd
			} else {
				*role(d, attack) = RoleReserve
			}
		}
		return
	}
	for _, i := range side {
		d := &s.Divisions[i]
		r := role(d, attack)
		if *r == RoleNone {
			*r = RoleReserve
		}
		if *r == RoleFront {
			used += w.UnitTypes[d.Type].Width
		}
	}
	purpose := purposeReinforce
	if attack {
		purpose = purposeReinforceAttack
	}
	for _, i := range side {
		d := &s.Divisions[i]
		r := role(d, attack)
		if *r != RoleReserve {
			continue
		}
		ut := &w.UnitTypes[d.Type]
		if used != 0 && used+ut.Width > limit {
			continue
		}
		if used != 0 {
			g := newRNG(s.Seed, s.Tick, purpose, uint64(d.ID))
			if g.intn(1000) >= ut.ReinforceChance {
				continue
			}
		}
		*r = RoleFront
		used += ut.Width
	}
}

func (e *Engine) front(s *State, side []int32, attack bool, out []int32) []int32 {
	out = out[:0]
	for _, i := range side {
		if *role(&s.Divisions[i], attack) == RoleFront {
			out = append(out, i)
		}
	}
	return out
}

// strike makes division ai hit one random division of targets.
func (e *Engine) strike(s *State, ai int32, targets []int32, terrainBonus int64) {
	w := e.w
	a := &s.Divisions[ai]
	ut := &w.UnitTypes[a.Type]
	r := &w.Rules
	g := newRNG(s.Seed, s.Tick, purposeCombat, uint64(a.ID))
	ti := targets[g.intn(int64(len(targets)))]
	t := &s.Divisions[ti]
	roll := r.RollMin + g.intn(r.RollMax-r.RollMin+1)

	supplyEffect := r.MinSupplyEffect + (1000-r.MinSupplyEffect)*int64(a.Supply)/1000
	power := ut.Attack * 1000 * int64(a.Strength) / int64(ut.MaxStrength)
	power = power * supplyEffect / 1000
	power = power * roll / 1000

	reduction := w.UnitTypes[t.Type].Defense + terrainBonus
	if reduction > r.MaxReduction {
		reduction = r.MaxReduction
	}
	damage := power * (1000 - reduction) / 1000
	e.pendOrg[ti] += damage * r.OrgDamage / 1_000_000
	e.pendStr[ti] += damage * r.StrengthDamage / 1_000_000
}

func (e *Engine) combat(s *State) {
	w := e.w
	n := len(s.Divisions)

	e.atkItems = e.bucket(s, e.atkStart, e.atkItems, func(d *Division) (ProvinceID, bool) {
		if e.isAttacker(d) {
			return d.Path[0], true
		}
		return 0, false
	})
	e.defItems = e.bucket(s, e.defStart, e.defItems, func(d *Division) (ProvinceID, bool) {
		return d.Loc, alive(d) && !d.Retreating
	})

	// Drop roles of divisions that are no longer in a battle.
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !e.isAttacker(d) {
			d.AttackRole = RoleNone
		}
		if e.atkStart[d.Loc+1] == e.atkStart[d.Loc] || d.Retreating {
			d.DefenseRole = RoleNone
		}
	}

	// Pass 1: front lines of every battle.
	for p := 0; p < e.np; p++ {
		atk := e.atkItems[e.atkStart[p]:e.atkStart[p+1]]
		if len(atk) == 0 {
			continue
		}
		def := e.defItems[e.defStart[p]:e.defStart[p+1]]
		e.dirs = e.dirs[:0]
		for _, i := range atk {
			from := s.Divisions[i].Loc
			seen := false
			for _, x := range e.dirs {
				if x == from {
					seen = true
					break
				}
			}
			if !seen {
				e.dirs = append(e.dirs, from)
			}
		}
		base := w.Terrains[w.Provinces[p].Terrain].CombatWidth
		limit := base + int64(len(e.dirs)-1)*base*w.Rules.ExtraDirectionWidth/1000
		newBattle := !s.Provinces[p].InBattle
		e.assignRoles(s, atk, true, newBattle, limit)
		e.assignRoles(s, def, false, newBattle, limit)
	}

	// Pass 2: damage, computed for every battle before any of it is applied.
	if cap(e.pendOrg) < n {
		e.pendOrg = make([]int64, n)
		e.pendStr = make([]int64, n)
	}
	e.pendOrg = e.pendOrg[:n]
	e.pendStr = e.pendStr[:n]
	clear(e.pendOrg)
	clear(e.pendStr)
	for p := 0; p < e.np; p++ {
		atk := e.atkItems[e.atkStart[p]:e.atkStart[p+1]]
		if len(atk) == 0 {
			continue
		}
		def := e.defItems[e.defStart[p]:e.defStart[p+1]]
		e.frontA = e.front(s, atk, true, e.frontA)
		e.frontD = e.front(s, def, false, e.frontD)
		bonus := w.Terrains[w.Provinces[p].Terrain].DefenseBonus
		for _, i := range e.frontA {
			e.strike(s, i, e.frontD, bonus)
		}
		for _, i := range e.frontD {
			// A division strikes once per tick: attacking elsewhere takes priority.
			if s.Divisions[i].AttackRole != RoleFront {
				e.strike(s, i, e.frontA, 0)
			}
		}
	}

	// Apply damage, then destruction, then retreats and broken attacks.
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !alive(d) || (e.pendOrg[i] == 0 && e.pendStr[i] == 0) {
			continue
		}
		org := int64(d.Org) - e.pendOrg[i]
		if org < 0 {
			org = 0
		}
		d.Org = int32(org)
		if int64(d.Strength) <= e.pendStr[i] {
			e.kill(d)
		} else {
			d.Strength -= int32(e.pendStr[i])
		}
	}
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !alive(d) || d.Org > 0 {
			continue
		}
		if d.DefenseRole == RoleFront {
			t, ok := e.retreatTarget(s, d)
			if !ok {
				e.kill(d)
				continue
			}
			e.occupy(d.Loc, d.Faction, -1)
			d.Retreating = true
			d.Path = []ProvinceID{t}
			d.Progress = 0
			d.AttackRole = RoleNone
			d.DefenseRole = RoleNone
		} else if d.AttackRole != RoleNone {
			d.Path = nil
			d.Progress = 0
			d.AttackRole = RoleNone
		}
	}

	// Battles that are still going after this round.
	for p := range s.Provinces {
		s.Provinces[p].InBattle = false
	}
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if d.AttackRole != RoleNone && e.isAttacker(d) {
			s.Provinces[d.Path[0]].InBattle = true
		} else {
			d.AttackRole = RoleNone
		}
	}
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !alive(d) || d.Retreating || !s.Provinces[d.Loc].InBattle {
			d.DefenseRole = RoleNone
		}
	}
}
