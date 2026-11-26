// Command sandtable-cli is a dummy text client for end-to-end checks.
//
// Commands on stdin, one per line:
//
//	start | pause | resume | speed N | move DIVISION PROVINCE | state | wait MS | quit
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "sandtable/server/internal/pb/sandtable/v1"

	"sandtable/server/internal/sim"
	"sandtable/server/internal/transport"
	"sandtable/server/internal/view"
)

type client struct {
	mu      sync.Mutex
	static  *pb.StaticData
	faction sim.FactionID
	view    *view.View
	quiet   bool
}

func (c *client) prov(id sim.ProvinceID) string {
	if int(id) < len(c.static.Provinces) {
		return c.static.Provinces[id].Key
	}
	return fmt.Sprintf("#%d", id)
}

func (c *client) fac(id sim.FactionID) string {
	if id >= 1 && int(id) <= len(c.static.Factions) {
		return c.static.Factions[id-1].Key
	}
	return "-"
}

func (c *client) divLine(d *view.Division) string {
	s := fmt.Sprintf("div %-2d %-8s %-14s at %s  str %5d  org %4d", d.ID, c.fac(d.Faction), d.Name,
		c.prov(d.Province), d.Strength, d.Org)
	if d.Retreating {
		s += "  RETREATING"
	}
	if d.Own {
		s += fmt.Sprintf("  supply %d%%", d.Supply/10)
		if len(d.Path) > 0 {
			s += "  -> " + c.prov(d.Path[len(d.Path)-1])
		}
	}
	return s
}

func (c *client) printState() {
	fmt.Printf("== tick %d, you are %s ==\n", c.view.Tick, c.fac(c.faction))
	for i, p := range c.view.Provinces {
		mark := ""
		if !p.Visible {
			mark = " (fog)"
		}
		if p.InBattle {
			mark += " BATTLE"
		}
		fmt.Printf("  %s %-12s %-8s%s\n", c.prov(sim.ProvinceID(i)), c.static.Provinces[i].Name, c.fac(p.Controller), mark)
	}
	for i := range c.view.Divisions {
		fmt.Println("  " + c.divLine(&c.view.Divisions[i]))
	}
}

