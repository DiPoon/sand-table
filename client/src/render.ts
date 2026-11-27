// Draws the map on a Canvas 2D context from the store, the geometry and the
// camera. Everything is drawn in screen pixels so that lines and text keep
// their size at any zoom.

import { type Camera, type MapGeometry, type Point, progressRatio, shownCounters, towards } from "./geometry";
import { type Division, RIVER_LARGE, RIVER_NONE, type StaticInfo, type Store } from "./store";

// ---- Colours ----

// Fixed palette indexed by faction ID. Neighbouring entries differ in hue so
// that factions 1 and 2, 2 and 3, ... never look alike.
const FACTION_COLORS = [
  "#3567c4", // blue
  "#c8433a", // red
  "#3a8f4c", // green
  "#d8962a", // amber
  "#7e52bd", // purple
  "#23999b", // teal
  "#9a663a", // brown
  "#cc4f98", // magenta
  "#7b8a25", // olive
  "#56687f", // slate
];
const NEUTRAL = "#b5ad9c";
const WATER = "#a7cbe0";
const SEA = "#8fb6d3";
const RIVER = "#3b7cc2";
const RAIL = "#463d35";

/** What the cells are coloured by. */
export type MapMode = "political" | "terrain" | "states";

// Terrain mode colours, by terrain key. Keys not listed get NEUTRAL.
const TERRAIN_COLORS: Record<string, string> = {
  plains: "#b9cf8a",
  forest: "#6f9a5c",
  hills: "#c2ab7e",
  mountain: "#958a7e",
  marsh: "#86a088",
  urban: "#a9a5a0",
};

// States get colours that differ from their neighbours' often enough: a
// golden-angle walk around the hue circle.
function stateColor(id: number): string {
  const hue = (id * 137.508) % 360;
  return `hsl(${hue.toFixed(1)}, 38%, ${id % 2 === 0 ? 66 : 74}%)`;
}
const BACKGROUND = "#1d2228";
const INK = "#1b1d21";

export function factionColor(id: number): string {
  return id >= 1 ? FACTION_COLORS[(id - 1) % FACTION_COLORS.length] : NEUTRAL;
}

function mix(hex: string, other: string, t: number): string {
  const a = parseInt(hex.slice(1), 16);
  const b = parseInt(other.slice(1), 16);
  const ch = (s: number) => Math.round(((a >> s) & 255) * (1 - t) + ((b >> s) & 255) * t);
  return `rgb(${ch(16)}, ${ch(8)}, ${ch(0)})`;
}

/** Map fill for a controller: the faction colour lightened so that names stay readable. */
function fillColor(controller: number): string {
  return controller >= 1 ? mix(factionColor(controller), "#ffffff", 0.28) : NEUTRAL;
}

// ---- Terrain patterns ----

type PatternPainter = (c: CanvasRenderingContext2D, size: number) => void;

// Keyed by terrain key from the data. Plains are left plain on purpose;
// keys not listed here are named in small letters on the map instead.
const TERRAIN_PATTERNS: Record<string, PatternPainter | null> = {
  plains: null,
  forest(c) {
    c.fillStyle = "rgba(20, 60, 25, 0.28)";
    for (const [x, y] of [
      [5, 6],
      [17, 15],
    ]) {
      c.beginPath();
      c.moveTo(x, y - 4);
      c.lineTo(x + 3.5, y + 3);
      c.lineTo(x - 3.5, y + 3);
      c.closePath();
      c.fill();
    }
  },
  hills(c) {
    c.strokeStyle = "rgba(70, 45, 20, 0.32)";
    c.lineWidth = 1.2;
    for (const [x, y] of [
      [6, 9],
      [18, 20],
    ]) {
      c.beginPath();
      c.arc(x, y, 4, Math.PI * 1.1, Math.PI * 1.9);
      c.stroke();
    }
  },
  mountain(c) {
    c.strokeStyle = "rgba(50, 40, 35, 0.4)";
    c.lineWidth = 1.3;
    for (const [x, y] of [
      [6, 10],
      [18, 21],
    ]) {
      c.beginPath();
      c.moveTo(x - 5, y + 3);
      c.lineTo(x, y - 4);
      c.lineTo(x + 5, y + 3);
      c.stroke();
    }
  },
  urban(c) {
    c.fillStyle = "rgba(40, 40, 50, 0.22)";
    c.fillRect(3, 3, 4, 4);
    c.fillRect(9, 4, 3, 3);
    c.fillRect(15, 14, 4, 4);
    c.fillRect(21, 15, 3, 3);
  },
  sea: null,
  marsh(c) {
    c.strokeStyle = "rgba(40, 70, 50, 0.35)";
    c.lineWidth = 1;
    for (const [x, y] of [
      [5, 8],
      [16, 19],
    ]) {
      c.beginPath();
      c.moveTo(x - 4, y);
      c.lineTo(x + 4, y);
      c.moveTo(x - 2, y - 3);
      c.lineTo(x - 2, y);
      c.moveTo(x + 1, y - 4);
      c.lineTo(x + 1, y);
      c.stroke();
    }
  },
  lake(c, n) {
    c.strokeStyle = "rgba(30, 80, 130, 0.35)";
    c.lineWidth = 1.2;
    for (const y of [7, 19]) {
      c.beginPath();
      for (let x = 0; x <= n; x += 2) {
        const yy = y + Math.sin((x / n) * Math.PI * 2 + (y > 10 ? Math.PI : 0)) * 1.8;
        if (x === 0) c.moveTo(x, yy);
        else c.lineTo(x, yy);
      }
      c.stroke();
    }
  },
};

