// Package mapgen generates a fictional map and a two-sided (or more)
// scenario for it: land and sea, terrain, rivers, states, factions,
// victory points, industry, supply hubs and railways, and the first
// divisions. The result is written as the YAML files under data/ that the
// server loads. See docs/land-war-plan.md section 4.
//
// The generator runs outside the simulation and uses floating point. The
// same options and the same build give the same files; matches are
// deterministic through the files they load, not through the generator.
package mapgen

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"

	"github.com/fogleman/delaunay"
)

type Options struct {
	Seed     uint64
	Name     string  // file name under data/maps and data/scenarios
	Land     int     // target number of land provinces
	Factions int     // number of factions
	Spacing  float64 // km between neighbouring province centres
}

func (o *Options) defaults() {
	if o.Name == "" {
		o.Name = "generated"
	}
	if o.Land == 0 {
		o.Land = 800
	}
	if o.Factions == 0 {
		o.Factions = 2
	}
	if o.Spacing == 0 {
		o.Spacing = 40
	}
}

// Generator tuning. Terrain shares are fractions of the land provinces.
const (
	landShare     = 0.55 // land provinces among all, which sets the map size
	minBorderKm   = 1.0  // shorter shared borders are corners, not adjacency
	mountainShare = 0.07
	hillShare     = 0.15
	forestShare   = 0.32
	marshShare    = 0.05
	stateSize     = 9   // provinces per state, on average
	minStateSize  = 3   // smaller states are merged into a neighbour
	hubReach      = 200 // km plains-equivalent from the nearest supply hub (supply fades out at 240)
	divisionsEach = 24  // infantry per faction on the border
	armorEach     = 6   // armour per faction around the capital
)

type Province struct {
	Key, Name, Terrain string
	X, Y               int32
	Land               bool
	elev, moist        float64
	State              int // index into Map.States, -1 for water
	VP                 int32
	Hub                bool
}

// Edge joins two adjacent land provinces, A < B.
type Edge struct {
	A, B  int
	Dist  int // km
	River int // 0 none, 1 small, 2 large
	Rail  int // railway level, 0 none
}

type State struct {
	Key, Name      string
	Capital        int
	Provinces      []int
	Manpower       int64
	Factories      int32
	Infrastructure int32
	Faction        int
}

type Faction struct {
	Key, Name string
	Capital   int
	States    []int
}

type Division struct {
	Faction    int
	Type, Name string
	At         int
}

type Map struct {
	Opt       Options
	W, H      float64
	Provinces []Province
	Edges     []Edge
	States    []State
	Factions  []Faction
	Divisions []Division

	rng   *rand.Rand
	near  [][]int // all provinces whose cells share a border, land or not
	adj   [][]int // indices into Edges, per province
	tri   *delaunay.Triangulation
	lakes [][]int // provinces of each lake
}

// Generate makes a map. The same options always give the same map.
func Generate(opt Options) (*Map, error) {
	opt.defaults()
	if opt.Land < 50 || opt.Factions < 2 || opt.Factions > 8 || opt.Spacing < 5 {
		return nil, errors.New("mapgen: need at least 50 land provinces, 2 to 8 factions and a spacing of 5 km or more")
	}
	m := &Map{Opt: opt, rng: rand.New(rand.NewPCG(opt.Seed, 0x73616e64))}
	m.place()
	if err := m.triangulate(); err != nil {
		return nil, err
	}
	m.classify()
	m.terrain()
	m.buildEdges()
	m.rivers()
	m.states()
	if err := m.factions(); err != nil {
		return nil, err
	}
	m.economy()
	m.supply()
	m.names()
	m.divisions()
	return m, nil
}

// ---- Points and height ----