func (c *client) handle(m *pb.ServerMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch x := m.Msg.(type) {
	case *pb.ServerMessage_Welcome:
		c.static = x.Welcome.Static
		c.faction = sim.FactionID(x.Welcome.FactionId)
		fmt.Printf("welcome: you are %s, token %s, data %x\n", c.fac(c.faction), x.Welcome.SessionToken, c.static.DataHash[:4])
	case *pb.ServerMessage_Snapshot:
		c.view = transport.ViewFromPB(x.Snapshot)
		c.printState()
	case *pb.ServerMessage_Delta:
		if c.view == nil {
			return
		}
		d := transport.DeltaFromPB(x.Delta)
		if d.Tick != c.view.Tick+1 {
			fmt.Printf("!! delta for tick %d after tick %d: reconnect needed\n", d.Tick, c.view.Tick)
		}
		if !c.quiet {
			for _, p := range d.Provinces {
				old := c.view.Provinces[p.ID]
				if old.Controller != p.Controller {
					fmt.Printf("[%d] %s now controlled by %s\n", d.Tick, c.prov(p.ID), c.fac(p.Controller))
				}
				if old.InBattle != p.InBattle {
					fmt.Printf("[%d] %s battle: %v\n", d.Tick, c.prov(p.ID), p.InBattle)
				}
			}
			for i := range d.Divisions {
				nd := &d.Divisions[i]
				var old *view.Division
				for j := range c.view.Divisions {
					if c.view.Divisions[j].ID == nd.ID {
						old = &c.view.Divisions[j]
					}
				}
				// Progress, strength and organization change every tick; only report events.
				if old == nil {
					fmt.Printf("[%d] sighted  %s\n", d.Tick, c.divLine(nd))
				} else if old.Province != nd.Province || old.Retreating != nd.Retreating || len(old.Path) != len(nd.Path) {
					fmt.Printf("[%d] update   %s\n", d.Tick, c.divLine(nd))
				}
			}
			for _, r := range d.Removed {
				what := "lost sight of"
				if r.Destroyed {
					what = "DESTROYED"
				}
				fmt.Printf("[%d] %s div %d\n", d.Tick, what, r.ID)
			}
		}
		view.Apply(c.view, d)
	case *pb.ServerMessage_CommandResult:
		r := x.CommandResult
		if r.Status == pb.CommandStatus_COMMAND_STATUS_ACCEPTED {
			fmt.Printf("command %d accepted, applies at tick %d\n", r.ClientSeq, r.ApplyTick)
		} else {
			fmt.Printf("command %d REJECTED: %s\n", r.ClientSeq, r.Reason)
		}
	case *pb.ServerMessage_MatchStatus:
		s := x.MatchStatus
		fmt.Printf("status: %s paused=%v speed=%d tick=%d host=%s players=%d\n", s.Phase, s.Paused, s.Speed,
			s.Tick, c.fac(sim.FactionID(s.HostFaction)), len(s.Players))
	case *pb.ServerMessage_Error:
		fmt.Printf("error: %s %s\n", x.Error.Code, x.Error.Message)
	}
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:8080/ws", "server URL")
	matchID := flag.String("match", "default", "match ID")
	faction := flag.String("faction", "azure", "faction key")
	name := flag.String("name", "cli", "player name")
	token := flag.String("token", "", "session token for reconnecting")
	quiet := flag.Bool("quiet", false, "do not print per-tick events")
	flag.Parse()

	ctx := context.Background()
	conn, err := transport.Dial(ctx, *url, &pb.Hello{MatchId: *matchID, Faction: *faction, PlayerName: *name, SessionToken: *token})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	c := &client{quiet: *quiet}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			m, err := conn.Recv(ctx)
			if err != nil {
				fmt.Println("connection closed:", err)
				return
			}
			c.handle(m)
		}
	}()

	send := func(m *pb.ClientMessage) {
		if err := conn.Send(ctx, m); err != nil {
			fmt.Println("send failed:", err)
		}
	}
	seq := uint32(0)
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		f := strings.Fields(in.Text())
		if len(f) == 0 {
			continue
		}
		arg := func(i int) string {
			if i < len(f) {
				return f[i]
			}
			return ""
		}
		switch f[0] {
		case "start":
			send(&pb.ClientMessage{Msg: &pb.ClientMessage_StartMatch{StartMatch: &pb.StartMatch{}}})
		case "pause", "resume":
			send(&pb.ClientMessage{Msg: &pb.ClientMessage_SetPause{SetPause: &pb.SetPause{Paused: f[0] == "pause"}}})
		case "speed":
			n, _ := strconv.Atoi(arg(1))
			send(&pb.ClientMessage{Msg: &pb.ClientMessage_SetSpeed{SetSpeed: &pb.SetSpeed{Speed: uint32(n)}}})
		case "move":
			id, _ := strconv.Atoi(arg(1))
			dest := uint32(1<<31 - 1) // unknown key: let the server reject it
			c.mu.Lock()
			if c.static != nil {
				for i, p := range c.static.Provinces {
					if p.Key == arg(2) {
						dest = uint32(i)
					}
				}
			}
			c.mu.Unlock()
			seq++
			send(transport.Move(seq, uint32(id), dest))
		case "state":
			c.mu.Lock()
			if c.view != nil {
				c.printState()
			}
			c.mu.Unlock()
		case "wait":
			ms, _ := strconv.Atoi(arg(1))
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-closed:
			}
		case "quit":
			conn.Close()
			return
		default:
			fmt.Println("commands: start | pause | resume | speed N | move DIVISION PROVINCE | state | wait MS | quit")
		}
	}
	conn.Close()
}
