// Lobby overlay: who is here, and the start button for the host.

import { MatchPhase } from "../gen/sandtable/v1/sandtable_pb";
import { factionColor } from "../render";
import type { Store } from "../store";
import { S } from "../strings";
import { el, swatch } from "./dom";

export class Lobby {
  readonly root: HTMLElement;
  private readonly list: HTMLElement;
  private readonly hint: HTMLElement;
  private readonly start: HTMLButtonElement;

  constructor(parent: HTMLElement, onStart: () => void) {
    this.root = el("div", "lobby", parent);
    this.root.hidden = true;
    const card = el("div", "lobby-card", this.root);
    el("h2", "", card, S.lobbyHeading);
    this.list = el("ul", "lobby-players", card);
    this.hint = el("p", "lobby-hint", card);
    this.start = el("button", "primary", card, S.start);
    this.start.type = "button";
    this.start.addEventListener("click", onStart);
  }

  update(store: Store) {
    const status = store.status;
    const st = store.static;
    this.root.hidden = !status || !st || status.phase !== MatchPhase.LOBBY;
    if (this.root.hidden || !status || !st) return;
    this.list.replaceChildren();
    for (const p of status.players) {
      const li = el("li", "", this.list);
      swatch(factionColor(p.factionId), li);
      el("span", "name", li, p.name);
      const f = st.faction(p.factionId);
      el("span", "faction", li, f ? f.name || f.key : "");
      if (p.factionId === status.hostFaction) el("span", "tag", li, S.host);
      if (p.factionId === store.factionId) el("span", "tag self", li, S.you);
    }
    const host = store.isHost;
    this.start.hidden = !host;
    this.hint.textContent = host ? S.lobbyHostHint : S.lobbyWaiting;
  }
}
