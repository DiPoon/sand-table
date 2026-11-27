import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { beforeEach, describe, expect, it } from "vitest";
import {
  DeltaSchema,
  DivisionViewSchema,
  RemoveReason,
  SnapshotSchema,
  WelcomeSchema,
  type DivisionView,
} from "./gen/sandtable/v1/sandtable_pb";
import { edgeKey, Store } from "./store";

// Round-trips through the wire format so that zero values really are absent,
// as they are when the server sends them.
function wire<T extends Parameters<typeof toBinary>[0]>(schema: T, init: Parameters<typeof create<T>>[1]) {
  return fromBinary(schema, toBinary(schema, create(schema, init)));
}

function div(init: Partial<Omit<DivisionView, "$typeName">>) {
  return create(DivisionViewSchema, init);
}

const welcome = wire(WelcomeSchema, {
  factionId: 1,
  sessionToken: "tok",
  static: {
    dataHash: new Uint8Array([0xab, 0xcd]),
    terrains: [{ key: "plains", name: "Plains", moveCost: 1000n, passable: true }],
    unitTypes: [{ key: "infantry", name: "Infantry", maxStrength: 10000, maxOrg: 1000, speed: 4n }],
    provinces: [
      { key: "p0", name: "A", x: 0, y: 0 },
      { key: "p1", name: "B", x: 100, y: 0 },
      { key: "p2", name: "C", x: 200, y: 0 },
    ],
    edges: [
      { a: 0, b: 1, distance: 40n },
      { a: 1, b: 2, distance: 40n },
    ],
    factions: [
      { key: "azure", name: "Azure", capital: 1 }, // province 0, as ID + 1 on the wire
      { key: "crimson", name: "Crimson" }, // no capital
    ],
  },
  status: { phase: 2, speed: 1, tick: 10n, hostFaction: 1 },
});

function snapshot(store: Store) {
  store.snapshot(
    wire(SnapshotSchema, {
      tick: 10n,
      provinces: [
        { id: 0, controller: 1, visible: true },
        { id: 1, controller: 1, visible: true },
        { id: 2, controller: 2, visible: false },
      ],
      divisions: [
        div({ id: 1, faction: 1, name: "1st", province: 0, strength: 9000, org: 800, own: true, path: [1], progress: 12000n, supply: 1000 }),
        div({ id: 5, faction: 2, name: "11th", province: 1, strength: 8000, org: 700 }),
      ],
    }),
  );
}

