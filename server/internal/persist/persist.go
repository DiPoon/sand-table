// Package persist stores the command log and snapshots of a match and
// replays them. See docs/persistence.md.
package persist

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sandtable/server/internal/sim"
)

const (
	logMagic  = "STLOG1\n\x00"
	snapMagic = "STSNAP1\x00"

	RecCommands uint8 = 1
	RecHash     uint8 = 2

	maxFrame = 64 << 20
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Header identifies what a log belongs to. A log can only be replayed with
// the same rule code, data and seed.
type Header struct {
	SimVersion uint32
	MatchID    string
	Scenario   string
	Seed       uint64
	DataHash   sim.Hash
}

type Record struct {
	Kind     uint8
	Tick     uint64
	Commands []sim.Command // RecCommands
	Hash     sim.Hash      // RecHash
}

// frame appends [len u32][payload][crc32c u32].
func frame(buf, payload []byte) []byte {
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(payload)))
	buf = append(buf, payload...)
	return binary.LittleEndian.AppendUint32(buf, crc32.Checksum(payload, castagnoli))
}

// unframe reads one frame from the front of b. ok is false if b holds no
// complete, intact frame (a torn or corrupt tail).
func unframe(b []byte) (payload, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, b, false
	}
	n := int(binary.LittleEndian.Uint32(b))
	if n > maxFrame || len(b) < 4+n+4 {
		return nil, b, false
	}
	payload = b[4 : 4+n]
	if binary.LittleEndian.Uint32(b[4+n:]) != crc32.Checksum(payload, castagnoli) {
		return nil, b, false
	}
	return payload, b[4+n+4:], true
}

func encodeHeader(h Header) []byte {
	var e sim.Enc
	e.U32(h.SimVersion)
	e.Str(h.MatchID)
	e.Str(h.Scenario)
	e.U64(h.Seed)
	e.B = append(e.B, h.DataHash[:]...)
	return e.B
}

// ---- Command log ----

type Log struct {
	f   *os.File
	buf []byte

	// TornBytes is how much OpenLog cut off the end of the file.
	TornBytes int64
}

// CreateLog starts a new log. It fails if the file exists.
func CreateLog(path string, h Header) (*Log, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f}
	if _, err := f.Write(frame([]byte(logMagic), encodeHeader(h))); err != nil {
		f.Close()
		return nil, err
	}
	return l, l.Sync()
}

// ReadLog reads a log file. A crash can leave the end of the log damaged:
// reading stops there, and validLen is the length of the intact prefix.
// Damage anywhere else is an error, because cutting the log there would
// silently throw away the intact records after it (see tornTail).
func ReadLog(path string) (h Header, recs []Record, validLen int64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return h, nil, 0, err
	}
	if !bytes.HasPrefix(data, []byte(logMagic)) {
		return h, nil, 0, fmt.Errorf("%s: not a command log", path)
	}
	rest := data[len(logMagic):]
	payload, rest, ok := unframe(rest)
	if !ok {
		return h, nil, 0, fmt.Errorf("%s: corrupt header", path)
	}
	d := sim.Dec{B: payload}
	h.SimVersion = d.U32()
	h.MatchID = d.Str()
	h.Scenario = d.Str()
	h.Seed = d.U64()
	if d.Err != nil || len(d.B) != len(h.DataHash) {
		return h, nil, 0, fmt.Errorf("%s: corrupt header", path)
	}
	copy(h.DataHash[:], d.B)

	for len(rest) > 0 {
		r, next, ok := readRecord(rest)
		if !ok {
			if !tornTail(rest) {
				return h, nil, 0, fmt.Errorf("%s: the record at byte %d is corrupt, and intact records follow it; "+
					"refusing to cut them off. Restore the log from a backup or move the match directory away",
					path, len(data)-len(rest))
			}
			break
		}
		recs = append(recs, r)
		rest = next
	}
	return h, recs, int64(len(data) - len(rest)), nil
}

// readRecord reads one framed record from the start of b.
func readRecord(b []byte) (r Record, rest []byte, ok bool) {
	payload, rest, ok := unframe(b)
	if !ok {
		return r, b, false
	}
	d := sim.Dec{B: payload}
	r = Record{Kind: d.U8(), Tick: d.U64()}
	switch r.Kind {
	case RecCommands:
		n := d.Len(10)
		r.Commands = make([]sim.Command, n)
		for i := range r.Commands {
			r.Commands[i] = sim.ReadCommand(&d)
		}
	case RecHash:
		if len(d.B) == len(r.Hash) {
			copy(r.Hash[:], d.B)
			d.B = nil
		} else {
			d.Err = sim.ErrCorrupt
		}
	default:
		d.Err = sim.ErrCorrupt
	}
	if d.Err != nil || len(d.B) != 0 {
		return Record{}, b, false
	}
	return r, rest, true
}

