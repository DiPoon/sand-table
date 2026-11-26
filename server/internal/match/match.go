// Package match runs one match: the tick loop, the command queue, the lobby
// and the connected players. One goroutine owns all of its state.
// See docs/tick.md.
package match

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sandtable/server/internal/persist"
	"sandtable/server/internal/sim"
	"sandtable/server/internal/view"
)

type Phase uint8

const (
	PhaseLobby   Phase = 1
	PhaseRunning Phase = 2
)

// ErrCode values match the ErrorCode enum of the wire protocol.
type ErrCode uint8

const (
	ErrBadMessage ErrCode = iota + 1
	ErrUnsupportedVersion
	ErrUnknownMatch
	ErrUnknownFaction
	ErrFactionTaken
	ErrNotJoined
	ErrNotHost
	ErrInvalidSpeed
	ErrSlowConsumer
	ErrServerShutdown
)

const maxQueuedPerFaction = 256

// Messages a match sends to a client through Client.Out, besides
// *view.View (snapshot) and *view.Delta.
type (
	Welcome struct {
		Faction sim.FactionID
		Token   string
		Status  Status
	}
	PlayerInfo struct {
		Faction sim.FactionID
		Name    string
	}
	Status struct {
		Phase   Phase
		Paused  bool
		Speed   uint32
		Tick    uint64
		Host    sim.FactionID
		Players []PlayerInfo
	}
	CommandResult struct {
		Seq       uint32
		Reason    sim.RejectReason // RejectNone means accepted
		ApplyTick uint64
	}
	Error struct {
		Code    ErrCode
		Message string
		Fatal   bool
	}
)

// Client is one connection. The transport creates it and reads Out until it
// is closed; the match is the only writer and closes Out when it drops the
// client.
type Client struct {
	Name string
	Out  chan any

	faction sim.FactionID
	order   uint64
	token   string
	last    *view.View
	closed  bool
}

func NewClient(name string) *Client {
	return &Client{Name: name, Out: make(chan any, 256)}
}

type Config struct {
	ID               string
	World            *sim.World
	Seed             uint64
	Dir              string // match directory for the log and snapshots; empty disables persistence
	HashInterval     uint64
	SnapshotInterval uint64
	Speeds           []time.Duration // tick interval per speed level
	Log              *slog.Logger
}

type queued struct {
	client *Client
	seq    uint32
	cmd    sim.Command
}

type Match struct {
	cfg   Config
	log   *slog.Logger
	eng   *sim.Engine
	state *sim.State

	inbox chan func()
	done  chan struct{}

	phase   Phase
	paused  bool
	speed   uint32
	next    time.Time
	players []*Client // indexed by faction
	orders  uint64
	queue   []queued
	cmds    []sim.Command
	perFac  []int

	wlog     *persist.Log
	header   persist.Header
	lastHash uint64
	snaps    chan *persist.Snapshot
	snapWG   sync.WaitGroup

	// tick timing, reported periodically
	statN                   int
	statStep, statSync      time.Duration
	statMaxStep, statMaxAll time.Duration
}

// Open creates a match, or recovers it if cfg.Dir already holds its log.
func Open(cfg Config) (*Match, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HashInterval == 0 || cfg.SnapshotInterval%cfg.HashInterval != 0 || cfg.SnapshotInterval == 0 {
		return nil, errors.New("match: snapshot interval must be a positive multiple of the hash interval")
	}
	if len(cfg.Speeds) == 0 {
		return nil, errors.New("match: no speed levels")
	}
	m := &Match{
		cfg: cfg, log: cfg.Log.With("match", cfg.ID),
		eng:   sim.NewEngine(cfg.World),
		inbox: make(chan func(), 256), done: make(chan struct{}),
		phase: PhaseLobby, speed: 1,
		players: make([]*Client, len(cfg.World.Factions)),
		perFac:  make([]int, len(cfg.World.Factions)),
	}
	m.header = persist.Header{SimVersion: sim.Version, MatchID: cfg.ID, Scenario: cfg.World.Scenario,
		Seed: cfg.Seed, DataHash: sim.HashWorld(cfg.World)}
	if cfg.Dir == "" {
		m.state = m.eng.NewState(cfg.Seed)
		return m, nil
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(cfg.Dir, "commands.log")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		m.state = m.eng.NewState(cfg.Seed)
		if m.wlog, err = persist.CreateLog(path, m.header); err != nil {
			return nil, err
		}
	} else if err := m.recover(path); err != nil {
		return nil, err
	}
	m.snaps = make(chan *persist.Snapshot, 2)
	m.snapWG.Add(1)
	go func() {
		defer m.snapWG.Done()
		for sn := range m.snaps {
			if err := persist.WriteSnapshot(m.snapDir(), sn, 3); err != nil {
				m.log.Error("snapshot failed", "tick", sn.Tick, "err", err)
			}
		}
	}()
	return m, nil
}