// place scatters province centres with Poisson disk sampling and gives each
// an elevation (a continent in the middle, sea along the edges) and moisture.
func (m *Map) place() {
	r := m.Opt.Spacing
	total := float64(m.Opt.Land) / landShare
	area := total * r * r / 0.7 // Poisson disk sampling fills about 0.7 points per r²
	m.W = math.Round(math.Sqrt(area * 4 / 3))
	m.H = math.Round(m.W * 3 / 4)

	pts := poisson(m.rng, m.W, m.H, r)
	height := newNoise(m.rng)
	wet := newNoise(m.rng)
	band := 1.2 * r
	for _, p := range pts {
		x, y := math.Round(p[0]), math.Round(p[1])
		nx, ny := x/m.W*2-1, y/m.H*2-1
		e := 1 - (nx*nx+ny*ny)*0.9 + 0.55*height.fbm(x/(m.W/3), y/(m.H/3), 5)
		if x < band || y < band || x > m.W-band || y > m.H-band {
			e = -10 // the edge of the map is always sea
		}
		moist := wet.fbm(x/(m.W/4)+17.3, y/(m.H/4)-5.1, 4)
		m.Provinces = append(m.Provinces, Province{X: int32(x), Y: int32(y), elev: e, moist: moist, State: -1})
	}
}

// poisson returns points at least r apart covering a w×h rectangle (Bridson's algorithm).
func poisson(rng *rand.Rand, w, h, r float64) [][2]float64 {
	cell := r / math.Sqrt2
	gw, gh := int(math.Ceil(w/cell)), int(math.Ceil(h/cell))
	grid := make([]int, gw*gh)
	for i := range grid {
		grid[i] = -1
	}
	var pts [][2]float64
	var active []int
	add := func(x, y float64) {
		grid[int(y/cell)*gw+int(x/cell)] = len(pts)
		active = append(active, len(pts))
		pts = append(pts, [2]float64{x, y})
	}
	add(w/2, h/2)
	for len(active) > 0 {
		k := rng.IntN(len(active))
		c := pts[active[k]]
		found := false
		for try := 0; try < 30; try++ {
			a := rng.Float64() * 2 * math.Pi
			d := r * (1 + rng.Float64())
			x, y := c[0]+d*math.Cos(a), c[1]+d*math.Sin(a)
			if x < 0 || y < 0 || x >= w || y >= h {
				continue
			}
			gx, gy := int(x/cell), int(y/cell)
			ok := true
			for yy := max(0, gy-2); yy <= min(gh-1, gy+2) && ok; yy++ {
				for xx := max(0, gx-2); xx <= min(gw-1, gx+2); xx++ {
					if j := grid[yy*gw+xx]; j >= 0 && math.Hypot(pts[j][0]-x, pts[j][1]-y) < r {
						ok = false
						break
					}
				}
			}
			if ok {
				add(x, y)
				found = true
				break
			}
		}
		if !found {
			active = slices.Delete(active, k, k+1)
		}
	}
	return pts
}

// ---- Adjacency ----

func nextHalfedge(e int) int {
	if e%3 == 2 {
		return e - 2
	}
	return e + 1
}

func (m *Map) circumcenter(t int) (float64, float64) {
	p := m.tri.Points
	a, b, c := p[m.tri.Triangles[3*t]], p[m.tri.Triangles[3*t+1]], p[m.tri.Triangles[3*t+2]]
	d := 2 * (a.X*(b.Y-c.Y) + b.X*(c.Y-a.Y) + c.X*(a.Y-b.Y))
	a2, b2, c2 := a.X*a.X+a.Y*a.Y, b.X*b.X+b.Y*b.Y, c.X*c.X+c.Y*c.Y
	return (a2*(b.Y-c.Y) + b2*(c.Y-a.Y) + c2*(a.Y-b.Y)) / d, (a2*(c.X-b.X) + b2*(a.X-c.X) + c2*(b.X-a.X)) / d
}

