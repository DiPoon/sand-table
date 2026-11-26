// Side panel: the selected divisions, or the clicked province.

import { factionColor } from "../render";
import type { Division, Store } from "../store";
import { S } from "../strings";
import { el, swatch } from "./dom";

export interface PanelActions {
  /** Select only this division. */
  selectDivision(id: number): void;
}

const num = (n: number) => n.toLocaleString("ko-KR");

export class InfoPanel {
  readonly root: HTMLElement;

  constructor(
    parent: HTMLElement,
    private readonly actions: PanelActions,
  ) {
    this.root = el("aside", "panel", parent);
    this.root.hidden = true;
  }

  update(store: Store, selected: ReadonlySet<number>, province: number) {
    const st = store.static;
    this.root.replaceChildren();
    const divs = [...selected].map((id) => store.division(id)).filter((d): d is Division => !!d);
    if (!st || (divs.length === 0 && (province < 0 || province >= st.provinces.length))) {
      this.root.hidden = true;
      return;
    }
    this.root.hidden = false;
    if (divs.length === 1) this.division(store, divs[0]);
    else if (divs.length > 1) this.divisionList(store, divs);
    else this.province(store, province);
  }

  private row(table: HTMLElement, label: string, value: string | HTMLElement) {
    el("dt", "", table, label);
    const dd = el("dd", "", table);
    if (typeof value === "string") dd.textContent = value;
    else dd.appendChild(value);
    return dd;
  }

  private bar(value: number, max: number, cls: string): HTMLElement {
    const wrap = el("span", "meter");
    el("span", "meter-text", wrap, `${num(value)} / ${num(max)}`);
    const track = el("span", `meter-track ${cls}`, wrap);
    const fill = el("span", "meter-fill", track);
    fill.style.width = `${max > 0 ? Math.max(0, Math.min(100, (value / max) * 100)) : 0}%`;
    return wrap;
  }

  private provName(store: Store, id: number): string {
    const p = store.static?.provinces[id];
    return p ? p.name || p.key : `#${id}`;
  }

  private division(store: Store, d: Division) {
    const st = store.static!;
    const type = st.unitTypes[d.type];
    const h = el("h2", "", this.root);
    swatch(factionColor(d.faction), h);
    h.append(d.name);
    const t = el("dl", "", this.root);
    this.row(t, S.unitType, type ? type.name || type.key : "?");
    this.row(t, S.location, this.provName(store, d.province));
    this.row(t, S.strength, this.bar(d.strength, type?.maxStrength ?? 0, "strength"));
    this.row(t, S.org, this.bar(d.org, type?.maxOrg ?? 0, "org"));
    if (d.own) {
      this.row(t, S.supply, `${Math.round(d.supply / 10)}%`);
      const dest = d.path.length > 0 ? this.provName(store, d.path[d.path.length - 1]) : S.holding;
      this.row(t, S.destination, dest);
    }
    const state: string[] = [];
    if (d.retreating) state.push(S.retreating);
    else if (d.own && d.path.length > 0 && store.provinces[d.path[0]]?.inBattle) {
      state.push(S.attacking(this.provName(store, d.path[0])));
    }
    if (state.length) el("p", "status-line", this.root, state.join(" · "));
  }

  private divisionList(store: Store, divs: Division[]) {
    el("h2", "", this.root, S.selectedCount(divs.length));
    const ul = el("ul", "divlist", this.root);
    for (const d of divs) this.divisionItem(store, ul, d);
  }

  private divisionItem(store: Store, ul: HTMLElement, d: Division) {
    const li = el("li", d.own ? "own" : "", ul);
    swatch(factionColor(d.faction), li);
    const type = store.static!.unitTypes[d.type];
    el("span", "name", li, d.name);
    const bars = el("span", "minibars", li);
    const s = el("span", "mini strength", bars);
    s.style.width = `${type?.maxStrength ? (100 * d.strength) / type.maxStrength : 0}%`;
    const o = el("span", "mini org", bars);
    o.style.width = `${type?.maxOrg ? (100 * d.org) / type.maxOrg : 0}%`;
    if (d.retreating) li.classList.add("retreating");
    if (d.own) {
      li.tabIndex = 0;
      li.title = d.name;
      li.addEventListener("click", () => this.actions.selectDivision(d.id));
      li.addEventListener("keydown", (e) => {
        if (e.key === "Enter") this.actions.selectDivision(d.id);
      });
    }
  }

  private province(store: Store, id: number) {
    const st = store.static!;
    const p = st.provinces[id];
    const view = store.provinces[id];
    const terrain = st.terrains[p.terrain];
    const ctl = view ? st.faction(view.controller) : undefined;
    const h = el("h2", "", this.root);
    if (terrain?.passable !== false) swatch(factionColor(view?.controller ?? 0), h);
    h.append(p.name || p.key);
    const t = el("dl", "", this.root);
    const tname = terrain ? terrain.name || terrain.key : "?";
    this.row(t, S.terrain, terrain?.passable === false ? `${tname} · ${S.impassable}` : tname);
    this.row(t, S.controller, ctl ? ctl.name || ctl.key : S.none);
    this.row(t, S.hub, p.hub ? S.yes : S.no);
    if (view && !view.visible) {
      el("p", "status-line muted", this.root, S.outOfSight);
      return; // nothing more is known about a province in the fog
    }
    if (view?.inBattle) el("p", "status-line battle", this.root, S.inBattle);
    const here = store.divisionsIn(id);
    if (here.length > 0) {
      el("h3", "", this.root, `${S.divisionsHere} ${here.length}`);
      const ul = el("ul", "divlist", this.root);
      for (const d of here) this.divisionItem(store, ul, d);
    }
  }
}
