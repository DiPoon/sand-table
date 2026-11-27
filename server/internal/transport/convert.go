package transport

import (
	pb "sandtable/server/internal/pb/sandtable/v1"

	"sandtable/server/internal/match"
	"sandtable/server/internal/sim"
	"sandtable/server/internal/view"
)

// ProtocolVersion is the wire protocol version this build speaks.
const ProtocolVersion = 1

func staticToPB(w *sim.World) *pb.StaticData {
	h := sim.HashWorld(w)
	s := &pb.StaticData{DataHash: h[:]}
	for _, t := range w.Terrains {
		s.Terrains = append(s.Terrains, &pb.Terrain{Key: t.Key, Name: t.Name, MoveCost: t.MoveCost,
			DefenseBonus: t.DefenseBonus, CombatWidth: t.CombatWidth, Passable: t.Passable})
	}
	for _, u := range w.UnitTypes {
		s.UnitTypes = append(s.UnitTypes, &pb.UnitType{Key: u.Key, Name: u.Name, MaxStrength: u.MaxStrength,
			MaxOrg: u.MaxOrg, Attack: u.Attack, Defense: u.Defense, Speed: u.Speed,
			OrgRecovery: u.OrgRecovery, Width: u.Width, ReinforceChance: u.ReinforceChance})
	}
	for i, p := range w.Provinces {
		s.Provinces = append(s.Provinces, &pb.Province{Key: p.Key, Name: p.Name, Terrain: uint32(p.Terrain),
			X: p.X, Y: p.Y, Hub: p.Hub, State: uint32(p.Region), Vp: p.VP})
		for _, e := range p.Adj {
			if int(e.To) > i {
				s.Edges = append(s.Edges, &pb.Edge{A: uint32(i), B: uint32(e.To), Distance: e.Distance,
					River: uint32(e.River), Rail: uint32(e.Rail)})
			}
		}
	}
	for i := 1; i < len(w.Regions); i++ { // index 0 is "none", and may be missing in a world built in code
		r := &w.Regions[i]
		s.States = append(s.States, &pb.State{Key: r.Key, Name: r.Name, Capital: uint32(r.Capital),
			Manpower: r.Manpower, Factories: r.Factories, Infrastructure: r.Infrastructure})
	}
	for _, f := range w.Factions[1:] {
		// ID + 1, so that a missing field (0) means "none" rather than province 0.
		s.Factions = append(s.Factions, &pb.Faction{Key: f.Key, Name: f.Name, Capital: uint32(f.Capital + 1)})
	}
	return s
}

func statusToPB(st match.Status) *pb.MatchStatus {
	out := &pb.MatchStatus{Phase: pb.MatchPhase(st.Phase), Paused: st.Paused, Speed: st.Speed,
		Tick: st.Tick, HostFaction: uint32(st.Host)}
	for _, p := range st.Players {
		out.Players = append(out.Players, &pb.Player{FactionId: uint32(p.Faction), Name: p.Name})
	}
	return out
}

func provinceToPB(id sim.ProvinceID, p view.Province) *pb.ProvinceView {
	return &pb.ProvinceView{Id: uint32(id), Controller: uint32(p.Controller), Visible: p.Visible, InBattle: p.InBattle}
}

func divisionToPB(d *view.Division) *pb.DivisionView {
	out := &pb.DivisionView{Id: uint32(d.ID), Faction: uint32(d.Faction), Type: uint32(d.Type), Name: d.Name,
		Province: uint32(d.Province), Strength: d.Strength, Org: d.Org, Retreating: d.Retreating,
		Own: d.Own, Progress: d.Progress, Supply: d.Supply}
	for _, p := range d.Path {
		out.Path = append(out.Path, uint32(p))
	}
	return out
}

