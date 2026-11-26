// Command sandtable-replay re-simulates a match from its command log and
// checks every recorded hash and snapshot. It exits non-zero on a mismatch.
package main

import (
	"flag"
	"fmt"
	"os"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/persist"
)

func main() {
	dataDir := flag.String("data", "../data", "data directory")
	dir := flag.String("dir", "var/default", "match directory (contains commands.log)")
	flag.Parse()

	h, _, _, err := persist.ReadLog(*dir + "/commands.log")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	world, err := gamedata.Load(*dataDir, h.Scenario)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	rep, err := persist.VerifyDir(world, *dir)
	fmt.Printf("match %q, scenario %q, seed %d\n", rep.Header.MatchID, rep.Header.Scenario, rep.Header.Seed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAILED:", err)
		os.Exit(1)
	}
	fmt.Printf("OK: %d records replayed to tick %d, %d hashes and %d snapshots verified, final hash %x\n",
		rep.Records, rep.LastTick, rep.Hashes, rep.Snapshots, rep.FinalHash[:8])
}