function makePattern(ctx: CanvasRenderingContext2D, paint: PatternPainter, size: number): CanvasPattern | null {
  const tile = document.createElement("canvas");
  tile.width = tile.height = size;
  const c = tile.getContext("2d")!;
  paint(c, size);
  return ctx.createPattern(tile, "repeat");
}

function fogPattern(ctx: CanvasRenderingContext2D): CanvasPattern | null {
  return makePattern(
    ctx,
    (c, n) => {
      c.strokeStyle = "rgba(0, 0, 0, 0.22)";
      c.lineWidth = 1;
      c.beginPath();
      c.moveTo(0, n);
      c.lineTo(n, 0);
      c.stroke();
    },
    8,
  );
}

// ---- Renderer ----

export interface Highlight {
  /** Province whose panel is open, or -1. */
  province: number;
  /** Province under the mouse, or -1. */
  hover: number;
  /** Selected divisions. */
  selected: ReadonlySet<number>;
  /** Destinations of orders sent but not yet in the view: acknowledged with a ring, not a path. */
  pending: readonly number[];
  mode: MapMode;
}

/** What is under a screen point: a division counter, or the "+N" box of a crowded province. */
export type Hit = { division: number } | { overflow: number };

interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
  hit: Hit;
}

const BLINK_MS = 450;

export class Renderer {
  readonly ctx: CanvasRenderingContext2D;
  width = 0; // CSS pixels
  height = 0;
  private dpr = 1;
  private geo: MapGeometry | null = null;
  private st: StaticInfo | null = null;
  private terrainPatterns: (CanvasPattern | null)[] = [];
  private fog: CanvasPattern | null = null;
  /** Median distance between neighbouring provinces, in world units. */
  private typical = 1;
  /** Counters drawn in the last frame, bottom to top, for hit testing. */
  private boxes: Box[] = [];

  constructor(readonly canvas: HTMLCanvasElement) {
    this.ctx = canvas.getContext("2d")!;
    this.fog = fogPattern(this.ctx);
  }

  setMap(geo: MapGeometry, st: StaticInfo) {
    this.geo = geo;
    this.st = st;
    const sorted = [...geo.spacing].sort((a, b) => a - b);
    this.typical = sorted[sorted.length >> 1] ?? 1;
    this.terrainPatterns = st.terrains.map((t) => {
      const p = TERRAIN_PATTERNS[t.key];
      return p ? makePattern(this.ctx, p, 24) : null;
    });
  }

  /** Matches the backing store to the element size. Returns true if it changed. */
  resize(): boolean {
    const r = this.canvas.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const w = Math.max(1, Math.round(r.width));
    const h = Math.max(1, Math.round(r.height));
    if (w === this.width && h === this.height && dpr === this.dpr) return false;
    this.width = w;
    this.height = h;
    this.dpr = dpr;
    this.canvas.width = Math.round(w * dpr);
    this.canvas.height = Math.round(h * dpr);
    return true;
  }

  private path(poly: Point[], cam: Camera) {
    const c = this.ctx;
    c.beginPath();
    poly.forEach((p, k) => {
      const s = cam.toScreen(p);
      if (k === 0) c.moveTo(s.x, s.y);
      else c.lineTo(s.x, s.y);
    });
    c.closePath();
  }

