// Package sim is the deterministic simulation core.
//
// Rules for this package (enforced by lint_test.go): integers only, no maps,
// no goroutines, no time, no I/O. See docs/tick.md section 5.
package sim

// Version changes whenever rule code changes simulation results.
// Logs and snapshots written by another version cannot be replayed.
const Version uint32 = 1

type (
	ProvinceID int32
	FactionID  uint8
	DivisionID uint32
	UnitTypeID uint16
	TerrainID  uint16
)

// NoFaction is the controller of an uncontrolled province.
const NoFaction FactionID = 0

// Combat roles of a division in a battle.
const (
	RoleNone    uint8 = 0
	RoleReserve uint8 = 1
	RoleFront   uint8 = 2
)

// ---- Static data ----

type Terrain struct {
	Key          string
	Name         string
	MoveCost     int64 // permille
	DefenseBonus int64 // permille
	CombatWidth  int64
	Passable     bool
}

type UnitType struct {
	Key             string
	Name            string
	MaxStrength     int32
	MaxOrg          int32
	Attack          int64
	Defense         int64 // permille damage reduction
	Speed           int64 // km per tick
	OrgRecovery     int64 // per tick at full supply
	Width           int64
	ReinforceChance int64 // permille per tick
}

type Edge struct {
	To       ProvinceID
	Distance int64 // km
}

type Province struct {
	Key     string
	Name    string
	Terrain TerrainID
	X, Y    int32
	Hub     bool
	Adj     []Edge // sorted by To
}

type Faction struct {
	Key  string
	Name string
}

type Rules struct {
	RollMin             int64
	RollMax             int64
	OrgDamage           int64
	StrengthDamage      int64
	MaxReduction        int64
	MinSupplyEffect     int64
	ExtraDirectionWidth int64
	SupplyFullRange     int64 // km
	SupplyMaxRange      int64 // km
	Attrition           int64
	VisionRange         int32
}

type InitDivision struct {
	Faction FactionID
	Type    UnitTypeID
	Name    string
	At      ProvinceID
}

// World is everything that never changes after a match is created.
type World struct {
	Scenario      string
	Rules         Rules
	Terrains      []Terrain
	UnitTypes     []UnitType
	Provinces     []Province
	Factions      []Faction // index 0 is the unused "none" entry
	InitControl   []FactionID
	InitDivisions []InitDivision
}

// EdgeCost returns the cost of moving from a to its neighbour b and whether
// that move is possible (adjacent and b passable).
func (w *World) EdgeCost(a, b ProvinceID) (int64, bool) {
	for _, e := range w.Provinces[a].Adj {
		if e.To == b {
			t := &w.Terrains[w.Provinces[b].Terrain]
			if !t.Passable {
				return 0, false
			}
			return e.Distance * t.MoveCost, true
		}
	}
	return 0, false
}

func (w *World) passable(p ProvinceID) bool {
	return w.Terrains[w.Provinces[p].Terrain].Passable
}

// ---- Dynamic state ----

type ProvinceState struct {
	Controller FactionID
	InBattle   bool
}

type Division struct {
	ID          DivisionID
	Faction     FactionID
	Type        UnitTypeID
	Name        string
	Loc         ProvinceID
	Strength    int32
	Org         int32
	Supply      int32 // permille
	Retreating  bool
	AttackRole  uint8
	DefenseRole uint8
	Path        []ProvinceID
	Progress    int64
}

type State struct {
	Tick           uint64
	Seed           uint64
	NextDivisionID uint32
	Provinces      []ProvinceState
	Divisions      []Division // ascending ID
}

// FindDivision returns the division with the given ID, or nil.
func (s *State) FindDivision(id DivisionID) *Division {
	lo, hi := 0, len(s.Divisions)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.Divisions[mid].ID < id {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(s.Divisions) && s.Divisions[lo].ID == id {
		return &s.Divisions[lo]
	}
	return nil
}

// ---- Commands ----

type CommandKind uint8

const CmdMove CommandKind = 1

type Command struct {
	Faction  FactionID
	Kind     CommandKind
	Division DivisionID
	Dest     ProvinceID
}

type RejectReason uint8

// Values match the RejectReason enum of the wire protocol.
const (
	RejectNone RejectReason = iota
	RejectMalformed
	RejectNotYourDivision
	RejectDivisionRetreating
	RejectUnknownProvince
	RejectNoPath
	RejectQueueFull
	RejectMatchNotStarted
)

type Rejection struct {
	Index  int // index into the command slice passed to Step
	Reason RejectReason
}

type StepResult struct {
	Rejections []Rejection
}
