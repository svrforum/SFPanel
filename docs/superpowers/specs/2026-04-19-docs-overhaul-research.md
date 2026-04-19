# SFPanel 문서 전면 개편 — Phase 0 리서치 스펙

**작성일:** 2026-04-19
**상태:** Phase 0 (리서치) 완료, Phase 1(스펙 싱크) 직전
**대상:** `README.md`, `CLAUDE.md`, `docs/specs/*.md` 5종

## 1. 목표

기여자 온보딩(B), 공개/배포(A), 스펙↔코드 싱크(C), 전반 품질(D) 네 드라이버를 모두 커버하는 문서 세트를 7,500줄+ 현 문서 기준으로 재정비한다. 본 문서는 Phase 0 리서치 산출물이며, Phase 1~4 단계별 수정의 기준점이 된다.

## 2. 리서치 방법

병렬 Explore 에이전트 6개로 각 영역을 전수 조사하고 결과를 통합했다. 상세 인벤토리는 `../research/2026-04-19-docs-overhaul/` 하위에 보존:

- `api-inventory.md` (63KB) — HTTP/REST 라우트 전수
- `ws-inventory.md` — WebSocket 6개 + SSE 8개
- `db-inventory.md` — SQLite 10개 테이블 스키마
- `features-inventory.md` (29KB) — 20+ 기능 모듈 카탈로그
- `frontend-inventory.md` — React SPA 구조
- `cluster-inventory.md` (46KB) — Raft/gRPC/mTLS/프록시

## 3. 프로젝트 스냅샷 (2026-04-19)

| 항목 | 값 |
|------|-----|
| 버전 | 0.9.0 (systemd `sfpanel.service`로 이 호스트에 설치됨) |
| 백엔드 | Go 1.24, Echo v4, SQLite(modernc), HashiCorp Raft, gRPC |
| 프런트 | React 19, TypeScript 5.9, Vite 7, Tailwind 4, shadcn/ui, React Router v7 |
| 데스크톱 | Tauri 2 (Windows/macOS/Linux) |
| REST 엔드포인트 | 200+ (보호 196+, 공개 4) |
| WebSocket | 6개 (metrics/logs/terminal/docker-logs/docker-exec/compose-logs) |
| SSE 스트리밍 | 8개 (system update, docker pull, compose up/update, packages install-docker/install-node, tailscale install, cluster update) |
| 에러 코드 | 150+ (`internal/api/response/errors.go`) |
| DB 테이블 | 10개 (admin/sessions[미사용]/compose_projects/settings/custom_log_sources/metrics_history/audit_logs/alert_channels/alert_rules/alert_history) |
| 기능 모듈 | 20+ `internal/feature/*` + `internal/cluster/*` + `internal/feature/alert/*` |
| 프런트 페이지 | 22개 (모두 React.lazy), 41개 라우트 |
| i18n | ko/en 각 1,902줄 |
| 테스트 파일 | 리포 전체 **6개** (기능 모듈 0개) |

## 4. 확인된 사실과 스펙 드리프트 (Phase 1이 처리할 목록)

### 4.1 `docs/specs/api-spec.md` (4,125줄)

- **Compose 라우트 20개 누락** — `/api/v1/docker/compose/*` 전체 경로 상세 필요
- **디스크/LVM/RAID 52개 라우트 부분 누락** — 파티션/포맷/마운트/LVM/RAID/swap 전수 등재 필요
- **Tailscale(12) / WireGuard(10) VPN 라우트 일부 누락**
- **SSE 섹션 빈약** — 8개 스트리밍 엔드포인트의 phase/line/step 이벤트 스키마 명시 필요
- **클러스터 프록시 메커니즘 언급 부족** — `?node=` 쿼리, 30초 gRPC 프록시, 5분 SSE HTTP 릴레이, `X-SFPanel-Internal-Proxy` 헤더, `X-SFPanel-Original-User`
- **Docker 조건부 등록 명시 없음** — `dockerHandler != nil`일 때만 `/docker/*` 26개 라우트 등록됨

### 4.2 `docs/specs/websocket-spec.md` (635줄)