  /** Draws one frame. Returns true while something animates and wants another frame soon. */
  draw(store: Store, cam: Camera, hl: Highlight, now: number): boolean {
    const c = this.ctx;
    c.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    c.fillStyle = BACKGROUND;
    c.fillRect(0, 0, this.width, this.height);
    const geo = this.geo;
    const st = this.st;
    if (!geo || !st || store.provinces.length !== geo.cells.length) return false;

    // Patterns are anchored to the map so that they pan with it.
    const anchor = new DOMMatrix().translateSelf(-cam.x * cam.scale, -cam.y * cam.scale);

    // Cells: by mode the controller, terrain or state colour, or water; then
    // the terrain pattern; then, on the political map, the fog over land.
    // Cells are grouped by fill so that a thousand cells take a few dozen fills.
    const fills = new Map<string, Path2D>();
    const patterned = new Map<number, Path2D>();
    const fogged = new Path2D();
    const group = <K>(m: Map<K, Path2D>, k: K) => {
      let p = m.get(k);
      if (!p) m.set(k, (p = new Path2D()));
      return p;
    };
    const margin = 4;
    geo.cells.forEach((poly, i) => {
      if (poly.length === 0) return;
      const pts = poly.map((p) => cam.toScreen(p));
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
      for (const p of pts) {
        x0 = Math.min(x0, p.x);
        y0 = Math.min(y0, p.y);
        x1 = Math.max(x1, p.x);
        y1 = Math.max(y1, p.y);
      }
      if (x1 < -margin || y1 < -margin || x0 > this.width + margin || y0 > this.height + margin) return;
      const prov = st.provinces[i];
      const terrain = st.terrains[prov.terrain];
      const view = store.provinces[i];
      let color: string;
      if (terrain?.passable === false) color = terrain.key === "sea" ? SEA : WATER;
      else if (hl.mode === "terrain") color = TERRAIN_COLORS[terrain?.key ?? ""] ?? NEUTRAL;
      else if (hl.mode === "states") color = prov.state ? stateColor(prov.state) : NEUTRAL;
      else color = fillColor(view?.controller ?? 0);
      const targets = [group(fills, color)];
      if (hl.mode !== "states" && this.terrainPatterns[prov.terrain]) targets.push(group(patterned, prov.terrain));
      if (hl.mode === "political" && terrain?.passable !== false && view && !view.visible) targets.push(fogged);
      for (const t of targets) {
        pts.forEach((p, k) => (k === 0 ? t.moveTo(p.x, p.y) : t.lineTo(p.x, p.y)));
        t.closePath();
      }
    });
    for (const [color, path] of fills) {
      c.fillStyle = color;
      c.fill(path);
    }
    for (const [terrain, path] of patterned) {
      const pat = this.terrainPatterns[terrain]!;
      pat.setTransform(anchor);
      c.fillStyle = pat;
      c.fill(path);
    }
    c.fillStyle = "rgba(12, 16, 24, 0.42)";
    c.fill(fogged);
    if (this.fog) {
      this.fog.setTransform(anchor);
      c.fillStyle = this.fog;
      c.fill(fogged);
    }

    this.drawBorders(store, cam, hl.mode);
    this.drawRivers(cam);
    this.drawRailways(cam, hl.mode);

    if (hl.hover >= 0 && hl.hover < geo.cells.length) {
      this.path(geo.cells[hl.hover], cam);
      c.fillStyle = "rgba(255, 255, 255, 0.12)";
      c.fill();
    }
    if (hl.province >= 0 && hl.province < geo.cells.length) {
      this.path(geo.cells[hl.province], cam);
      c.strokeStyle = "#fff6c8";
      c.lineWidth = 2.5;
      c.setLineDash([6, 4]);
      c.stroke();
      c.setLineDash([]);
    }

    const battles = this.drawBattles(store, cam, now);
    this.drawMarkers(cam);
    this.drawLabels(store, cam, hl.mode);
    this.drawPending(cam, hl.pending);
    this.drawDivisions(store, cam, hl);
    return battles;
  }

