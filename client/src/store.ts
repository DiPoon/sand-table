// Static data and the current view of one player. It follows the server by
// applying Snapshot and Delta messages exactly like view.Apply on the server
// (server/internal/view/view.go). It does not know about the DOM.
//
// Wire integers that arrive as bigint (uint64 tick, int64 costs and progress)
// are converted to number here; everything after the store uses number only.

import type {
  Delta,
  DivisionView,
  MatchStatus,
  ProvinceView,
  Snapshot,
  StaticData,
  Welcome,
} from "./gen/sandtable/v1/sandtable_pb";
import { type MatchPhase, RemoveReason } from "./gen/sandtable/v1/sandtable_pb";

export interface Terrain {
  key: string;
  name: string;
  moveCost: number; // permille
  defenseBonus: number;
  combatWidth: number;
  passable: boolean;
}

export interface UnitType {
  key: string;
  name: string;
  maxStrength: number;
  maxOrg: number;
  attack: number;
  defense: number;
  speed: number; // km per tick
  orgRecovery: number;
  width: number;
  reinforceChance: number;
}

export interface Province {
  id: number; // index in StaticInfo.provinces
  key: string;
  name: string;
  terrain: number;
  x: number;
  y: number;
  hub: boolean;
}

export interface Edge {
  a: number;
  b: number;
  distance: number; // km
}

export interface Faction {
  id: number; // index + 1; 0 means none
  key: string;
  name: string;
}

export class StaticInfo {
  readonly dataHash: string;
  readonly terrains: Terrain[];
  readonly unitTypes: UnitType[];
  readonly provinces: Province[];
  readonly edges: Edge[];
  readonly factions: Faction[];
  private readonly dist = new Map<number, number>();

  constructor(s: StaticData) {
    this.dataHash = Array.from(s.dataHash, (b) => b.toString(16).padStart(2, "0")).join("");
    this.terrains = s.terrains.map((t) => ({
      key: t.key,
      name: t.name,
      moveCost: Number(t.moveCost),
      defenseBonus: Number(t.defenseBonus),
      combatWidth: Number(t.combatWidth),
      passable: t.passable,
    }));
    this.unitTypes = s.unitTypes.map((u) => ({
      key: u.key,
      name: u.name,
      maxStrength: u.maxStrength,
      maxOrg: u.maxOrg,
      attack: Number(u.attack),
      defense: Number(u.defense),
      speed: Number(u.speed),
      orgRecovery: Number(u.orgRecovery),
      width: Number(u.width),
      reinforceChance: Number(u.reinforceChance),
    }));
    this.provinces = s.provinces.map((p, i) => ({
      id: i,
      key: p.key,
      name: p.name,
      terrain: p.terrain,
      x: p.x,
      y: p.y,
      hub: p.hub,
    }));
    this.edges = s.edges.map((e) => ({ a: e.a, b: e.b, distance: Number(e.distance) }));
    this.factions = s.factions.map((f, i) => ({ id: i + 1, key: f.key, name: f.name }));
    for (const e of this.edges) {
      this.dist.set(this.edgeKey(e.a, e.b), e.distance);
    }
  }

  private edgeKey(a: number, b: number): number {
    return a < b ? a * 0x100000 + b : b * 0x100000 + a;
  }

  /** Distance in km between two adjacent provinces, or undefined if they are not adjacent. */
  distance(a: number, b: number): number | undefined {
    return this.dist.get(this.edgeKey(a, b));
  }

  /** Cost of the edge a→b as the server computes it: distance × move_cost of b. */
  edgeCost(a: number, b: number): number | undefined {
    const d = this.distance(a, b);
    const t = this.terrains[this.provinces[b]?.terrain ?? -1];
    if (d === undefined || !t) return undefined;
    return d * t.moveCost;
  }

  faction(id: number): Faction | undefined {
    return id >= 1 ? this.factions[id - 1] : undefined;
  }
}

export interface ProvinceState {
  controller: number; // faction ID, 0 = none
  visible: boolean;
  inBattle: boolean; // only meaningful when visible
}

export interface Division {
  id: number;
  faction: number;
  type: number;
  name: string;
  province: number;
  strength: number;
  org: number;
  retreating: boolean;
  // Own divisions only; empty or zero for others.
  own: boolean;
  path: number[]; // remaining provinces, path[0] is the next one
  progress: number;
  supply: number; // permille
}

export interface Player {
  factionId: number;
  name: string;
}

export interface Status {
  phase: MatchPhase;
  paused: boolean;
  speed: number;
  tick: number;
  hostFaction: number; // 0 = nobody connected
  players: Player[];
}

export type Change = "welcome" | "snapshot" | "delta" | "status";

function provinceFrom(p: ProvinceView): ProvinceState {
  return { controller: p.controller, visible: p.visible, inBattle: p.inBattle };
}

