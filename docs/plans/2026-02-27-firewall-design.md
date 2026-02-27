# 방화벽 관리 기능 설계

## 개요

UFW 방화벽 규칙 관리 + 열린 포트 조회 + Fail2ban 상태 관리를 하나의 페이지에서 제공.

## 결정 사항

| 항목 | 결정 |
|------|------|
| 방화벽 백엔드 | UFW 단독 |
| Fail2ban 범위 | 상태 조회 + jail 관리 + 차단 IP 해제 (설정 편집 제외) |
| 페이지 구조 | 단일 페이지 + 3개 탭 |
| IP 차단 방식 | UFW deny 규칙으로 처리 |

## 탭 구조

| 탭 | 내용 |
|---|---|
| UFW 규칙 | 방화벽 상태 on/off, 규칙 목록(CRUD), 포트 열기/차단, IP 차단 |
| 열린 포트 | 현재 리스닝 중인 포트 목록 (ss -tlnp 기반), 원클릭 UFW 규칙 추가 |
| Fail2ban | 서비스 상태, jail 목록 + 활성/비활성, 차단 IP 조회 및 해제 |

## 백엔드 API

### UFW

| Method | Endpoint | 설명 |
|--------|----------|------|
| GET | `/api/v1/firewall/status` | UFW 상태 (active/inactive) + 기본 정책 |
| POST | `/api/v1/firewall/enable` | UFW 활성화 |
| POST | `/api/v1/firewall/disable` | UFW 비활성화 |
| GET | `/api/v1/firewall/rules` | 규칙 목록 (ufw status numbered 파싱) |
| POST | `/api/v1/firewall/rules` | 규칙 추가 (allow/deny, port, protocol, from IP) |
| DELETE | `/api/v1/firewall/rules/:number` | 규칙 삭제 (번호 기반) |
| GET | `/api/v1/firewall/ports` | 열린 포트 목록 (ss -tlnp 파싱) |

### Fail2ban

| Method | Endpoint | 설명 |
|--------|----------|------|
| GET | `/api/v1/fail2ban/status` | Fail2ban 서비스 상태 + 설치 여부 |
| POST | `/api/v1/fail2ban/install` | Fail2ban 설치 (apt) |
| GET | `/api/v1/fail2ban/jails` | jail 목록 + 각 jail 상태 |
| GET | `/api/v1/fail2ban/jails/:name` | jail 상세 (차단 IP 목록, 통계) |
| POST | `/api/v1/fail2ban/jails/:name/enable` | jail 활성화 |
| POST | `/api/v1/fail2ban/jails/:name/disable` | jail 비활성화 |
| POST | `/api/v1/fail2ban/jails/:name/unban` | IP 차단 해제 |

## 프론트엔드

- 단일 페이지 `/firewall` → 3개 탭
- 디스크 관리 페이지(`Disk.tsx` + `disk/`)와 동일한 패턴
- `web/src/pages/Firewall.tsx` (탭 컨테이너)
- `web/src/pages/firewall/FirewallRules.tsx` (UFW 규칙 탭)
- `web/src/pages/firewall/FirewallPorts.tsx` (열린 포트 탭)
- `web/src/pages/firewall/FirewallFail2ban.tsx` (Fail2ban 탭)

## 수정 파일 목록

| 파일 | 작업 |
|------|------|
| `internal/api/handlers/firewall.go` | 신규 — UFW + Fail2ban 핸들러 |
| `internal/api/router.go` | 수정 — 라우트 등록 |
| `web/src/pages/Firewall.tsx` | 신규 — 탭 컨테이너 |
| `web/src/pages/firewall/*.tsx` | 신규 — 3개 탭 컴포넌트 |
| `web/src/App.tsx` | 수정 — 라우트 추가 |
| `web/src/components/Layout.tsx` | 수정 — 네비게이션 추가 |
| `web/src/lib/api.ts` | 수정 — API 메서드 추가 |
| `web/src/types/api.ts` | 수정 — 타입 정의 추가 |
| `web/src/i18n/locales/ko.json` | 수정 — 한국어 번역 |
| `web/src/i18n/locales/en.json` | 수정 — 영어 번역 |
| `docs/specs/api-spec.md` | 수정 — API 문서 업데이트 |
| `docs/specs/frontend-spec.md` | 수정 — 프론트엔드 문서 업데이트 |