// toPB converts a message from a match into its wire form.
func toPB(msg any, w *sim.World) *pb.ServerMessage {
	switch m := msg.(type) {
	case match.Welcome:
		return &pb.ServerMessage{Msg: &pb.ServerMessage_Welcome{Welcome: &pb.Welcome{
			FactionId: uint32(m.Faction), SessionToken: m.Token, Static: staticToPB(w), Status: statusToPB(m.Status)}}}
	case *view.View:
		s := &pb.Snapshot{Tick: m.Tick}
		for i, p := range m.Provinces {
			s.Provinces = append(s.Provinces, provinceToPB(sim.ProvinceID(i), p))
		}
		for i := range m.Divisions {
			s.Divisions = append(s.Divisions, divisionToPB(&m.Divisions[i]))
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_Snapshot{Snapshot: s}}
	case *view.Delta:
		d := &pb.Delta{Tick: m.Tick}
		for _, p := range m.Provinces {
			d.Provinces = append(d.Provinces, provinceToPB(p.ID, p.Province))
		}
		for i := range m.Divisions {
			d.Divisions = append(d.Divisions, divisionToPB(&m.Divisions[i]))
		}
		for _, r := range m.Removed {
			reason := pb.RemoveReason_REMOVE_REASON_OUT_OF_SIGHT
			if r.Destroyed {
				reason = pb.RemoveReason_REMOVE_REASON_DESTROYED
			}
			d.Removed = append(d.Removed, &pb.RemovedDivision{Id: uint32(r.ID), Reason: reason})
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_Delta{Delta: d}}
	case match.CommandResult:
		status := pb.CommandStatus_COMMAND_STATUS_ACCEPTED
		if m.Reason != sim.RejectNone {
			status = pb.CommandStatus_COMMAND_STATUS_REJECTED
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_CommandResult{CommandResult: &pb.CommandResult{
			ClientSeq: m.Seq, Status: status, Reason: pb.RejectReason(m.Reason), ApplyTick: m.ApplyTick}}}
	case match.Status:
		return &pb.ServerMessage{Msg: &pb.ServerMessage_MatchStatus{MatchStatus: statusToPB(m)}}
	case match.Error:
		return errorPB(m.Code, m.Message, m.Fatal)
	}
	return nil
}

func errorPB(code match.ErrCode, msg string, fatal bool) *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Error{Error: &pb.Error{
		Code: pb.ErrorCode(code), Message: msg, Fatal: fatal}}}
}

// ---- Client side: wire form back to views ----

func provinceFromPB(p *pb.ProvinceView) view.Province {
	return view.Province{Controller: sim.FactionID(p.Controller), Visible: p.Visible, InBattle: p.InBattle}
}

func divisionFromPB(d *pb.DivisionView) view.Division {
	out := view.Division{ID: sim.DivisionID(d.Id), Faction: sim.FactionID(d.Faction), Type: sim.UnitTypeID(d.Type),
		Name: d.Name, Province: sim.ProvinceID(d.Province), Strength: d.Strength, Org: d.Org,
		Retreating: d.Retreating, Own: d.Own, Progress: d.Progress, Supply: d.Supply}
	for _, p := range d.Path {
		out.Path = append(out.Path, sim.ProvinceID(p))
	}
	return out
}

// ViewFromPB converts a Snapshot message into a view.
func ViewFromPB(s *pb.Snapshot) *view.View {
	v := &view.View{Tick: s.Tick, Provinces: make([]view.Province, len(s.Provinces))}
	for _, p := range s.Provinces {
		v.Provinces[p.Id] = provinceFromPB(p)
	}
	for _, d := range s.Divisions {
		v.Divisions = append(v.Divisions, divisionFromPB(d))
	}
	return v
}

// DeltaFromPB converts a Delta message into a view delta.
func DeltaFromPB(d *pb.Delta) *view.Delta {
	out := &view.Delta{Tick: d.Tick}
	for _, p := range d.Provinces {
		out.Provinces = append(out.Provinces, view.ProvinceUpdate{ID: sim.ProvinceID(p.Id), Province: provinceFromPB(p)})
	}
	for _, x := range d.Divisions {
		out.Divisions = append(out.Divisions, divisionFromPB(x))
	}
	for _, r := range d.Removed {
		out.Removed = append(out.Removed, view.Removed{ID: sim.DivisionID(r.Id),
			Destroyed: r.Reason == pb.RemoveReason_REMOVE_REASON_DESTROYED})
	}
	return out
}
