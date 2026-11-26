package sim

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Canonical binary encoding: fixed-width little-endian integers, bool as one
// byte, strings and arrays as u32 length + elements. See docs/state.md section 5.

const stateFormat uint32 = 1

var ErrCorrupt = errors.New("sim: corrupt encoding")

type Hash [32]byte

// Enc appends canonical values to a byte slice.
type Enc struct{ B []byte }

func (e *Enc) U8(v uint8)   { e.B = append(e.B, v) }
func (e *Enc) U16(v uint16) { e.B = binary.LittleEndian.AppendUint16(e.B, v) }
func (e *Enc) U32(v uint32) { e.B = binary.LittleEndian.AppendUint32(e.B, v) }
func (e *Enc) U64(v uint64) { e.B = binary.LittleEndian.AppendUint64(e.B, v) }
func (e *Enc) I32(v int32)  { e.U32(uint32(v)) }
func (e *Enc) I64(v int64)  { e.U64(uint64(v)) }
func (e *Enc) Bool(v bool) {
	if v {
		e.U8(1)
	} else {
		e.U8(0)
	}
}
func (e *Enc) Str(s string) { e.U32(uint32(len(s))); e.B = append(e.B, s...) }
func (e *Enc) Bytes(b []byte) {
	e.U32(uint32(len(b)))
	e.B = append(e.B, b...)
}

// Dec reads canonical values. After the first error every read returns zero.
type Dec struct {
	B   []byte
	Err error
}

func (d *Dec) take(n int) []byte {
	if d.Err != nil || n < 0 || len(d.B) < n {
		d.Err = ErrCorrupt
		return nil
	}
	b := d.B[:n]
	d.B = d.B[n:]
	return b
}
func (d *Dec) U8() uint8 {
	if b := d.take(1); b != nil {
		return b[0]
	}
	return 0
}
func (d *Dec) U16() uint16 {
	if b := d.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}
func (d *Dec) U32() uint32 {
	if b := d.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}
func (d *Dec) U64() uint64 {
	if b := d.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}
func (d *Dec) I32() int32 { return int32(d.U32()) }
func (d *Dec) I64() int64 { return int64(d.U64()) }
func (d *Dec) Bool() bool {
	v := d.U8()
	if v > 1 {
		d.Err = ErrCorrupt
	}
	return v == 1
}
func (d *Dec) Bytes() []byte { return d.take(int(d.U32())) }
func (d *Dec) Str() string   { return string(d.Bytes()) }

// Len reads an array length and checks it against the remaining input.
func (d *Dec) Len(minElem int) int {
	n := int(d.U32())
	if d.Err == nil && n*minElem > len(d.B) {
		d.Err = ErrCorrupt
		return 0
	}
	return n
}

// AppendState appends the canonical encoding of s to buf.
func AppendState(buf []byte, s *State) []byte {
	e := Enc{B: buf}
	e.U32(stateFormat)
	e.U64(s.Tick)
	e.U64(s.Seed)
	e.U32(s.NextDivisionID)
	e.U32(uint32(len(s.Provinces)))
	for i := range s.Provinces {
		e.U8(uint8(s.Provinces[i].Controller))
		e.Bool(s.Provinces[i].InBattle)
	}
	e.U32(uint32(len(s.Divisions)))
	for i := range s.Divisions {
		d := &s.Divisions[i]
		e.U32(uint32(d.ID))
		e.U8(uint8(d.Faction))
		e.U16(uint16(d.Type))
		e.Str(d.Name)
		e.U32(uint32(d.Loc))
		e.I32(d.Strength)
		e.I32(d.Org)
		e.I32(d.Supply)
		e.Bool(d.Retreating)
		e.U8(d.AttackRole)
		e.U8(d.DefenseRole)
		e.U32(uint32(len(d.Path)))
		for _, p := range d.Path {
			e.U32(uint32(p))
		}
		e.I64(d.Progress)
	}
	return e.B
}

