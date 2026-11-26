// The game screen: map canvas plus the DOM around it, wired to one session.

import { CommandStatus, type CommandResult, MatchPhase, type Error as ServerError } from "../gen/sandtable/v1/sandtable_pb";
import { Camera, geometryFor, type MapGeometry, type Point } from "../geometry";
import { attachInput } from "../input";
import { clearSession, type ConnState, defaultURL, Session, type JoinInfo } from "../net";
import { type Highlight, Renderer } from "../render";
import { type Change, type StaticInfo, Store } from "../store";
import { S } from "../strings";
import { el } from "./dom";
import { Lobby } from "./lobby";
import { InfoPanel } from "./panel";
import { Toasts } from "./toast";
import { TopBar } from "./topbar";

export interface GameExits {
  /** The server refused us for good; go back to the join screen. */
  fatal(e: ServerError): void;
  /** The player left on purpose. */
  leave(): void;
}

/** An order sent but not yet visible in the view. */
interface Pending {
  division: number;
  dest: number;
  applyTick: number; // -1 until the server accepted it
}

export class Game {
  readonly store = new Store();
  readonly cam = new Camera();
  readonly session: Session;
  readonly root: HTMLElement;
  readonly selected = new Set<number>();
  readonly toasts: Toasts;
  private readonly canvas: HTMLCanvasElement;
  private readonly renderer: Renderer;
  private readonly panel: InfoPanel;
  private readonly topbar: TopBar;
  private readonly lobby: Lobby;
  private readonly banner: HTMLElement;
  private conn: ConnState = "connecting";
  private lost = false; // the connection dropped and we are trying to get it back
  private geo: MapGeometry | null = null;
  private mapOf: StaticInfo | null = null;
  private fitted = false;
  private hl: Highlight = { province: -1, hover: -1, selected: this.selected, pending: [] };
  private pending = new Map<number, Pending>();
  private frame = 0;
  private blink: ReturnType<typeof setTimeout> | null = null;
  private cleanup: (() => void)[] = [];

  constructor(container: HTMLElement, join: JoinInfo, exits: GameExits) {
    this.root = el("div", "game", container);
    this.topbar = new TopBar(this.root, {
      setPause: (paused) => this.session.send({ case: "setPause", value: { paused } }),
      setSpeed: (speed) => this.setSpeed(speed),
      leave: () => {
        this.session.stop();
        clearSession(sessionStorage);
        exits.leave();
      },
    });
    const wrap = el("div", "mapwrap", this.root);
    this.canvas = el("canvas", "map", wrap);
    this.renderer = new Renderer(this.canvas);
    this.panel = new InfoPanel(wrap, { selectDivision: (id) => this.selectOnly(id) });
    el("div", "hint", wrap, S.hintSelect);
    this.toasts = new Toasts(wrap);
    this.lobby = new Lobby(wrap, () => this.session.send({ case: "startMatch", value: {} }));
    this.banner = el("div", "banner", wrap, S.reconnecting);
    this.banner.setAttribute("role", "status");
    this.banner.hidden = true;

    this.session = new Session(this.store, join, { url: defaultURL(), storage: sessionStorage }, {
      commandResult: (r) => this.onCommandResult(r),
      error: (e) => this.onServerError(e),
      state: (s) => this.onConn(s),
      fatal: (e) => exits.fatal(e),
    });
    this.cleanup.push(this.store.subscribe((c) => this.onChange(c)));

    const ro = new ResizeObserver(() => this.layout());
    ro.observe(wrap);
    this.cleanup.push(() => ro.disconnect());

    this.cleanup.push(
      attachInput(this.canvas, this.cam, {
        moved: () => this.requestDraw(),
        hover: (p) => this.setHover(p),
        click: (p, shift) => this.click(p, shift),
        rightClick: (p) => this.order(p),
        key: (e) => this.key(e),
      }),
    );

    // For screenshots and end-to-end checks.
    (window as unknown as { sandtable: unknown }).sandtable = this;
    this.updateChrome();
    this.session.start();
  }

  private onChange(c: Change) {
    if (c === "welcome" && this.store.static && this.store.static !== this.mapOf) {
      this.mapOf = this.store.static;
      this.geo = geometryFor(this.store.static);
      this.renderer.setMap(this.geo, this.store.static);
      this.fitted = false;
      this.layout();
    }
    if (c === "snapshot" || c === "delta") {
      // Only own divisions that still exist stay selected.
      for (const id of this.selected) if (!this.store.division(id)?.own) this.selected.delete(id);
      for (const [seq, p] of this.pending) {
        if (p.applyTick >= 0 && this.store.tick >= p.applyTick) this.pending.delete(seq);
      }
      if (c === "snapshot") this.pending.clear(); // a reconnect: earlier results will not come
      this.syncPending();
    }
    this.updatePanel();
    this.updateChrome();
    this.requestDraw();
  }

  private onConn(s: ConnState) {
    this.conn = s;
    if (s === "closed") this.lost = true;
    if (s === "open") this.lost = false;
    this.updateChrome();
  }

  private updateChrome() {
    this.topbar.update(this.store, this.conn);
    this.lobby.update(this.store);
    this.banner.hidden = !this.lost;
  }

  private setSpeed(speed: number) {
    if (!this.store.isHost) {
      this.toasts.show(S.speedHostOnly);
      return;
    }
    this.session.send({ case: "setSpeed", value: { speed } });
  }

