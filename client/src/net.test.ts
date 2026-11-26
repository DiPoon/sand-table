import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ClientMessageSchema,
  ErrorCode,
  ServerMessageSchema,
  type ClientMessage,
} from "./gen/sandtable/v1/sandtable_pb";
import { loadSession, Session, type SessionStorageLike } from "./net";
import { Store } from "./store";

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

// Just enough of a WebSocket for Session.
class FakeSocket {
  static all: FakeSocket[] = [];
  readyState = 0;
  binaryType = "blob";
  sent: ClientMessage[] = [];
  closedWith: number | undefined;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeSocket.all.push(this);
  }
  send(data: Uint8Array) {
    this.sent.push(fromBinary(ClientMessageSchema, data));
  }
  close(code?: number) {
    this.closedWith = code;
    this.readyState = 3;
  }
  // Test side.
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  deliver(init: Parameters<typeof create<typeof ServerMessageSchema>>[1]) {
    const bytes = toBinary(ServerMessageSchema, create(ServerMessageSchema, init));
    this.onmessage?.({ data: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) });
  }
  drop() {
    this.readyState = 3;
    this.onclose?.();
  }
}

const join = { matchId: "default", faction: "azure", playerName: "alice" };
const staticData = { dataHash: new Uint8Array([1]), provinces: [{ key: "p0" }], factions: [{ key: "azure" }] };

function welcomeAndSnapshot(ws: FakeSocket, token: string, tick: bigint) {
  ws.deliver({ msg: { case: "welcome", value: { factionId: 1, sessionToken: token, static: staticData } } });
  ws.deliver({ msg: { case: "snapshot", value: { tick, provinces: [{ id: 0, controller: 1, visible: true }] } } });
}

function hello(ws: FakeSocket) {
  const m = ws.sent[0]?.msg;
  if (m?.case !== "hello") throw new Error("first message is not Hello");
  return m.value;
}

describe("Session", () => {
  let storage: MemoryStorage;
  let store: Store;
  let session: Session;
  let fatal: ErrorCode[];
  let states: string[];

  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
    storage = new MemoryStorage();
    store = new Store();
    fatal = [];
    states = [];
    session = new Session(
      store,
      join,
      { url: "ws://x/ws", storage, retryMs: 2000, socket: (u) => new FakeSocket(u) as unknown as WebSocket },
      { fatal: (e) => fatal.push(e.code), state: (s) => states.push(s) },
    );
    session.start();
  });
  afterEach(() => {
    session.stop();
    vi.useRealTimers();
  });

  it("sends Hello, then saves the session token", () => {
    const ws = FakeSocket.all[0];
    ws.open();
    const h = hello(ws);
    expect(h.protocolVersion).toBe(1);
    expect(h.faction).toBe("azure");
    expect(h.sessionToken).toBe("");
    welcomeAndSnapshot(ws, "tok1", 5n);
    expect(loadSession(storage)).toEqual({ ...join, token: "tok1" });
    expect(store.tick).toBe(5);
    expect(session.state).toBe("open");
  });

  it("rejoins with the saved token after a disconnect, every 2 seconds", () => {
    const ws = FakeSocket.all[0];
    ws.open();
    welcomeAndSnapshot(ws, "tok1", 5n);
    ws.drop();
    expect(session.state).toBe("closed");
    vi.advanceTimersByTime(1999);
    expect(FakeSocket.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(2);
    // The server is still down.
    FakeSocket.all[1].drop();
    vi.advanceTimersByTime(2000);
    expect(FakeSocket.all).toHaveLength(3);
    const ws3 = FakeSocket.all[2];
    ws3.open();
    expect(hello(ws3).sessionToken).toBe("tok1");
  });

  it("reconnects at once when a delta skips a tick", () => {
    const ws = FakeSocket.all[0];
    ws.open();
    welcomeAndSnapshot(ws, "tok1", 5n);
    ws.deliver({ msg: { case: "delta", value: { tick: 6n } } });
    expect(store.tick).toBe(6);
    expect(FakeSocket.all).toHaveLength(1);
    ws.deliver({ msg: { case: "delta", value: { tick: 8n } } });
    expect(store.tick).toBe(6);
    expect(ws.closedWith).toBe(1000);
    expect(FakeSocket.all).toHaveLength(2);
    // Messages still in flight on the old socket are ignored.
    ws.deliver({ msg: { case: "delta", value: { tick: 7n } } });
    expect(store.tick).toBe(6);
    const ws2 = FakeSocket.all[1];
    ws2.open();
    expect(hello(ws2).sessionToken).toBe("tok1");
    welcomeAndSnapshot(ws2, "tok1", 8n);
    expect(store.tick).toBe(8);
  });

  it("does not treat a quiet connection as dead", () => {
    const ws = FakeSocket.all[0];
    ws.open();
    welcomeAndSnapshot(ws, "tok1", 5n);
    vi.advanceTimersByTime(60_000); // paused: no deltas for a minute
    expect(FakeSocket.all).toHaveLength(1);
    expect(session.state).toBe("open");
  });

  it("stops and forgets the session on a fatal error", () => {
    storage.setItem("sandtable.session", JSON.stringify({ ...join, token: "old" }));
    const ws = FakeSocket.all[0];
    ws.open();
    ws.deliver({ msg: { case: "error", value: { code: ErrorCode.FACTION_TAKEN, fatal: true } } });
    ws.drop();
    expect(fatal).toEqual([ErrorCode.FACTION_TAKEN]);
    expect(loadSession(storage)).toBeNull();
    vi.advanceTimersByTime(10_000);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it("rejoins after the server shut down", () => {
    const ws = FakeSocket.all[0];
    ws.open();
    welcomeAndSnapshot(ws, "tok1", 5n);
    ws.deliver({ msg: { case: "error", value: { code: ErrorCode.SERVER_SHUTDOWN, fatal: true } } });
    ws.drop();
    expect(fatal).toEqual([]);
    vi.advanceTimersByTime(2000);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it("numbers commands", () => {
    const ws = FakeSocket.all[0];
    ws.open();
    expect(session.move(3, 9)).toBe(1);
    expect(session.move(4, 9)).toBe(2);
    const cmds = ws.sent.slice(1).map((m) => (m.msg.case === "command" ? m.msg.value : null));
    expect(cmds.map((c) => c?.clientSeq)).toEqual([1, 2]);
    expect(cmds[1]?.cmd).toEqual({ case: "moveDivision", value: expect.objectContaining({ divisionId: 4, destination: 9 }) });
  });

  it("does not send while disconnected", () => {
    expect(session.send({ case: "setPause", value: { paused: true } })).toBe(false);
  });
});
