// Join screen: player name, faction choice from /api/match, and the phase of the room.

import type { JoinInfo } from "../net";
import { factionColor } from "../render";
import { S } from "../strings";
import { MatchPhase } from "../gen/sandtable/v1/sandtable_pb";
import { el, swatch } from "./dom";

/** GET /api/match, see docs/protocol.md. */
export interface MatchInfo {
  id: string;
  phase: "LOBBY" | "RUNNING" | string;
  tick: number;
  factions: { key: string; name: string; taken: boolean; player?: string }[];
}

export async function fetchMatch(id: string, signal?: AbortSignal): Promise<MatchInfo> {
  const r = await fetch(`/api/match?id=${encodeURIComponent(id)}`, { signal, cache: "no-store" });
  if (!r.ok) throw new Error(`GET /api/match: ${r.status}`);
  return (await r.json()) as MatchInfo;
}

const NAME_KEY = "sandtable.name";
const REFRESH_MS = 2000;

export class JoinScreen {
  readonly root: HTMLElement;
  private readonly status: HTMLElement;
  private readonly list: HTMLElement;
  private readonly name: HTMLInputElement;
  private readonly button: HTMLButtonElement;
  private readonly message: HTMLElement;
  private info: MatchInfo | null = null;
  private choice = "";
  private timer: ReturnType<typeof setTimeout> | null = null;
  private abort = new AbortController();

  constructor(
    container: HTMLElement,
    private readonly matchId: string,
    private readonly onJoin: (j: JoinInfo) => void,
    message?: string,
  ) {
    this.root = el("div", "join", container);
    const card = el("form", "join-card", this.root);
    el("h1", "", card, S.appTitle);
    this.status = el("p", "join-status", card, S.loadingMatch);

    const label = el("label", "field", card);
    el("span", "", label, S.playerName);
    this.name = el("input", "", label);
    this.name.name = "player";
    this.name.placeholder = S.playerNamePlaceholder;
    this.name.maxLength = 32;
    this.name.autocomplete = "username";
    this.name.value = localStorage.getItem(NAME_KEY) ?? "";

    el("h2", "", card, S.chooseFaction);
    this.list = el("div", "factions", card);
    this.list.setAttribute("role", "radiogroup");
    this.list.setAttribute("aria-label", S.chooseFaction);

    this.message = el("p", "join-message", card);
    this.message.setAttribute("role", "alert");
    if (message) this.message.textContent = message;

    this.button = el("button", "primary", card, S.join);
    this.button.type = "submit";
    card.addEventListener("submit", (e) => {
      e.preventDefault();
      this.submit();
    });

    this.refresh();
    if (!this.name.value) this.name.focus();
  }

  private async refresh() {
    this.timer = null;
    try {
      this.info = await fetchMatch(this.matchId, this.abort.signal);
      this.render();
    } catch (err) {
      if (this.abort.signal.aborted) return;
      this.info = null;
      this.status.textContent = S.matchLoadFailed;
      this.status.classList.add("error");
      this.list.replaceChildren();
      console.warn(err);
    }
    // Keep the taken/free state current while the player decides.
    if (!this.abort.signal.aborted) this.timer = setTimeout(() => this.refresh(), REFRESH_MS);
  }

  private render() {
    const info = this.info!;
    const phase = info.phase === "LOBBY" ? MatchPhase.LOBBY : info.phase === "RUNNING" ? MatchPhase.RUNNING : MatchPhase.UNSPECIFIED;
    this.status.classList.remove("error");
    this.status.textContent = S.matchLine(info.id, S.phase[phase], info.tick);
    if (info.factions.find((f) => f.key === this.choice)?.taken) this.choice = "";
    this.list.replaceChildren();
    info.factions.forEach((f, i) => {
      const b = el("button", "faction", this.list);
      b.type = "button";
      b.setAttribute("role", "radio");
      b.setAttribute("aria-checked", String(f.key === this.choice));
      b.dataset.key = f.key;
      b.disabled = f.taken;
      swatch(factionColor(i + 1), b);
      el("span", "faction-name", b, f.name || f.key);
      el("span", "faction-state", b, f.taken ? S.takenBy(f.player || "?") : S.free);
      b.addEventListener("click", () => {
        this.choice = f.key;
        this.message.textContent = "";
        this.render();
      });
    });
  }

  private submit() {
    const name = this.name.value.trim();
    if (!name) {
      this.message.textContent = S.nameRequired;
      this.name.focus();
      return;
    }
    if (!this.choice) {
      this.message.textContent = S.factionRequired;
      return;
    }
    localStorage.setItem(NAME_KEY, name);
    this.onJoin({ matchId: this.matchId, faction: this.choice, playerName: name });
  }

  destroy() {
    this.abort.abort();
    if (this.timer) clearTimeout(this.timer);
    this.root.remove();
  }
}
