// Top bar: clock, pause, speed, players, connection.

import { MatchPhase } from "../gen/sandtable/v1/sandtable_pb";
import type { ConnState } from "../net";
import { factionColor } from "../render";
import type { Store } from "../store";
import { clock, S } from "../strings";
import { el, swatch } from "./dom";

export interface TopBarActions {
  setPause(paused: boolean): void;
  setSpeed(speed: number): void;
  leave(): void;
}

export const SPEEDS = [1, 2, 3, 4, 5];

export class TopBar {
  readonly root: HTMLElement;
  private readonly me: HTMLElement;
  private readonly clock: HTMLElement;
  private readonly pause: HTMLButtonElement;
  private readonly speeds: HTMLButtonElement[];
  private readonly players: HTMLElement;
  private readonly conn: HTMLElement;
  private shown: unknown = null; // the status object the player list was built from

  constructor(parent: HTMLElement, actions: TopBarActions) {
    this.root = el("header", "topbar", parent);
    el("span", "brand", this.root, S.appTitle);
    this.me = el("span", "me", this.root);
    this.clock = el("span", "clock", this.root);

    const controls = el("div", "controls", this.root);
    this.pause = el("button", "pause", controls);
    this.pause.type = "button";
    this.pause.addEventListener("click", () => actions.setPause(this.pause.dataset.paused !== "true"));
    const speed = el("div", "speeds", controls);
    speed.setAttribute("role", "group");
    speed.setAttribute("aria-label", S.speed);
    el("span", "label", speed, S.speed);
    this.speeds = SPEEDS.map((n) => {
      const b = el("button", "speed", speed, String(n));
      b.type = "button";
      b.addEventListener("click", () => actions.setSpeed(n));
      return b;
    });

    this.players = el("div", "players", this.root);
    this.conn = el("span", "conn", this.root);
    const leave = el("button", "leave", this.root, S.leave);
    leave.type = "button";
    leave.addEventListener("click", () => actions.leave());
    // Mouse clicks do not move the focus here, so Space keeps meaning "pause" afterwards.
    this.root.addEventListener("pointerdown", (e) => {
      if ((e.target as HTMLElement).closest("button")) e.preventDefault();
    });
  }

  update(store: Store, conn: ConnState) {
    const st = store.static;
    const status = store.status;
    const mine = st?.faction(store.factionId);
    this.me.replaceChildren();
    if (mine) {
      swatch(factionColor(mine.id), this.me);
      this.me.append(mine.name || mine.key);
    }

    const tick = store.tick >= 0 ? store.tick : (status?.tick ?? 0);
    this.clock.textContent = clock(tick);

    const running = status?.phase === MatchPhase.RUNNING;
    const paused = !!status?.paused;
    this.pause.dataset.paused = String(paused);
    this.pause.textContent = paused ? S.resume : S.pause;
    this.pause.disabled = !running || conn !== "open";
    this.pause.classList.toggle("active", paused && running);
    this.clock.classList.toggle("paused", paused && running);
    this.clock.title = paused && running ? S.paused : "";

    const host = store.isHost;
    for (const [i, b] of this.speeds.entries()) {
      b.disabled = !host || conn !== "open";
      b.title = host ? `${S.speed} ${i + 1}` : S.speedHostOnly;
      b.classList.toggle("active", status?.speed === i + 1);
      b.setAttribute("aria-pressed", String(status?.speed === i + 1));
    }

    if (status !== this.shown) {
      this.shown = status;
      this.players.replaceChildren();
      el("span", "label", this.players, S.players);
      for (const p of status?.players ?? []) {
        const chip = el("span", "player", this.players);
        swatch(factionColor(p.factionId), chip);
        chip.append(p.name);
        if (p.factionId === status?.hostFaction) el("span", "tag", chip, S.host);
        if (p.factionId === store.factionId) chip.classList.add("self");
      }
    }

    this.conn.className = `conn ${conn}`;
    this.conn.textContent = S.conn[conn];
  }
}