  private drawPending(cam: Camera, provinces: readonly number[]) {
    const c = this.ctx;
    const geo = this.geo!;
    for (const id of provinces) {
      if (id < 0 || id >= geo.sites.length) continue;
      const s = cam.toScreen(geo.sites[id]);
      const r = Math.max(10, Math.min(26, geo.spacing[id] * cam.scale * 0.16));
      c.beginPath();
      c.arc(s.x, s.y, r, 0, Math.PI * 2);
      c.lineWidth = 4;
      c.strokeStyle = "rgba(15, 18, 24, 0.5)";
      c.stroke();
      c.setLineDash([5, 4]);
      c.lineWidth = 2;
      c.strokeStyle = "#fff6c8";
      c.stroke();
      c.setLineDash([]);
    }
  }

  /** The counter of a division as drawn in the last frame. */
  boxOf(division: number): { x: number; y: number; w: number; h: number } | null {
    const b = this.boxes.find((x) => "division" in x.hit && x.hit.division === division);
    return b ? { x: b.x, y: b.y, w: b.w, h: b.h } : null;
  }

  /** The counter or "+N" box under a screen point, if any. */
  hit(p: Point): Hit | null {
    for (let k = this.boxes.length - 1; k >= 0; k--) {
      const b = this.boxes[k];
      if (p.x >= b.x && p.x <= b.x + b.w && p.y >= b.y && p.y <= b.y + b.h) return b.hit;
    }
    return null;
  }

  /** Blinking outline and crossed swords on visible provinces in battle. Returns whether any. */
  private drawBattles(store: Store, cam: Camera, now: number): boolean {
    const c = this.ctx;
    const geo = this.geo!;
    const on = Math.floor(now / BLINK_MS) % 2 === 0;
    let any = false;
    store.provinces.forEach((p, i) => {
      if (!p.visible || !p.inBattle) return;
      any = true;
      this.path(geo.cells[i], cam);
      c.lineJoin = "round";
      c.strokeStyle = on ? "#ff4632" : "rgba(150, 20, 10, 0.85)";
      c.lineWidth = on ? 4 : 2.5;
      c.stroke();
      const cell = geo.spacing[i] * cam.scale;
      const s = cam.toScreen(geo.sites[i]);
      drawSwords(c, s.x + cell * 0.33, s.y - cell * 0.3, Math.max(7, Math.min(13, cell * 0.08)), on);
    });
    return any;
  }

  /** Counter size for a province cell of the given size in pixels. */
  private counterSize(cell: number): { w: number; h: number; gap: number } {
    const w = Math.round(Math.max(26, Math.min(46, cell * 0.3)));
    return { w, h: Math.round(w * 0.74), gap: 4 };
  }

  private drawDivisions(store: Store, cam: Camera, hl: Highlight) {
    const geo = this.geo!;
    const st = this.st!;
    this.boxes = [];

    // Group by province; own divisions first, so that they are the last to be folded away.
    const by = new Map<number, Division[]>();
    for (const d of store.divisions) {
      if (d.province < 0 || d.province >= geo.sites.length) continue;
      let list = by.get(d.province);
      if (!list) by.set(d.province, (list = []));
      list.push(d);
    }

    // Where each shown counter goes, before drawing anything, so that paths go under counters.
    const placed: { d: Division; x: number; y: number; w: number; h: number }[] = [];
    const folded: { province: number; x: number; y: number; w: number; h: number; n: number }[] = [];
    for (const [prov, list] of by) {
      list.sort((a, b) => Number(b.own) - Number(a.own) || a.id - b.id);
      const cell = geo.spacing[prov] * cam.scale;
      const { w, h, gap } = this.counterSize(cell);
      const fit = Math.floor((cell * 0.92 + gap) / (w + gap));
      const shown = list.slice(0, shownCounters(list.length, fit));
      const items = shown.length + (shown.length < list.length ? 1 : 0);
      const site = geo.sites[prov];
      const s = cam.toScreen(site);
      let x = s.x - (items * w + (items - 1) * gap) / 2;
      const y = s.y - h / 2;
      for (const d of shown) {
        let ox = 0;
        let oy = 0;
        const r = progressRatio(st, d);
        if (r > 0) {
          const t = cam.toScreen(towards(site, geo.sites[d.path[0]], r));
          ox = t.x - s.x;
          oy = t.y - s.y;
        }
        placed.push({ d, x: x + ox, y: y + oy, w, h });
        x += w + gap;
      }
      if (items > shown.length) folded.push({ province: prov, x, y, w, h, n: list.length - shown.length });
    }

    for (const p of placed) if (p.d.own && p.d.path.length > 0) this.drawPath(cam, p, hl.selected.has(p.d.id));
    for (const p of placed) {
      const target = store.attackTarget(p.d);
      if (target !== undefined) this.drawAttack(cam, p, target);
    }
    for (const p of placed) {
      this.drawCounter(p.d, p.x, p.y, p.w, p.h, hl.selected.has(p.d.id));
      this.boxes.push({ x: p.x, y: p.y, w: p.w, h: p.h, hit: { division: p.d.id } });
    }
    for (const f of folded) {
      this.drawFolded(f.x, f.y, f.w, f.h, f.n);
      this.boxes.push({ x: f.x, y: f.y, w: f.w, h: f.h, hit: { overflow: f.province } });
    }
  }

