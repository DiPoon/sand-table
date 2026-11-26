// Runs against a live server: SANDTABLE_WS=ws://127.0.0.1:8080/ws npm test
// The server should host a fresh match (e.g. -state-dir= to disable persistence).
//
// Checks the store against the server's own view.Apply: after a few hundred
// ticks of deltas with movement and battle, the view built by the store must
// equal a fresh snapshot of the same tick.

import { describe, expect, it } from "vitest";
import { fromBinary } from "@bufbuild/protobuf";
import { MatchPhase, ServerMessageSchema } from "./gen/sandtable/v1/sandtable_pb";
import { encodeClient, PROTOCOL_VERSION, Session, type SessionStorageLike } from "./net";
import { Store } from "./store";

declare const process: { env: Record<string, string | undefined> };
const url = process.env.SANDTABLE_WS;

class MemoryStorage implements SessionStorageLike {
  m = new Map<string, string>();
  getItem(k: string) {
    return this.m.get(k) ?? null;
  }
  setItem(k: string, v: string) {
    this.m.set(k, v);
  }
  removeItem(k: string) {
    this.m.delete(k);
  }
}

function until(what: string, cond: () => boolean, ms = 20_000): Promise<void> {
  return new Promise((resolve, reject) => {
    const t0 = Date.now();
    const iv = setInterval(() => {
      if (cond()) {
        clearInterval(iv);
        resolve();
      } else if (Date.now() - t0 > ms) {
        clearInterval(iv);
        reject(new Error("timed out waiting for " + what));
      }
    }, 10);
  });
}

/** Joins with a token on a raw socket and returns the store after the snapshot. */
async function freshView(faction: string, token: string): Promise<{ store: Store; ws: WebSocket }> {
  const store = new Store();
  const ws = new WebSocket(url!);
  ws.binaryType = "arraybuffer";
  ws.onopen = () =>
    ws.send(
      encodeClient({
        case: "hello",
        value: { protocolVersion: PROTOCOL_VERSION, matchId: "default", faction, playerName: "check", sessionToken: token },
      }),
    );
  ws.onmessage = (ev) => {
    const m = fromBinary(ServerMessageSchema, new Uint8Array(ev.data as ArrayBuffer));
    if (m.msg.case === "welcome") store.welcome(m.msg.value);
    if (m.msg.case === "snapshot") store.snapshot(m.msg.value);
  };
  await until("snapshot", () => store.tick >= 0);
  return { store, ws };
}

describe.skipIf(!url)("sync with a live server", () => {
  it("builds the same view from deltas as from a fresh snapshot", async () => {
    const players = ["azure", "crimson"].map((faction) => {
      const store = new Store();
      const storage = new MemoryStorage();
      const session = new Session(store, { matchId: "default", faction, playerName: faction }, { url: url!, storage });
      session.start();
      return { faction, store, storage, session };
    });
    const [a, b] = players;
    await until("both joined", () => a.store.joined && b.store.joined);
    const host = players.find((p) => p.store.isHost)!;
    if (host.store.status!.phase === MatchPhase.LOBBY) host.session.send({ case: "startMatch", value: {} });
    host.session.send({ case: "setSpeed", value: { speed: 5 } });
    if (host.store.status!.paused) host.session.send({ case: "setPause", value: { paused: false } });
    await until("running", () => a.store.status?.phase === MatchPhase.RUNNING && !a.store.status.paused);

    const key = (st: Store, k: string) => st.static!.provinces.findIndex((p) => p.key === k);
    a.session.move(2, key(a.store, "p10")); // attack
    b.session.move(8, key(b.store, "p14"));
    const start = a.store.tick;
    let sawBattle = false;
    let sawRemoval = false;
    const unsub = a.store.subscribe(() => {
      if (a.store.provinces.some((p) => p.inBattle)) sawBattle = true;
      if (a.store.lastRemoved.length > 0) sawRemoval = true;
    });
    await until("200 ticks", () => a.store.tick >= start + 200, 30_000);
    unsub();

    a.session.send({ case: "setPause", value: { paused: true } });
    for (const p of players) {
      await until("paused " + p.faction, () => !!p.store.status?.paused && p.store.tick === p.store.status.tick);
    }
    for (const p of players) {
      const token = p.store.token;
      p.session.stop(); // the fresh connection takes the slot over with the token
      const fresh = await freshView(p.faction, token);
      expect(fresh.store.tick).toBe(p.store.tick);
      expect(fresh.store.provinces).toEqual(p.store.provinces);
      expect(fresh.store.divisions).toEqual(p.store.divisions);
      fresh.ws.close();
    }
    expect(sawBattle).toBe(true);
    console.log(`checked at tick ${a.store.tick}; azure saw a removal: ${sawRemoval}`);
  }, 60_000);
});