// A record is always complete: proto3 leaves zero values off the wire, so a
// missing field means zero, never "unchanged".
function divisionFrom(d: DivisionView): Division {
  return {
    id: d.id,
    faction: d.faction,
    type: d.type,
    name: d.name,
    province: d.province,
    strength: d.strength,
    org: d.org,
    retreating: d.retreating,
    own: d.own,
    path: [...d.path],
    progress: Number(d.progress),
    supply: d.supply,
  };
}

export function statusFrom(s: MatchStatus): Status {
  return {
    phase: s.phase,
    paused: s.paused,
    speed: s.speed,
    tick: Number(s.tick),
    hostFaction: s.hostFaction,
    players: s.players.map((p) => ({ factionId: p.factionId, name: p.name })),
  };
}

/** Index of the division with this ID in a list sorted by ID, or the insertion point as ~index. */
function search(list: Division[], id: number): number {
  let lo = 0;
  let hi = list.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    const x = list[mid].id;
    if (x === id) return mid;
    if (x < id) lo = mid + 1;
    else hi = mid;
  }
  return ~lo;
}

export class Store {
  static: StaticInfo | null = null;
  factionId = 0;
  token = "";
  status: Status | null = null;
  /** Tick of the current view; -1 until the first snapshot after a Welcome. */
  tick = -1;
  provinces: ProvinceState[] = [];
  /** Ascending ID. */
  divisions: Division[] = [];
  /** Divisions removed by the last delta, with whether the viewer saw them die. */
  lastRemoved: { id: number; destroyed: boolean }[] = [];

  private listeners = new Set<(c: Change) => void>();

  subscribe(f: (c: Change) => void): () => void {
    this.listeners.add(f);
    return () => this.listeners.delete(f);
  }

  private emit(c: Change) {
    for (const f of this.listeners) f(c);
  }

  get joined(): boolean {
    return this.static !== null && this.tick >= 0;
  }

  get isHost(): boolean {
    return this.status !== null && this.factionId !== 0 && this.status.hostFaction === this.factionId;
  }

  welcome(w: Welcome) {
    if (!w.static) throw new Error("Welcome without static data");
    // Keep the object when the data is the same so that derived geometry survives a reconnect.
    const next = new StaticInfo(w.static);
    if (!this.static || this.static.dataHash !== next.dataHash) this.static = next;
    this.factionId = w.factionId;
    this.token = w.sessionToken;
    this.status = w.status ? statusFrom(w.status) : null;
    // The old view stays drawn until the snapshot that always follows replaces it.
    this.emit("welcome");
  }

  snapshot(s: Snapshot) {
    const provinces: ProvinceState[] = new Array(s.provinces.length);
    for (const p of s.provinces) provinces[p.id] = provinceFrom(p);
    this.provinces = provinces;
    this.divisions = s.divisions.map(divisionFrom).sort((a, b) => a.id - b.id);
    this.tick = Number(s.tick);
    this.lastRemoved = [];
    this.emit("snapshot");
  }

  /**
   * Applies a delta. Returns false without changing anything when the delta
   * does not follow the current tick; the caller must then reconnect.
   */
  delta(d: Delta): boolean {
    const tick = Number(d.tick);
    if (this.tick < 0 || tick !== this.tick + 1) return false;
    this.tick = tick;
    for (const p of d.provinces) this.provinces[p.id] = provinceFrom(p);
    if (d.removed.length > 0) {
      const gone = new Set(d.removed.map((r) => r.id));
      this.divisions = this.divisions.filter((x) => !gone.has(x.id));
    }
    for (const x of d.divisions) {
      const nd = divisionFrom(x);
      const i = search(this.divisions, nd.id);
      if (i >= 0) this.divisions[i] = nd;
      else this.divisions.splice(~i, 0, nd);
    }
    this.lastRemoved = d.removed.map((r) => ({ id: r.id, destroyed: r.reason === RemoveReason.DESTROYED }));
    this.emit("delta");
    return true;
  }

  setStatus(s: MatchStatus) {
    this.status = statusFrom(s);
    this.emit("status");
  }

  division(id: number): Division | undefined {
    const i = search(this.divisions, id);
    return i >= 0 ? this.divisions[i] : undefined;
  }

  divisionsIn(province: number): Division[] {
    return this.divisions.filter((d) => d.province === province);
  }

  /**
   * The province an own division is attacking: the next one on its path, if a
   * battle goes on there and the division's faction does not hold it. Control
   * changes when a division arrives, so in a province we hold the defenders
   * are ours, and a division moving in reinforces them.
   */
  attackTarget(d: Division): number | undefined {
    if (!d.own || d.retreating || d.path.length === 0) return undefined;
    const next = d.path[0];
    const p = this.provinces[next];
    return p?.inBattle && p.controller !== d.faction ? next : undefined;
  }
}