// DecodeState is the inverse of AppendState.
func DecodeState(b []byte) (*State, error) {
	d := Dec{B: b}
	if d.U32() != stateFormat {
		return nil, ErrCorrupt
	}
	s := &State{Tick: d.U64(), Seed: d.U64(), NextDivisionID: d.U32()}
	s.Provinces = make([]ProvinceState, d.Len(2))
	for i := range s.Provinces {
		s.Provinces[i].Controller = FactionID(d.U8())
		s.Provinces[i].InBattle = d.Bool()
	}
	s.Divisions = make([]Division, d.Len(40))
	for i := range s.Divisions {
		v := &s.Divisions[i]
		v.ID = DivisionID(d.U32())
		v.Faction = FactionID(d.U8())
		v.Type = UnitTypeID(d.U16())
		v.Name = d.Str()
		v.Loc = ProvinceID(d.U32())
		v.Strength = d.I32()
		v.Org = d.I32()
		v.Supply = d.I32()
		v.Retreating = d.Bool()
		v.AttackRole = d.U8()
		v.DefenseRole = d.U8()
		n := d.Len(4)
		if n > 0 {
			v.Path = make([]ProvinceID, n)
			for j := range v.Path {
				v.Path[j] = ProvinceID(d.U32())
			}
		}
		v.Progress = d.I64()
	}
	if d.Err != nil || len(d.B) != 0 {
		return nil, ErrCorrupt
	}
	return s, nil
}

// HashState returns SHA-256 of the canonical encoding of s.
func HashState(s *State) Hash {
	return sha256.Sum256(AppendState(nil, s))
}

// AppendCommand appends the canonical encoding of c.
func AppendCommand(e *Enc, c Command) {
	e.U8(uint8(c.Faction))
	e.U8(uint8(c.Kind))
	e.U32(uint32(c.Division))
	e.U32(uint32(c.Dest))
}

// ReadCommand is the inverse of AppendCommand.
func ReadCommand(d *Dec) Command {
	return Command{
		Faction:  FactionID(d.U8()),
		Kind:     CommandKind(d.U8()),
		Division: DivisionID(d.U32()),
		Dest:     ProvinceID(d.U32()),
	}
}

// HashWorld returns the data hash: SHA-256 of the canonical encoding of w.
func HashWorld(w *World) Hash {
	var e Enc
	e.U32(stateFormat)
	e.Str(w.Scenario)
	r := &w.Rules
	for _, v := range []int64{r.RollMin, r.RollMax, r.OrgDamage, r.StrengthDamage, r.MaxReduction,
		r.MinSupplyEffect, r.ExtraDirectionWidth, r.SupplyFullRange, r.SupplyMaxRange, r.Attrition, int64(r.VisionRange)} {
		e.I64(v)
	}
	e.U32(uint32(len(w.Terrains)))
	for i := range w.Terrains {
		t := &w.Terrains[i]
		e.Str(t.Key)
		e.Str(t.Name)
		e.I64(t.MoveCost)
		e.I64(t.DefenseBonus)
		e.I64(t.CombatWidth)
		e.Bool(t.Passable)
	}
	e.U32(uint32(len(w.UnitTypes)))
	for i := range w.UnitTypes {
		u := &w.UnitTypes[i]
		e.Str(u.Key)
		e.Str(u.Name)
		e.I32(u.MaxStrength)
		e.I32(u.MaxOrg)
		for _, v := range []int64{u.Attack, u.Defense, u.Speed, u.OrgRecovery, u.Width, u.ReinforceChance} {
			e.I64(v)
		}
	}
	e.U32(uint32(len(w.Provinces)))
	for i := range w.Provinces {
		p := &w.Provinces[i]
		e.Str(p.Key)
		e.Str(p.Name)
		e.U16(uint16(p.Terrain))
		e.I32(p.X)
		e.I32(p.Y)
		e.Bool(p.Hub)
		e.U32(uint32(len(p.Adj)))
		for _, a := range p.Adj {
			e.U32(uint32(a.To))
			e.I64(a.Distance)
		}
	}
	e.U32(uint32(len(w.Factions)))
	for _, f := range w.Factions {
		e.Str(f.Key)
		e.Str(f.Name)
	}
	e.U32(uint32(len(w.InitControl)))
	for _, c := range w.InitControl {
		e.U8(uint8(c))
	}
	e.U32(uint32(len(w.InitDivisions)))
	for _, d := range w.InitDivisions {
		e.U8(uint8(d.Faction))
		e.U16(uint16(d.Type))
		e.Str(d.Name)
		e.U32(uint32(d.At))
	}
	return sha256.Sum256(e.B)
}
