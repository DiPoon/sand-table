# 명령 프로토콜

## 1. 전송

- WebSocket, 엔드포인트 `GET /ws`. **바이너리 프레임 1개 = Protobuf 메시지 1개**다. 메시지는 `ClientMessage` 또는 `ServerMessage`이고 둘 다 `oneof` 봉투다.
- 스키마는 `proto/sandtable/v1/sandtable.proto` (proto3, 패키지 `sandtable.v1`). 와이어 형식의 기준은 그 파일이고, 이 문서는 의미를 설명한다.
- 개체는 서버가 부여한 정수 ID로 가리킨다. 문자열 키(`p03`, `infantry`)와의 대응은 `Welcome.static`에 들어 있다.
- `protocol_version`이 다르면 접속을 거부한다. 1단계 값은 `1`.

## 2. 접속 흐름

```
C → S   Hello{protocol_version, match_id, faction, player_name, session_token?}
S → C   Welcome{faction_id, session_token, static, status}      실패하면 Error 후 연결 종료
S → C   Snapshot{tick = T, ...}                                 이 플레이어에게 보이는 상태 전체
S → C   Delta{tick = T+1, ...}, Delta{tick = T+2, ...}, ...     틱마다 1개
```

진영 키는 `Welcome`보다 먼저 알아야 하므로, 클라이언트는 접속 전에 `GET /api/match`로 진영 목록을 받는다 (§8).

진영 배정:

- 진영 슬롯에 살아 있는 연결이 없으면 배정하고 새 `session_token`을 발급한다.
- 살아 있는 연결이 있으면 `session_token`이 일치할 때만 교체한다 (기존 연결은 끊는다). 아니면 `FACTION_TAKEN`. 교체한 연결은 기존 연결의 접속 순서를 이어받으므로 호스트는 그대로 호스트다.
- 계정·인증은 없다. 토큰은 서버 메모리에만 있어서 서버를 재시작하면 모든 슬롯이 다시 빈다.

재접속은 새 접속과 같다. 항상 `Snapshot`부터 다시 받는다.

방이 `LOBBY` 단계면 접속한 사람들이 진영을 잡고 기다리다가 호스트가 `StartMatch`를 보내면 시작한다. `RUNNING` 단계의 방에도 같은 흐름으로 빈 진영에 들어갈 수 있다 (중간 참여). 서버만 시뮬레이션하므로 중간 참여에 필요한 것은 `Snapshot` 한 번뿐이다.

## 3. 메시지

클라이언트 → 서버:

| 메시지 | 필드 | 설명 |
|---|---|---|
| `Hello` | `protocol_version`, `match_id`, `faction`(키), `player_name`, `session_token`(선택) | 접속 후 첫 메시지여야 한다 |
| `Command` | `client_seq`, `oneof { MoveDivision }` | `client_seq`는 클라이언트가 붙이는 번호. 결과 회신에 그대로 돌아온다 |
| `StartMatch` | | `LOBBY` → `RUNNING`. 호스트만 |
| `SetPause` | `paused` | 일시정지/재개. 누구나 |
| `SetSpeed` | `speed` (1~5) | 배속 변경. 호스트만 |

서버 → 클라이언트:

| 메시지 | 필드 | 언제 |
|---|---|---|
| `Welcome` | `faction_id`, `session_token`, `static`, `status` | 접속 승인. `static`은 정적 데이터 전체 (프로빈스, 간선, 지형, 유닛 타입, 진영, 데이터 해시) |
| `Snapshot` | `tick`, `provinces[]`, `divisions[]` | 접속 직후 1회 |
| `Delta` | `tick`, `provinces[]`, `divisions[]`, `removed[]` | 틱마다 1개. 변화가 없어도 `tick`만 담아 보낸다 |
| `CommandResult` | `client_seq`, `status`, `reason`, `apply_tick` | 명령 접수 직후. 드물게 적용 시점에 한 번 더 (거부) |
| `MatchStatus` | `phase`, `paused`, `speed`, `tick`, `host_faction`, `players[]` | 단계·일시정지·배속·호스트·참가자가 바뀔 때 전원에게 |
| `Error` | `code`, `message`, `fatal` | 프로토콜 오류. `fatal`이면 서버가 연결을 닫는다 |

## 4. 명령과 검증

1단계 명령은 하나다.

**`MoveDivision{division_id, destination}`** : 사단을 목적지 프로빈스로 보낸다. 경로는 서버가 계산한다 ([rules.md §1](rules.md)).

| 검사 | 거부 사유 |
|---|---|
| 메시지를 해석할 수 없음 | `MALFORMED` |
| 사단이 없거나 내 진영 소유가 아님 | `NOT_YOUR_DIVISION` |
| 사단이 후퇴 중 | `DIVISION_RETREATING` |
| 목적지 프로빈스 ID가 없음 | `UNKNOWN_PROVINCE` |
| 경로가 없음 (통행 불가 지형, 단절) | `NO_PATH` |
| 진영의 대기 명령이 상한 초과 | `QUEUE_FULL` |
| 방이 아직 시작하지 않음 | `MATCH_NOT_STARTED` |

