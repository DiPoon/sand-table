import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { StaticDataSchema } from "./gen/sandtable/v1/sandtable_pb";
import { Camera, MapGeometry, progressRatio, towards, type Point } from "./geometry";
import { StaticInfo, type Division } from "./store";

// The sample map's 4×4 grid, 100 apart.
const grid: Point[] = [];
for (let r = 0; r < 4; r++) for (let c = 0; c < 4; c++) grid.push({ x: 100 + c * 100, y: 100 + r * 100 });

function area(poly: Point[]): number {
  let s = 0;
  for (let i = 0; i < poly.length; i++) {
    const a = poly[i];
    const b = poly[(i + 1) % poly.length];
    s += a.x * b.y - b.x * a.y;
  }
  return Math.abs(s) / 2;
}

// Deterministic pseudo-random points.
function scatter(n: number, seed: number): Point[] {
  let s = seed;
  const rnd = () => ((s = (s * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  return Array.from({ length: n }, () => ({ x: Math.round(rnd() * 1000), y: Math.round(rnd() * 600) }));
}

describe("MapGeometry", () => {
  it("cuts a grid into equal squares inside a margin of half the spacing", () => {
    const g = new MapGeometry(grid);
    expect(g.bounds).toEqual({ x0: 50, y0: 50, x1: 450, y1: 450 });
    expect(g.cells).toHaveLength(16);
    for (const c of g.cells) expect(area(c)).toBeCloseTo(10000, 6);
  });

  it("finds the province by the nearest site", () => {
    const g = new MapGeometry(grid);
    expect(g.find({ x: 100, y: 100 })).toBe(0);
    expect(g.find({ x: 149, y: 60 })).toBe(0);
    expect(g.find({ x: 151, y: 60 })).toBe(1);
    expect(g.find({ x: 420, y: 430 })).toBe(15);
    expect(g.find({ x: 30, y: 100 })).toBe(-1);
    expect(g.find({ x: 200, y: 460 })).toBe(-1);
  });

  it("lists each border once and only between cells that touch along a segment", () => {
    const g = new MapGeometry(grid);
    const shared = g.borders.filter((b) => b.q >= 0);
    const outer = g.borders.filter((b) => b.q < 0);
    // Grid neighbours only; diagonal cells meet in a point, not a segment.
    expect(shared).toHaveLength(24);
    for (const b of shared) {
      const dx = Math.abs((b.p % 4) - (b.q % 4));
      const dy = Math.abs(Math.floor(b.p / 4) - Math.floor(b.q / 4));
      expect(dx + dy).toBe(1);
      expect(b.p).toBeLessThan(b.q);
    }
    expect(outer.reduce((s, b) => s + Math.hypot(b.a.x - b.b.x, b.a.y - b.b.y), 0)).toBeCloseTo(1600, 6);
  });

  it("tiles the rectangle with irregular cells whose borders lie on both cells", () => {
    const pts = scatter(300, 7);
    const g = new MapGeometry(pts);
    const b = g.bounds;
    const total = g.cells.reduce((s, c) => s + area(c), 0);
    expect(total).toBeCloseTo((b.x1 - b.x0) * (b.y1 - b.y0), 3);
    pts.forEach((p, i) => {
      // Duplicate coordinates would share a cell; scatter() has none worth checking here.
      if (pts.findIndex((o) => o.x === p.x && o.y === p.y) === i) expect(g.find(p)).toBe(i);
    });
    for (const br of g.borders.filter((x) => x.q >= 0)) {
      const m = { x: (br.a.x + br.b.x) / 2, y: (br.a.y + br.b.y) / 2 };
      const dp = Math.hypot(m.x - pts[br.p].x, m.y - pts[br.p].y);
      const dq = Math.hypot(m.x - pts[br.q].x, m.y - pts[br.q].y);
      expect(dp).toBeCloseTo(dq, 6);
    }
  });

  it("handles a single province", () => {
    const g = new MapGeometry([{ x: 10, y: 20 }]);
    expect(g.cells[0].length).toBe(4);
    expect(g.find({ x: 10, y: 20 })).toBe(0);
  });
});

describe("progress", () => {
  const st = new StaticInfo(
    create(StaticDataSchema, {
      terrains: [
        { key: "plains", moveCost: 1000n, passable: true },
        { key: "hills", moveCost: 1500n, passable: true },
      ],
      provinces: [
        { key: "a", terrain: 0, x: 0, y: 0 },
        { key: "b", terrain: 1, x: 100, y: 0 },
      ],
      edges: [{ a: 0, b: 1, distance: 40n }],
    }),
  );
  const div = (d: Partial<Division>): Division => ({
    id: 1, faction: 1, type: 0, name: "", province: 0, strength: 1, org: 1, retreating: false,
    own: true, path: [1], progress: 0, supply: 1000, ...d,
  });

  it("is progress over distance × move cost of the next province", () => {
    // 40 km into hills (1500‰) = 60000; infantry gains 4000 a tick, so 15 ticks.
    expect(progressRatio(st, div({ progress: 4000 }))).toBeCloseTo(1 / 15);
    expect(progressRatio(st, div({ progress: 60000 }))).toBe(1);
    expect(progressRatio(st, div({ province: 1, path: [0], progress: 20000 }))).toBe(0.5);
  });

  it("is zero for divisions that stand still or are not ours", () => {
    expect(progressRatio(st, div({ path: [], progress: 4000 }))).toBe(0);
    expect(progressRatio(st, div({ own: false, progress: 4000 }))).toBe(0);
  });

  it("moves the counter at most to the border", () => {
    const a = { x: 0, y: 0 };
    const b = { x: 100, y: 50 };
    expect(towards(a, b, 0)).toEqual(a);
    expect(towards(a, b, 1)).toEqual({ x: 50, y: 25 });
    expect(towards(a, b, 0.5)).toEqual({ x: 25, y: 12.5 });
  });
});

describe("Camera", () => {
  it("round-trips between world and screen", () => {
    const c = new Camera();
    c.fit({ x0: 50, y0: 50, x1: 450, y1: 450 }, 800, 600, 0);
    expect(c.scale).toBe(1.5);
    expect(c.toScreen({ x: 250, y: 250 })).toEqual({ x: 400, y: 300 });
    const p = { x: 123, y: 321 };
    const q = c.toWorld(c.toScreen(p));
    expect(q.x).toBeCloseTo(p.x);
    expect(q.y).toBeCloseTo(p.y);
  });

  it("zooms around the cursor and pans by screen pixels", () => {
    const c = new Camera();
    c.fit({ x0: 0, y0: 0, x1: 100, y1: 100 }, 100, 100, 0);
    const under = c.toWorld({ x: 30, y: 70 });
    c.zoomAt({ x: 30, y: 70 }, 2);
    expect(c.scale).toBe(2);
    const after = c.toWorld({ x: 30, y: 70 });
    expect(after.x).toBeCloseTo(under.x);
    expect(after.y).toBeCloseTo(under.y);
    c.zoomAt({ x: 0, y: 0 }, 1000);
    expect(c.scale).toBe(c.maxScale);
    const before = c.toScreen({ x: 10, y: 10 });
    c.pan(5, -3);
    const moved = c.toScreen({ x: 10, y: 10 });
    expect(moved.x - before.x).toBeCloseTo(5);
    expect(moved.y - before.y).toBeCloseTo(-3);
  });
});