func (m *Match) snapDir() string { return filepath.Join(m.cfg.Dir, "snapshots") }

// recover rebuilds the state from the newest snapshot plus the command log.
func (m *Match) recover(path string) error {
	wlog, h, recs, err := persist.OpenLog(path)
	if err != nil {
		return err
	}
	want := m.header
	want.Seed = h.Seed // the seed of an existing match comes from its log
	if h != want {
		wlog.Close()
		return fmt.Errorf("match %s: the log in %s was written with different data, scenario or rule version; "+
			"move the directory away to start a new match", m.cfg.ID, m.cfg.Dir)
	}
	m.header = h
	var last uint64
	if len(recs) > 0 {
		last = recs[len(recs)-1].Tick
	}
	st, skipped := persist.LoadLatestSnapshot(m.snapDir(), h, last)
	for _, s := range skipped {
		m.log.Warn("snapshot skipped", "reason", s)
	}
	from := "initial state"
	if st == nil {
		st = m.eng.NewState(h.Seed)
	} else {
		from = fmt.Sprintf("snapshot at tick %d", st.Tick)
	}
	if err := persist.Replay(m.eng, st, recs); err != nil {
		wlog.Close()
		return fmt.Errorf("match %s: recovery failed: %w", m.cfg.ID, err)
	}
	m.state, m.wlog = st, wlog
	for _, r := range recs {
		if r.Kind == persist.RecHash {
			m.lastHash = r.Tick
		}
	}
	if st.Tick > 0 {
		m.phase = PhaseRunning
		m.paused = true
	}
	m.log.Info("recovered", "from", from, "tick", st.Tick, "records", len(recs))
	return nil
}

// do runs f on the match goroutine. It is a no-op after the match stopped.
func (m *Match) do(f func()) {
	select {
	case m.inbox <- f:
	case <-m.done:
	}
}

func (m *Match) Join(c *Client, faction, token string) { m.do(func() { m.join(c, faction, token) }) }
func (m *Match) Leave(c *Client)                       { m.do(func() { m.leave(c) }) }
func (m *Match) Start(c *Client)                       { m.do(func() { m.start(c) }) }
func (m *Match) SetPause(c *Client, paused bool)       { m.do(func() { m.setPause(c, paused) }) }
func (m *Match) SetSpeed(c *Client, speed uint32)      { m.do(func() { m.setSpeed(c, speed) }) }
func (m *Match) Command(c *Client, seq uint32, cmd sim.Command) {
	m.do(func() { m.command(c, seq, cmd) })
}

// Done is closed when Run has returned.
func (m *Match) Done() <-chan struct{} { return m.done }

func (m *Match) running() bool { return m.phase == PhaseRunning && !m.paused }

func (m *Match) interval() time.Duration { return m.cfg.Speeds[m.speed-1] }

// Run executes the match loop until ctx is cancelled or the log fails.
func (m *Match) Run(ctx context.Context) error {
	defer close(m.done)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	var failure error
	for failure == nil {
		var tc <-chan time.Time
		if m.running() {
			timer.Reset(time.Until(m.next))
			tc = timer.C
		}
		was := m.running()
		select {
		case <-ctx.Done():
			return m.shutdown()
		case f := <-m.inbox:
			f()
			if !was && m.running() {
				m.next = time.Now().Add(m.interval())
			}
		case <-tc:
			start := time.Now()
			failure = m.tick()
			// No catch-up: if the tick overran, the next one starts right away.
			m.next = start.Add(m.interval())
		}
	}
	m.log.Error("match stopped", "err", failure)
	return errors.Join(failure, m.shutdown())
}