- `NOT_YOUR_DIVISION`은 "없음"과 "남의 것"을 일부러 구분하지 않는다. 구분하면 보이지 않는 적 사단의 생존 여부가 새어 나간다.
- 목적지가 현재 위치면 이동 취소(정지)로 처리한다.
- 전투 중인 사단에도 명령을 내릴 수 있다. 공격 취소와 방어 측의 이탈이 이것으로 이루어진다.
- `LOBBY` 단계에서는 명령을 받지 않는다 (`MATCH_NOT_STARTED`).
- 접수와 적용의 흐름은 [tick.md §4](tick.md).

## 5. 스냅샷과 델타

서버는 세션마다 "마지막으로 보낸 뷰"를 기억하고, 틱마다 새 뷰를 계산해 **달라진 레코드만** 보낸다. 뷰의 정의는 [state.md §6](state.md).

| 레코드 | 필드 |
|---|---|
| `ProvinceView` | `id`, `controller`, `visible`, `in_battle` (`visible`일 때만 의미 있음) |
| `DivisionView` | `id`, `faction`, `type`, `name`, `province`, `strength`, `org`, `retreating` |
| `DivisionView` (자기 사단만 추가) | `path[]`, `progress`, `supply` |

- `Snapshot` = 뷰 전체. `Delta` = 직전 틱 뷰와의 차이.
- `Delta.provinces[]`, `Delta.divisions[]`에는 새로 보이거나 값이 바뀐 레코드가 통째로 들어간다 (필드 단위 델타는 나중에).
- `Delta.removed[]`는 뷰에서 사라진 사단이다: `{id, reason}`.
  - `DESTROYED` : 소멸했고, 그 순간을 볼 수 있었음 (자기 사단은 항상)
  - `OUT_OF_SIGHT` : 시야에서 벗어남 (그 뒤의 생사는 알 수 없음)
- 불변식: `Snapshot`에 `Delta`를 순서대로 적용한 결과는 그 틱의 서버 측 뷰와 정확히 같다. 테스트로 보장한다.

## 6. 방 제어

- `StartMatch`, `SetPause`, `SetSpeed`는 시뮬레이션 명령이 아니다. 즉시 적용되고 로그에 남지 않는다. `LOBBY` 단계에서 `SetPause`는 무시한다.
- 적용되면 `MatchStatus`를 방 전체에 보낸다. 범위 밖 배속은 `Error{INVALID_SPEED}`, 호스트가 아닌 사람의 시작/배속 요청은 `Error{NOT_HOST}` (둘 다 연결 유지).

## 7. 오류와 연결 관리

- `Error.code`: `BAD_MESSAGE`, `UNSUPPORTED_VERSION`, `UNKNOWN_MATCH`, `UNKNOWN_FACTION`, `FACTION_TAKEN`, `NOT_JOINED`, `NOT_HOST`, `INVALID_SPEED`, `SLOW_CONSUMER`, `SERVER_SHUTDOWN`.
- 세션마다 상한이 있는 송신 큐를 둔다. 느린 클라이언트 때문에 틱 루프가 멈추지 않는다. 큐가 넘치면 `SLOW_CONSUMER`로 끊고 (큐에 남은 메시지를 다 보낸 뒤 이 오류를 보낸다), 클라이언트는 재접속해 새 스냅샷을 받는다.
- `Hello` 이후에도 해석할 수 없는 메시지(디코딩 실패, 텍스트 프레임, 빈 `oneof`)를 받으면 `BAD_MESSAGE`(fatal)를 보내고 끊는다.
- `MatchStatus`는 이벤트(메시지 하나 또는 틱 하나)를 다 처리한 뒤에 한 번 보낸다. 그 사이에 느린 클라이언트가 끊겨도 남은 참가자는 최종 상태를 마지막으로 받는다.
- `Delta.tick`은 항상 직전 값 + 1이다. 건너뛰었다면 클라이언트는 재접속한다.
- 일시정지 중에는 `Delta`가 오지 않는다. 연결 생존 확인은 WebSocket ping/pong으로 한다.

## 8. HTTP

같은 서버가 WebSocket 외에 HTTP로 두 가지를 제공한다.

**`GET /api/match?id=<match>`** : 접속 전에 보여 줄 방 정보. JSON이고 캐시하지 않는다 (`Cache-Control: no-store`).

```json
{"id":"default","phase":"LOBBY","tick":0,
 "factions":[{"key":"azure","name":"Azure League","taken":true,"player":"alice"},
             {"key":"crimson","name":"Crimson Pact","taken":false}]}
```

| 필드 | 의미 |
|---|---|
| `phase` | `LOBBY` 또는 `RUNNING` |
| `tick` | 현재 틱 |
| `factions[]` | 진영 ID 순 (`factions[i]`의 ID는 `i + 1`). `taken`이면 살아 있는 연결이 그 진영을 잡고 있고, `player`는 그 이름 |

- 없는 방이면 `404`.
- 방 상태는 방 고루틴이 읽어서 돌려준다 (`match.Info`). 응답은 읽은 순간의 값이라, 그 사이에 누가 진영을 잡으면 `Hello`가 `FACTION_TAKEN`으로 거부될 수 있다.

**정적 파일** : `sandtable-server -web <dir>`(기본 `../client/dist`) 디렉토리가 서버 시작 시점에 있으면 `/`에서 제공한다. `/`(페이지)에는 `Cache-Control: no-cache`를 붙여 새 빌드가 새로고침으로 반영되게 하고, 해시가 붙은 `assets/`는 기본 캐시를 그대로 둔다.