  /** Remaining route of an own division: counter → next → ... → destination. */
  private drawPath(cam: Camera, p: { d: Division; x: number; y: number; w: number; h: number }, selected: boolean) {
    const c = this.ctx;
    const geo = this.geo!;
    const pts: Point[] = [{ x: p.x + p.w / 2, y: p.y + p.h / 2 }];
    for (const id of p.d.path) pts.push(cam.toScreen(geo.sites[id]));
    const line = new Path2D();
    pts.forEach((q, k) => (k === 0 ? line.moveTo(q.x, q.y) : line.lineTo(q.x, q.y)));
    c.lineCap = "round";
    c.lineJoin = "round";
    c.strokeStyle = "rgba(15, 18, 24, 0.55)";
    c.lineWidth = selected ? 5 : 4;
    c.stroke(line);
    c.strokeStyle = p.d.retreating ? "rgba(255, 255, 255, 0.6)" : selected ? "#fff6c8" : "rgba(255, 255, 255, 0.85)";
    c.lineWidth = selected ? 2.5 : 1.75;
    c.setLineDash([7, 5]);
    c.stroke(line);
    c.setLineDash([]);
    const end = pts[pts.length - 1];
    c.beginPath();
    c.arc(end.x, end.y, selected ? 6 : 5, 0, Math.PI * 2);
    c.fillStyle = factionColor(p.d.faction);
    c.fill();
    c.lineWidth = 2;
    c.strokeStyle = "#ffffff";
    c.stroke();
  }

  /** Arrow from an attacking own division into the province it attacks. */
  private drawAttack(cam: Camera, p: { d: Division; x: number; y: number; w: number; h: number }, target: number) {
    const c = this.ctx;
    const geo = this.geo!;
    const from = cam.toScreen(geo.sites[p.d.province]);
    const to = cam.toScreen(geo.sites[target]);
    const dx = to.x - from.x;
    const dy = to.y - from.y;
    const len = Math.hypot(dx, dy);
    if (len < 1) return;
    const ux = dx / len;
    const uy = dy / len;
    // From the edge of the counter, which may already stand at the border, into the attacked province.
    const cx = p.x + p.w / 2;
    const cy = p.y + p.h / 2;
    const edge = Math.min(Math.abs(ux) > 1e-6 ? p.w / 2 / Math.abs(ux) : Infinity, Math.abs(uy) > 1e-6 ? p.h / 2 / Math.abs(uy) : Infinity);
    const start = { x: cx + ux * edge, y: cy + uy * edge };
    const head = Math.max(10, Math.min(18, len * 0.12));
    const along = (start.x - from.x) * ux + (start.y - from.y) * uy;
    const reach = Math.max(len * 0.68, along + head + 14);
    const tip = { x: from.x + ux * reach, y: from.y + uy * reach };
    const shaft = { x: tip.x - ux * head, y: tip.y - uy * head };
    const arrow = new Path2D();
    arrow.moveTo(start.x, start.y);
    arrow.lineTo(shaft.x, shaft.y);
    const head2 = new Path2D();
    head2.moveTo(tip.x, tip.y);
    head2.lineTo(shaft.x - uy * head * 0.6, shaft.y + ux * head * 0.6);
    head2.lineTo(shaft.x + uy * head * 0.6, shaft.y - ux * head * 0.6);
    head2.closePath();
    c.lineCap = "round";
    c.strokeStyle = INK;
    c.lineWidth = 8;
    c.stroke(arrow);
    c.lineWidth = 3;
    c.lineJoin = "round";
    c.stroke(head2);
    c.strokeStyle = "#e8392b";
    c.lineWidth = 5;
    c.stroke(arrow);
    c.fillStyle = "#e8392b";
    c.fill(head2);
  }

