package transport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	pb "sandtable/server/internal/pb/sandtable/v1"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/match"
	"sandtable/server/internal/persist"
	"sandtable/server/internal/transport"
	"sandtable/server/internal/view"
)

type player struct {
	t       *testing.T
	conn    *transport.Conn
	token   string
	faction uint32
	static  *pb.StaticData
	view    *view.View
	status  *pb.MatchStatus
	results map[uint32]*pb.CommandResult
	errs    []pb.ErrorCode
	battles map[uint32]bool
}

func dial(t *testing.T, url, faction, token string) *player {
	t.Helper()
	conn, err := transport.Dial(context.Background(), url, &pb.Hello{MatchId: "m", Faction: faction,
		PlayerName: faction, SessionToken: token})
	if err != nil {
		t.Fatal(err)
	}
	return &player{t: t, conn: conn, results: map[uint32]*pb.CommandResult{}, battles: map[uint32]bool{}}
}

// pump receives messages until done() is true.
func (p *player) pump(what string, done func() bool) {
	p.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		m, err := p.conn.Recv(ctx)
		cancel()
		if err != nil {
			p.t.Fatalf("waiting for %s: %v", what, err)
		}
		switch x := m.Msg.(type) {
		case *pb.ServerMessage_Welcome:
			p.token, p.faction, p.static, p.status = x.Welcome.SessionToken, x.Welcome.FactionId, x.Welcome.Static, x.Welcome.Status
		case *pb.ServerMessage_Snapshot:
			p.view = transport.ViewFromPB(x.Snapshot)
		case *pb.ServerMessage_Delta:
			if x.Delta.Tick != p.view.Tick+1 {
				p.t.Fatalf("delta for tick %d after tick %d", x.Delta.Tick, p.view.Tick)
			}
			view.Apply(p.view, transport.DeltaFromPB(x.Delta))
			for i, pv := range p.view.Provinces {
				if pv.InBattle {
					p.battles[uint32(i)] = true
				}
			}
		case *pb.ServerMessage_CommandResult:
			p.results[x.CommandResult.ClientSeq] = x.CommandResult
		case *pb.ServerMessage_MatchStatus:
			p.status = x.MatchStatus
		case *pb.ServerMessage_Error:
			p.errs = append(p.errs, x.Error.Code)
		}
	}
}

func (p *player) send(m *pb.ClientMessage) {
	p.t.Helper()
	if err := p.conn.Send(context.Background(), m); err != nil {
		p.t.Fatal(err)
	}
}

func (p *player) result(seq uint32) *pb.CommandResult {
	p.t.Helper()
	p.pump("command result", func() bool { return p.results[seq] != nil })
	return p.results[seq]
}

func provinceID(st *pb.StaticData, key string) uint32 {
	for i, p := range st.Provinces {
		if p.Key == key {
			return uint32(i)
		}
	}
	panic("no province " + key)
}

func getMatchInfo(t *testing.T, base, id string) (transport.MatchInfo, int) {
	t.Helper()
	resp, err := http.Get(base + "/api/match?id=" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info transport.MatchInfo
	if resp.StatusCode == http.StatusOK {
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("/api/match content type %q", ct)
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			t.Fatal(err)
		}
	}
	return info, resp.StatusCode
}

