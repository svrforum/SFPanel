# SFPanel WebSocket 및 스트리밍 엔드포인트 인벤토리

## 요약

- **WebSocket (WS)**: 6개 엔드포인트
- **Server-Sent Events (SSE)**: 8개 엔드포인트
- 모든 WS 엔드포인트는 `?node=X` 파라미터로 클러스터 릴레이 지원

---

## 1. WebSocket 엔드포인트 표

| 유형 | 경로 | 인증 | 용도 | 릴레이 | 상태 |
|------|------|------|------|-----------|------|
| WS | `/ws/metrics` | `?token=JWT` | 실시간 시스템 메트릭 | 예 | 활성 |
| WS | `/ws/logs` | `?token=JWT` | 시스템 로그 스트리밍 | 예 | 활성 |
| WS | `/ws/terminal` | `?token=JWT` | 호스트 PTY 셸 (영속) | 예 | 활성 |
| WS | `/ws/docker/containers/:id/logs` | `?token=JWT` | 컨테이너 로그 스트리밍 | 예 | Docker 필수 |
| WS | `/ws/docker/containers/:id/exec` | `?token=JWT` | 컨테이너 인터랙티브 셸 | 예 | Docker 필수 |
| WS | `/ws/docker/compose/:project/logs` | `?token=JWT` | Compose 프로젝트 로그 | 예 | Docker 필수 |

## 2. SSE 엔드포인트 표

| 경로 | 메서드 | 용도 | 스트림 형식 |
|------|--------|------|----------|
| `/api/v1/system/update` | POST | SFPanel 자체 업데이트 (바이너리 다운로드/검증/재시작) | JSON step+message |
| `/api/v1/docker/images/pull` | POST | Docker 이미지 풀 | JSON (Docker API 이벤트) |
| `/api/v1/docker/compose/:project/up-stream` | POST | Compose 프로젝트 시작 | JSON phase+line |
| `/api/v1/docker/compose/:project/update-stream` | POST | Compose 스택 업데이트 | JSON phase+line |
| `/api/v1/packages/install-docker` | POST | Docker 엔진 설치 (get.docker.com) | 평문 라인 |
| `/api/v1/packages/install-node` | POST | Node.js/NVM 설치 | 평문 라인 |
| `/api/v1/network/tailscale/install` | POST | Tailscale VPN 설치 | 평문 라인 |
| `/api/v1/cluster/update` | POST | 클러스터 멀티노드 업데이트 | JSON per-node step+status |

## 3. 메시지 스키마 요약

### `/ws/metrics` (서버→클라이언트, 2초 주기 JSON)
```json
{"cpu":23.5,"mem_total":..,"mem_used":..,"mem_percent":50.0,
 "swap_total":..,"swap_used":..,"swap_percent":25.0,
 "disk_total":..,"disk_used":..,"disk_percent":50.0,
 "net_bytes_sent":..,"net_bytes_recv":..,"timestamp":1740500000000}
```

### `/ws/logs`
- 쿼리: `token`, `source` (syslog/auth/kern/sfpanel/dpkg/firewall/fail2ban/custom)
- 서버→클라이언트: 평문 라인 실시간

### `/ws/terminal` — 영속 세션
- 쿼리: `token`, `session_id` (기본 `"default"`)
- 서버→클라이언트: BinaryMessage (PTY stdout/err 4KB 청크)
- 클라이언트→서버:
  - BinaryMessage = 키 입력
  - TextMessage `{"type":"resize","cols":120,"rows":40}` = 리사이즈
- 특징: 256KB 링 버퍼 스크롤백, 재접속 시 자동 복원, 최대 20 동시 세션, 기본 30분 idle 타임아웃 (DB `terminal_timeout`)

### `/ws/docker/containers/:id/logs`
- 쿼리: `token`, `tail`(기본 100), `timestamps`, `stream`, `since`
- 서버→클라이언트: 라인 단위 평문

### `/ws/docker/containers/:id/exec`
- TextMessage로 양방향 (셸 출력 / 키 입력), resize는 JSON `{"type":"resize"}`

### `/ws/docker/compose/:project/logs`
- 쿼리: `token`, `tail`, `service`
- 서버→클라이언트: 라인 단위 (서비스명 프리픽스 포함)

