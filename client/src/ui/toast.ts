// Short notifications at the bottom of the map.

import { el } from "./dom";

export type ToastKind = "info" | "error";

export class Toasts {
  private readonly root: HTMLElement;

  constructor(parent: HTMLElement) {
    this.root = el("div", "toasts", parent);
    this.root.setAttribute("role", "status");
    this.root.setAttribute("aria-live", "polite");
  }

  show(text: string, kind: ToastKind = "info", ms = 4000) {
    const t = el("div", `toast ${kind}`, this.root, text);
    while (this.root.children.length > 5) this.root.firstElementChild?.remove();
    setTimeout(() => {
      t.classList.add("leaving");
      setTimeout(() => t.remove(), 300);
    }, ms);
  }
}