// triangulate finds the cells that share a border, from the same integer
// coordinates the client draws its Voronoi cells from. Two cells are
// neighbours when the Voronoi edge between them is at least minBorderKm
// long, so that a pair the client draws as touching only at a corner is
// not adjacent, whatever triangulation either side picked.
func (m *Map) triangulate() error {
	pts := make([]delaunay.Point, len(m.Provinces))
	for i, p := range m.Provinces {
		pts[i] = delaunay.Point{X: float64(p.X), Y: float64(p.Y)}
	}
	tri, err := delaunay.Triangulate(pts)
	if err != nil {
		return fmt.Errorf("mapgen: triangulation: %w", err)
	}
	m.tri = tri
	m.near = make([][]int, len(m.Provinces))
	for e, o := range tri.Halfedges {
		if o < e { // each interior edge once; hull edges (o = -1) belong to the sea at the edge
			continue
		}
		ax, ay := m.circumcenter(e / 3)
		bx, by := m.circumcenter(o / 3)
		if math.Hypot(ax-bx, ay-by) < minBorderKm {
			continue
		}
		a, b := tri.Triangles[e], tri.Triangles[nextHalfedge(e)]
		m.near[a] = append(m.near[a], b)
		m.near[b] = append(m.near[b], a)
	}
	for i := range m.near {
		sort.Ints(m.near[i])
	}
	return nil
}

// components returns the connected groups of provinces for which in is true.
func (m *Map) components(in func(int) bool) [][]int {
	seen := make([]bool, len(m.Provinces))
	var out [][]int
	for s := range m.Provinces {
		if seen[s] || !in(s) {
			continue
		}
		comp := []int{s}
		seen[s] = true
		for q := 0; q < len(comp); q++ {
			for _, n := range m.near[comp[q]] {
				if !seen[n] && in(n) {
					seen[n] = true
					comp = append(comp, n)
				}
			}
		}
		out = append(out, comp)
	}
	return out
}

// classify makes the highest Land provinces land, keeps the largest piece
// of land, and turns enclosed water into lakes.
func (m *Map) classify() {
	order := make([]int, len(m.Provinces))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return m.Provinces[order[a]].elev > m.Provinces[order[b]].elev })
	for _, i := range order[:min(m.Opt.Land, len(order))] {
		if m.Provinces[i].elev > -10 {
			m.Provinces[i].Land = true
		}
	}
	land := m.components(func(i int) bool { return m.Provinces[i].Land })
	sort.SliceStable(land, func(a, b int) bool { return len(land[a]) > len(land[b]) })
	for _, comp := range land[1:] {
		for _, i := range comp {
			m.Provinces[i].Land = false // islands would be unreachable
		}
	}
	for _, comp := range m.components(func(i int) bool { return !m.Provinces[i].Land }) {
		ocean := false
		for _, i := range comp {
			if m.Provinces[i].elev <= -10 {
				ocean = true
				break
			}
		}
		terrain := "sea"
		if !ocean {
			terrain = "lake"
			m.lakes = append(m.lakes, comp)
		}
		for _, i := range comp {
			m.Provinces[i].Terrain = terrain
		}
	}
}

// ---- Terrain and rivers ----

func (m *Map) landIndices() []int {
	var out []int
	for i, p := range m.Provinces {
		if p.Land {
			out = append(out, i)
		}
	}
	return out
}

