// The generated map, on its own server (see playwright.config.ts).
import { expect, test } from "@playwright/test";

test.skip(!!process.env.SANDTABLE_URL, "needs the generated map on its own server");
test.use({ baseURL: "http://127.0.0.1:8092" });

test("the client's cells touch exactly where the server's provinces are adjacent", async ({ page, request }) => {
  const info = await (await request.get("/api/match?id=default")).json();
  await page.goto("/");
  await page.getByLabel("이름").fill("mapper");
  await page.locator(`.faction[data-key="${info.factions[0].key}"]`).click();
  await page.getByRole("button", { name: "참가하기" }).click();
  await page.waitForFunction(() => (window as any).sandtable?.store.joined);

  const r = await page.evaluate(() => {
    const g = (window as any).sandtable;
    const st = g.store.static;
    const geo = g.geometry;
    const passable = (i: number) => st.terrains[st.provinces[i].terrain].passable;
    const key = (a: number, b: number) => (a < b ? `${a},${b}` : `${b},${a}`);
    const edges = new Set<string>(st.edges.map((e: any) => key(e.a, e.b)));
    const border = new Map<string, number>();
    for (const b of geo.borders) {
      if (b.q >= 0) border.set(key(b.p, b.q), Math.hypot(b.a.x - b.b.x, b.a.y - b.b.y));
    }
    // Every edge is a shared border on screen, and every land border longer
    // than a corner (the generator's threshold is 1 km) is an edge.
    const missing = [...edges].filter((k) => !((border.get(k) ?? 0) > 0.5));
    const extra = [...border].filter(([k, len]) => {
      const [a, b] = k.split(",").map(Number);
      return passable(a) && passable(b) && len > 1.5 && !edges.has(k);
    });
    return { provinces: st.provinces.length, states: st.states.length, edges: edges.size, missing, extra: extra.map(([k]) => k) };
  });
  console.log(`${r.provinces} provinces, ${r.states} states, ${r.edges} edges`);
  expect(r.provinces).toBeGreaterThan(1000);
  expect(r.missing).toEqual([]);
  expect(r.extra).toEqual([]);
});

test("map modes switch and a province shows its state and victory points", async ({ page, request }) => {
  const info = await (await request.get("/api/match?id=default")).json();
  await page.goto("/");
  await page.getByLabel("이름").fill("mapper");
  await page.locator(`.faction[data-key="${info.factions[1].key}"]`).click();
  await page.getByRole("button", { name: "참가하기" }).click();
  await page.waitForFunction(() => (window as any).sandtable?.store.joined);
  await page.evaluate(() => ((document.querySelector(".lobby") as HTMLElement).style.display = "none"));

  for (const label of ["지형", "주", "정치"]) {
    await page.locator(".mapmodes button", { hasText: label }).click();
    await expect(page.locator(".mapmodes button", { hasText: label })).toHaveAttribute("aria-pressed", "true");
  }
  // Zoom in on this faction's capital and click a spot in its cell that no
  // counter covers: a click on a counter would select the division instead.
  const capital = await page.evaluate(() => {
    const g = (window as any).sandtable;
    return g.store.static.faction(g.store.factionId).capital as number;
  });
  const c = await page.evaluate((p) => (window as any).sandtable.screenOf(p), capital);
  await page.mouse.move(c.x, c.y);
  for (let i = 0; i < 6; i++) await page.mouse.wheel(0, -300);
  await page.waitForTimeout(200);
  const at = await page.evaluate((cap) => {
    const g = (window as any).sandtable;
    const s = g.screenOf(cap);
    const cell = g.geometry.spacing[cap] * g.cam.scale;
    for (let r = 0.1; r <= 0.45; r += 0.05) {
      for (let k = 0; k < 16; k++) {
        const p = { x: s.x + Math.cos((k * Math.PI) / 8) * r * cell, y: s.y + Math.sin((k * Math.PI) / 8) * r * cell };
        const hit = g.probe(p.x, p.y);
        if (hit.province === cap && !hit.hit) return p;
      }
    }
    return null;
  }, capital);
  expect(at).not.toBeNull();
  await page.mouse.click(at!.x, at!.y);
  await expect(page.locator(".panel")).toContainText("주");
  await expect(page.locator(".panel")).toContainText("승점");
  await expect(page.locator(".panel")).toContainText("의 수도");
});