  private drawCounter(d: Division, x: number, y: number, w: number, h: number, selected: boolean) {
    const c = this.ctx;
    const st = this.st!;
    const type = st.unitTypes[d.type];
    const color = factionColor(d.faction);
    c.save();
    // A retreating division is drawn see-through.
    c.globalAlpha = d.retreating ? 0.45 : 1;
    if (selected) {
      c.fillStyle = "#fff6c8";
      roundRect(c, x - 4, y - 4, w + 8, h + 8, 6);
      c.fill();
    }
    c.shadowColor = "rgba(0, 0, 0, 0.45)";
    c.shadowBlur = 4;
    c.shadowOffsetY = 1;
    c.fillStyle = "#f4f1e9";
    roundRect(c, x, y, w, h, 3);
    c.fill();
    c.shadowColor = "transparent";
    c.lineWidth = 2.5;
    c.strokeStyle = color;
    c.stroke();

    // Unit type letter.
    const letter = (type?.name || type?.key || "?").charAt(0).toUpperCase();
    const pad = 3;
    const barH = Math.max(2, Math.round(h * 0.1));
    const textH = h - 2 * pad - 2 * barH - 2;
    c.fillStyle = color;
    c.font = `700 ${Math.round(textH * 0.9)}px system-ui, "Malgun Gothic", sans-serif`;
    c.textAlign = "center";
    c.textBaseline = "middle";
    c.fillText(letter, x + w / 2, y + pad + textH / 2 + 1);

    // Strength and organization bars.
    const bw = w - 2 * pad;
    const by1 = y + h - pad - 2 * barH - 1;
    const by2 = y + h - pad - barH;
    const ratio = (v: number, max: number | undefined) => (max && max > 0 ? Math.max(0, Math.min(1, v / max)) : 0);
    c.fillStyle = "#cbc4b4";
    c.fillRect(x + pad, by1, bw, barH);
    c.fillRect(x + pad, by2, bw, barH);
    c.fillStyle = "#3d9a46";
    c.fillRect(x + pad, by1, bw * ratio(d.strength, type?.maxStrength), barH);
    c.fillStyle = "#d9a21f";
    c.fillRect(x + pad, by2, bw * ratio(d.org, type?.maxOrg), barH);
    c.restore();
  }

  private drawFolded(x: number, y: number, w: number, h: number, n: number) {
    const c = this.ctx;
    c.fillStyle = "rgba(30, 34, 40, 0.88)";
    roundRect(c, x, y, w, h, 3);
    c.fill();
    c.lineWidth = 1.5;
    c.strokeStyle = "#e8ecf0";
    c.stroke();
    c.fillStyle = "#ffffff";
    c.font = `700 ${Math.round(h * 0.42)}px system-ui, "Malgun Gothic", sans-serif`;
    c.textAlign = "center";
    c.textBaseline = "middle";
    c.fillText(`+${n}`, x + w / 2, y + h / 2 + 1);
  }

  private drawBorders(store: Store, cam: Camera, mode: MapMode) {
    const c = this.ctx;
    const geo = this.geo!;
    const st = this.st!;
    const thin = new Path2D();
    const state = new Path2D();
    const coast = new Path2D();
    const thick = new Path2D();
    const passable = (i: number) => st.terrains[st.provinces[i].terrain]?.passable !== false;
    for (const b of geo.borders) {
      let target: Path2D;
      if (b.q < 0) {
        if (!passable(b.p)) continue; // the open sea at the edge of the map
        target = coast;
      } else if (passable(b.p) !== passable(b.q)) target = coast;
      else if (!passable(b.p)) {
        // Between two water cells: only a lake shore against the sea shows.
        if (st.terrains[st.provinces[b.p].terrain]?.key === st.terrains[st.provinces[b.q].terrain]?.key) continue;
        target = coast;
      } else if (store.provinces[b.p]?.controller !== store.provinces[b.q]?.controller) target = thick;
      else if (st.provinces[b.p].state !== st.provinces[b.q].state) target = state;
      else target = thin;
      const a = cam.toScreen(b.a);
      const z = cam.toScreen(b.b);
      target.moveTo(a.x, a.y);
      target.lineTo(z.x, z.y);
    }
    c.lineCap = "round";
    c.strokeStyle = "rgba(20, 20, 25, 0.22)";
    c.lineWidth = 1;
    c.stroke(thin);
    c.strokeStyle = mode === "states" ? "rgba(20, 20, 25, 0.85)" : "rgba(20, 20, 25, 0.45)";
    c.lineWidth = mode === "states" ? 2 : 1.3;
    c.stroke(state);
    c.strokeStyle = "rgba(25, 45, 70, 0.7)";
    c.lineWidth = 1.5;
    c.stroke(coast);
    c.strokeStyle = INK;
    c.lineWidth = 3;
    c.stroke(thick);
  }

