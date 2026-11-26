package persist_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sandtable/server/internal/persist"
	"sandtable/server/internal/sim"
	"sandtable/server/internal/sim/simtest"
)

const hashEvery = 24

// record runs a scripted match for `ticks` ticks, writing the log the way the
// match loop does, and returns the hash after every tick.
func record(t *testing.T, dir string, w *sim.World, ticks int, snapshotAt []uint64) (persist.Header, []sim.Hash) {
	t.Helper()
	h := persist.Header{SimVersion: sim.Version, MatchID: "m", Scenario: w.Scenario, Seed: 42, DataHash: sim.HashWorld(w)}
	l, err := persist.CreateLog(filepath.Join(dir, "commands.log"), h)
	if err != nil {
		t.Fatal(err)
	}
	e := sim.NewEngine(w)
	s := e.NewState(h.Seed)
	sc := simtest.NewScript(w, 7)
	var hashes []sim.Hash
	for i := 0; i < ticks; i++ {
		cmds := sc.Commands(s, 30)
		if len(cmds) > 0 {
			if err := l.AppendCommands(s.Tick+1, cmds); err != nil {
				t.Fatal(err)
			}
		}
		e.Step(s, cmds)
		hash := sim.HashState(s)
		hashes = append(hashes, hash)
		if s.Tick%hashEvery == 0 {
			if err := l.AppendHash(s.Tick, hash); err != nil {
				t.Fatal(err)
			}
		}
		for _, at := range snapshotAt {
			if s.Tick == at {
				sn := &persist.Snapshot{SimVersion: sim.Version, MatchID: h.MatchID, DataHash: h.DataHash,
					Tick: s.Tick, StateHash: hash, State: sim.AppendState(nil, s)}
				if err := persist.WriteSnapshot(filepath.Join(dir, "snapshots"), sn, 3); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return h, hashes
}

func lastTick(recs []persist.Record) uint64 {
	if len(recs) == 0 {
		return 0
	}
	return recs[len(recs)-1].Tick
}

func TestReplayFromStartMatchesLive(t *testing.T) {
	dir := t.TempDir()
	w := simtest.Grid(12, 8, 3, 20)
	_, live := record(t, dir, w, 500, nil)

	h, recs, _, err := persist.ReadLog(filepath.Join(dir, "commands.log"))
	if err != nil {
		t.Fatal(err)
	}
	e := sim.NewEngine(w)
	s := e.NewState(h.Seed)
	if err := persist.Replay(e, s, recs); err != nil {
		t.Fatal(err)
	}
	if s.Tick != lastTick(recs) || sim.HashState(s) != live[s.Tick-1] {
		t.Fatalf("replay ended at tick %d with a different state than the live run", s.Tick)
	}
}

func TestRecoveryFromSnapshotMatchesLive(t *testing.T) {
	dir := t.TempDir()
	w := simtest.Grid(12, 8, 3, 20)
	h, live := record(t, dir, w, 500, []uint64{120, 240, 360, 480})

	snaps := persist.SnapshotFiles(filepath.Join(dir, "snapshots"))
	if len(snaps) != 3 {
		t.Fatalf("kept %d snapshots, want 3", len(snaps))
	}
	// Corrupt the newest snapshot: recovery must fall back to the one before.
	data, _ := os.ReadFile(snaps[2])
	data[len(data)/2] ^= 0xFF
	os.WriteFile(snaps[2], data, 0o644)

	_, recs, _, err := persist.ReadLog(filepath.Join(dir, "commands.log"))
	if err != nil {
		t.Fatal(err)
	}
	s, skipped := persist.LoadLatestSnapshot(filepath.Join(dir, "snapshots"), h, lastTick(recs))
	if s == nil || s.Tick != 360 || len(skipped) != 1 {
		t.Fatalf("want fallback to the tick 360 snapshot, got %v (skipped %v)", s, skipped)
	}
	e := sim.NewEngine(w)
	if err := persist.Replay(e, s, recs); err != nil {
		t.Fatal(err)
	}
	if sim.HashState(s) != live[s.Tick-1] {
		t.Fatal("recovered state differs from the live run")
	}
}

func TestTornTailIsCutOff(t *testing.T) {
	dir := t.TempDir()
	w := simtest.Grid(12, 8, 3, 20)
	record(t, dir, w, 200, nil)
	path := filepath.Join(dir, "commands.log")
	_, full, fullLen, _ := persist.ReadLog(path)

	// Simulate a crash in the middle of writing the last record.
	if err := os.Truncate(path, fullLen-5); err != nil {
		t.Fatal(err)
	}
	l, _, recs, err := persist.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(full)-1 {
		t.Fatalf("read %d records, want %d", len(recs), len(full)-1)
	}
	// Appending after the cut must give a clean log again.
	if err := l.AppendHash(9999, sim.Hash{1}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	_, again, _, _ := persist.ReadLog(path)
	if len(again) != len(recs)+1 || again[len(again)-1].Tick != 9999 {
		t.Fatalf("log not clean after reopening: %d records", len(again))
	}
}

func TestReplayDetectsDivergence(t *testing.T) {
	dir := t.TempDir()
	w := simtest.Grid(12, 8, 3, 20)
	h, _ := record(t, dir, w, 300, nil)
	_, recs, _, _ := persist.ReadLog(filepath.Join(dir, "commands.log"))

	// A different seed stands in for any source of nondeterminism.
	e := sim.NewEngine(w)
	s := e.NewState(h.Seed + 1)
	var mm *persist.MismatchError
	if err := persist.Replay(e, s, recs); !errors.As(err, &mm) {
		t.Fatalf("want a hash mismatch, got %v", err)
	}
}
