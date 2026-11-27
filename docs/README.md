# sandtable 설계 문서

서버 권위형 그랜드 스트래티지 시뮬레이션. 서버만 시뮬레이션하고, 클라이언트는 명령(의도)을 보내고 상태를 받아 그리기만 한다.

> **상태: 1단계(서버)와 2단계(웹 뷰어) 구현 완료.** 문서와 코드가 어긋나면 코드와 테스트가 기준이고, 문서를 고친다.

## 실행

```sh
make test          # 서버 전체 테스트 (결정론, 규칙, 영속성, end-to-end)
make bench         # 틱당 소요 시간 벤치마크
make build         # 웹 클라이언트(client/dist) + bin/ 에 실행 파일 4개. npm이 필요하다

cd server
../bin/sandtable-server                       # http://127.0.0.1:8080, 상태는 server/var/default/
../bin/sandtable-server -scenario generated -state-dir var/generated   # 생성된 큰 맵으로. 샘플 맵의 방과 상태 디렉토리를 나눈다
../bin/sandtable-cli -faction azure           # 터미널 클라이언트. crimson도 같은 방식
../bin/sandtable-replay -dir var/default      # 명령 로그를 틱 0부터 재생해 해시와 스냅샷 검증
../bin/sandtable-mapgen -seed 1               # data/maps, data/scenarios에 generated.yaml을 만든다
```

맵 생성기 옵션: `-seed`(같은 시드면 같은 파일), `-land`(육지 프로빈스 수, 기본 800), `-factions`(기본 2), `-spacing`(프로빈스 간격 km, 기본 40), `-name`(파일 이름: 소문자·숫자·`-`·`_`), `-data`(기본 `../data`). 보급 허브는 `-data`의 `rules.yaml`에서 읽은 이동 비용과 보급 거리에 맞춰 놓으므로, 규칙을 바꾸면 맵도 다시 만든다 (`TestCommittedMapIsSupplied`가 잡는다). 생성기가 쓰지 않은 파일(샘플 맵처럼 손으로 쓴 것)은 덮어쓰지 않는다. 생성 결과는 커밋한다. 이미 그 맵으로 진행 중인 방은 데이터가 달라지면 복구를 거부하므로, 다시 만들었다면 그 방의 상태 디렉토리를 옮긴다. 방의 상태는 `-state-dir` 아래 방 이름의 디렉토리에 있다 (기본 `server/var/default`, 위 예시의 생성 맵은 `server/var/generated/default`).

브라우저로 `http://127.0.0.1:8080`에 접속해 이름과 진영을 고르면 참가한다. 서버는 `-web`(기본 `../client/dist`) 디렉토리가 있으면 `/`에서 웹 클라이언트를 제공한다. 탭마다 다른 진영을 잡을 수 있고, 새로고침하면 같은 진영으로 다시 들어온다.

| 조작 | 동작 |
|---|---|
| 왼쪽 클릭 | 자기 사단 선택 (Shift: 추가/해제). 사단이 아닌 곳은 프로빈스 정보 |
| 오른쪽 클릭 | 선택한 사단들을 그 프로빈스로 이동/공격 |
| 드래그, 휠 | 지도 이동, 확대/축소 |
| `Space`, `1`~`5`, `Esc` | 일시정지/재개, 배속(호스트만), 선택 해제 |

CLI 명령: `start`, `pause`, `resume`, `speed N`, `move 사단ID 프로빈스키`, `state`, `wait MS`, `quit`.

웹 클라이언트 개발 (`client/`):

```sh
make client-dev    # Vite 개발 서버 http://127.0.0.1:5173. /ws와 /api를 8080의 서버로 넘긴다 (서버는 따로 띄운다)
cd client
npm test           # Vitest: 상태 저장소(스냅샷/델타), 좌표 계산, 재접속
npm run build      # 타입 검사 + client/dist
npm run e2e        # Playwright: 서버를 8091에 새로 띄워 탭 두 개로 끝까지 플레이. make build를 먼저 한다
SANDTABLE_WS=ws://127.0.0.1:8080/ws npm test   # 실행 중인 서버(새 방)와 델타 동기화를 대조
```

`npm run e2e`는 처음 한 번 `npx playwright install chromium`이 필요하다 (시스템 라이브러리가 없으면 `--with-deps`).

## 원칙

- **클린룸.** 특정 상용 게임의 바이너리·에셋·코드·고유 명칭·맵 데이터를 쓰지 않는다. 규칙은 일반적인 워게임 개념(프로빈스 그래프, 이동, 전투, 보급)으로 직접 설계한다.
- 레포·코드 어디에도 특정 게임명(상표)을 넣지 않는다.
- **결정론.** 같은 데이터 + 같은 시드 + 같은 명령 = 같은 상태 해시. CPU 아키텍처가 달라도 같아야 한다.
- **데이터 주도.** 프로빈스·유닛 타입·전투 수치는 코드가 아니라 `data/`의 YAML에서 읽는다.

