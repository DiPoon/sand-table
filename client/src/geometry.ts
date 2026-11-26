// Map geometry derived from province coordinates: Voronoi cells clipped to a
// rectangle, borders between cells, province lookup, the camera, and where
// to draw a division that is on its way. No DOM.

import { Delaunay } from "d3-delaunay";
import type { Division, StaticInfo } from "./store";

export interface Point {
  x: number;
  y: number;
}

export interface Rect {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

/** A border segment. q is -1 when the segment is on the outer edge of the map. */
export interface Border {
  p: number;
  q: number;
  a: Point;
  b: Point;
}

export class MapGeometry {
  readonly sites: Point[];
  readonly bounds: Rect;
  /** Cell polygon per province, without the closing vertex. */
  readonly cells: Point[][];
  /** Every border once: shared ones with p < q, then the outer edge. */
  readonly borders: Border[];
  /** Distance from each site to its nearest neighbour site. */
  readonly spacing: number[];
  private readonly delaunay: Delaunay<Point>;

  constructor(points: Point[]) {
    if (points.length === 0) throw new Error("a map needs at least one province");
    this.sites = points.map((p) => ({ x: p.x, y: p.y }));
    this.delaunay = Delaunay.from(
      this.sites,
      (p) => p.x,
      (p) => p.y,
    );

    // Nearest-neighbour distances; the median sets the margin around the map.
    this.spacing = this.sites.map((s, i) => {
      let best = Infinity;
      for (const j of this.neighbors(i)) best = Math.min(best, dist(s, this.sites[j]));
      return Number.isFinite(best) ? best : 100;
    });
    const sorted = [...this.spacing].sort((a, b) => a - b);
    const margin = sorted[sorted.length >> 1] / 2;
    let x0 = Infinity,
      y0 = Infinity,
      x1 = -Infinity,
      y1 = -Infinity;
    for (const s of this.sites) {
      x0 = Math.min(x0, s.x);
      y0 = Math.min(y0, s.y);
      x1 = Math.max(x1, s.x);
      y1 = Math.max(y1, s.y);
    }
    this.bounds = { x0: x0 - margin, y0: y0 - margin, x1: x1 + margin, y1: y1 + margin };

    const voronoi = this.delaunay.voronoi([this.bounds.x0, this.bounds.y0, this.bounds.x1, this.bounds.y1]);
    this.cells = this.sites.map((_, i) => {
      const poly = voronoi.cellPolygon(i) as [number, number][] | null;
      if (!poly) return [];
      const pts = poly.map(([x, y]) => ({ x, y }));
      pts.pop(); // closing vertex
      return pts;
    });

    // For each cell edge, the cell on the other side is the neighbour whose
    // site is as far from the edge's midpoint as this cell's site is.
    const shared: Border[] = [];
    const outer: Border[] = [];
    this.cells.forEach((cell, i) => {
      const nbrs = [...voronoi.neighbors(i)].filter((j) => j >= 0 && j < this.sites.length && j !== i);
      for (let k = 0; k < cell.length; k++) {
        const a = cell[k];
        const b = cell[(k + 1) % cell.length];
        const len = dist(a, b);
        if (len < 1e-9) continue;
        const m = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
        const di = dist(m, this.sites[i]);
        let q = -1;
        let bestErr = 1e-6 * Math.max(1, di);
        for (const j of nbrs) {
          const err = Math.abs(dist(m, this.sites[j]) - di);
          if (err < bestErr) {
            bestErr = err;
            q = j;
          }
        }
        if (q < 0) outer.push({ p: i, q: -1, a, b });
        else if (i < q) shared.push({ p: i, q, a, b });
      }
    });
    this.borders = shared.concat(outer);
  }

  private neighbors(i: number): number[] {
    // d3-delaunay reports odd indices for degenerate inputs such as a single point.
    return [...this.delaunay.neighbors(i)].filter((j) => j >= 0 && j < this.sites.length && j !== i);
  }

  /** The province whose cell contains the point, or -1 outside the map. */
  find(p: Point): number {
    const b = this.bounds;
    if (p.x < b.x0 || p.x > b.x1 || p.y < b.y0 || p.y > b.y1) return -1;
    return this.delaunay.find(p.x, p.y);
  }
}

export function dist(a: Point, b: Point): number {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

export function geometryFor(st: StaticInfo): MapGeometry {
  return new MapGeometry(st.provinces);
}

/**
 * How far a division has come towards path[0], from 0 to 1. The server's
 * progress counts up to cost(current → next) = distance × move_cost(next).
 * Zero for divisions that are not moving or whose progress we cannot see.
 */
export function progressRatio(st: StaticInfo, d: Division): number {
  if (!d.own || d.path.length === 0 || d.progress <= 0) return 0;
  const cost = st.edgeCost(d.province, d.path[0]);
  if (!cost) return 0;
  return Math.min(1, d.progress / cost);
}

/**
 * Where to draw a division that is ratio of the way to the next province.
 * It is still in its own province until it arrives, so it moves at most to
 * the border, half way between the two sites.
 */
export function towards(from: Point, to: Point, ratio: number): Point {
  const t = 0.5 * Math.max(0, Math.min(1, ratio));
  return { x: from.x + (to.x - from.x) * t, y: from.y + (to.y - from.y) * t };
}

/**
 * How many of `count` counters in one province to draw when `fit` fit side by
 * side; the rest fold into a "+N" box. One counter always shows, even when the
 * box does not fit beside it, so that a zoomed-out stack can still be clicked
 * and its first division still shows where it is going.
 */
export function shownCounters(count: number, fit: number): number {
  if (count <= Math.max(1, fit)) return count;
  return Math.max(1, fit - 1);
}

/** Maps world coordinates to screen pixels: screen = (world - origin) × scale. */
export class Camera {
  x = 0;
  y = 0;
  scale = 1;
  minScale = 0.05;
  maxScale = 20;

  toScreen(p: Point): Point {
    return { x: (p.x - this.x) * this.scale, y: (p.y - this.y) * this.scale };
  }

  toWorld(p: Point): Point {
    return { x: p.x / this.scale + this.x, y: p.y / this.scale + this.y };
  }

  /** Shows the whole rectangle centred in a viewport, and sets the zoom limits around that. */
  fit(r: Rect, width: number, height: number, padding = 16) {
    const w = Math.max(1, r.x1 - r.x0);
    const h = Math.max(1, r.y1 - r.y0);
    const s = Math.max(1e-6, Math.min((width - 2 * padding) / w, (height - 2 * padding) / h));
    this.scale = s;
    this.minScale = s / 4;
    this.maxScale = s * 12;
    this.x = r.x0 + w / 2 - width / 2 / s;
    this.y = r.y0 + h / 2 - height / 2 / s;
  }

  /** Zooms by factor, keeping the world point under the screen point where it is. */
  zoomAt(screen: Point, factor: number) {
    const before = this.toWorld(screen);
    this.scale = Math.max(this.minScale, Math.min(this.maxScale, this.scale * factor));
    this.x = before.x - screen.x / this.scale;
    this.y = before.y - screen.y / this.scale;
  }

  /** Moves the view by a drag of (dx, dy) screen pixels. */
  pan(dx: number, dy: number) {
    this.x -= dx / this.scale;
    this.y -= dy / this.scale;
  }
}
