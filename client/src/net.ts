// WebSocket connection to the server: Protobuf encoding, the join handshake,
// and reconnection. Messages are applied to a Store; the rest goes to events.
// See docs/protocol.md.

import { create, fromBinary, type MessageInitShape, toBinary } from "@bufbuild/protobuf";
import {
  ClientMessageSchema,
  type CommandResult,
  ErrorCode,
  type Error as ServerError,
  ServerMessageSchema,
  type ServerMessage,
} from "./gen/sandtable/v1/sandtable_pb";
import type { Store } from "./store";

export const PROTOCOL_VERSION = 1;
export const RETRY_MS = 2000;

/** The oneof of ClientMessage, e.g. {case: "setPause", value: {paused: true}}. */
export type ClientMsg = NonNullable<MessageInitShape<typeof ClientMessageSchema>["msg"]>;

export function encodeClient(msg: ClientMsg): Uint8Array<ArrayBuffer> {
  // toBinary always allocates a fresh ArrayBuffer.
  return toBinary(ClientMessageSchema, create(ClientMessageSchema, { msg })) as Uint8Array<ArrayBuffer>;
}

export function decodeServer(data: ArrayBuffer | Uint8Array): ServerMessage {
  return fromBinary(ServerMessageSchema, data instanceof Uint8Array ? data : new Uint8Array(data));
}

/** URL of the WebSocket endpoint on the server that served the page. */
export function defaultURL(loc: { protocol: string; host: string } = location): string {
  return `${loc.protocol === "https:" ? "wss" : "ws"}://${loc.host}/ws`;
}

export interface JoinInfo {
  matchId: string;
  faction: string; // faction key
  playerName: string;
}

/** What survives a page reload: who we joined as and the token that proves it. */
export interface SavedSession extends JoinInfo {
  token: string;
}

export interface SessionStorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const STORAGE_KEY = "sandtable.session";

export function loadSession(storage: SessionStorageLike): SavedSession | null {
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const s = JSON.parse(raw) as Partial<SavedSession>;
    if (typeof s.matchId !== "string" || typeof s.faction !== "string" || typeof s.playerName !== "string") return null;
    return { matchId: s.matchId, faction: s.faction, playerName: s.playerName, token: s.token ?? "" };
  } catch {
    return null;
  }
}

export function saveSession(storage: SessionStorageLike, s: SavedSession) {
  storage.setItem(STORAGE_KEY, JSON.stringify(s));
}

export function clearSession(storage: SessionStorageLike) {
  storage.removeItem(STORAGE_KEY);
}

export type ConnState = "connecting" | "open" | "closed";

export interface SessionEvents {
  /** Connection state changed. Only WebSocket events decide this: no deltas arrive while paused. */
  state?(s: ConnState): void;
  commandResult?(r: CommandResult): void;
  /** A non-fatal error; the connection stays open. */
  error?(e: ServerError): void;
  /** The server refused us for good (faction taken, unknown match, ...). The session has stopped. */
  fatal?(e: ServerError): void;
  /** Every decoded message, after the store applied it. */
  message?(m: ServerMessage): void;
}

// Fatal errors after which joining again with the same Hello makes sense.
const RETRYABLE = new Set([ErrorCode.SLOW_CONSUMER, ErrorCode.SERVER_SHUTDOWN]);

/**
 * One player's session: keeps a connection open, rejoins with the session
 * token after any disconnect, and reconnects when a delta skips a tick.
 */
export class Session {
  private ws: WebSocket | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private fatalError: ServerError | null = null;
  private seq = 0;
  state: ConnState = "closed";

  constructor(
    private readonly store: Store,
    private readonly join: JoinInfo,
    private readonly opts: {
      url: string;
      storage: SessionStorageLike;
      retryMs?: number;
      socket?: (url: string) => WebSocket;
    },
    private readonly events: SessionEvents = {},
  ) {}

  start() {
    this.stopped = false;
    this.connect();
  }

  /** Closes the connection and stops reconnecting. */
  stop() {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    const ws = this.ws;
    this.ws = null;
    ws?.close(1000);
    this.setState("closed");
  }

  /** Sends a message if connected. Returns false when it could not be sent. */
  send(msg: ClientMsg): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    this.ws.send(encodeClient(msg));
    return true;
  }

  /** Sends MoveDivision with the next client_seq. Returns the seq, or 0 if not sent. */
  move(division: number, destination: number): number {
    const seq = ++this.seq;
    const sent = this.send({
      case: "command",
      value: { clientSeq: seq, cmd: { case: "moveDivision", value: { divisionId: division, destination } } },
    });
    return sent ? seq : 0;
  }

  private setState(s: ConnState) {
    if (this.state === s) return;
    this.state = s;
    this.events.state?.(s);
  }

  private token(): string {
    const saved = loadSession(this.opts.storage);
    if (saved && saved.matchId === this.join.matchId && saved.faction === this.join.faction) return saved.token;
    return "";
  }

  private connect() {
    if (this.stopped) return;
    this.timer = null;
    this.fatalError = null;
    this.setState("connecting");
    const ws = this.opts.socket ? this.opts.socket(this.opts.url) : new WebSocket(this.opts.url);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    ws.onopen = () => {
      if (ws !== this.ws) return;
      ws.send(
        encodeClient({
          case: "hello",
          value: {
            protocolVersion: PROTOCOL_VERSION,
            matchId: this.join.matchId,
            faction: this.join.faction,
            playerName: this.join.playerName,
            sessionToken: this.token(),
          },
        }),
      );
    };
    ws.onmessage = (ev) => {
      if (ws !== this.ws || !(ev.data instanceof ArrayBuffer)) return;
      let m: ServerMessage;
      try {
        m = decodeServer(ev.data);
      } catch (err) {
        console.warn("undecodable message, reconnecting", err);
        this.reconnectNow();
        return;
      }
      this.handle(m);
    };
    ws.onclose = () => {
      if (ws !== this.ws) return;
      this.ws = null;
      this.closed();
    };
    // onerror is always followed by onclose.
  }

  private handle(m: ServerMessage) {
    const s = this.store;
    switch (m.msg.case) {
      case "welcome":
        s.welcome(m.msg.value);
        this.setState("open");
        saveSession(this.opts.storage, { ...this.join, token: m.msg.value.sessionToken });
        break;
      case "snapshot":
        s.snapshot(m.msg.value);
        break;
      case "delta":
        if (!s.delta(m.msg.value)) {
          console.warn(`delta for tick ${m.msg.value.tick} after tick ${s.tick}: reconnecting`);
          this.reconnectNow();
          return;
        }
        break;
      case "matchStatus":
        s.setStatus(m.msg.value);
        break;
      case "commandResult":
        this.events.commandResult?.(m.msg.value);
        break;
      case "error":
        if (m.msg.value.fatal) this.fatalError = m.msg.value; // the server closes the connection next
        else this.events.error?.(m.msg.value);
        break;
    }
    this.events.message?.(m);
  }

  private reconnectNow() {
    const ws = this.ws;
    this.ws = null;
    ws?.close(1000);
    this.setState("closed");
    this.connect();
  }

  private closed() {
    this.setState("closed");
    const f = this.fatalError;
    if (f && !RETRYABLE.has(f.code)) {
      this.stopped = true;
      clearSession(this.opts.storage);
      this.events.fatal?.(f);
      return;
    }
    if (this.stopped) return;
    this.timer = setTimeout(() => this.connect(), this.opts.retryMs ?? RETRY_MS);
  }
}