## 문서

| 문서 | 내용 |
|---|---|
| [tick.md](tick.md) | 틱 구조 — 방 루프, 틱 단계 순서, 배속/일시정지, 명령 큐, 결정론 규칙 |
| [protocol.md](protocol.md) | 명령 프로토콜 — 메시지, 접속 흐름, 명령 검증과 거부 사유, 스냅샷/델타 |
| [state.md](state.md) | 상태 모델 — 정적 데이터 / 동적 상태 / 플레이어 뷰, 단위, 정규 인코딩 |
| [rules.md](rules.md) | 게임 규칙 — 이동, 전투, 후퇴, 점령, 보급, 시야 |
| [persistence.md](persistence.md) | 영속성 — 명령 로그, 해시, 스냅샷, 복구, 리플레이 |
| [client-plan.md](client-plan.md) | 2단계 — 웹 뷰어 클라이언트 (구현 완료, 계획과 달라진 점은 문서 끝) |
| [land-war-plan.md](land-war-plan.md) | 3단계 계획 — 한 판이 성립하는 지상전 (진행 중) |

## 확정된 결정

| 항목 | 결정 |
|---|---|
| 영속성 범위 | 1단계에 전부 구현: 명령 로그 + 주기적 해시 + 주기적 스냅샷 + 재시작 복구 + 리플레이 검증 |
| 데이터 포맷 | YAML |
| 와이어 포맷 | Protobuf 바이너리 (WebSocket 바이너리 프레임) |
| 설계 방향 | 시스템은 장르 관례를 따르고, 콘텐츠(맵, 수치, 명칭)는 직접 만든다 |
| 전투 모델 | 병력 + 조직력. 인접 프로빈스를 공격하고 이겨야 들어간다. 방어 측은 조직력 0이면 후퇴, 공격 측은 공격 중단. 병력 0이면 소멸 |
| 보급 거리 | 거점에서 아군 통제 프로빈스만 경유한 최단 경로 비용. 끊기면 차단 |

## 레포 구조

```
sandtable/
├── proto/                     # 와이어 스키마(.proto). 서버와 클라이언트가 공유
├── buf.yaml, buf.gen.yaml     # 스키마 린트와 코드 생성 설정 (make proto: Go와 TypeScript)
├── Makefile
├── server/                    # Go 모듈 (sandtable/server)
│   ├── cmd/
│   │   ├── sandtable-server/  # 서버
│   │   ├── sandtable-cli/     # 더미 CLI 클라이언트
│   │   ├── sandtable-replay/  # 명령 로그 리플레이 검증
│   │   └── sandtable-mapgen/  # 맵·시나리오 생성기
│   └── internal/
│       ├── sim/               # 결정론 코어: 상태, Step, 규칙, 해시, 정규 인코딩
│       ├── gamedata/          # data/ YAML 로드와 검증
│       ├── mapgen/            # 맵 생성 (지형, 강, 주, 진영, 승점, 철도). 시뮬레이션 밖이라 부동소수점을 쓴다
│       ├── view/              # 시야 필터, 플레이어 뷰, 델타
│       ├── match/             # 방: 틱 루프, 명령 큐, 세션
│       ├── persist/           # 명령 로그, 스냅샷, 복구
│       ├── transport/         # WebSocket, Protobuf 변환, /api/match, 웹 클라이언트 정적 파일
│       └── pb/                # Protobuf 생성 코드 (커밋)
├── client/                    # 웹 뷰어 (TypeScript + Vite, Canvas 2D)
│   ├── src/
│   │   ├── main.ts            # 화면 전환: 접속 화면 → 게임
│   │   ├── strings.ts         # UI 문자열 (한국어)
│   │   ├── gen/               # Protobuf 생성 코드 (커밋)
│   │   ├── net.ts             # WebSocket, 인코딩, 접속·재접속, 세션 토큰
│   │   ├── store.ts           # 정적 데이터 + 현재 뷰. 스냅샷/델타 적용 (DOM 없음)
│   │   ├── geometry.ts        # Voronoi 셀, 프로빈스 찾기, 진행도 → 좌표, 카메라 (DOM 없음)
│   │   ├── render.ts          # Canvas 그리기
│   │   ├── input.ts           # 마우스·키보드
│   │   └── ui/                # 게임 화면, 접속 화면, 상단 바, 대기실, 패널, 알림
│   └── e2e/                   # Playwright 종단 테스트
├── data/                      # 규칙·유닛·맵·시나리오 (YAML)
└── docs/
```

