// Every string the player sees. Code, comments and identifiers stay English.

import { ErrorCode, MatchPhase, RejectReason } from "./gen/sandtable/v1/sandtable_pb";

/** Tick → "N일차 HH시". One tick is one hour of game time; day 1 starts at tick 0. */
export function clock(tick: number): string {
  const t = Math.max(0, tick);
  const day = Math.floor(t / 24) + 1;
  const hour = t % 24;
  return `${day}일차 ${String(hour).padStart(2, "0")}시`;
}

export const S = {
  appTitle: "sandtable",

  // Join screen
  joinHeading: "참가",
  playerName: "이름",
  playerNamePlaceholder: "플레이어 이름",
  chooseFaction: "진영 선택",
  join: "참가하기",
  joining: "접속 중…",
  takenBy: (name: string) => `${name} 사용 중`,
  free: "비어 있음",
  matchLine: (id: string, phase: string, tick: number) => `방 ${id} · ${phase} · ${clock(tick)}`,
  loadingMatch: "방 정보를 불러오는 중…",
  matchLoadFailed: "방 정보를 불러오지 못했습니다. 서버가 켜져 있는지 확인하세요.",
  retry: "다시 시도",
  nameRequired: "이름을 입력하세요.",
  factionRequired: "진영을 고르세요.",

  phase: {
    [MatchPhase.UNSPECIFIED]: "알 수 없음",
    [MatchPhase.LOBBY]: "대기실",
    [MatchPhase.RUNNING]: "진행 중",
  } as Record<MatchPhase, string>,

  // Lobby
  lobbyHeading: "대기실",
  lobbyWaiting: "호스트가 게임을 시작하기를 기다리는 중입니다.",
  lobbyHostHint: "모두 모이면 시작하세요.",
  start: "시작",
  host: "호스트",
  you: "나",

  // Top bar
  pause: "일시정지",
  resume: "재개",
  paused: "일시정지됨",
  speed: "배속",
  speedHostOnly: "배속은 호스트만 바꿀 수 있습니다",
  players: "참가자",
  leave: "나가기",
  conn: {
    connecting: "연결 중…",
    open: "연결됨",
    closed: "연결 끊김",
  },
  reconnecting: "서버와 연결이 끊겼습니다. 2초마다 다시 연결합니다…",

  // Panels
  province: "프로빈스",
  terrain: "지형",
  impassable: "통행 불가",
  controller: "통제",
  hub: "거점",
  yes: "예",
  no: "아니요",
  none: "없음",
  outOfSight: "시야 밖",
  inBattle: "전투 중",
  divisionsHere: "사단",
  division: "사단",
  unitType: "유형",
  strength: "병력",
  org: "조직력",
  supply: "보급",
  location: "위치",
  destination: "목적지",
  holding: "정지",
  retreating: "후퇴 중",
  attacking: (name: string) => `${name} 공격 중`,
  selectedCount: (n: number) => `사단 ${n}개 선택`,
  hintSelect: "왼쪽 클릭: 선택 · Shift: 추가 · 오른쪽 클릭: 이동/공격 · Space: 일시정지 · 1–5: 배속 · Esc: 해제",

  // Notifications
  commandRejected: (reason: string) => `명령 거부: ${reason}`,
  reject: {
    [RejectReason.UNSPECIFIED]: "알 수 없는 이유",
    [RejectReason.MALFORMED]: "명령을 해석할 수 없습니다",
    [RejectReason.NOT_YOUR_DIVISION]: "내 사단이 아닙니다",
    [RejectReason.DIVISION_RETREATING]: "후퇴 중인 사단입니다",
    [RejectReason.UNKNOWN_PROVINCE]: "없는 프로빈스입니다",
    [RejectReason.NO_PATH]: "그곳으로 가는 경로가 없습니다",
    [RejectReason.QUEUE_FULL]: "대기 중인 명령이 너무 많습니다",
    [RejectReason.MATCH_NOT_STARTED]: "게임이 아직 시작되지 않았습니다",
  } as Record<RejectReason, string>,
  error: {
    [ErrorCode.UNSPECIFIED]: "알 수 없는 오류",
    [ErrorCode.BAD_MESSAGE]: "서버가 메시지를 해석하지 못했습니다",
    [ErrorCode.UNSUPPORTED_VERSION]: "서버와 프로토콜 버전이 다릅니다",
    [ErrorCode.UNKNOWN_MATCH]: "없는 방입니다",
    [ErrorCode.UNKNOWN_FACTION]: "없는 진영입니다",
    [ErrorCode.FACTION_TAKEN]: "이미 다른 사람이 잡은 진영입니다",
    [ErrorCode.NOT_JOINED]: "참가하지 않은 연결입니다",
    [ErrorCode.NOT_HOST]: "호스트만 할 수 있습니다",
    [ErrorCode.INVALID_SPEED]: "배속은 1에서 5까지입니다",
    [ErrorCode.SLOW_CONSUMER]: "연결이 느려 서버가 끊었습니다",
    [ErrorCode.SERVER_SHUTDOWN]: "서버가 종료되었습니다",
  } as Record<ErrorCode, string>,
  destroyed: (name: string) => `${name} 소멸`,
};