func TestEndToEnd(t *testing.T) {
	world, err := gamedata.Load("../../../data", "sample")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := match.Config{ID: "m", World: world, Seed: 42, Dir: dir, HashInterval: 24, SnapshotInterval: 120,
		Speeds: []time.Duration{2 * time.Millisecond, time.Millisecond}}
	m, err := match.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- m.Run(ctx) }()
	srv := httptest.NewServer((&transport.Server{Matches: map[string]transport.Hosted{"m": {Match: m, World: world}}}).Handler())
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// Before anyone joins, /api/match lists the free factions.
	info, code := getMatchInfo(t, srv.URL, "m")
	want := transport.MatchInfo{ID: "m", Phase: "LOBBY", Tick: 0, Factions: []transport.FactionInfo{
		{Key: "azure", Name: "Azure League"}, {Key: "crimson", Name: "Crimson Pact"}}}
	if code != http.StatusOK || !reflect.DeepEqual(info, want) {
		t.Fatalf("/api/match before joining: %d %+v", code, info)
	}
	if _, code := getMatchInfo(t, srv.URL, "nope"); code != http.StatusNotFound {
		t.Fatalf("/api/match for an unknown match: %d", code)
	}

	// Join: the first player is the host.
	a := dial(t, url, "azure", "")
	a.pump("snapshot", func() bool { return a.view != nil })
	b := dial(t, url, "crimson", "")
	b.pump("snapshot", func() bool { return b.view != nil })
	if a.status.Phase != pb.MatchPhase_MATCH_PHASE_LOBBY {
		t.Fatalf("new match is in phase %s", a.status.Phase)
	}
	info, _ = getMatchInfo(t, srv.URL, "m")
	want.Factions[0].Taken, want.Factions[0].Player = true, "azure"
	want.Factions[1].Taken, want.Factions[1].Player = true, "crimson"
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("/api/match after joining: %+v", info)
	}

	// Fog: azure sees its 4 divisions and the 3 crimson divisions on the border, not the one in the rear.
	if len(a.view.Divisions) != 7 {
		t.Fatalf("azure sees %d divisions, want 7", len(a.view.Divisions))
	}
	for _, d := range a.view.Divisions {
		if d.ID == 8 {
			t.Fatal("azure sees the crimson division in the rear")
		}
		if !d.Own && (d.Supply != 0 || d.Path != nil) {
			t.Fatal("azure sees private fields of a crimson division")
		}
	}

	// A taken faction cannot be joined without the token.
	c := dial(t, url, "azure", "")
	c.pump("rejection", func() bool { return len(c.errs) > 0 })
	if c.errs[0] != pb.ErrorCode_ERROR_CODE_FACTION_TAKEN {
		t.Fatalf("joining a taken faction: %s", c.errs[0])
	}

	// Lobby rules.
	p10 := provinceID(a.static, "p10")
	a.send(transport.Move(1, 2, p10))
	if r := a.result(1); r.Reason != pb.RejectReason_REJECT_REASON_MATCH_NOT_STARTED {
		t.Fatalf("command in lobby: %s", r.Reason)
	}
	b.send(&pb.ClientMessage{Msg: &pb.ClientMessage_StartMatch{StartMatch: &pb.StartMatch{}}})
	b.pump("NOT_HOST", func() bool { return len(b.errs) > 0 })
	if b.errs[0] != pb.ErrorCode_ERROR_CODE_NOT_HOST {
		t.Fatalf("non-host start: %s", b.errs[0])
	}
	a.send(&pb.ClientMessage{Msg: &pb.ClientMessage_StartMatch{StartMatch: &pb.StartMatch{}}})
	a.pump("running", func() bool { return a.status.Phase == pb.MatchPhase_MATCH_PHASE_RUNNING })
	if info, _ := getMatchInfo(t, srv.URL, "m"); info.Phase != "RUNNING" {
		t.Fatalf("/api/match after the start: phase %s", info.Phase)
	}

	// Commands: an attack, and an order for somebody else's division.
	a.send(transport.Move(2, 2, p10))
	if r := a.result(2); r.Status != pb.CommandStatus_COMMAND_STATUS_ACCEPTED || r.ApplyTick == 0 {
		t.Fatalf("attack order: %v", r)
	}
	a.send(transport.Move(3, 5, p10))
	if r := a.result(3); r.Reason != pb.RejectReason_REJECT_REASON_NOT_YOUR_DIVISION {
		t.Fatalf("order for a foreign division: %s", r.Reason)
	}
	b.send(transport.Move(1, 8, provinceID(b.static, "p14")))
	if r := b.result(1); r.Status != pb.CommandStatus_COMMAND_STATUS_ACCEPTED {
		t.Fatalf("crimson move: %v", r)
	}

	// Both sides watch the battle for p10 through their own deltas.
	a.pump("battle seen by azure", func() bool { return a.battles[p10] })
	b.pump("battle seen by crimson", func() bool { return b.battles[p10] })
	a.pump("tick 300", func() bool { return a.view.Tick >= 300 })

	// Pause, then check delta sync: a fresh snapshot must equal the view built from deltas.
	a.send(&pb.ClientMessage{Msg: &pb.ClientMessage_SetPause{SetPause: &pb.SetPause{Paused: true}}})
	a.pump("paused", func() bool { return a.status.Paused })
	b.pump("paused", func() bool { return b.status.Paused && b.view.Tick == b.status.Tick })
	if a.view.Tick != a.status.Tick {
		t.Fatalf("azure is at tick %d, match at %d", a.view.Tick, a.status.Tick)
	}
	for _, old := range []*player{a, b} {
		key := world.Factions[old.faction].Key
		fresh := dial(t, url, key, old.token) // rejoin with the token, replacing the old connection
		fresh.pump("snapshot", func() bool { return fresh.view != nil })
		if !view.Equal(old.view, fresh.view) {
			t.Fatalf("%s: view built from deltas differs from a fresh snapshot at tick %d", key, fresh.view.Tick)
		}
		defer fresh.conn.Close()
	}

	// Shut down, then verify persistence: replay from tick 0 and recovery both reach the same state.
	stop()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	tick, hash := m.Snapshot()
	if info, code := getMatchInfo(t, srv.URL, "m"); code != http.StatusOK || info.Tick != tick {
		t.Fatalf("/api/match after the match stopped: %d, tick %d, want %d", code, info.Tick, tick)
	}
	rep, err := persist.VerifyDir(world, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LastTick != tick || rep.FinalHash != hash || rep.Snapshots == 0 || rep.Hashes == 0 {
		t.Fatalf("replay reached tick %d (%d hashes, %d snapshots), live match was at tick %d; hashes equal: %v",
			rep.LastTick, rep.Hashes, rep.Snapshots, tick, rep.FinalHash == hash)
	}

	m2, err := match.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, stop2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- m2.Run(ctx2) }()
	tick2, hash2 := m2.Snapshot()
	stop2()
	if err := <-done2; err != nil {
		t.Fatal(err)
	}
	if tick2 != tick || hash2 != hash {
		t.Fatalf("recovered match is at tick %d, want %d; hashes equal: %v", tick2, tick, hash2 == hash)
	}

	// Recovery refuses a log that belongs to different data.
	other := *world
	other.Rules.Attrition++
	cfg.World = &other
	if _, err := match.Open(cfg); err == nil {
		t.Fatal("recovery with changed data did not fail")
	}
}

