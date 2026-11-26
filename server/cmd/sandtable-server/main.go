// Command sandtable-server hosts one match over WebSocket.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sandtable/server/internal/gamedata"
	"sandtable/server/internal/match"
	"sandtable/server/internal/transport"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dataDir := flag.String("data", "../data", "data directory")
	scenario := flag.String("scenario", "sample", "scenario name")
	matchID := flag.String("match", "default", "match ID")
	seed := flag.Uint64("seed", 42, "seed for a new match (an existing match keeps its own)")
	stateDir := flag.String("state-dir", "var", "directory for command logs and snapshots; empty disables persistence")
	hashEvery := flag.Uint64("hash-interval", 24, "ticks between state hashes in the log")
	snapEvery := flag.Uint64("snapshot-interval", 720, "ticks between snapshots (multiple of hash-interval)")
	speeds := flag.String("speeds", "1000,500,250,100,50", "tick interval in ms for each speed level")
	web := flag.String("web", "../client/dist", "directory of the built web client, served at / if it exists")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *addr, *dataDir, *scenario, *matchID, *seed, *stateDir, *hashEvery, *snapEvery, *speeds, *web); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, addr, dataDir, scenario, matchID string, seed uint64, stateDir string,
	hashEvery, snapEvery uint64, speeds, web string) error {
	world, err := gamedata.Load(dataDir, scenario)
	if err != nil {
		return err
	}
	var intervals []time.Duration
	for _, s := range strings.Split(speeds, ",") {
		ms, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || ms < 1 {
			return fmt.Errorf("bad -speeds value %q", s)
		}
		intervals = append(intervals, time.Duration(ms)*time.Millisecond)
	}
	cfg := match.Config{ID: matchID, World: world, Seed: seed, HashInterval: hashEvery,
		SnapshotInterval: snapEvery, Speeds: intervals, Log: log}
	if stateDir != "" {
		cfg.Dir = filepath.Join(stateDir, matchID)
	}
	m, err := match.Open(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: (&transport.Server{
		Matches: map[string]transport.Hosted{matchID: {Match: m, World: world}}, Log: log, Web: web}).Handler()}
	go srv.Serve(ln)
	log.Info("listening", "addr", ln.Addr().String(), "match", matchID, "scenario", scenario,
		"provinces", len(world.Provinces), "divisions", len(world.InitDivisions))
	if st, err := os.Stat(web); err == nil && st.IsDir() {
		log.Info("serving web client", "dir", web, "url", "http://"+ln.Addr().String()+"/")
	} else {
		log.Info("no web client; build it with `make client`", "dir", web)
	}

	// Run returns after a shutdown signal (final hash and snapshot written) or a log failure.
	runErr := m.Run(ctx)
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		log.Warn("http shutdown", "err", err)
	}
	log.Info("stopped")
	return runErr
}