  /** Rivers run along the borders of the two provinces they separate. */
  private drawRivers(cam: Camera) {
    const c = this.ctx;
    const geo = this.geo!;
    const st = this.st!;
    const small = new Path2D();
    const large = new Path2D();
    for (const b of geo.borders) {
      if (b.q < 0) continue;
      const river = st.edge(b.p, b.q)?.river ?? RIVER_NONE;
      if (river === RIVER_NONE) continue;
      const target = river === RIVER_LARGE ? large : small;
      const a = cam.toScreen(b.a);
      const z = cam.toScreen(b.b);
      target.moveTo(a.x, a.y);
      target.lineTo(z.x, z.y);
    }
    const w = Math.max(1.2, Math.min(3, cam.scale * 1.6));
    c.lineCap = "round";
    c.strokeStyle = RIVER;
    c.lineWidth = w;
    c.stroke(small);
    c.lineWidth = w * 1.8;
    c.stroke(large);
  }

  /** Railways join the centres of the provinces they connect. */
  private drawRailways(cam: Camera, mode: MapMode) {
    const c = this.ctx;
    const st = this.st!;
    const geo = this.geo!;
    const lines = [new Path2D(), new Path2D()];
    for (const e of st.edges) {
      if (e.rail <= 0) continue;
      const a = cam.toScreen(geo.sites[e.a]);
      const z = cam.toScreen(geo.sites[e.b]);
      const path = lines[Math.min(1, e.rail - 1)];
      path.moveTo(a.x, a.y);
      path.lineTo(z.x, z.y);
    }
    c.save();
    // Quiet on the political map and when zoomed out, where the network would hide the map.
    const zoom = Math.max(0.35, Math.min(1, cam.scale));
    c.globalAlpha = (mode === "political" ? 0.4 : 0.8) * (0.6 + 0.4 * zoom);
    c.lineCap = "round";
    c.strokeStyle = RAIL;
    lines.forEach((path, level) => {
      c.lineWidth = (level === 0 ? 1.6 : 2.6) * zoom;
      c.stroke(path);
    });
    // Sleepers, once there is room to see them.
    if (cam.scale > 1.2) {
      c.strokeStyle = "rgba(255, 250, 240, 0.8)";
      c.lineWidth = 1;
      c.setLineDash([2, 5]);
      lines.forEach((path) => c.stroke(path));
      c.setLineDash([]);
    }
    c.restore();
  }

  /** Capitals, victory points and supply hubs, below the counters. */
  private drawMarkers(cam: Camera) {
    const c = this.ctx;
    const geo = this.geo!;
    const st = this.st!;
    st.provinces.forEach((prov, i) => {
      const capital = st.capitalOf(i);
      if (!capital && prov.vp <= 0 && !prov.hub) return;
      const cell = geo.spacing[i] * cam.scale;
      const s = cam.toScreen(geo.sites[i]);
      const y = s.y + cell * 0.3;
      if (capital) {
        drawStar(c, s.x, y, Math.max(5, Math.min(12, cell * 0.12)));
      } else if (prov.vp > 0) {
        const r = Math.max(2.5, Math.min(7, cell * 0.06));
        c.beginPath();
        c.rect(s.x - r, y - r, 2 * r, 2 * r);
        c.fillStyle = "#ffffff";
        c.fill();
        c.lineWidth = 1.2;
        c.strokeStyle = INK;
        c.stroke();
        if (cell >= 70) {
          c.font = `700 ${Math.round(r * 1.6)}px system-ui, "Malgun Gothic", sans-serif`;
          c.textAlign = "left";
          c.textBaseline = "middle";
          c.fillStyle = INK;
          c.fillText(String(prov.vp), s.x + r + 3, y);
        }
      }
      if (prov.hub && cell >= 24) {
        const r = Math.max(3, Math.min(6, cell * 0.05));
        const hx = s.x - cell * 0.22;
        c.beginPath();
        c.moveTo(hx, y - r);
        c.lineTo(hx + r, y);
        c.lineTo(hx, y + r);
        c.lineTo(hx - r, y);
        c.closePath();
        c.fillStyle = "#2f6f4f";
        c.fill();
        c.lineWidth = 1;
        c.strokeStyle = "#ffffff";
        c.stroke();
      }
    });
  }

