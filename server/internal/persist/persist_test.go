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

func TestCorruptionBeforeTheEndIsRefused(t *testing.T) {
	dir := t.TempDir()
	w := simtest.Grid(12, 8, 3, 20)
	record(t, dir, w, 200, nil)
	path := filepath.Join(dir, "commands.log")
	clean, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// One flipped byte anywhere in the first half: a length, a payload or a checksum.
	for at := len(clean) / 8; at < len(clean)/2; at += len(clean) / 97 {
		data := append([]byte(nil), clean...)
		data[at] ^= 0x40
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := persist.OpenLog(path); err == nil {
			t.Fatalf("byte %d flipped: the log was opened", at)
		}
		if st, _ := os.Stat(path); st.Size() != int64(len(clean)) {
			t.Fatalf("byte %d flipped: the log was cut to %d bytes", at, st.Size())
		}
		if _, err := persist.VerifyDir(w, dir); err == nil {
			t.Fatalf("byte %d flipped: VerifyDir found nothing", at)
		}
	}
}

func TestDamagedEndIsCutOff(t *testing.T) {
	dir := t.TempDir()
	w := simtest.Grid(12, 8, 3, 20)
	record(t, dir, w, 200, nil) // records after the last hash (tick 192) were never synced
	path := filepath.Join(dir, "commands.log")
	clean, _ := os.ReadFile(path)
	_, full, _, _ := persist.ReadLog(path)

	for _, tc := range []struct {
		name string
		data []byte
		recs int
	}{
		{"last record damaged", func() []byte { d := append([]byte(nil), clean...); d[len(d)-6] ^= 1; return d }(), len(full) - 1},
		{"zeros the file system never filled", append(append([]byte(nil), clean...), make([]byte, 4096)...), len(full)},
	} {
		if err := os.WriteFile(path, tc.data, 0o644); err != nil {
			t.Fatal(err)
		}
		l, _, recs, err := persist.OpenLog(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		l.Close()
		if len(recs) != tc.recs || l.TornBytes == 0 {
			t.Fatalf("%s: %d records, %d bytes cut; want %d records", tc.name, len(recs), l.TornBytes, tc.recs)
		}
	}
}

func TestRecordsOutOfOrderAreRejected(t *testing.T) {
	w := simtest.Grid(12, 8, 3, 20)
	h := persist.Header{SimVersion: sim.Version, MatchID: "m", Scenario: w.Scenario, Seed: 42, DataHash: sim.HashWorld(w)}
	type rec struct {
		kind uint8
		tick uint64
	}
	for _, tc := range []struct {
		name string
		recs []rec
		ok   bool
	}{
		{"in order", []rec{{persist.RecCommands, 40}, {persist.RecCommands, 50}}, true},
		{"tick goes back", []rec{{persist.RecCommands, 50}, {persist.RecCommands, 40}}, false},
		{"two command records for one tick", []rec{{persist.RecCommands, 50}, {persist.RecCommands, 50}}, false},
		{"commands after the hash of their tick", []rec{{persist.RecHash, 24}, {persist.RecCommands, 24}}, false},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "commands.log")
		l, err := persist.CreateLog(path, h)
		if err != nil {
			t.Fatal(err)
		}
		// Hash records carry the true hash, so that only the order can be wrong.
		e := sim.NewEngine(w)
		s := e.NewState(h.Seed)
		for _, r := range tc.recs {
			if r.kind == persist.RecCommands {
				l.AppendCommands(r.tick, nil)
				continue
			}
			for s.Tick < r.tick {
				e.Step(s, nil)
			}
			l.AppendHash(r.tick, sim.HashState(s))
		}
		l.Close()
		_, recs, _, _ := persist.ReadLog(path)
		_, verr := persist.VerifyDir(w, dir)
		rerr := persist.Replay(sim.NewEngine(w), sim.NewEngine(w).NewState(h.Seed), recs)
		if tc.ok && (verr != nil || rerr != nil) {
			t.Fatalf("%s: VerifyDir %v, Replay %v", tc.name, verr, rerr)
		}
		if !tc.ok && (verr == nil || rerr == nil) {
			t.Fatalf("%s: VerifyDir %v, Replay %v; want both to fail", tc.name, verr, rerr)
		}
	}
}

// A crash while the last HASH was being synced can leave that HASH on disk
// but not the records before it. That is a torn end, not corruption.
func TestDamageBeforeAnUnsyncedLastHashIsCutOff(t *testing.T) {
	dir := t.TempDir()
	w := simtest.Grid(12, 8, 3, 20)
	record(t, dir, w, 192, nil) // ends with the HASH of tick 192
	path := filepath.Join(dir, "commands.log")
	_, full, _, _ := persist.ReadLog(path)
	n := len(full)
	if full[n-1].Kind != persist.RecHash || full[n-2].Kind != persist.RecCommands {
		t.Fatalf("the log does not end with COMMANDS, HASH")
	}
	data, _ := os.ReadFile(path)
	const hashFrame = 4 + 1 + 8 + len(sim.Hash{}) + 4
	data[len(data)-hashFrame-6] ^= 1 // inside the COMMANDS record before the last HASH
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	l, _, recs, err := persist.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if len(recs) != n-2 || l.TornBytes == 0 {
		t.Fatalf("%d records, %d bytes cut; want %d records", len(recs), l.TornBytes, n-2)
	}
}

func TestSnapshotsNewerThanTheLogAreSetAside(t *testing.T) {
	dir := t.TempDir()
	write := func(tick uint64) {
		sn := &persist.Snapshot{Tick: tick, State: []byte{1}}
		if err := persist.WriteSnapshot(dir, sn, 3); err != nil {
			t.Fatal(err)
		}
	}
	write(4)
	write(8)
	moved, err := persist.SetAsideSnapshotsAfter(dir, 5)
	if err != nil || len(moved) != 1 || filepath.Base(moved[0]) != "000000000008.snap.orphaned" {
		t.Fatalf("moved %v, %v", moved, err)
	}
	files := persist.SnapshotFiles(dir)
	if len(files) != 1 || filepath.Base(files[0]) != "000000000004.snap" {
		t.Fatalf("snapshots left: %v", files)
	}
}
