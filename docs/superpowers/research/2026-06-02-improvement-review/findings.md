# SFPanel 기능 개선 리뷰 (2026-06-02)

전체 메인 기능에 대한 **기능 개선 / 최적화 / UI·UX** 관점의 코드 리뷰.
6개 도메인 병렬 리뷰(Opus) 결과 종합. 방향: **기존 모듈 심화**(신규 대형 서브시스템 X).
v0.16.x에서 고친 버그 재보고 아님 — 앞으로의 개선 기회.

---

## Tier 1 — "백엔드는 됐는데 UI가 없는" 항목 (최고 ROI, 대부분 S~M)

이미 백엔드/엔드포인트/일부 api 클라이언트까지 구현돼 있는데 UI만 빠져 미완성으로 방치된 기능들.

| # | 항목 | 현황 | 작업 |
|---|------|------|------|
| 1 | **디스크 사용량(du) 탐색기** | `POST /disks/usage` + 트리 빌더 + `api.getDiskUsage()` 전부 존재, **호출하는 컴포넌트가 없음** | 트리 렌더 React 페이지 + `du`에 `-x` 추가 (disk_swap.go:548, api.ts:1176) |
| 2 | **방화벽 `force=true` 오버라이드** | lockout 가드가 409 "pass force"인데 api 클라이언트가 force 미전송 → 운영자 막힘 | `enable/add/deleteFirewallRule`에 `force?` + 409 시 확인 다이얼로그 (firewall_ufw.go:90/300/392, api.ts:1469) |
| 3 | **클러스터 토큰 목록/취소** | `CreateToken`만 있고 list/revoke 없음 — 잘못 만든 토큰 무효화 불가 | `GET/DELETE /cluster/tokens` + UI (handler.go:594) |
| 4 | **클러스터 주소 PATCH / Leave** | `UpdateNodeAddress`·`LeaveCluster` 라우트 존재, api/UI 없음 (CLAUDE.md가 "주소 변경의 핵심 경로"라 명시) | 노드 행 인라인 편집 + per-node Leave (handler.go:810/955) |
| 5 | **클러스터 롤링 업데이트 진행 UX** | 백엔드가 per-node·per-step 구조화 SSE를 보내는데 UI는 색점 로그로 평탄화 | per-node 스테퍼 + N/total 바 (ClusterOverview.tsx:178) |
| 6 | **감사 로그 필터 + 범위삭제** | `?days/before` 범위삭제 백엔드 지원, UI는 전체 wipe. user/method/status 필터 없음 | 필터 바 + "N일 이전 삭제" (audit/handler.go:55/200) |
| 7 | **터미널 세션 재연결** | 서버가 세션+스크롤백 보존하는데 프론트가 새 탭ID 발급 → 도달 불가 | `GET /terminal/sessions` + 재연결 picker (handler.go:450) |
| 8 | **알림 specific-scope 노드 선택** | 백엔드 지원, UI에 노드 멀티셀렉트 없어 "specific" 규칙이 노드 타겟 불가(빈 배열→fail-closed) | scope=specific 시 노드 멀티셀렉트 (AlertSettings.tsx:583) |

## Tier 2 — 오인 유발(correctness illusion) — 작은 수정, 사용자 신뢰 직결

| # | 항목 | 문제 |
|---|------|------|
| 1 | **패키지 SSE 성공 아이콘 거짓** | apt/npm/AI 설치가 `ERROR:` 후 `[DONE]`이면 **초록 체크 + "완료"** 표시 (Packages.tsx:1535). `[DONE] ok/error` 또는 `ERROR:` 스캔 |
| 2 | **알림 이력 필드 불일치** | 백엔드 `node_id`/`sent_channels` vs 프론트 `node`/`status` → Node/Status 컬럼 항상 빈값 (AlertSettings.tsx:42) |
| 3 | **대시보드 stale 가정** | 주석은 1s 샘플인데 실제 WS 2s, 라이브 버퍼가 12/24h 레인지 라벨을 못 채움 (Dashboard.tsx:38/181) |
| 4 | **compose `hasComposeHealthcheck` 취약** | 라인 들여쓰기 스캐너라 anchor/extends/flow-style 헬스체크 누락 → "none" 오표시 (compose.go:558) |

## Tier 3 — 고가치 기능 심화 (기존 모듈 성숙)