  // ---- Selection and orders ----

  private click(p: Point, shift: boolean) {
    if (!this.geo) return;
    const hit = this.renderer.hit(p);
    if (hit && "division" in hit && this.store.division(hit.division)?.own) {
      const id = hit.division;
      if (!shift) {
        this.selected.clear();
        this.selected.add(id);
      } else if (this.selected.has(id)) this.selected.delete(id);
      else this.selected.add(id);
      this.hl.province = -1;
    } else if (hit && "overflow" in hit) {
      // A crowded province: its panel lists every division there.
      this.selected.clear();
      this.hl.province = hit.overflow;
    } else {
      if (!shift) this.selected.clear();
      this.hl.province = this.geo.find(this.cam.toWorld(p));
    }
    this.updatePanel();
    this.requestDraw();
  }

  selectOnly(id: number) {
    if (!this.store.division(id)?.own) return;
    this.selected.clear();
    this.selected.add(id);
    this.hl.province = -1;
    this.updatePanel();
    this.requestDraw();
  }

  clearSelection() {
    this.selected.clear();
    this.hl.province = -1;
    this.updatePanel();
    this.requestDraw();
  }

  /** Right click: send every selected division to the province under the mouse. */
  private order(p: Point) {
    if (!this.geo || this.selected.size === 0) return;
    const dest = this.geo.find(this.cam.toWorld(p));
    if (dest < 0) return;
    for (const id of this.selected) {
      const seq = this.session.move(id, dest);
      if (!seq) {
        this.toasts.show(S.conn.closed, "error");
        return;
      }
      this.pending.set(seq, { division: id, dest, applyTick: -1 });
    }
    this.syncPending();
    this.requestDraw();
  }

  private onCommandResult(r: CommandResult) {
    const p = this.pending.get(r.clientSeq);
    if (r.status === CommandStatus.ACCEPTED) {
      if (p) p.applyTick = Number(r.applyTick);
      return;
    }
    // Rejected on receipt, or (rarely) later when it was applied.
    this.pending.delete(r.clientSeq);
    this.syncPending();
    this.requestDraw();
    const name = p ? this.store.division(p.division)?.name : undefined;
    const reason = S.reject[r.reason] ?? S.reject[0];
    this.toasts.show(S.commandRejected(name ? `${name} — ${reason}` : reason), "error");
  }

  private onServerError(e: ServerError) {
    this.toasts.show(S.error[e.code] ?? e.message, "error");
  }

  private syncPending() {
    this.hl.pending = [...new Set([...this.pending.values()].map((p) => p.dest))];
  }

  // ---- Keys ----

  private key(e: KeyboardEvent) {
    const status = this.store.status;
    if (e.key === "Escape") {
      this.clearSelection();
    } else if (e.code === "Space" || e.key === " ") {
      e.preventDefault();
      if (status?.phase === MatchPhase.RUNNING) this.session.send({ case: "setPause", value: { paused: !status.paused } });
    } else if (e.key.length === 1 && e.key >= "1" && e.key <= "5") {
      this.setSpeed(Number(e.key));
    }
  }

  // ---- Drawing ----

  private updatePanel() {
    this.panel.update(this.store, this.selected, this.hl.province);
  }

  private layout() {
    this.renderer.resize();
    if (this.geo && !this.fitted && this.renderer.width > 1) {
      this.cam.fit(this.geo.bounds, this.renderer.width, this.renderer.height);
      this.fitted = true;
    }
    this.draw();
  }

  private setHover(p: Point | null) {
    const id = p && this.geo ? this.geo.find(this.cam.toWorld(p)) : -1;
    const hit = p ? this.renderer.hit(p) : null;
    const own = hit && "division" in hit && this.store.division(hit.division)?.own;
    this.canvas.style.cursor = own || (hit && "overflow" in hit) ? "pointer" : this.selected.size > 0 && id >= 0 ? "crosshair" : "";
    if (id !== this.hl.hover) {
      this.hl.hover = id;
      this.requestDraw();
    }
  }

  /** Screen position of a province's site, for tests. */
  screenOf(province: number): Point | null {
    if (!this.geo) return null;
    const r = this.canvas.getBoundingClientRect();
    const s = this.cam.toScreen(this.geo.sites[province]);
    return { x: s.x + r.left, y: s.y + r.top };
  }

  /** Screen position of a division's counter as last drawn, for tests. */
  counterOf(division: number): Point | null {
    const b = this.renderer.boxOf(division);
    if (!b) return null;
    const r = this.canvas.getBoundingClientRect();
    return { x: b.x + b.w / 2 + r.left, y: b.y + b.h / 2 + r.top };
  }

  requestDraw() {
    if (this.frame) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = 0;
      this.draw();
    });
  }

  private draw() {
    const animating = this.renderer.draw(this.store, this.cam, this.hl, performance.now());
    // Battles blink; a slow timer is enough and costs nothing when there are none.
    if (animating && !this.blink) {
      this.blink = setTimeout(() => {
        this.blink = null;
        this.requestDraw();
      }, 150);
    }
  }

  destroy() {
    this.session.stop();
    for (const f of this.cleanup) f();
    if (this.frame) cancelAnimationFrame(this.frame);
    if (this.blink) clearTimeout(this.blink);
    this.root.remove();
    const w = window as unknown as { sandtable?: unknown };
    if (w.sandtable === this) delete w.sandtable;
  }
}
