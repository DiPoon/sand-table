// The web client against the real server, in order on one fresh match:
// two tabs from the join screen to a capture, mouse controls, and a refused join.
// Screenshots go to test-results/screens for a person to look at.

import { expect, type Page, test } from "@playwright/test";

type Pt = { x: number; y: number };

const SHOTS = "test-results/screens";

function g<T>(page: Page, expr: string): Promise<T> {
  return page.evaluate(`(() => { const g = window.sandtable; return ${expr}; })()`) as Promise<T>;
}

function prov(page: Page, key: string): Promise<number> {
  return g<number>(page, `g.store.static.provinces.findIndex(p => p.key === ${JSON.stringify(key)})`);
}

async function shot(page: Page, name: string) {
  await page.screenshot({ path: `${SHOTS}/${name}.png` });
}

async function joinAs(page: Page, name: string, faction: string) {
  await page.goto("/");
  const button = page.locator(`.faction[data-key="${faction}"]`);
  await expect(button).toBeEnabled();
  await page.getByLabel("이름").fill(name);
  await button.click();
  await page.getByRole("button", { name: "참가하기" }).click();
  await page.waitForFunction(() => (window as any).sandtable?.store.joined);
}

async function clickCounter(page: Page, division: number, shift = false) {
  await page.waitForFunction((id) => !!(window as any).sandtable.counterOf(id), division);
  const p = await g<Pt>(page, `g.counterOf(${division})`);
  if (shift) await page.keyboard.down("Shift");
  await page.mouse.click(p.x, p.y);
  if (shift) await page.keyboard.up("Shift");
}

async function rightClickProvince(page: Page, key: string) {
  const p = await g<Pt>(page, `g.screenOf(${await prov(page, key)})`);
  await page.mouse.click(p.x, p.y, { button: "right" });
}

/** Waits until this tab shows the paused (or running) state and the last tick is applied. */
async function settled(page: Page, paused: boolean) {
  await page.waitForFunction((v) => {
    const s = (window as any).sandtable.store;
    return s.status?.paused === v && (!v || s.tick === s.status.tick);
  }, paused);
  if (paused) await page.waitForTimeout(200); // let the last frame paint
}

async function setPaused(page: Page, paused: boolean) {
  if ((await g<boolean>(page, "g.store.status.paused")) !== paused) await page.keyboard.press("Space");
  await settled(page, paused);
}

/** Pauses, takes a screenshot of every tab, resumes. */
async function moment(name: string, host: Page, others: Record<string, Page>) {
  await setPaused(host, true);
  for (const [who, p] of Object.entries(others)) {
    await settled(p, true);
    await shot(p, `${name}-${who}`);
  }
  await setPaused(host, false);
}