func (m *Match) tick() error {
	t0 := time.Now()
	m.cmds = m.cmds[:0]
	for _, q := range m.queue {
		m.cmds = append(m.cmds, q.cmd)
	}
	// Write-ahead: the inputs of a tick are logged before the tick runs.
	if m.wlog != nil && len(m.cmds) > 0 {
		if err := m.wlog.AppendCommands(m.state.Tick+1, m.cmds); err != nil {
			return err
		}
	}
	res := m.eng.Step(m.state, m.cmds)
	t1 := time.Now()
	for _, r := range res.Rejections {
		q := m.queue[r.Index]
		m.send(q.client, CommandResult{Seq: q.seq, Reason: r.Reason, ApplyTick: m.state.Tick})
	}
	clear(m.queue)
	m.queue = m.queue[:0]
	clear(m.perFac)

	tick := m.state.Tick
	if m.wlog != nil && tick%m.cfg.HashInterval == 0 {
		if err := m.persistHash(tick%m.cfg.SnapshotInterval == 0); err != nil {
			return err
		}
	}
	t2 := time.Now()
	for _, c := range m.players {
		if c != nil {
			cur := view.Build(m.cfg.World, m.state, c.faction)
			m.send(c, view.Diff(c.last, cur, m.state))
			c.last = cur
		}
	}
	t3 := time.Now()

	step, all := t1.Sub(t0), t3.Sub(t0)
	m.statN++
	m.statStep += step
	m.statSync += t3.Sub(t2)
	m.statMaxStep = max(m.statMaxStep, step)
	m.statMaxAll = max(m.statMaxAll, all)
	if m.statN == 500 {
		m.log.Info("tick timing", "tick", tick, "ticks", m.statN,
			"step_avg", m.statStep/time.Duration(m.statN), "step_max", m.statMaxStep,
			"sync_avg", m.statSync/time.Duration(m.statN), "total_max", m.statMaxAll)
		m.statN, m.statStep, m.statSync, m.statMaxStep, m.statMaxAll = 0, 0, 0, 0, 0
	}
	return nil
}

// persistHash logs the state hash and syncs the log, then optionally queues a snapshot.
// The log is always synced before a snapshot of the same tick is written.
func (m *Match) persistHash(snapshot bool) error {
	enc := sim.AppendState(nil, m.state)
	hash := sim.Hash(sha256.Sum256(enc))
	if err := m.wlog.AppendHash(m.state.Tick, hash); err != nil {
		return err
	}
	if err := m.wlog.Sync(); err != nil {
		return err
	}
	m.lastHash = m.state.Tick
	if snapshot {
		m.snaps <- &persist.Snapshot{SimVersion: sim.Version, MatchID: m.cfg.ID, DataHash: m.header.DataHash,
			Tick: m.state.Tick, StateHash: hash, State: enc}
	}
	return nil
}

func (m *Match) shutdown() error {
	for _, c := range m.players {
		if c != nil {
			m.send(c, Error{Code: ErrServerShutdown, Message: "server is shutting down", Fatal: true})
			m.drop(c)
		}
	}
	if m.wlog == nil {
		return nil
	}
	var err error
	if m.state.Tick > m.lastHash {
		err = m.persistHash(true)
	}
	close(m.snaps)
	m.snapWG.Wait()
	return errors.Join(err, m.wlog.Close())
}

// ---- Players ----

func (m *Match) send(c *Client, msg any) {
	if c.closed {
		return
	}
	select {
	case c.Out <- msg:
	default:
		// A slow client never blocks the tick loop: it is dropped and must rejoin.
		m.log.Warn("dropping slow client", "name", c.Name, "faction", c.faction)
		m.drop(c)
		m.afterLeave()
	}
}

// drop closes a client's queue and frees its faction slot.
func (m *Match) drop(c *Client) {
	if c.closed {
		return
	}
	c.closed = true
	close(c.Out)
	if c.faction != sim.NoFaction && m.players[c.faction] == c {
		m.players[c.faction] = nil
	}
}

func (m *Match) reject(c *Client, code ErrCode, msg string, fatal bool) {
	m.send(c, Error{Code: code, Message: msg, Fatal: fatal})
	if fatal {
		m.drop(c)
	}
}

func (m *Match) host() *Client {
	var h *Client
	for _, c := range m.players {
		if c != nil && (h == nil || c.order < h.order) {
			h = c
		}
	}
	return h
}

func (m *Match) status() Status {
	st := Status{Phase: m.phase, Paused: m.paused, Speed: m.speed, Tick: m.state.Tick}
	if h := m.host(); h != nil {
		st.Host = h.faction
	}
	for _, c := range m.players {
		if c != nil {
			st.Players = append(st.Players, PlayerInfo{Faction: c.faction, Name: c.Name})
		}
	}
	return st
}

func (m *Match) broadcastStatus() {
	st := m.status()
	for _, c := range m.players {
		if c != nil {
			m.send(c, st)
		}
	}
}

func (m *Match) join(c *Client, faction, token string) {
	if c.closed || c.faction != sim.NoFaction {
		return
	}
	var f sim.FactionID
	for i := 1; i < len(m.cfg.World.Factions); i++ {
		if m.cfg.World.Factions[i].Key == faction {
			f = sim.FactionID(i)
		}
	}
	if f == sim.NoFaction {
		m.reject(c, ErrUnknownFaction, fmt.Sprintf("unknown faction %q", faction), true)
		return
	}
	if old := m.players[f]; old != nil {
		if token == "" || token != old.token {
			m.reject(c, ErrFactionTaken, fmt.Sprintf("faction %q is taken", faction), true)
			return
		}
		// Same player reconnecting over a connection the server still thinks is alive.
		m.drop(old)
	}
	var raw [16]byte
	rand.Read(raw[:])
	m.orders++
	c.faction, c.order, c.token = f, m.orders, hex.EncodeToString(raw[:])
	m.players[f] = c
	m.log.Info("player joined", "name", c.Name, "faction", faction)
	c.last = view.Build(m.cfg.World, m.state, f)
	m.send(c, Welcome{Faction: f, Token: c.token, Status: m.status()})
	m.send(c, c.last)
	m.broadcastStatus()
}

