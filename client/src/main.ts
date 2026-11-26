// Screen switching: join screen → game. A tab that already joined (the
// session token is in sessionStorage) goes straight back into the game.

import "./style.css";
import { loadSession, type JoinInfo } from "./net";
import { S } from "./strings";
import { Game } from "./ui/game";
import { JoinScreen } from "./ui/join";

const app = document.getElementById("app")!;
const matchId = new URLSearchParams(location.search).get("match") || "default";
let screen: Game | JoinScreen | null = null;

function showJoin(message?: string) {
  screen?.destroy();
  screen = new JoinScreen(app, matchId, play, message);
}

function play(join: JoinInfo) {
  screen?.destroy();
  screen = new Game(app, join, {
    fatal: (e) => showJoin(S.error[e.code] ?? e.message),
    leave: () => showJoin(),
  });
}

const saved = loadSession(sessionStorage);
if (saved && saved.matchId === matchId) play(saved);
else showJoin();