func TestWebClient(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "assets", "index-abc123.js"), []byte("1"), 0o644)
	srv := httptest.NewServer((&transport.Server{Matches: map[string]transport.Hosted{}, Web: dir}).Handler())
	defer srv.Close()
	// The page is revalidated on every load; hashed assets are not.
	for _, tc := range []struct{ path, cache string }{{"/", "no-cache"}, {"/assets/index-abc123.js", ""}} {
		resp, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != tc.cache {
			t.Fatalf("GET %s: %d, Cache-Control %q", tc.path, resp.StatusCode, resp.Header.Get("Cache-Control"))
		}
	}

	// Without the directory there is no page, but the API still answers.
	srv2 := httptest.NewServer((&transport.Server{Matches: map[string]transport.Hosted{}, Web: filepath.Join(dir, "missing")}).Handler())
	defer srv2.Close()
	if resp, err := http.Get(srv2.URL + "/"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / without a web directory: %v %v", resp.StatusCode, err)
	}
}

func TestBadMessageAfterHello(t *testing.T) {
	world, err := gamedata.Load("../../../data", "sample")
	if err != nil {
		t.Fatal(err)
	}
	m, err := match.Open(match.Config{ID: "m", World: world, Seed: 1, HashInterval: 24, SnapshotInterval: 24,
		Speeds: []time.Duration{time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- m.Run(runCtx) }()
	defer func() { stop(); <-runDone }()
	srv := httptest.NewServer((&transport.Server{Matches: map[string]transport.Hosted{"m": {Match: m, World: world}}}).Handler())
	defer srv.Close()

	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(64 << 20)
	hello, _ := proto.Marshal(&pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{
		ProtocolVersion: transport.ProtocolVersion, MatchId: "m", Faction: "azure", PlayerName: "x"}}})
	if err := ws.Write(ctx, websocket.MessageBinary, hello); err != nil {
		t.Fatal(err)
	}
	// After joining, a text frame is not a ClientMessage.
	if err := ws.Write(ctx, websocket.MessageText, []byte("hello?")); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("connection closed without BAD_MESSAGE: %v", err)
		}
		msg := &pb.ServerMessage{}
		if err := proto.Unmarshal(data, msg); err != nil {
			t.Fatal(err)
		}
		if e := msg.GetError(); e != nil {
			if e.Code != pb.ErrorCode_ERROR_CODE_BAD_MESSAGE || !e.Fatal {
				t.Fatalf("got error %s fatal=%v", e.Code, e.Fatal)
			}
			break
		}
	}
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("the connection stayed open after BAD_MESSAGE")
	}
	// The faction is free again.
	if info := m.Info(); info.Factions[0].Taken {
		t.Fatal("azure is still taken")
	}
}