func (m *Match) leave(c *Client) {
	if c.closed {
		return
	}
	m.drop(c)
	m.log.Info("player left", "name", c.Name)
	m.afterLeave()
}

func (m *Match) afterLeave() {
	empty := m.host() == nil
	if empty && m.phase == PhaseRunning {
		m.paused = true
	}
	m.broadcastStatus()
}

func (m *Match) joined(c *Client) bool {
	if c.closed {
		return false
	}
	if c.faction == sim.NoFaction {
		m.reject(c, ErrNotJoined, "send Hello first", false)
		return false
	}
	return true
}

func (m *Match) isHost(c *Client) bool {
	if !m.joined(c) {
		return false
	}
	if m.host() != c {
		m.reject(c, ErrNotHost, "only the host can do that", false)
		return false
	}
	return true
}

func (m *Match) start(c *Client) {
	if !m.isHost(c) || m.phase != PhaseLobby {
		return
	}
	m.phase = PhaseRunning
	m.paused = false
	m.log.Info("match started")
	m.broadcastStatus()
}

func (m *Match) setPause(c *Client, paused bool) {
	if !m.joined(c) || m.phase != PhaseRunning || m.paused == paused {
		return
	}
	m.paused = paused
	m.broadcastStatus()
}

func (m *Match) setSpeed(c *Client, speed uint32) {
	if !m.isHost(c) {
		return
	}
	if speed < 1 || int(speed) > len(m.cfg.Speeds) {
		m.reject(c, ErrInvalidSpeed, fmt.Sprintf("speed must be 1..%d", len(m.cfg.Speeds)), false)
		return
	}
	if m.speed != speed {
		m.speed = speed
		m.next = time.Now().Add(m.interval())
		m.broadcastStatus()
	}
}

func (m *Match) command(c *Client, seq uint32, cmd sim.Command) {
	if !m.joined(c) {
		return
	}
	cmd.Faction = c.faction
	res := CommandResult{Seq: seq, ApplyTick: m.state.Tick + 1}
	switch {
	case m.phase != PhaseRunning:
		res.Reason = sim.RejectMatchNotStarted
	case m.perFac[c.faction] >= maxQueuedPerFaction:
		res.Reason = sim.RejectQueueFull
	default:
		res.Reason = m.eng.Validate(m.state, cmd)
	}
	if res.Reason == sim.RejectNone {
		m.queue = append(m.queue, queued{client: c, seq: seq, cmd: cmd})
		m.perFac[c.faction]++
	}
	m.send(c, res)
}

// Snapshot returns the current tick and state hash. Call it while Run is
// active or after it returned.
func (m *Match) Snapshot() (tick uint64, hash sim.Hash) {
	m.read(func() { tick, hash = m.state.Tick, sim.HashState(m.state) })
	return
}

// FactionSlot is one faction as seen before joining: who holds it, if anyone.
type FactionSlot struct {
	Key    string
	Name   string
	Taken  bool
	Player string
}

// Info is what a client needs to know before it joins.
type Info struct {
	Phase    Phase
	Tick     uint64
	Factions []FactionSlot // in faction ID order
}

// Info returns the phase, the tick and the faction slots. Call it while Run is
// active or after it returned.
func (m *Match) Info() (info Info) {
	m.read(func() {
		info = Info{Phase: m.phase, Tick: m.state.Tick}
		for f := 1; f < len(m.cfg.World.Factions); f++ {
			slot := FactionSlot{Key: m.cfg.World.Factions[f].Key, Name: m.cfg.World.Factions[f].Name}
			if c := m.players[f]; c != nil {
				slot.Taken, slot.Player = true, c.Name
			}
			info.Factions = append(info.Factions, slot)
		}
	})
	return
}

// read runs f on the match goroutine and waits for it, or runs it directly
// once Run has returned and nothing else touches the match any more. f only
// reads, so running it a second time after Run returned is harmless.
func (m *Match) read(f func()) {
	ch := make(chan struct{})
	select {
	case m.inbox <- func() { f(); close(ch) }:
		select {
		case <-ch:
			return
		case <-m.done:
		}
	case <-m.done:
	}
	f()
}
