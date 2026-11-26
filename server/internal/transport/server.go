// Package transport speaks the WebSocket + Protobuf wire protocol and
// translates between it and a match. See docs/protocol.md.
package transport

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	pb "sandtable/server/internal/pb/sandtable/v1"

	"sandtable/server/internal/match"
	"sandtable/server/internal/sim"
)

const (
	readLimit    = 64 << 10
	helloTimeout = 10 * time.Second
	writeTimeout = 10 * time.Second
	pingInterval = 20 * time.Second
)

// Hosted is a match together with the static data its clients need.
type Hosted struct {
	Match *match.Match
	World *sim.World
}

// Server serves the /ws endpoint and /api/match for a set of matches, and the
// web client if Web names a directory.
type Server struct {
	Matches map[string]Hosted
	Log     *slog.Logger
	Web     string // directory of the built web client; served at / if it exists
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", s.serveWS)
	mux.HandleFunc("GET /api/match", s.serveMatchInfo)
	if s.Web != "" {
		if st, err := os.Stat(s.Web); err == nil && st.IsDir() {
			mux.Handle("GET /", noCacheHTML(http.FileServer(http.Dir(s.Web))))
		}
	}
	return mux
}

// noCacheHTML makes browsers revalidate the page itself, so that a new build
// is picked up on reload. The hashed files under assets/ keep default caching.
func noCacheHTML(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p == "/" || path.Ext(p) == ".html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.ServeHTTP(w, r)
	})
}

// MatchInfo is the JSON body of GET /api/match: what a client shows before it joins.
type MatchInfo struct {
	ID       string        `json:"id"`
	Phase    string        `json:"phase"` // LOBBY or RUNNING
	Tick     uint64        `json:"tick"`
	Factions []FactionInfo `json:"factions"`
}

type FactionInfo struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Taken  bool   `json:"taken"`
	Player string `json:"player,omitempty"`
}

func phaseName(p match.Phase) string {
	switch p {
	case match.PhaseLobby:
		return "LOBBY"
	case match.PhaseRunning:
		return "RUNNING"
	}
	return "UNSPECIFIED"
}

func (s *Server) serveMatchInfo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	h, ok := s.Matches[id]
	if !ok {
		http.Error(w, "unknown match", http.StatusNotFound)
		return
	}
	info := h.Match.Info()
	out := MatchInfo{ID: id, Phase: phaseName(info.Phase), Tick: info.Tick, Factions: []FactionInfo{}}
	for _, f := range info.Factions {
		out.Factions = append(out.Factions, FactionInfo{Key: f.Key, Name: f.Name, Taken: f.Taken, Player: f.Player})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

func writeMsg(ctx context.Context, conn *websocket.Conn, m *pb.ServerMessage) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageBinary, data)
}

func readMsg(ctx context.Context, conn *websocket.Conn) (*pb.ClientMessage, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	m := &pb.ClientMessage{}
	if typ != websocket.MessageBinary || proto.Unmarshal(data, m) != nil || m.Msg == nil {
		return nil, errBadMessage
	}
	return m, nil
}

type badMessage struct{}

func (badMessage) Error() string { return "malformed message" }

var errBadMessage error = badMessage{}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	// Any origin may connect: there is no cookie-based authentication to protect.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)
	ctx := r.Context()

	fail := func(code match.ErrCode, msg string) {
		writeMsg(ctx, conn, errorPB(code, msg, true))
		conn.Close(websocket.StatusPolicyViolation, msg)
	}
	hctx, cancel := context.WithTimeout(ctx, helloTimeout)
	first, err := readMsg(hctx, conn)
	cancel()
	if err != nil {
		if err == errBadMessage {
			fail(match.ErrBadMessage, "malformed message")
		}
		return
	}
	hello := first.GetHello()
	if hello == nil {
		fail(match.ErrNotJoined, "the first message must be Hello")
		return
	}
	if hello.ProtocolVersion != ProtocolVersion {
		fail(match.ErrUnsupportedVersion, "unsupported protocol version")
		return
	}
	h, ok := s.Matches[hello.MatchId]
	if !ok {
		fail(match.ErrUnknownMatch, "unknown match")
		return
	}

	c := match.NewClient(hello.PlayerName)
	h.Match.Join(c, hello.Faction, hello.SessionToken)

	// Writer: the only goroutine that writes to the connection.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		broken := false
		for {
			select {
			case msg, open := <-c.Out:
				if !open {
					if !broken {
						conn.Close(websocket.StatusNormalClosure, "")
					}
					return
				}
				if broken {
					continue // keep draining until the match closes the queue
				}
				if m := toPB(msg, h.World); m != nil {
					if err := writeMsg(ctx, conn, m); err != nil {
						broken = true
						conn.CloseNow()
					}
				}
			case <-ping.C:
				if broken {
					continue
				}
				pctx, cancel := context.WithTimeout(ctx, writeTimeout)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					broken = true
					conn.CloseNow()
				}
			case <-h.Match.Done():
				// The match stopped: flush what it already queued, then close.
				for !broken {
					select {
					case msg, open := <-c.Out:
						if open {
							if m := toPB(msg, h.World); m != nil {
								broken = writeMsg(ctx, conn, m) != nil
							}
							continue
						}
					default:
					}
					break
				}
				conn.Close(websocket.StatusGoingAway, "server stopped")
				return
			}
		}
	}()

	// Reader.
	for {
		m, err := readMsg(ctx, conn)
		if err != nil {
			break
		}
		switch x := m.Msg.(type) {
		case *pb.ClientMessage_Command:
			cmd := sim.Command{}
			if mv := x.Command.GetMoveDivision(); mv != nil {
				cmd = sim.Command{Kind: sim.CmdMove, Division: sim.DivisionID(mv.DivisionId), Dest: sim.ProvinceID(mv.Destination)}
				if mv.Destination > 1<<30 {
					cmd.Dest = -1
				}
			}
			h.Match.Command(c, x.Command.ClientSeq, cmd)
		case *pb.ClientMessage_StartMatch:
			h.Match.Start(c)
		case *pb.ClientMessage_SetPause:
			h.Match.SetPause(c, x.SetPause.Paused)
		case *pb.ClientMessage_SetSpeed:
			h.Match.SetSpeed(c, x.SetSpeed.Speed)
		}
	}
	h.Match.Leave(c)
	<-writerDone
}

// ---- Client ----

// Conn is a client connection to a sandtable server.
type Conn struct {
	ws *websocket.Conn
}

// Dial connects and sends Hello. url looks like ws://host:port/ws.
func Dial(ctx context.Context, url string, hello *pb.Hello) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(64 << 20)
	hello.ProtocolVersion = ProtocolVersion
	c := &Conn{ws: ws}
	if err := c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: hello}}); err != nil {
		ws.CloseNow()
		return nil, err
	}
	return c, nil
}

func (c *Conn) Send(ctx context.Context, m *pb.ClientMessage) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageBinary, data)
}

func (c *Conn) Recv(ctx context.Context) (*pb.ServerMessage, error) {
	_, data, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	m := &pb.ServerMessage{}
	if err := proto.Unmarshal(data, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *Conn) Close() { c.ws.Close(websocket.StatusNormalClosure, "") }

// Move builds a MoveDivision command message.
func Move(seq, division, destination uint32) *pb.ClientMessage {
	return &pb.ClientMessage{Msg: &pb.ClientMessage_Command{Command: &pb.Command{ClientSeq: seq,
		Cmd: &pb.Command_MoveDivision{MoveDivision: &pb.MoveDivision{DivisionId: division, Destination: destination}}}}}
}
