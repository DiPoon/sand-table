package match

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/persist"
	"sandtable/server/internal/sim"
)

const (
	azure   sim.FactionID = 1
	crimson sim.FactionID = 2
)

func newMatch(t *testing.T, dir string) *Match {
	t.Helper()
	w, err := gamedata.Load("../../../data", "sample")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(Config{ID: "m", World: w, Seed: 1, Dir: dir, HashInterval: 2, SnapshotInterval: 4,
		Speeds: []time.Duration{time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// run starts the match loop and stops it when the test ends.
func run(t *testing.T, m *Match) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
}

// drain returns what is queued for c. Call it after a synchronous call such
// as Info, which returns only after the events before it were handled.
func drain(c *Client) (msgs []any, closed bool) {
	for {
		select {
		case msg, ok := <-c.Out:
			if !ok {
				return msgs, true
			}
			msgs = append(msgs, msg)
		default:
			return msgs, false
		}
	}
}

func lastStatus(t *testing.T, msgs []any) Status {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		if st, ok := msgs[i].(Status); ok {
			return st
		}
	}
	t.Fatal("no status message")
	return Status{}
}

func TestDroppedClientLeavesNoStaleStatus(t *testing.T) {
	m := newMatch(t, "")
	run(t, m)
	// Room for Welcome, snapshot and the first status, and no more.
	a := &Client{Name: "a", Out: make(chan any, 3)}
	m.Join(a, "azure", "")
	m.Info()
	b := NewClient("b")
	m.Join(b, "crimson", "") // the status broadcast drops a, the host, half way through
	m.Info()

	msgs, _ := drain(b)
	if st := lastStatus(t, msgs); st.Host != crimson || len(st.Players) != 1 {
		t.Fatalf("crimson's last status: host %d, %d players; want host %d alone", st.Host, len(st.Players), crimson)
	}
	if _, closed := drain(a); !closed {
		t.Fatal("the slow client was not dropped")
	}
	if e, ok := a.DropError(); !ok || e.Code != ErrSlowConsumer || !e.Fatal {
		t.Fatalf("drop error of the slow client: %+v %v", e, ok)
	}
	if e, ok := b.DropError(); ok {
		t.Fatalf("crimson has a drop error: %+v", e)
	}
}

func TestReconnectWithTokenKeepsHost(t *testing.T) {
	m := newMatch(t, "")
	run(t, m)
	a := NewClient("a")
	m.Join(a, "azure", "")
	b := NewClient("b")
	m.Join(b, "crimson", "")
	m.Info()
	msgs, _ := drain(a)
	w, ok := msgs[0].(Welcome)
	if !ok {
		t.Fatalf("first message to azure is %T", msgs[0])
	}

	// Azure reconnects while the server still holds its old connection.
	a2 := NewClient("a")
	m.Join(a2, "azure", w.Token)
	m.Info()
	if _, closed := drain(a); !closed {
		t.Fatal("the old connection was not replaced")
	}
	msgs, _ = drain(b)
	if st := lastStatus(t, msgs); st.Host != azure || len(st.Players) != 2 {
		t.Fatalf("after azure reconnected: host %d, %d players", st.Host, len(st.Players))
	}
}

func TestShutdownWritesSnapshotOnHashTick(t *testing.T) {
	dir := t.TempDir()
	m := newMatch(t, dir) // hash every 2 ticks, snapshot every 4
	m.phase = PhaseRunning
	for range 6 { // tick 6 gets a hash but no regular snapshot
		if err := m.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.shutdown(); err != nil {
		t.Fatal(err)
	}

	var newest uint64
	for _, path := range persist.SnapshotFiles(filepath.Join(dir, "snapshots")) {
		sn, _, err := persist.ReadSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		newest = max(newest, sn.Tick)
	}
	if newest != 6 {
		t.Fatalf("newest snapshot is at tick %d, want 6", newest)
	}
	_, recs, _, err := persist.ReadLog(filepath.Join(dir, "commands.log"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if r.Kind == persist.RecHash && r.Tick == 6 {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d hash records for tick 6, want 1", n)
	}
}

// Cutting the torn end of a log can cut ticks that a snapshot was taken at.
// Recovery must not start from, or later pick, such a snapshot.
func TestRecoverySetsAsideSnapshotsNewerThanTheLog(t *testing.T) {
	dir := t.TempDir()
	m := newMatch(t, dir) // hash every 2 ticks, snapshot every 4
	m.phase = PhaseRunning
	for range 8 {
		if err := m.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.shutdown(); err != nil { // log: HASH 2, 4, 6, 8; snapshots 4, 8
		t.Fatal(err)
	}
	path := filepath.Join(dir, "commands.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const hashFrame = 4 + 1 + 8 + len(sim.Hash{}) + 4
	data[len(data)-hashFrame-10] ^= 1 // inside HASH 6; HASH 8 after it has nothing after it
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	m2 := newMatch(t, dir)
	defer m2.shutdown()
	if m2.state.Tick != 4 {
		t.Fatalf("recovered at tick %d, want 4", m2.state.Tick)
	}
	snaps := filepath.Join(dir, "snapshots")
	if files := persist.SnapshotFiles(snaps); len(files) != 1 || filepath.Base(files[0]) != "000000000004.snap" {
		t.Fatalf("snapshots in use: %v", files)
	}
	if _, err := os.Stat(filepath.Join(snaps, "000000000008.snap.orphaned")); err != nil {
		t.Fatal(err)
	}
}