// tornTail reports whether b, which starts where a record does not read, is
// the end of the log that a crash left behind. Every HASH record is synced to
// disk as soon as it is written (see the match loop), so a crash can only
// damage what comes after the last one: a record being written, or space the
// file system allocated but never filled. If a HASH record still reads
// anywhere after the damage, the damage is in data that had reached the disk.
func tornTail(b []byte) bool {
	for q := 1; q < len(b); q++ {
		if r, _, ok := readRecord(b[q:]); ok && r.Kind == RecHash {
			return false
		}
	}
	return true
}

// OpenLog reads an existing log, cuts off a torn tail and opens it for appending.
func OpenLog(path string) (*Log, Header, []Record, error) {
	h, recs, validLen, err := ReadLog(path)
	if err != nil {
		return nil, h, nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, h, nil, err
	}
	st, err := f.Stat()
	if err == nil {
		err = f.Truncate(validLen)
	}
	if err == nil {
		_, err = f.Seek(validLen, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, h, nil, err
	}
	return &Log{f: f, TornBytes: st.Size() - validLen}, h, recs, nil
}

func (l *Log) write(payload []byte) error {
	l.buf = frame(l.buf[:0], payload)
	_, err := l.f.Write(l.buf)
	return err
}

// AppendCommands records the commands of a tick. Call it before the tick runs.
func (l *Log) AppendCommands(tick uint64, cmds []sim.Command) error {
	var e sim.Enc
	e.U8(RecCommands)
	e.U64(tick)
	e.U32(uint32(len(cmds)))
	for _, c := range cmds {
		sim.AppendCommand(&e, c)
	}
	return l.write(e.B)
}

func (l *Log) AppendHash(tick uint64, h sim.Hash) error {
	var e sim.Enc
	e.U8(RecHash)
	e.U64(tick)
	e.B = append(e.B, h[:]...)
	return l.write(e.B)
}

func (l *Log) Sync() error  { return l.f.Sync() }
func (l *Log) Close() error { return errors.Join(l.f.Sync(), l.f.Close()) }

// ---- Replay ----

// inOrder checks that r may follow prev in a log. Ticks never go back, and a
// tick has at most one COMMANDS record, written before the tick ran and so
// before that tick's HASH record.
func inOrder(prev, r *Record) error {
	if r.Tick < prev.Tick || r.Kind == RecCommands && r.Tick == prev.Tick {
		return fmt.Errorf("log records out of order: %s at tick %d after %s at tick %d",
			kindName(r.Kind), r.Tick, kindName(prev.Kind), prev.Tick)
	}
	return nil
}

func kindName(k uint8) string {
	switch k {
	case RecCommands:
		return "COMMANDS"
	case RecHash:
		return "HASH"
	}
	return fmt.Sprintf("kind %d", k)
}

// MismatchError reports a state hash that differs from the recorded one.
type MismatchError struct {
	Tick      uint64
	Got, Want sim.Hash
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("state hash mismatch at tick %d: got %x, log has %x", e.Tick, e.Got[:8], e.Want[:8])
}

// Replay applies the records after s.Tick to s and checks every recorded
// hash from s.Tick on. It leaves s at the tick of the last record.
func Replay(e *sim.Engine, s *sim.State, recs []Record) error {
	for i, r := range recs {
		if i > 0 {
			if err := inOrder(&recs[i-1], &r); err != nil {
				return err
			}
		}
		switch r.Kind {
		case RecCommands:
			if r.Tick <= s.Tick {
				continue
			}
			for s.Tick < r.Tick-1 {
				e.Step(s, nil)
			}
			e.Step(s, r.Commands)
		case RecHash:
			if r.Tick < s.Tick {
				continue
			}
			for s.Tick < r.Tick {
				e.Step(s, nil)
			}
			if got := sim.HashState(s); got != r.Hash {
				return &MismatchError{Tick: r.Tick, Got: got, Want: r.Hash}
			}
		}
	}
	return nil
}

// ---- Snapshots ----

type Snapshot struct {
	SimVersion uint32
	MatchID    string
	DataHash   sim.Hash
	Tick       uint64
	StateHash  sim.Hash
	State      []byte // canonical encoding
}

func snapName(tick uint64) string { return fmt.Sprintf("%012d.snap", tick) }

// WriteSnapshot stores a snapshot atomically and keeps only the newest `keep` files.
func WriteSnapshot(dir string, sn *Snapshot, keep int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var e sim.Enc
	e.U32(sn.SimVersion)
	e.Str(sn.MatchID)
	e.B = append(e.B, sn.DataHash[:]...)
	e.U64(sn.Tick)
	e.B = append(e.B, sn.StateHash[:]...)
	e.Bytes(sn.State)
	data := frame([]byte(snapMagic), e.B)

	final := filepath.Join(dir, snapName(sn.Tick))
	tmp := final + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	names := listSnapshots(dir)
	for i := 0; i < len(names)-keep; i++ {
		os.Remove(filepath.Join(dir, names[i]))
	}
	return nil
}

func listSnapshots(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".snap") {
			names = append(names, en.Name())
		}
	}
	sort.Strings(names)
	return names
}