// terrain gives the highest land mountains and hills, the wettest lowland
// marsh and forest, and the rest plains. Cities come later with the states.
func (m *Map) terrain() {
	land := m.landIndices()
	byElev := slices.Clone(land)
	sort.SliceStable(byElev, func(a, b int) bool { return m.Provinces[byElev[a]].elev > m.Provinces[byElev[b]].elev })
	n := len(land)
	nMount, nHill := int(mountainShare*float64(n)), int(hillShare*float64(n))
	for k, i := range byElev {
		switch {
		case k < nMount:
			m.Provinces[i].Terrain = "mountain"
		case k < nMount+nHill:
			m.Provinces[i].Terrain = "hills"
		}
	}
	low := map[int]bool{}
	for _, i := range byElev[int(0.6*float64(n)):] {
		low[i] = true
	}
	var rest []int
	for _, i := range land {
		if m.Provinces[i].Terrain == "" {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool { return m.Provinces[rest[a]].moist > m.Provinces[rest[b]].moist })
	nMarsh, nForest := int(marshShare*float64(n)), int(forestShare*float64(n))
	for _, i := range rest {
		switch {
		case nMarsh > 0 && low[i]:
			m.Provinces[i].Terrain = "marsh"
			nMarsh--
		case nForest > 0:
			m.Provinces[i].Terrain = "forest"
			nForest--
		default:
			m.Provinces[i].Terrain = "plains"
		}
	}
}

// buildEdges turns the borders between land provinces into edges.
func (m *Map) buildEdges() {
	m.adj = make([][]int, len(m.Provinces))
	for a := range m.Provinces {
		for _, b := range m.near[a] {
			if b > a && m.Provinces[a].Land && m.Provinces[b].Land {
				pa, pb := m.Provinces[a], m.Provinces[b]
				d := int(math.Round(math.Hypot(float64(pa.X-pb.X), float64(pa.Y-pb.Y))))
				m.adj[a] = append(m.adj[a], len(m.Edges))
				m.adj[b] = append(m.adj[b], len(m.Edges))
				m.Edges = append(m.Edges, Edge{A: a, B: b, Dist: max(1, d)})
			}
		}
	}
}

func (m *Map) edgeBetween(a, b int) int {
	for _, e := range m.adj[a] {
		if m.Edges[e].A == b || m.Edges[e].B == b {
			return e
		}
	}
	return -1
}

func (m *Map) other(e, p int) int {
	if m.Edges[e].A == p {
		return m.Edges[e].B
	}
	return m.Edges[e].A
}

// rivers run downhill along cell borders from high ground to water. A
// Voronoi vertex is a triangle; stepping to a neighbouring triangle follows
// the border of the two provinces of the shared triangle edge.
func (m *Map) rivers() {
	t := m.tri
	nt := len(t.Triangles) / 3
	elev := make([]float64, nt)
	wet := make([]bool, nt) // touches water
	allLand := make([]bool, nt)
	for i := 0; i < nt; i++ {
		allLand[i] = true
		for k := 0; k < 3; k++ {
			p := &m.Provinces[t.Triangles[3*i+k]]
			elev[i] += p.elev / 3
			if !p.Land {
				wet[i] = true
				allLand[i] = false
			}
		}
		elev[i] += m.rng.Float64() * 1e-9 // no ties
	}
	var sources []int
	for i := 0; i < nt; i++ {
		if allLand[i] {
			sources = append(sources, i)
		}
	}
	sort.SliceStable(sources, func(a, b int) bool { return elev[sources[a]] > elev[sources[b]] })
	sources = sources[:len(sources)/4] // the highest quarter
	m.rng.Shuffle(len(sources), func(a, b int) { sources[a], sources[b] = sources[b], sources[a] })

	flow := map[int]int{} // edge index → rivers through it
	used := map[int]bool{}
	want := max(3, m.Opt.Land/45)
	for _, s := range sources {
		if want == 0 {
			break
		}
		if used[s] {
			continue
		}
		var path []int // edges along the way
		cur := s
		ok := false
		for step := 0; step < 400; step++ {
			best, bestHalf := -1, -1
			for k := 0; k < 3; k++ {
				h := 3*cur + k
				o := t.Halfedges[h]
				if o >= 0 && (best < 0 || elev[o/3] < elev[best]) {
					best, bestHalf = o/3, h
				}
			}
			if best < 0 || elev[best] >= elev[cur] {
				break // a sink: no way down
			}
			a, b := t.Triangles[bestHalf], t.Triangles[nextHalfedge(bestHalf)]
			if e := m.edgeBetween(a, b); e >= 0 {
				path = append(path, e)
			}
			if wet[best] {
				ok = true
				break
			}
			cur = best
		}
		if !ok || len(path) < 4 {
			continue
		}
		want--
		for _, e := range path {
			flow[e]++
		}
		used[s] = true
	}
	for e, f := range flow {
		if f >= 3 {
			m.Edges[e].River = 2
		} else {
			m.Edges[e].River = 1
		}
	}
}

// ---- States and factions ----

// dijkstra runs from several sources over land edges, with the cost of an
// edge given by cost (or the distance if cost is nil) and only through
// provinces where in is true. It returns the distance and the source each
// province is closest to (-1 if unreached).
func (m *Map) dijkstra(sources []int, in func(int) bool, cost func(e, to int) float64) ([]float64, []int) {
	dist := make([]float64, len(m.Provinces))
	from := make([]int, len(m.Provinces))
	for i := range dist {
		dist[i], from[i] = math.Inf(1), -1
	}
	pq := &pqueue{}
	for k, s := range sources {
		dist[s], from[s] = 0, k
		heap.Push(pq, pqItem{s, 0})
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if it.d > dist[it.p] {
			continue
		}
		for _, e := range m.adj[it.p] {
			n := m.other(e, it.p)
			if in != nil && !in(n) {
				continue
			}
			c := float64(m.Edges[e].Dist)
			if cost != nil {
				c = cost(e, n)
			}
			if nd := it.d + c; nd < dist[n] {
				dist[n], from[n] = nd, from[it.p]
				heap.Push(pq, pqItem{n, nd})
			}
		}
	}
	return dist, from
}

type pqItem struct {
	p int
	d float64
}
type pqueue []pqItem

func (q pqueue) Len() int { return len(q) }
func (q pqueue) Less(i, j int) bool {
	if q[i].d != q[j].d {
		return q[i].d < q[j].d
	}
	return q[i].p < q[j].p
}
func (q pqueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *pqueue) Push(x any)   { *q = append(*q, x.(pqItem)) }
func (q *pqueue) Pop() any {
	old := *q
	it := old[len(old)-1]
	*q = old[:len(old)-1]
	return it
}

// states grows regions of about stateSize provinces from far-apart seeds.
func (m *Map) states() {
	land := m.landIndices()
	n := max(2*m.Opt.Factions, int(math.Round(float64(len(land))/stateSize)))
	seeds := []int{land[m.rng.IntN(len(land))]}
	minD := make([]float64, len(m.Provinces))
	for i := range minD {
		minD[i] = math.Inf(1)
	}
	for len(seeds) < n {
		last := m.Provinces[seeds[len(seeds)-1]]
		best := -1
		for _, i := range land {
			p := m.Provinces[i]
			minD[i] = math.Min(minD[i], math.Hypot(float64(p.X-last.X), float64(p.Y-last.Y)))
			if best < 0 || minD[i] > minD[best] {
				best = i
			}
		}
		seeds = append(seeds, best)
	}
	_, owner := m.dijkstra(seeds, func(i int) bool { return m.Provinces[i].Land }, nil)

	members := make([][]int, n)
	for _, i := range land {
		members[owner[i]] = append(members[owner[i]], i)
	}
	// Merge states that came out too small into their smallest neighbour.
	for changed := true; changed; {
		changed = false
		for s := range members {
			if len(members[s]) == 0 || len(members[s]) >= minStateSize {
				continue
			}
			best := -1
			for _, i := range members[s] {
				for _, e := range m.adj[i] {
					o := owner[m.other(e, i)]
					if o != s && (best < 0 || len(members[o]) < len(members[best])) {
						best = o
					}
				}
			}
			if best < 0 {
				continue
			}
			for _, i := range members[s] {
				owner[i] = best
			}
			members[best] = append(members[best], members[s]...)
			members[s] = nil
			changed = true
		}
	}
	for _, mem := range members {
		if len(mem) == 0 {
			continue
		}
		sort.Ints(mem)
		var cx, cy float64
		for _, i := range mem {
			cx += float64(m.Provinces[i].X)
			cy += float64(m.Provinces[i].Y)
		}
		cx, cy = cx/float64(len(mem)), cy/float64(len(mem))
		capital := mem[0]
		for _, i := range mem {
			p, c := m.Provinces[i], m.Provinces[capital]
			if math.Hypot(float64(p.X)-cx, float64(p.Y)-cy) < math.Hypot(float64(c.X)-cx, float64(c.Y)-cy) {
				capital = i
			}
		}
		id := len(m.States)
		for _, i := range mem {
			m.Provinces[i].State = id
		}
		m.States = append(m.States, State{Capital: capital, Provinces: mem})
	}
}

// stateGraph returns, for each state, the neighbouring states and the
// distance between the two capitals.
func (m *Map) stateGraph() [][][2]float64 {
	g := make([][][2]float64, len(m.States))
	seen := map[[2]int]bool{}
	for _, e := range m.Edges {
		a, b := m.Provinces[e.A].State, m.Provinces[e.B].State
		if a == b || seen[[2]int{a, b}] {
			continue
		}
		seen[[2]int{a, b}], seen[[2]int{b, a}] = true, true
		ca, cb := m.Provinces[m.States[a].Capital], m.Provinces[m.States[b].Capital]
		d := math.Hypot(float64(ca.X-cb.X), float64(ca.Y-cb.Y))
		g[a] = append(g[a], [2]float64{float64(b), d})
		g[b] = append(g[b], [2]float64{float64(a), d})
	}
	return g
}

func stateDistances(g [][][2]float64, sources []int) ([]float64, []int) {
	dist := make([]float64, len(g))
	from := make([]int, len(g))
	for i := range dist {
		dist[i], from[i] = math.Inf(1), -1
	}
	pq := &pqueue{}
	for k, s := range sources {
		dist[s], from[s] = 0, k
		heap.Push(pq, pqItem{s, 0})
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if it.d > dist[it.p] {
			continue
		}
		for _, nb := range g[it.p] {
			n := int(nb[0])
			if nd := it.d + nb[1]; nd < dist[n] {
				dist[n], from[n] = nd, from[it.p]
				heap.Push(pq, pqItem{n, nd})
			}
		}
	}
	return dist, from
}

// factions places capitals in states far apart and gives every state to the
// nearest capital over the state graph, so that each faction is one piece.
func (m *Map) factions() error {
	g := m.stateGraph()
	cx, cy := m.W/2, m.H/2
	first := 0
	for s := range m.States {
		p, f := m.Provinces[m.States[s].Capital], m.Provinces[m.States[first].Capital]
		if math.Hypot(float64(p.X)-cx, float64(p.Y)-cy) > math.Hypot(float64(f.X)-cx, float64(f.Y)-cy) {
			first = s
		}
	}
	caps := []int{first}
	for len(caps) < m.Opt.Factions {
		dist, _ := stateDistances(g, caps)
		best := -1
		for s := range dist {
			if !math.IsInf(dist[s], 1) && (best < 0 || dist[s] > dist[best]) {
				best = s
			}
		}
		if best < 0 || slices.Contains(caps, best) {
			return errors.New("mapgen: not enough states for the factions")
		}
		caps = append(caps, best)
	}
	if len(caps) == 2 {
		// Move the first capital away from the second, so the two are about as far apart as the map allows.
		dist, _ := stateDistances(g, caps[1:])
		for s := range dist {
			if dist[s] > dist[caps[0]] {
				caps[0] = s
			}
		}
	}
	_, owner := stateDistances(g, caps)
	for _, s := range caps {
		m.Factions = append(m.Factions, Faction{Capital: m.States[s].Capital})
	}
	for s := range m.States {
		f := owner[s]
		m.States[s].Faction = f
		m.Factions[f].States = append(m.Factions[f].States, s)
	}
	return nil
}

// ---- Industry, victory points, supply ----

func (m *Map) economy() {
	capitalOf := map[int]int{} // state → faction whose capital it holds
	for f, fac := range m.Factions {
		capitalOf[m.Provinces[fac.Capital].State] = f
	}
	for s := range m.States {
		st := &m.States[s]
		_, isCapital := capitalOf[s]
		urban := isCapital || m.rng.Float64() < 0.35
		capP := &m.Provinces[st.Capital]
		if urban {
			capP.Terrain = "urban"
			capP.VP = int32(3 + m.rng.IntN(4))
			st.Factories = int32(2 + m.rng.IntN(3))
			st.Infrastructure = 4
		} else {
			capP.VP = int32(1 + m.rng.IntN(2))
			st.Factories = int32(m.rng.IntN(2))
			st.Infrastructure = int32(2 + m.rng.IntN(2))
		}
		rough := 0
		for _, i := range st.Provinces {
			switch m.Provinces[i].Terrain {
			case "mountain", "hills", "marsh":
				rough++
			}
			pop := int64(40_000 + m.rng.IntN(40_000))
			if m.Provinces[i].Terrain == "urban" {
				pop = 300_000
			}
			st.Manpower += pop
		}
		if !urban && rough*5 > len(st.Provinces)*2 {
			st.Infrastructure = 1
		}
		if isCapital {
			capP.VP = 30
			st.Factories += 6
		}
	}
}

// moveFactor approximates the movement cost of terrain, per mille, as in data/rules.yaml.
var moveFactor = map[string]float64{"plains": 1, "forest": 1.5, "hills": 1.5, "mountain": 2.5, "urban": 1.2, "marsh": 2}

// supply puts supply hubs where provinces would be far from one, and lays
// railways from each faction's capital to its hubs.
func (m *Map) supply() {
	for f := range m.Factions {
		own := func(i int) bool { return m.Provinces[i].Land && m.States[m.Provinces[i].State].Faction == f }
		cost := func(e, to int) float64 { return float64(m.Edges[e].Dist) * moveFactor[m.Provinces[to].Terrain] }
		hubs := []int{m.Factions[f].Capital}
		for {
			dist, _ := m.dijkstra(hubs, own, cost)
			far := -1
			for i := range m.Provinces {
				if own(i) && (far < 0 || dist[i] > dist[far]) {
					far = i
				}
			}
			if far < 0 || dist[far] <= hubReach {
				break
			}
			hub := m.States[m.Provinces[far].State].Capital
			if slices.Contains(hubs, hub) || dist[hub] < hubReach/2 {
				hub = far
			}
			hubs = append(hubs, hub)
		}
		for _, h := range hubs {
			m.Provinces[h].Hub = true
		}
		// Railways: the shortest route from the capital to every hub, inside the faction.
		capital := m.Factions[f].Capital
		dist, _ := m.dijkstra([]int{capital}, own, nil)
		uses := map[int]int{}
		for _, h := range hubs[1:] {
			for p := h; p != capital; {
				next := -1
				for _, e := range m.adj[p] {
					n := m.other(e, p)
					if own(n) && math.Abs(dist[n]+float64(m.Edges[e].Dist)-dist[p]) < 1e-6 && (next < 0 || n < m.other(next, p)) {
						next = e
					}
				}
				if next < 0 {
					break
				}
				uses[next]++
				p = m.other(next, p)
			}
		}
		for e, n := range uses {
			m.Edges[e].Rail = 1
			if n >= 3 {
				m.Edges[e].Rail = 2
			}
		}
	}
}

// ---- Names and divisions ----

func (m *Map) names() {
	nm := newNamer(m.rng)
	width := len(fmt.Sprint(len(m.Provinces)))
	for i := range m.Provinces {
		p := &m.Provinces[i]
		p.Key = fmt.Sprintf("p%0*d", width, i+1)
		if p.Land {
			p.Name = nm.name()
		}
	}
	for _, lake := range m.lakes {
		m.Provinces[slices.Min(lake)].Name = "Lake " + nm.name()
	}
	sw := len(fmt.Sprint(len(m.States)))
	for s := range m.States {
		m.States[s].Key = fmt.Sprintf("s%0*d", sw, s+1)
		m.States[s].Name = nm.name()
	}
	for f := range m.Factions {
		m.Factions[f].Name, m.Factions[f].Key = nm.faction()
	}
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// divisions puts each faction's infantry along its border and its armour
// around its capital, with the unit types of data/units.yaml.
func (m *Map) divisions() {
	for f := range m.Factions {
		var border []int
		for i, p := range m.Provinces {
			if !p.Land || m.States[p.State].Faction != f {
				continue
			}
			for _, e := range m.adj[i] {
				if m.States[m.Provinces[m.other(e, i)].State].Faction != f {
					border = append(border, i)
					break
				}
			}
		}
		n := min(divisionsEach, len(border))
		for k := 0; k < n; k++ {
			at := border[k*len(border)/n]
			m.Divisions = append(m.Divisions, Division{Faction: f, Type: "infantry", Name: ordinal(k+1) + " Infantry", At: at})
		}
		around := []int{m.Factions[f].Capital}
		for _, e := range m.adj[around[0]] {
			if n := m.other(e, around[0]); m.States[m.Provinces[n].State].Faction == f {
				around = append(around, n)
			}
		}
		for k := 0; k < armorEach; k++ {
			m.Divisions = append(m.Divisions, Division{Faction: f, Type: "armor", Name: ordinal(k+1) + " Armored", At: around[k%len(around)]})
		}
	}
}