test.describe.serial("web client", () => {
  test("two tabs: join, lobby, start, attack, retreat, capture, fog, reload", async ({ context }) => {
    // Tab A: the join screen lists the free factions of the room.
    const a = await context.newPage();
    await a.goto("/");
    await expect(a.locator(".join-status")).toContainText("대기실");
    await expect(a.locator(".faction")).toHaveCount(2);
    // The list refreshes every 2 seconds without replacing the buttons: focus and clicks survive it.
    const crimsonButton = await a.locator('.faction[data-key="crimson"]').elementHandle();
    await crimsonButton!.focus();
    await a.waitForTimeout(2500);
    expect(await crimsonButton!.evaluate((b) => b.isConnected && document.activeElement === b)).toBe(true);
    await shot(a, "1-join");
    await joinAs(a, "alice", "azure");
    await expect(a.locator(".lobby")).toBeVisible();
    await expect(a.locator(".lobby button.primary")).toBeVisible(); // first in: host

    // Tab B sees that azure is taken and by whom.
    const b = await context.newPage();
    await b.goto("/");
    const azure = b.locator('.faction[data-key="azure"]');
    await expect(azure).toBeDisabled();
    await expect(azure).toContainText("alice 사용 중");
    await shot(b, "2-join-taken");
    await joinAs(b, "bob", "crimson");
    await expect(b.locator(".lobby-hint")).toHaveText("호스트가 게임을 시작하기를 기다리는 중입니다.");
    await expect(b.locator(".lobby button.primary")).toBeHidden();
    await expect(a.locator(".lobby-players li")).toHaveCount(2);
    await shot(a, "3-lobby-host");
    await shot(b, "3-lobby-guest");

    // The host starts; only the host may change the speed.
    await a.locator(".lobby button.primary").click();
    await expect(a.locator(".lobby")).toBeHidden();
    await expect(b.locator(".lobby")).toBeHidden();
    await expect(b.locator(".speed").first()).toBeDisabled();
    await a.locator(".speed", { hasText: "4" }).click();
    await expect(b.locator(".speed.active")).toHaveText("4");
    await expect(a.locator(".clock")).not.toHaveText("1일차 00시");

    // Orders with the mouse: azure's 3rd Infantry attacks Larkspur, and crimson's
    // 11th Infantry falls back from Ironbridge to Marrowdale, out of azure's sight.
    const larkspur = await prov(a, "p12");
    await clickCounter(a, 3);
    await rightClickProvince(a, "p12");
    await clickCounter(b, 5);
    await rightClickProvince(b, "p13");

    await a.waitForFunction((p) => (window as any).sandtable.store.provinces[p].inBattle, larkspur);
    await moment("4-battle", a, { azure: a, crimson: b });
    await a.waitForFunction(() => (window as any).sandtable.store.divisions.some((d: any) => d.retreating));
    await moment("5-retreat", a, { azure: a, crimson: b });
    await a.waitForFunction((p) => (window as any).sandtable.store.provinces[p].controller === 1, larkspur, { timeout: 60_000 });
    await setPaused(a, true);
    await settled(b, true);
    await shot(a, "6-captured-azure");
    await shot(b, "6-captured-crimson");

    // Fog: crimson sees its 11th Infantry in Marrowdale, azure does not.
    const marrowdale = await prov(b, "p13");
    expect(await g<number>(b, "g.store.division(5)?.province")).toBe(marrowdale);
    expect(await g<boolean>(a, "!!g.store.division(5)")).toBe(false);
    expect(await g<boolean>(a, `g.store.provinces[${marrowdale}].visible`)).toBe(false);
    // Azure's rear is in crimson's fog, and still shows azure as controller.
    const ashford = await prov(b, "p01");
    expect(await g<object>(b, `g.store.provinces[${ashford}]`)).toEqual({ controller: 1, visible: false, inBattle: false });

    // Reloading a tab rejoins the same faction without the join screen.
    for (const [page, faction, name] of [
      [a, 1, "Azure League"],
      [b, 2, "Crimson Pact"],
    ] as const) {
      const tick = await g<number>(page, "g.store.tick");
      await page.reload();
      await page.waitForFunction(() => (window as any).sandtable?.store.joined);
      await expect(page.locator(".join")).toHaveCount(0);
      expect(await g<number>(page, "g.store.factionId")).toBe(faction);
      await expect(page.locator(".me")).toContainText(name);
      expect(await g<number>(page, "g.store.tick")).toBe(tick); // still paused
    }
    await expect(a.locator(".player")).toHaveCount(2);
    await shot(a, "7-reloaded-azure");
    await a.close();
    await b.close();
  });

  test("select, order with a right click, and the server moves the division", async ({ page }) => {
    // The match is running but paused: everybody left after the last test.
    await joinAs(page, "carol", "azure");
    await expect(page.locator(".lobby")).toBeHidden();
    await setPaused(page, true);

    // Left click selects an own division.
    await clickCounter(page, 2);
    expect(await g<number[]>(page, "[...g.selected]")).toEqual([2]);
    await expect(page.locator(".panel h2")).toHaveText("2nd Infantry");
    await expect(page.locator(".panel")).toContainText("Fallowfield");

    // A foreign division is not selected: its province is shown instead.
    await clickCounter(page, 6);
    expect(await g<number[]>(page, "[...g.selected]")).toEqual([]);
    await expect(page.locator(".panel h2")).toHaveText("Jadewater");
    await expect(page.locator(".panel")).toContainText("Hills");
    await expect(page.locator(".panel")).toContainText("Crimson Pact");
    await shot(page, "8-province-panel");

    // Right click on Greyhaven orders the move; the browser menu stays closed.
    await clickCounter(page, 2);
    const greyhaven = await prov(page, "p07");
    const target = await g<Pt>(page, `g.screenOf(${greyhaven})`);
    const prevented = await page.evaluate(({ x, y }) => {
      const e = new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 2 });
      document.elementFromPoint(x, y)!.dispatchEvent(e);
      return e.defaultPrevented;
    }, target);
    expect(prevented).toBe(true);
    await shot(page, "9-order-pending");

    // Orders apply on the next tick: nothing changes while paused.
    await page.waitForTimeout(500);
    expect(await g<number[]>(page, "g.store.division(2).path")).toEqual([]);
    await setPaused(page, false);
    await page.waitForFunction((p) => (window as any).sandtable.store.division(2)?.path[0] === p, greyhaven);
    await page.keyboard.press("5");
    await expect(page.locator(".speed.active")).toHaveText("5");
    await page.waitForFunction((p) => (window as any).sandtable.store.division(2)?.province === p, greyhaven, { timeout: 30_000 });
    await expect(page.locator(".panel")).toContainText("Greyhaven");
    await expect(page.locator(".panel")).toContainText("정지");
    await shot(page, "10-division-panel");

    // Shift adds to the selection. A lake has no path; the refusal is shown in Korean.
    await clickCounter(page, 4, true);
    expect((await g<number[]>(page, "[...g.selected]")).sort()).toEqual([2, 4]);
    await expect(page.locator(".panel h2")).toHaveText("사단 2개 선택");
    await rightClickProvince(page, "p11");
    await expect(page.locator(".toast.error").first()).toContainText("그곳으로 가는 경로가 없습니다");
    await shot(page, "11-rejected");

    // Both into Greyhaven. Zoomed all the way out, the stack still shows one counter beside "+1".
    await rightClickProvince(page, "p07");
    await page.waitForFunction((p) => (window as any).sandtable.store.division(4)?.province === p, greyhaven, { timeout: 30_000 });
    const center = await g<Pt>(page, `g.screenOf(${greyhaven})`);
    await page.mouse.move(center.x, center.y);
    for (let i = 0; i < 10; i++) await page.mouse.wheel(0, 400);
    await page.waitForFunction(() => {
      const g = (window as any).sandtable;
      return g.cam.scale === g.cam.minScale && (!!g.counterOf(2) || !!g.counterOf(4));
    });
    await shot(page, "11b-zoomed-out");

    // Esc clears the selection.
    await page.keyboard.press("Escape");
    expect(await g<number[]>(page, "[...g.selected]")).toEqual([]);
    await expect(page.locator(".panel")).toBeHidden();
  });

  test("a refused join goes back to the join screen", async ({ context }) => {
    const owner = await context.newPage();
    await joinAs(owner, "dave", "azure");

    // A tab that believes it holds azure, with a token the server does not know.
    const other = await context.newPage();
    await other.goto("/");
    await other.evaluate(() =>
      sessionStorage.setItem("sandtable.session", JSON.stringify({ matchId: "default", faction: "azure", playerName: "eve", token: "bogus" })),
    );
    await other.reload();
    await expect(other.locator(".join-message")).toHaveText("이미 다른 사람이 잡은 진영입니다");
    expect(await other.evaluate(() => sessionStorage.getItem("sandtable.session"))).toBeNull();
    await shot(other, "12-refused");
    // The owner was not disturbed.
    expect(await g<number>(owner, "g.store.factionId")).toBe(1);
  });
});