- **6개 WS 엔드포인트 모두 존재하고 일치** — 드리프트 거의 없음
- **SSE 섹션이 전혀 없음** — 8개 SSE 엔드포인트는 api-spec 또는 websocket-spec 둘 중 한 곳에 별도 섹션으로 들어가야 함
- **클러스터 릴레이 프로토콜 상세 추가 필요** — `internal/cluster/ws_relay.go`의 양방향 포워딩, 메시지 타입 보존, 한쪽 종료 시 전파, `X-SFPanel-Internal-Proxy` SHA-256 해시 인증

### 4.3 `docs/specs/db-schema.md` (411줄)

- **`metrics_history` 수집 간격 오기재** — 문서 "30초", 코드 `60 * time.Second` → 60초로 수정
- **`settings.max_upload_size`(기본 1024MB) 누락**
- **`appstore_installed_{appID}` 동적 키 패턴 상세 부족**
- **`admin.totp_secret` 평문 저장 명시 필요** + DB 파일 권한(0600) 주의
- **백업/복구 기능 미구현** — 문서에도 없음, 존재하지 않는 동작으로 기대하지 않도록 명시 필요
- **`sessions` 테이블 미사용** — 스키마만 있음을 명시

### 4.4 `docs/specs/frontend-spec.md` (1,033줄)

- **실제 코드와 99% 일치** — 대형 drift 없음
- 누락/보강 필요:
  - Vite 수동 청크 6개 구성 (`react-vendor`, `ui-vendor`, `xterm`, `i18n`, `uplot`, `monaco`)
  - PWA(`vite-plugin-pwa`) 서비스워커 캐시 정책 (monaco/xterm 워커 제외)
  - 실제 번들 크기 ~16MB
  - localStorage 7개 키 목록
  - `ApiClient` 싱글턴 패턴, `?node=` 자동 주입, `readSSEStream()` 헬퍼
  - 클러스터 UI 컴포넌트 (`ClusterSidebar`, `TreePanel`, `ContextMenu`, `NodeSelector`)
  - 모바일 컴포넌트 (`BottomNav`, `MobileHeader`)
  - 테스트 프레임워크 없음(Vitest/Playwright 미설정) 사실을 명시

### 4.5 `docs/specs/tech-features.md` (785줄)

- **알림(alert) 모듈 섹션 필요** — 채널/규칙/이력 3개 DB 테이블, Discord/Telegram 채널, 60초 주기 평가, cooldown/severity/node_scope 처리
- **클러스터 재설계 내용 반영** — 최근 3 커밋(ba5cd60, 1355a74, 6ccbd7f)의 zero-restart 조인, pre-flight 검증, 공유 엔진, 원자적 롤백, `OnManagerActivated` 콜백
- **데스크톱(Tauri) 섹션 상세화** — CSP, 플랫폼별 패키지 포맷, `beforeDevCommand`/`beforeBuildCommand` 스크립트
- **의존성 버전 재확인** — 섹션의 표가 현재 `go.mod`/`package.json`과 일치하는지 재검증

## 5. 코드베이스의 중요한 현황 — CLAUDE.md 리팩토링 근거

Phase 2(CLAUDE.md)에서 다루어야 할 **실제 코드와 문서상 규칙의 괴리**:

### 5.1 `os/exec` 직접 사용 — 규칙 위반 **7개 파일**

현재 CLAUDE.md는 "NEVER use `os/exec.Command` directly in handlers" 규칙을 명시. 그러나 실제로:

- `internal/feature/appstore/handler.go`
- `internal/feature/network/tailscale.go`
- `internal/feature/system/handler.go`
- `internal/feature/logs/handler.go`
- `internal/feature/terminal/handler.go`
- `internal/feature/packages/handler.go`
- `internal/docker/compose.go`

(정당한 예외: `internal/common/exec/exec.go` — Commander 자체, `internal/common/lifecycle/systemd.go` — 생명주기 훅)

→ CLAUDE.md를 수정하여 **스트리밍/PTY/장기 프로세스 같은 합법적 예외 사유를 명시**하거나, 대상 파일들을 Commander로 마이그레이션하는 후속 과제로 분리.

### 5.2 테스트 부재 — 규칙과 현실의 괴리

CLAUDE.md: "All new code must have unit tests."
현실: `internal/feature/` 하위 `*_test.go` **0개**. 리포 전체 6개.

→ 규칙을 삭제하지 말고, **"신규 PR은 테스트 포함. 기존 모듈은 점진 확충"** 식으로 현실화.

### 5.3 라우터 규약 일관성

