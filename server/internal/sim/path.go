package sim

type heapItem struct {
	cost int64
	id   ProvinceID
}

func heapLess(a, b heapItem) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	return a.id < b.id
}

func (e *Engine) heapPush(it heapItem) {
	h := append(e.heap, it)
	i := len(h) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !heapLess(h[i], h[p]) {
			break
		}
		h[i], h[p] = h[p], h[i]
		i = p
	}
	e.heap = h
}

func (e *Engine) heapPop() heapItem {
	h := e.heap
	top := h[0]
	n := len(h) - 1
	h[0] = h[n]
	h = h[:n]
	i := 0
	for {
		l, r, m := 2*i+1, 2*i+2, i
		if l < n && heapLess(h[l], h[m]) {
			m = l
		}
		if r < n && heapLess(h[r], h[m]) {
			m = r
		}
		if m == i {
			break
		}
		h[i], h[m] = h[m], h[i]
		i = m
	}
	e.heap = h
	return top
}

// FindPath returns the cheapest path from a to b over passable provinces,
// excluding a and including b, or nil if there is none. Costs are static, so
// the result never depends on who controls or occupies provinces.
// Ties are broken deterministically: nodes are expanded in (cost, ID) order
// and a node is only updated by a strictly cheaper route.
func (e *Engine) FindPath(a, b ProvinceID) []ProvinceID {
	w := e.w
	if a == b || !w.passable(b) {
		return nil
	}
	for i := range e.dist {
		e.dist[i] = -1
	}
	e.heap = e.heap[:0]
	e.dist[a] = 0
	e.heapPush(heapItem{0, a})
	for len(e.heap) > 0 {
		it := e.heapPop()
		if it.cost > e.dist[it.id] {
			continue
		}
		if it.id == b {
			break
		}
		for _, ed := range w.Provinces[it.id].Adj {
			t := &w.Terrains[w.Provinces[ed.To].Terrain]
			if !t.Passable {
				continue
			}
			c := it.cost + ed.Distance*t.MoveCost
			if e.dist[ed.To] < 0 || c < e.dist[ed.To] {
				e.dist[ed.To] = c
				e.prev[ed.To] = it.id
				e.heapPush(heapItem{c, ed.To})
			}
		}
	}
	if e.dist[b] < 0 {
		return nil
	}
	n := 0
	for p := b; p != a; p = e.prev[p] {
		n++
	}
	path := make([]ProvinceID, n)
	for p := b; p != a; p = e.prev[p] {
		n--
		path[n] = p
	}
	return path
}

// updateSupplyLevels recomputes supply levels per faction if province
// control changed since the last call.
func (e *Engine) updateSupplyLevels(s *State) {
	if e.supplyValid {
		same := true
		for i := range s.Provinces {
			if s.Provinces[i].Controller != e.supplyCtl[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	for i := range s.Provinces {
		e.supplyCtl[i] = s.Provinces[i].Controller
	}
	e.supplyValid = true

	w := e.w
	full := w.Rules.SupplyFullRange * 1000
	max := w.Rules.SupplyMaxRange * 1000
	for f := 1; f < e.nf; f++ {
		fid := FactionID(f)
		for i := range e.dist {
			e.dist[i] = -1
		}
		e.heap = e.heap[:0]
		for p := range w.Provinces {
			if w.Provinces[p].Hub && s.Provinces[p].Controller == fid {
				e.dist[p] = 0
				e.heapPush(heapItem{0, ProvinceID(p)})
			}
		}
		for len(e.heap) > 0 {
			it := e.heapPop()
			if it.cost > e.dist[it.id] || it.cost >= max {
				continue
			}
			// Supply only flows onward through provinces the faction controls.
			if s.Provinces[it.id].Controller != fid {
				continue
			}
			for _, ed := range w.Provinces[it.id].Adj {
				t := &w.Terrains[w.Provinces[ed.To].Terrain]
				if !t.Passable {
					continue
				}
				c := it.cost + ed.Distance*t.MoveCost
				if e.dist[ed.To] < 0 || c < e.dist[ed.To] {
					e.dist[ed.To] = c
					e.heapPush(heapItem{c, ed.To})
				}
			}
		}
		lv := e.supplyLvl[f*e.np : (f+1)*e.np]
		for p := range lv {
			d := e.dist[p]
			switch {
			case d < 0 || d >= max:
				lv[p] = 0
			case d <= full:
				lv[p] = 1000
			default:
				lv[p] = int32(1000 * (max - d) / (max - full))
			}
		}
	}
}

func (e *Engine) supply(s *State) {
	e.updateSupplyLevels(s)
	attr := e.w.Rules.Attrition
	for i := range s.Divisions {
		d := &s.Divisions[i]
		if !alive(d) {
			continue
		}
		d.Supply = e.supplyLvl[int(d.Faction)*e.np+int(d.Loc)]
		if d.Supply == 0 {
			if int64(d.Strength) <= attr {
				e.kill(d)
			} else {
				d.Strength -= int32(attr)
			}
		}
	}
}

// SupplyLevel returns the supply level (permille) of faction f in province p
// for the current control map.
func (e *Engine) SupplyLevel(s *State, f FactionID, p ProvinceID) int32 {
	e.updateSupplyLevels(s)
	return e.supplyLvl[int(f)*e.np+int(p)]
}
