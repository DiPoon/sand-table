// Mouse and keyboard on the map: drag to pan, wheel to zoom, clicks and
// shortcuts handed to the game as screen points and keys.

import type { Camera, Point } from "./geometry";

export interface InputHandlers {
  /** The camera moved; redraw. */
  moved(): void;
  /** Left click without dragging. */
  click?(p: Point, shift: boolean): void;
  /** Right click. The browser menu is suppressed on the map. */
  rightClick?(p: Point): void;
  /** Mouse over the map, or null when it left. */
  hover?(p: Point | null): void;
  /** A key pressed while no text field has focus. */
  key?(e: KeyboardEvent): void;
}

const DRAG_THRESHOLD = 4; // px before a press becomes a drag

export function attachInput(canvas: HTMLCanvasElement, cam: Camera, h: InputHandlers): () => void {
  let press: { id: number; x: number; y: number; button: number; dragging: boolean } | null = null;

  const local = (e: MouseEvent): Point => {
    const r = canvas.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  const down = (e: PointerEvent) => {
    if (e.button !== 0 && e.button !== 1) return;
    if (e.button === 1) e.preventDefault(); // no autoscroll
    press = { id: e.pointerId, x: e.clientX, y: e.clientY, button: e.button, dragging: false };
    canvas.setPointerCapture(e.pointerId);
  };

  const move = (e: PointerEvent) => {
    if (press && press.id === e.pointerId) {
      const dx = e.clientX - press.x;
      const dy = e.clientY - press.y;
      if (!press.dragging && Math.hypot(dx, dy) >= DRAG_THRESHOLD) {
        press.dragging = true;
        canvas.style.cursor = "grabbing";
      }
      if (press.dragging) {
        cam.pan(dx, dy);
        press.x = e.clientX;
        press.y = e.clientY;
        h.moved();
      }
      return;
    }
    h.hover?.(local(e));
  };

  const up = (e: PointerEvent) => {
    if (!press || press.id !== e.pointerId) return;
    const p = press;
    press = null;
    canvas.releasePointerCapture(e.pointerId);
    canvas.style.cursor = "";
    if (!p.dragging && p.button === 0) h.click?.(local(e), e.shiftKey);
  };

  const cancel = () => {
    press = null;
    canvas.style.cursor = "";
  };

  const leave = () => {
    if (!press) h.hover?.(null);
  };

  const wheel = (e: WheelEvent) => {
    e.preventDefault();
    // Line-based deltas (some mice) are much smaller numbers than pixel deltas.
    const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
    cam.zoomAt(local(e), Math.exp(-dy * 0.0015));
    h.moved();
  };

  const menu = (e: MouseEvent) => {
    e.preventDefault();
    h.rightClick?.(local(e));
  };

  const key = (e: KeyboardEvent) => {
    const t = e.target as HTMLElement | null;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
    // A focused button activates itself on Space and Enter.
    if (t?.tagName === "BUTTON" && (e.key === " " || e.key === "Enter")) return;
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    h.key?.(e);
  };

  canvas.addEventListener("pointerdown", down);
  canvas.addEventListener("pointermove", move);
  canvas.addEventListener("pointerup", up);
  canvas.addEventListener("pointercancel", cancel);
  canvas.addEventListener("pointerleave", leave);
  canvas.addEventListener("wheel", wheel, { passive: false });
  canvas.addEventListener("contextmenu", menu);
  window.addEventListener("keydown", key);
  return () => {
    canvas.removeEventListener("pointerdown", down);
    canvas.removeEventListener("pointermove", move);
    canvas.removeEventListener("pointerup", up);
    canvas.removeEventListener("pointercancel", cancel);
    canvas.removeEventListener("pointerleave", leave);
    canvas.removeEventListener("wheel", wheel);
    canvas.removeEventListener("contextmenu", menu);
    window.removeEventListener("keydown", key);
  };
}