의존 방향은 한쪽으로만 흐른다: `sim` ← `gamedata`·`view`·`persist` ← `match` ← `transport` ← `cmd`. `mapgen`은 내부 패키지를 쓰지 않고 YAML만 만든다 (테스트만 결과를 `gamedata`로 읽어 시뮬레이션을 돌려 본다).
`sim`은 표준 라이브러리 일부만 import하고, 네트워크·Protobuf·YAML을 모른다.

## 의존성

| 용도 | 모듈 |
|---|---|
| WebSocket | `github.com/coder/websocket` |
| Protobuf 런타임 | `google.golang.org/protobuf` |
| YAML | `go.yaml.in/yaml/v3` |
| Delaunay 삼각분할 (맵 생성기만) | `github.com/fogleman/delaunay`. 클라이언트의 `d3-delaunay`와 같은 delaunator 알고리즘 |
| 코드 생성 (개발 시에만) | `buf` + `protoc-gen-go` + `protoc-gen-es`(client의 npm 패키지). 생성물을 커밋하므로 빌드에는 필요 없다 |

웹 클라이언트 (`client/package.json`):

| 용도 | 패키지 |
|---|---|
| Protobuf 런타임 | `@bufbuild/protobuf` |
| Voronoi | `d3-delaunay` |
| 개발 도구 | `typescript`, `vite`, `vitest`, `@playwright/test`, `@bufbuild/protoc-gen-es`, `@types/d3-delaunay`, `@types/node` |

런타임 의존성은 위 두 개뿐이다.

## 1단계에서 만들지 않는 것

로비/방 생성 API, 계정·인증, 외교, 생산·증원, 해·공군, AI, 뷰어 클라이언트(2단계에서 구현), 로그 분할·압축, 필드 단위 델타.

## 확인이 필요한 제안

질문으로 확정하지 않고 정한 것들이다. 현재 구현은 이대로 되어 있다. 번호로 짚어서 바꿔 달라고 하면 된다.

1. **`proto/` 최상위 디렉토리 추가.** 요청한 4개 디렉토리 외에 하나 더 생긴다. 서버와 나중의 클라이언트가 같은 스키마를 쓰기 때문이다. (대안: `server/proto/`)
2. **시간 축척.** 1틱 = 게임 1시간. 배속 5단계 = 틱 간격 1000/500/250/100/50ms. → [tick.md §3](tick.md)
3. **방 모델.** 구조는 다중 방이지만 1단계는 서버 시작 시 설정으로 방 1개를 만든다. → [tick.md §1](tick.md)
4. **플레이어 = 진영 1:1, 로비와 중간 참여.** 로비에서 진영을 잡고 호스트가 시작한다. 시작한 뒤에도 빈 진영으로 들어올 수 있고, 연결이 끊긴 진영은 누구나 다시 잡을 수 있다. → [protocol.md §2](protocol.md)
5. **호스트와 권한.** 가장 먼저 들어온 참가자가 호스트. 시작과 배속은 호스트만, 일시정지는 누구나. 접속자가 0명이면 자동 일시정지. → [tick.md §3](tick.md)
6. **진영 관계.** 서로 다른 진영은 전부 적대. → [rules.md](rules.md)
7. **시야.** 프로빈스 통제자는 전원에게 공개하고, 사단과 전투만 안개 대상이다. 시야는 자기 통제 프로빈스 + 자기 사단 위치 + 인접 1칸. → [rules.md §7](rules.md)
8. **전투 세부.** 이동 명령이 곧 공격이고 전투 중에도 진행도가 쌓인다. 전투 폭과 예비대가 있고, 예비대는 틱마다 확률로 전선에 합류한다. 사단은 틱마다 한 번만 공격한다. → [rules.md §1~3](rules.md)
9. **병력 회복 없음.** 1단계에는 증원이 없어 병력은 줄기만 하고, 조직력만 회복한다. → [rules.md §6](rules.md)
10. **거점은 프로빈스 속성.** 점령하면 점령한 쪽의 거점이 된다. → [rules.md §5](rules.md)
11. **난수.** 상태를 가진 PRNG 대신 `(시드, 틱, 용도, 개체)` 키에서 파생하는 PRNG. → [tick.md §6](tick.md)
12. **로그/스냅샷 인코딩.** Protobuf가 아니라 코어의 정규 바이너리 인코딩을 쓴다. Protobuf는 와이어 전용. → [persistence.md §2](persistence.md)
13. **더미 CLI 위치.** `server/cmd/sandtable-cli`. 생성된 Protobuf 코드를 서버 모듈과 공유하기 위해서다. `client/`는 웹 뷰어용이다.
14. **언어.** 문서는 한국어, 코드·주석·식별자는 영어.