### SSE 공통
- 각 이벤트 후 명시적 `flusher.Flush()`
- 종료 시 `[DONE]` 텍스트 또는 `step:"complete"` JSON

## 4. 라이프사이클 / 백프레셔

- 킵얼라이브: 앱 레벨 `ReadMessage()` 루프로 클라이언트 종료 감지 (`defer close(done)`)
- 동시 쓰기: `safeWSWriter{conn, mu sync.Mutex}`로 `WriteMessage()` 직렬화
- gorilla/websocket 자동 핑 미사용
- 메트릭 주기: 2초 ticker + 1초 CPU 샘플 ≈ 3초 주기

## 5. 클러스터 릴레이 (internal/cluster/ws_relay.go)

- 로컬 노드가 `?node=REMOTE_ID` 감지 → 클라이언트 업그레이드 후 원격 노드의 동일 경로에 WS 다이얼
- 양방향 메시지 포워딩, 메시지 타입 보존, 한쪽 종료 시 반대쪽도 종료
- 원격 URL 재구성: `Scheme=ws/wss`, `Host=apiAddr`, `Path=originalPath`, `RawQuery=stripNodeParam(...)`
- 내부 프록시 인증: `X-SFPanel-Internal-Proxy` = 클러스터 CA SHA-256 해시 (hex), 상수시간 비교

## 6. 인증

- WS: `?token=<JWT>` 쿼리 (HTTP 핸드셰이크 시 커스텀 헤더 불가 제약), `auth.ParseToken(token, jwtSecret)` HS256
- SSE: 표준 `Authorization: Bearer <JWT>` 헤더 + 기존 JWT 미들웨어
- WS `CheckOrigin: true` — 모든 origin 허용, JWT가 유일 방어선

## 7. 파일 위치

| 파일 | 스트림 | 엔드포인트 |
|------|--------|----------|
| `internal/feature/websocket/handler.go` | WS | `/ws/metrics`, `/ws/docker/containers/*`, `/ws/docker/compose/*` |
| `internal/feature/terminal/handler.go` | WS | `/ws/terminal` |
| `internal/feature/logs/handler.go` | WS | `/ws/logs` |
| `internal/feature/docker/handler.go` | SSE | `/api/v1/docker/images/pull` |
| `internal/feature/system/handler.go` | SSE | `/api/v1/system/update` |
| `internal/feature/compose/handler.go` | SSE | compose up-stream / update-stream |
| `internal/feature/packages/handler.go` | SSE | install-docker / install-node |
| `internal/feature/network/tailscale.go` | SSE | tailscale/install |
| `internal/feature/cluster/handler.go` | SSE | cluster/update |
| `internal/cluster/ws_relay.go` | 릴레이 | (래퍼) |

## 8. 문서 대비 편차

### `docs/specs/websocket-spec.md` (6개 WS)
모두 일치 — 누락/미구현 엔드포인트 없음.

### SSE 엔드포인트는 WebSocket 스펙에 없음
- `/api/v1/system/update`, `/api/v1/docker/images/pull`, compose up-stream/update-stream, packages install-docker/install-node, tailscale install, cluster/update 8개는 **api-spec.md에만** 부분 기재, WebSocket-spec에는 언급 없음

→ **Phase 1 과제**: WebSocket 스펙에 SSE 섹션을 별도로 추가하거나, api-spec.md의 스트리밍 섹션을 확장하여 SSE 이벤트 스키마를 완전히 기술.

## 9. 보안 노트

| 항목 | 상태 |
|------|------|
| Origin 검사 (WS) | 모든 origin 허용, JWT가 유일 방어선 |
| Token URL 노출 | 쿼리 파라미터라 서버 로그에 기록될 수 있음 (악용 리스크) |
| 로그 경로 제한 | 화이트리스트 `defaultLogSources` 기반 |
| 터미널 WS | JWT만으로 호스트 셸 접근 허용 (관리자 전용 운영 필수) |
| 내부 프록시 인증 | SHA-256 해시 + 상수시간 비교 |
| SSE Flusher | 타입 어설션으로 존재 확인 |
