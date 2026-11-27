// Command sandtable-mapgen generates a fictional map and scenario into the
// data directory. See docs/land-war-plan.md section 4.
package main

import (
	"flag"
	"fmt"
	"os"

	"sandtable/server/internal/mapgen"
)

func main() {
	var o mapgen.Options
	flag.Uint64Var(&o.Seed, "seed", 1, "seed: the same seed gives the same map")
	flag.StringVar(&o.Name, "name", "generated", "file name of the map and the scenario")
	flag.IntVar(&o.Land, "land", 800, "number of land provinces, about")
	flag.IntVar(&o.Factions, "factions", 2, "number of factions")
	flag.Float64Var(&o.Spacing, "spacing", 40, "km between neighbouring province centres")
	dir := flag.String("data", "../data", "data directory")
	flag.Parse()

	m, err := mapgen.Generate(o)
	if err == nil {
		err = m.Write(*dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	land, rivers, rails, hubs := 0, 0, 0, 0
	for _, p := range m.Provinces {
		if p.Land {
			land++
		}
		if p.Hub {
			hubs++
		}
	}
	for _, e := range m.Edges {
		if e.River > 0 {
			rivers++
		}
		if e.Rail > 0 {
			rails++
		}
	}
	fmt.Printf("%s: %d provinces (%d land), %d edges, %d states, %d factions, %d river borders, %d railway edges, %d hubs, %d divisions; %.0f×%.0f km\n",
		m.Opt.Name, len(m.Provinces), land, len(m.Edges), len(m.States), len(m.Factions), rivers, rails, hubs, len(m.Divisions), m.W, m.H)
}