describe("Store", () => {
  let store: Store;
  beforeEach(() => {
    store = new Store();
    store.welcome(welcome);
    snapshot(store);
  });

  it("keeps static data with numbers, not bigints", () => {
    const st = store.static!;
    expect(st.dataHash).toBe("abcd");
    expect(st.terrains[0].moveCost).toBe(1000);
    expect(st.factions.map((f) => f.id)).toEqual([1, 2]);
    expect(st.distance(1, 0)).toBe(40);
    expect(st.edgeCost(0, 1)).toBe(40000);
    expect(st.distance(0, 2)).toBeUndefined();
    expect(store.factionId).toBe(1);
    expect(store.status?.tick).toBe(10);
    expect(store.isHost).toBe(true);
  });

  it("replaces the view with a snapshot", () => {
    expect(store.tick).toBe(10);
    expect(typeof store.tick).toBe("number");
    expect(store.provinces).toHaveLength(3);
    expect(store.provinces[2]).toEqual({ controller: 2, visible: false, inBattle: false });
    expect(store.divisions.map((d) => d.id)).toEqual([1, 5]);
    expect(store.division(1)?.progress).toBe(12000);
    expect(typeof store.division(1)?.progress).toBe("number");
  });

  it("adds a newly sighted division in ID order", () => {
    const ok = store.delta(
      wire(DeltaSchema, {
        tick: 11n,
        divisions: [div({ id: 3, faction: 2, name: "13th", province: 1, strength: 5000, org: 100 })],
      }),
    );
    expect(ok).toBe(true);
    expect(store.tick).toBe(11);
    expect(store.divisions.map((d) => d.id)).toEqual([1, 3, 5]);
    expect(store.division(3)?.own).toBe(false);
    expect(store.division(3)?.path).toEqual([]);
  });

  it("replaces a changed record whole, so zeroed fields do not keep old values", () => {
    // Division 1 arrived: path empty, progress 0. Neither is on the wire.
    store.delta(
      wire(DeltaSchema, {
        tick: 11n,
        divisions: [div({ id: 1, faction: 1, name: "1st", province: 1, strength: 8900, org: 790, own: true, supply: 1000 })],
      }),
    );
    const d = store.division(1)!;
    expect(d.province).toBe(1);
    expect(d.path).toEqual([]);
    expect(d.progress).toBe(0);
    expect(d.strength).toBe(8900);
  });

  it("updates province records", () => {
    store.delta(
      wire(DeltaSchema, {
        tick: 11n,
        provinces: [
          { id: 1, controller: 2, visible: true, inBattle: true },
          { id: 2, controller: 0, visible: true },
        ],
      }),
    );
    expect(store.provinces[0]).toEqual({ controller: 1, visible: true, inBattle: false });
    expect(store.provinces[1]).toEqual({ controller: 2, visible: true, inBattle: true });
    expect(store.provinces[2]).toEqual({ controller: 0, visible: true, inBattle: false });
  });

  it("removes destroyed divisions", () => {
    store.delta(wire(DeltaSchema, { tick: 11n, removed: [{ id: 5, reason: RemoveReason.DESTROYED }] }));
    expect(store.divisions.map((d) => d.id)).toEqual([1]);
    expect(store.lastRemoved).toEqual([{ id: 5, destroyed: true }]);
  });

  it("removes divisions that left sight", () => {
    store.delta(wire(DeltaSchema, { tick: 11n, removed: [{ id: 5, reason: RemoveReason.OUT_OF_SIGHT }] }));
    expect(store.divisions.map((d) => d.id)).toEqual([1]);
    expect(store.lastRemoved).toEqual([{ id: 5, destroyed: false }]);
  });

  it("removes before it inserts, like view.Apply", () => {
    store.delta(
      wire(DeltaSchema, {
        tick: 11n,
        divisions: [div({ id: 7, faction: 2, name: "x", province: 2, strength: 1 })],
        removed: [{ id: 1, reason: RemoveReason.DESTROYED }],
      }),
    );
    expect(store.divisions.map((d) => d.id)).toEqual([5, 7]);
  });

  it("accepts an empty delta that only advances the tick", () => {
    expect(store.delta(wire(DeltaSchema, { tick: 11n }))).toBe(true);
    expect(store.delta(wire(DeltaSchema, { tick: 12n }))).toBe(true);
    expect(store.tick).toBe(12);
    expect(store.divisions).toHaveLength(2);
  });

  it("refuses a delta that skips a tick and leaves the view alone", () => {
    const before = structuredClone({ p: store.provinces, d: store.divisions });
    expect(store.delta(wire(DeltaSchema, { tick: 12n, removed: [{ id: 5, reason: RemoveReason.DESTROYED }] }))).toBe(false);
    expect(store.delta(wire(DeltaSchema, { tick: 10n }))).toBe(false);
    expect(store.tick).toBe(10);
    expect({ p: store.provinces, d: store.divisions }).toEqual(before);
  });

  it("refuses a delta before any snapshot", () => {
    const fresh = new Store();
    fresh.welcome(welcome);
    expect(fresh.delta(wire(DeltaSchema, { tick: 1n }))).toBe(false);
  });

  it("tells an attack from reinforcing a province we hold", () => {
    // The 1st (own, in A) is on its way to B, which azure holds.
    store.delta(wire(DeltaSchema, { tick: 11n, provinces: [{ id: 1, controller: 1, visible: true, inBattle: true }] }));
    const d = store.division(1)!;
    expect(d.path).toEqual([1]);
    expect(store.attackTarget(d)).toBeUndefined(); // crimson attacks B; the 1st reinforces it

    store.delta(wire(DeltaSchema, { tick: 12n, provinces: [{ id: 1, controller: 2, visible: true, inBattle: true }] }));
    expect(store.attackTarget(store.division(1)!)).toBe(1); // crimson took B; the 1st attacks it

    const now = store.division(1)!;
    expect(store.attackTarget({ ...now, retreating: true })).toBeUndefined();
    expect(store.attackTarget({ ...now, own: false })).toBeUndefined();
    expect(store.attackTarget({ ...now, path: [] })).toBeUndefined();
    store.delta(wire(DeltaSchema, { tick: 13n, provinces: [{ id: 1, controller: 2, visible: true }] }));
    expect(store.attackTarget(store.division(1)!)).toBeUndefined(); // the battle is over
  });

  it("finds capitals by province", () => {
    const st = store.static!;
    expect(st.capitalOf(0)?.key).toBe("azure");
    expect(st.capitalOf(1)).toBeUndefined();
  });

  it("keeps the static object across a reconnect with the same data", () => {
    const before = store.static;
    store.welcome(welcome);
    expect(store.static).toBe(before);
  });
});

describe("edgeKey", () => {
  it("does not mix up edges of a map with more than 2^20 provinces", () => {
    const n = 1_048_579;
    expect(edgeKey(0, 1_048_578, n)).not.toBe(edgeKey(1, 2, n));
    expect(edgeKey(5, 3, n)).toBe(edgeKey(3, 5, n));
    // The largest map the loader takes still gives exact keys.
    const big = 1 << 24;
    expect(edgeKey(big - 2, big - 1, big)).toBe((big - 2) * big + big - 1);
    expect(Number.isSafeInteger(edgeKey(big - 2, big - 1, big))).toBe(true);
  });
});