| 기능 | 제안 | 영향/노력 |
|------|------|-----------|
| **Monitor/Dashboard** | `MetricsPoint`에 disk%/네트워크 추가(차트에 디스크·네트워크 표시 가능해짐) + **단일 공유 메트릭 브로드캐스터**(N×/2s syscall → 1) | High/M |
| **Compose** | per-stack **자동 업데이트 스케줄**(check+update+cron 조합, Watchtower 대체). 앱스토어 업데이트 배지/인플레이스 업그레이드까지 같이 해결 | High/M |
| **Services** | **유닛 파일 보기**(`systemctl cat`, 읽기전용) — 가장 요청 많은 추가. + reload 액션, 라이브 로그(SSE) | High/S~M |
| **Process** | **프로세스 트리**(ppid) 뷰, 절대 RSS, 추가 시그널(HUP/STOP/CONT), renice | High/M |
| **WireGuard** | **피어 추가 + 키 생성 + 클라이언트 QR**(현재 raw config 수기 입력만), boot 자동시작 | High/M |
| **Files** | 재귀 **검색**, **복사**, 아카이브 압축/해제, 다중선택 일괄삭제 | High/M |
| **Cron** | **run-now**(즉시 실행+출력), 출력 캡처/로그, **시각적 스케줄 빌더** + 다음 실행시각 미리보기 | High/M |
| **Settings/2FA** | **복구 코드**(현재 인증앱 분실 시 DB 수정만이 복구) | High/M |
| **Alert** | 규칙 **test-fire**, **webhook 채널**(Slack/email 커버), mute/snooze | Med~High/S~M |
| **Settings/Backup** | 로컬 **백업 스케줄**(cron+보존수), 복원 dry-run 미리보기 | High/M |
| **Disk** | SMART self-test 트리거+결과, SMART 추세, RAID 재동기화 % | Med |
| **Docker** | 단독 컨테이너 **생성 폼**(현재 없음, compose/셸로만 가능), 이미지 prune 용량 미리보기 | High/L, Med |

## Tier 4 — 최적화

- **공유 메트릭 브로드캐스터**: history 수집기(60s) + WS(클라이언트당 2s)가 같은 `GetMetrics`를 중복 호출. 뷰어 수만큼 syscall 선형 증가 → 1회 샘플 후 fan-out.
- **portmap/docker 방화벽 중복 iptables**: `GetDockerFirewall`가 `iptables nat -L DOCKER`를 요청당 2회+, `AddDockerUserRule`이 또 1회. DNAT 맵 1회 계산 후 공유 (firewall_docker.go:67/249/192).
- **클러스터 15s 삼중 폴링** → WS relay로 overview 델타 push (탭당 4 leader RPC/15s).
- **Docker stats**: inspect 다이얼로그 3s + 페이지 10s 이중 폴링, WS stats 스트림화.
- **process 200ms 블로킹 sleep**을 요청 경로 밖 백그라운드 워머로.
- **로그 unfiltered 경로 2회 읽기**(tail + wc -l), fail2ban `getJailInfo` jail당 5 subprocess.
- **packages**: dev-tool 상태 5요청 → 1 집계 엔드포인트, SSE 파서 4중복 → 공유 헬퍼.

## Tier 5 — UI/UX 가로지르는 폴리시

- **로딩 스켈레톤 / 에러 상태 부재**: 거의 모든 페이지가 `.catch(()=>{})`로 에러를 삼켜 "빈 상태"와 "실패"가 구분 안 됨. 대시보드/서비스/프로세스/감사 등.
- **네이티브 `window.prompt`/`confirm()`**: 앱스토어 고급 재인증, compose 롤백, 클러스터 disband, 파일 삭제 등 — 디자인 언어 깨고 스타일 불가. 스타일드 다이얼로그로.
- **파괴적 작업 type-to-confirm**: 디스크 포맷/파티션·RAID 삭제, 클러스터 disband — 장치명/클러스터명 타이핑 요구.
- **i18n 누락 하드코딩**: compose("변경사항 미리보기" 등), 앱스토어 안전경고(영문), 파일 5MB 경고, 알림 severity 라벨.
- **출력/로그 다이얼로그 copy 버튼 부재** + 자동스크롤이 위로 스크롤한 사용자를 끌어내림.
- **모바일/밀도**: portmap·감사·알림 와이드 테이블 모바일 카드 폴백 없음.
- **정렬 UX**: 프로세스 정렬 단방향(토글 없음), 컬럼 헤더 클릭 정렬 부재.

## 각 그룹 "단일 최고가치"
1. Dashboard/Monitor/Process/Services → **MetricsPoint에 disk/net 추가 + 공유 브로드캐스터**
2. Docker/Compose/AppStore → **per-stack 자동 업데이트 스케줄**(앱스토어 업데이트도 함께 해결)
3. Disk/Files/Logs → **디스크 사용량 탐색기 UI 출하**(백엔드 이미 완성)
4. Network/Firewall → **방화벽 force=true 오버라이드 UI**(반쪽 안전기능 완성)
5. Packages/Cron/Terminal → **SSE 작업 실제 성공/실패 표시**(전 기능의 오인 제거)
6. Cluster/Settings/Alert/Audit → **롤링 업데이트 스테퍼 + WS push overview**
