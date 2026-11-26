// Small DOM helpers shared by the screens.

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  cls?: string,
  parent?: HTMLElement,
  text?: string,
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  parent?.appendChild(e);
  return e;
}

/** A coloured dot for a faction. */
export function swatch(color: string, parent?: HTMLElement): HTMLSpanElement {
  const s = el("span", "swatch", parent);
  s.style.background = color;
  return s;
}