`internal/api/router.go`가 중앙 등록점이고, 각 Handler는 `type Handler struct { Cmd exec.Commander ... }` 패턴을 대체로 따름 — **이건 유지/강조**.

### 5.4 응답 표준

- `response.OK() / response.Fail()` 일관 사용됨 — 유지
- `response.SanitizeOutput()` stderr 정제 — 유지
- 150+ 에러 코드 `response/errors.go` — 유지

## 6. Phase 1~4 실행 계획

### Phase 1 — 스펙 싱크 (C)

5개 스펙 파일을 4.1~4.5의 드리프트 리스트에 따라 수정. 기존 M 수정과 충돌 시 사용자 확인. 큰 변경은 파일별 섹션 추가/치환 단위로 진행.

**우선순위:**
1. `tech-features.md` — alert/cluster 섹션 최신화 (뼈대)
2. `db-schema.md` — 짧고 정확도 크리티컬
3. `websocket-spec.md` — SSE 섹션 추가
4. `api-spec.md` — 가장 큼, 라우트 표 재생성
5. `frontend-spec.md` — 작은 보강들

### Phase 2 — CLAUDE.md 리팩토링 (B)

- 5.1~5.4 근거로 규칙을 "현실화". 아스피레이셔널 규칙은 "권장" 톤으로, 예외가 명확한 규칙은 예외 명시
- 영문 유지
- 섹션 재편: Overview / Architecture / Development workflow / Code conventions / Testing reality / Cluster awareness / Build & run / Troubleshooting
- 약 150~200줄 목표 (현 103줄에서 완화/정밀화)

### Phase 3 — README.md 리라이트 (A)

- 한국어 유지
- 타깃: "처음 방문한 사람"
- 섹션: 1줄 소개 → 스크린샷(옵션) → 주요 기능 → 빠른 시작(설치/첫 실행/첫 로그인) → 구성 요소 아키텍처 1 다이어그램 → 기여/라이선스/링크
- 깊은 기술 상세는 `docs/specs/*`로 링크만
- 약 200~300줄 목표 (현 344줄보다 간결)

### Phase 4 — 일관성 패스 (D)

- 용어 통일 (예: "노드 ID" vs "node_id", "패키지" vs "package")
- 파일 간 링크 왕복 확인 (README → specs, CLAUDE.md → specs, tech-features ↔ api-spec 등)
- 각 스펙 헤더에 "마지막 업데이트: 2026-04-19, 기준: v0.9.0" 추가
- 변경 전체에 대한 최종 diff 리뷰

## 7. 명시적 결정 / 전제

- **언어:** 파일별 현 언어 유지 (README/specs 한국어, CLAUDE.md 영문)
- **기존 uncommitted M 수정:** 유지하고 그 위에 쌓음
- **상세 인벤토리 보존 위치:** `docs/superpowers/research/2026-04-19-docs-overhaul/`
- **커밋 정책:** 리포 규칙 준수 (author/committer = svrforum, 메시지에 Claude/AI 흔적 금지)
- **단계별 커밋:** Phase 0(본 문서) → Phase 1(5개 스펙) → Phase 2(CLAUDE.md) → Phase 3(README.md) → Phase 4(마이크로 정리) 순으로 단계 단위 커밋

## 8. 리스크 / 대응

| 리스크 | 대응 |
|--------|------|
| 거대한 api-spec.md 재작성 중 정보 유실 | 라우트 표 자동 재생성 + 기존 설명 섹션 보존 우선 |
| 기존 M 수정과 새 변경 충돌 | 각 파일 편집 시작 전 현 상태 Read로 재확인 |
| 스펙-코드가 또 벌어지는 미래 | Phase 4에서 "문서 갱신 체크리스트"를 `CLAUDE.md`에 추가 (PR 병합 전 관련 스펙 업데이트 의무) |
| 7개 `os/exec` 규칙 위반의 처리 방향 불확실 | 본 Phase 0 단계에서 결정하지 않고, CLAUDE.md 리팩토링 시 "예외 허용" vs "마이그레이션 요망" 중 사용자에게 확인 후 진행 |

## 9. 다음 액션

1. **사용자가 본 스펙을 리뷰**
2. 승인 시 Phase 1 (스펙 싱크) 진입
3. Phase 1은 5개 파일을 개별 커밋으로 순차 수정하며, 각 파일 시작 전 한 번씩 중간 요약으로 사용자에게 진행 보고