// ReadSnapshot reads and verifies one snapshot file.
func ReadSnapshot(path string) (*Snapshot, *sim.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	bad := fmt.Errorf("%s: corrupt snapshot", path)
	if !bytes.HasPrefix(data, []byte(snapMagic)) {
		return nil, nil, bad
	}
	payload, rest, ok := unframe(data[len(snapMagic):])
	if !ok || len(rest) != 0 {
		return nil, nil, bad
	}
	d := sim.Dec{B: payload}
	sn := &Snapshot{SimVersion: d.U32(), MatchID: d.Str()}
	if len(d.B) < 32 {
		return nil, nil, bad
	}
	copy(sn.DataHash[:], d.B)
	d.B = d.B[32:]
	sn.Tick = d.U64()
	if len(d.B) < 32 {
		return nil, nil, bad
	}
	copy(sn.StateHash[:], d.B)
	d.B = d.B[32:]
	sn.State = d.Bytes()
	if d.Err != nil || len(d.B) != 0 {
		return nil, nil, bad
	}
	st, err := sim.DecodeState(sn.State)
	if err != nil || st.Tick != sn.Tick || sim.HashState(st) != sn.StateHash {
		return nil, nil, bad
	}
	return sn, st, nil
}

// SnapshotFiles returns the snapshot paths in dir, oldest first.
func SnapshotFiles(dir string) []string {
	names := listSnapshots(dir)
	for i := range names {
		names[i] = filepath.Join(dir, names[i])
	}
	return names
}

// LoadLatestSnapshot returns the newest intact snapshot that belongs to h
// and is not newer than maxTick. It returns nil if there is none; skipped
// lists the files that were passed over and why.
func LoadLatestSnapshot(dir string, h Header, maxTick uint64) (st *sim.State, skipped []string) {
	files := SnapshotFiles(dir)
	for i := len(files) - 1; i >= 0; i-- {
		sn, s, err := ReadSnapshot(files[i])
		switch {
		case err != nil:
			skipped = append(skipped, err.Error())
		case sn.SimVersion != h.SimVersion || sn.MatchID != h.MatchID || sn.DataHash != h.DataHash || s.Seed != h.Seed:
			skipped = append(skipped, files[i]+": belongs to another match, data or version")
		case sn.Tick > maxTick:
			skipped = append(skipped, files[i]+": newer than the command log")
		default:
			return s, skipped
		}
	}
	return nil, skipped
}

// Report is the result of VerifyDir.
type Report struct {
	Header    Header
	Records   int
	LastTick  uint64
	Hashes    int
	Snapshots int
	FinalHash sim.Hash
}

// VerifyDir replays the command log of a match directory from tick 0,
// checking every recorded hash and every snapshot against the replayed state.
func VerifyDir(w *sim.World, dir string) (Report, error) {
	var rep Report
	h, recs, _, err := ReadLog(filepath.Join(dir, "commands.log"))
	if err != nil {
		return rep, err
	}
	rep.Header, rep.Records = h, len(recs)
	if h.SimVersion != sim.Version {
		return rep, fmt.Errorf("log was written by rule version %d, this build is version %d", h.SimVersion, sim.Version)
	}
	if h.DataHash != sim.HashWorld(w) {
		return rep, errors.New("log was written with different data (data hash mismatch)")
	}
	type snap struct {
		tick uint64
		hash sim.Hash
		path string
	}
	var snaps []snap
	for _, path := range SnapshotFiles(filepath.Join(dir, "snapshots")) {
		sn, _, err := ReadSnapshot(path)
		if err != nil {
			return rep, err
		}
		snaps = append(snaps, snap{sn.Tick, sn.StateHash, path})
	}
	e := sim.NewEngine(w)
	s := e.NewState(h.Seed)
	check := func() error {
		for len(snaps) > 0 && snaps[0].tick == s.Tick {
			if got := sim.HashState(s); got != snaps[0].hash {
				return fmt.Errorf("%s: %w", snaps[0].path, &MismatchError{Tick: s.Tick, Got: got, Want: snaps[0].hash})
			}
			rep.Snapshots++
			snaps = snaps[1:]
		}
		return nil
	}
	// Replay one record at a time so snapshots between records are checked too.
	for i, r := range recs {
		if i > 0 {
			if err := inOrder(&recs[i-1], &r); err != nil {
				return rep, err
			}
		}
		// From tick 0 nothing is skipped: a record the state has already passed is an error.
		if r.Tick < s.Tick || r.Kind == RecCommands && r.Tick <= s.Tick {
			return rep, fmt.Errorf("%s record for tick %d comes after tick %d was replayed", kindName(r.Kind), r.Tick, s.Tick)
		}
		for len(snaps) > 0 && snaps[0].tick < r.Tick && s.Tick < snaps[0].tick {
			e.Step(s, nil)
			if err := check(); err != nil {
				return rep, err
			}
		}
		if err := Replay(e, s, recs[i:i+1]); err != nil {
			return rep, err
		}
		if r.Kind == RecHash {
			rep.Hashes++
		}
		if err := check(); err != nil {
			return rep, err
		}
	}
	if len(snaps) > 0 {
		return rep, fmt.Errorf("%s: snapshot is newer than the command log", snaps[0].path)
	}
	rep.LastTick, rep.FinalHash = s.Tick, sim.HashState(s)
	return rep, nil
}