  private drawLabels(store: Store, cam: Camera, mode: MapMode) {
    const c = this.ctx;
    const geo = this.geo!;
    const st = this.st!;
    c.textAlign = "center";
    c.textBaseline = "middle";
    c.lineJoin = "round";
    // On the state map, state names until provinces are big enough to name.
    const cell = this.typical * cam.scale;
    if (mode === "states" && st.states.length > 0 && cell < 70) {
      for (const state of st.states) {
        const s = cam.toScreen(geo.sites[state.capital]);
        c.font = `700 ${Math.round(Math.max(10, Math.min(16, cell * 0.45)))}px system-ui, "Malgun Gothic", sans-serif`;
        c.lineWidth = 3;
        c.strokeStyle = "rgba(255, 255, 255, 0.8)";
        c.strokeText(state.name, s.x, s.y);
        c.fillStyle = INK;
        c.fillText(state.name, s.x, s.y);
      }
      return;
    }
    st.provinces.forEach((prov, i) => {
      const cell = geo.spacing[i] * cam.scale;
      if (cell < 44 || !prov.name) return;
      const s = cam.toScreen(geo.sites[i]);
      const y = s.y - cell * 0.3;
      const size = Math.round(Math.min(15, Math.max(10, cell / 9)));
      c.font = `600 ${size}px system-ui, "Malgun Gothic", sans-serif`;
      const name = prov.name;
      const w = c.measureText(name).width;
      if (w > cell * 0.95) return; // too crowded at this zoom
      c.lineWidth = 3;
      c.strokeStyle = "rgba(255, 255, 255, 0.75)";
      c.strokeText(name, s.x, y);
      c.fillStyle = store.provinces[i]?.visible === false && mode === "political" ? "#2a2d33" : INK;
      c.fillText(name, s.x, y);
      // Terrain without a pattern is named in small letters instead.
      const t = st.terrains[prov.terrain];
      if (t && !(t.key in TERRAIN_PATTERNS) && cell >= 70) {
        c.font = `${Math.max(9, size - 3)}px system-ui, "Malgun Gothic", sans-serif`;
        c.fillStyle = "rgba(30, 30, 30, 0.55)";
        c.fillText(t.name || t.key, s.x, y + size);
      }
    });
  }
}

function roundRect(c: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  c.beginPath();
  c.roundRect(x, y, w, h, r);
}

function drawSwords(c: CanvasRenderingContext2D, x: number, y: number, r: number, on: boolean) {
  c.beginPath();
  c.arc(x, y, r + 4, 0, Math.PI * 2);
  c.fillStyle = on ? "#e8392b" : "#8f1d14";
  c.fill();
  c.lineWidth = 1.5;
  c.strokeStyle = INK;
  c.stroke();
  c.strokeStyle = "#ffffff";
  c.lineCap = "round";
  for (const sgn of [1, -1]) {
    c.lineWidth = 2;
    c.beginPath();
    c.moveTo(x - sgn * r * 0.75, y + r * 0.75);
    c.lineTo(x + sgn * r * 0.75, y - r * 0.75);
    c.stroke();
    // Cross guard.
    c.lineWidth = 1.5;
    c.beginPath();
    const gx = x - sgn * r * 0.35;
    const gy = y + r * 0.35;
    c.moveTo(gx - r * 0.3, gy - sgn * r * 0.3);
    c.lineTo(gx + r * 0.3, gy + sgn * r * 0.3);
    c.stroke();
  }
}

function drawStar(c: CanvasRenderingContext2D, x: number, y: number, r: number) {
  c.beginPath();
  for (let k = 0; k < 10; k++) {
    const rr = k % 2 === 0 ? r : r * 0.45;
    const a = -Math.PI / 2 + (k * Math.PI) / 5;
    const px = x + Math.cos(a) * rr;
    const py = y + Math.sin(a) * rr;
    if (k === 0) c.moveTo(px, py);
    else c.lineTo(px, py);
  }
  c.closePath();
  c.fillStyle = "#f2c230";
  c.fill();
  c.lineWidth = 1.2;
  c.strokeStyle = INK;
  c.stroke();
}
