# SFPanel 모듈 코드리뷰 결과 (2026-06-02)

전체 22개 feature 모듈 + 핵심 `internal/cluster`·`internal/monitor`·`internal/docker`를
7개 도메인 그룹으로 병렬 리뷰한 결과. **안정성 / 최적화 / 보안** 관점 고신호 결함만 수집.
모든 위치는 리뷰 시점(2026-06-02, main @ f70fb32) 기준 — 수정 전 실제 라인은 재확인 필요.

## 가로지르는 테마

1. **스트리밍/연결 종료 시 누수** — 클라이언트 disconnect 후에도 subprocess·goroutine·registry
   호출이 살아 죽은 커넥션에 write. (appstore, docker, terminal, cluster relay)
2. **느린 exec/syscall을 잡은 채 락 유지** — 캐시 갱신 write-lock이 200ms~수백ms 동안
   대시보드 리더 전체를 블록. (process, services, disk)
3. **DB row 에러 무시** (`rows.Err()`/`Scan` 미확인) — 중간 에러를 삼키고 truncated 결과 반환.
   (audit, settings, docker/observability, alert×2)
4. **경로/allowlist 검증 불일치** — logs는 폐기된 `strings.Contains("..")` + 경계 없는 prefix.
   disk device 파싱 엣지케이스가 파괴적 growpart/mkfs를 잘못 겨냥.
5. **방화벽 lockout 가드의 source-scope 맹점** — EnableUFW/AddRule이 목적지 포트만 보고 From 무시.
6. **동기 알림 발송이 hot goroutine 블록** — 느린 webhook이 docker 이벤트 파이프라인 전체 정지.
7. **취약한 텍스트 파싱** — `LastIndex('#')` 오분할, 프로토콜 substring 매칭 등 회귀.

---

## 검증 로그 (최종)

브랜치 `fix/module-hardening-2026-06`. 각 항목을 "수정" 전에 코드로 재현·검증함.
총 49건 중 **42건 수정**, **7건 오탐/비이슈로 변경 안 함**(아래 명시).

### 수정 완료 ✅
- Critical: C1, C4
- High: H1, H4, H5, H6, H7, H8, H9, H10, H11, H12, H13, H14, H15, H16, H17
- Medium: M1, M2, M3, M4, M5, M6, M8, M9, M10, M11, M12, M14, M15, M16, M17, M18, M19
- Low: L1, L2, L3, L4, L5, L7, L8

### 오탐 / 비이슈 — 코드 변경 안 함 (근거)
- **C1** — 부분 정정: 운영 호출부가 trailing-slash prefix(`/var/log/`)라 실제 익스플로잇은 성립 안 함.
  다만 caller의 trailing-slash 규율에 암묵 의존 + `..` 거짓 양성 존재 → 세그먼트 경계 매칭으로 하드닝(✅).
  보안 등급은 과대평가였음.
- **C2/C3** — 재현 안 됨. `AddRule`은 포트 필수 + `wouldLockOutOnAdd`가 From 무관하게 22/panel deny를
  차단. `EnableUFW` staging 파서가 `from`-선두 규칙을 스킵해 source-scoped allow를 fail-safe로 거부.
- **H2** — 의도된 설계. `installCtx`는 detached(클라 끊겨도 docker 계속 진행)가 명시적 의도. 끊김 시 취소는
  정상 설치를 중단시킴. `sendSSE`는 닫힌 conn에 조용히 실패(블록/누수 없음).
- **H3** — 거의 비이슈. `PullImage`가 SDK `ImagePull(ctx)` 반환 reader라 ctx 취소 시 `Decode` 자동 해제.
  남은 건 "레지스트리 정지 + 클라 연결 유지" 케이스뿐인데, 그 stall 타임아웃은 정상 느린 pull을 죽일 위험.
- **M7** — 비이슈. `getParentDisk`는 이미 "suffix 숫자 AND prefix 끝 숫자" 가드 + fallback으로 `/dev/sdp1`
  등을 올바르게 처리. 제안된 좁은 regex는 `mmcblk0p1` 등을 오히려 회귀시킴.
- **M13** — 보류. `validLogPath`는 이미 `..` 차단. 잔여 위험은 admin-only 엔드포인트에서 `.log` 파일 읽기
  (admin은 logs 모듈로 이미 가능). `/var/log` 강제는 정당한 jail(nginx/docker/custom) 파손 위험.
- **L6** — moot. go.mod이 Go 1.25라 루프 변수가 per-iteration(Go 1.22 변경). `&t`는 안전.
- **L9** — 미적용(YAGNI). 사용처 없는 `joinOnly bool` 필드 추가는 프로젝트가 지양하는 투기적 스캐폴딩.

## Critical

| ID | 위치 | 카테고리 | 문제 | 수정 방향 |
|----|------|----------|------|-----------|
| C1 | `internal/feature/logs/handler.go:45` | 보안 | `validateCustomSourcePath`가 폐기된 `..` substring + 경계 없는 prefix(`/var/log-evil/x`가 통과). tail -F로 읽기 표면 확장 | `filepath.Clean` 동등성 + 세그먼트 경계(`prefix+"/"`) 매칭 |
| C2 | `internal/feature/firewall/firewall_ufw.go:88-94` (EnableUFW) | 안정성 | `hasAccessRule`이 To포트만 보고 From 무시 → source 제한 allow가 거짓 "안전" 판정, default-deny flip 시 잠김 | 비-Anywhere From 규칙은 일반 접근 규칙으로 안 치거나 경고 표면화 |
| C3 | `internal/feature/firewall/firewall_lockout.go:77` (wouldLockOutOnAdd) | 안정성 | add 가드가 deny/reject/limit만 검사, From 범위 개념 없음 | From 섀도잉 고려해 가드 확장 |
| C4 | `internal/feature/alert/manager.go:176` (Fire) ← `internal/monitor/docker_events.go:156` | 안정성 | docker 이벤트 단일 goroutine에서 webhook POST 동기 호출 → 느린 webhook이 전체 정지 | Fire/채널 send를 bounded async 큐로 분리 |

## High

| ID | 위치 | 카테고리 | 문제 | 수정 방향 |
|----|------|----------|------|-----------|
| H1 | `internal/feature/auth/refresh.go:110-227` | 안정성 | refresh 토큰 회전이 deferred read-tx → 동시 회전 시 write-write 데드락, spurious 500 | `BEGIN IMMEDIATE` 또는 guarded `UPDATE ... WHERE consumed_at IS NULL` + RowsAffected==0 처리 |
| H2 | `internal/feature/appstore/handler.go:814-840` (streamCommand) | 안정성 | detached installCtx가 끊긴 SSE에 10분간 write, 조기 취소 없음 | write 실패/ctx.Done() 감지해 install 취소 |
| H3 | `internal/feature/docker/handler.go:355-371` (PullImage) | 안정성 | ctx.Done()을 non-blocking으로만 보고 decoder.Decode에서 블록 | Decode를 goroutine+channel로 select |
| H4 | `internal/feature/docker/observability.go:153` (GetRecentEvents) | 안정성 | rows.Scan 반환값 무시 → zero값 row append | Scan 에러 체크 후 continue |
| H5 | `internal/feature/process/handler.go:58-77` | 최적화 | 캐시 write-lock을 /proc 2회 순회 + 200ms sleep 동안 유지 → 모든 리더 블록 | 로컬 수집 후 swap만 락 |
| H6 | `internal/feature/services/handler.go:279-298` | 최적화 | 락을 systemctl 2회 exec에 걸쳐 유지 + serviceCache가 package-global | Handler로 이동 + swap만 락 |
| H7 | `internal/feature/disk/disk_filesystems.go:847-854` (findMountPoint) | 안정성 | EvalSymlinks 실패 시 `""==""`로 pseudo-fs 오매칭 → format 보호 가드 오판 | 빈 문자열일 때 비교 스킵 |
| H8 | `internal/feature/disk/disk_blocks.go:240-269` (GetSmartInfo) | 안정성 | smartctl 명시 타임아웃 없어 불량 디스크에서 최대 5분 goroutine 점유 | 30~60s ctx.WithTimeout |
| H9 | `internal/feature/terminal/handler.go:499-529` | 안정성 | WS→PTY 입력 루프에 read deadline/keepalive 없음 → half-open이 TCP RTO까지 점유 | websocket.startWSKeepalive 패턴 적용 |
| H10 | `internal/cluster/grpc_server.go:295-368` (ProxyRequest) | 안정성 | 응답 전체를 io.ReadAll, gRPC 4MB recv 한계서 신규 대형 라우트 조용히 truncate | 크기 임박 시 명확한 에러 반환 |
| H11 | `internal/feature/firewall/firewall_docker.go:412-427` (DeleteDockerUserRule) | 안정성 | LOG 동반 규칙을 인덱스 number-1 가정 + 락 없음 → 동시 삭제 시 off-by-one | DOCKER-USER 변경 직렬화 또는 spec 매칭 |
| H12 | `internal/feature/firewall/firewall_docker.go:184-209` (lookupDNATMapping) | 보안 | 프로토콜을 라인 전체 `udp` substring으로 판정 → 오매칭 시 무효 방화벽 규칙 | regex 캡처 그룹 사용 |
| H13 | `internal/feature/alert/manager.go:178-234` (Fire cooldown) | 안정성 | cooldown RLock 읽고 성공 후 write → check-then-act 레이스로 중복 발송 | 단일 Lock에서 reserve |
| H14 | `internal/feature/alert/manager.go:161` (evaluate) | 최적화 | rows 커서 연 채 동기 Fire → 느린 webhook이 커서 점유 + 다음 틱 지연 | payload 모아 커서 닫고 async |
| H15 | `internal/feature/firewall/firewall_docker.go:248,358,384` | 최적화 | GET마다 iptables -L 다회 + 컨테이너당 docker inspect N회 | inspect 1회 배치 + NAT 체인 1회 파싱 |
| H16 | `internal/feature/docker/handler.go:390-417` / `internal/docker/compose.go:761-796` | 최적화 | 이미지 업데이트 확인이 이미지당 직렬 레지스트리 왕복, 타임아웃 없음 | bounded worker 병렬 + 개별 타임아웃 |
| H17 | `internal/feature/portmap/handler.go:118` | 안정성 | `LastIndex('#')`로 코멘트 분할(형제 파서는 `" # "`로 수정됨) → 규칙 오파싱 | `" # "`로 분할 |

## Medium

| ID | 위치 | 카테고리 | 문제 |
|----|------|----------|------|
| M1 | `internal/feature/compose/handler.go:348-409` | 보안 | SSE error 이벤트에 SanitizeOutput 미적용 |
| M2 | `internal/feature/audit/handler.go:116-176` (ClearAuditLogs) | 안정성 | tombstone INSERT + DELETE가 비트랜잭션 → 기록 count와 실제 삭제 불일치 |
| M3 | `internal/feature/auth/handler.go:157-159` (Login) | 보안 | unknown username 실패가 audit 미기록 → credential-spray 추적 불가 |
| M4 | `internal/feature/system/handler.go:988` (RestoreBackup) | 안정성 | restart가 fire-and-forget, 실패 무시 + Commander 우회 (RunUpdate와 불일치) |
| M5 | `internal/monitor/history.go:123-126` | 안정성 | cleanup DELETE가 bare `go func()` (비 safe.Go) → panic 시 프로세스 크래시 |
| M6 | `internal/feature/disk/disk_filesystems.go:217-220` (ExpandFilesystem) | 안정성 | validateDeviceName이 `/dev/mapper/` 거부 → LVM expand 브랜치 데드코드 |
| M7 | `internal/feature/disk/disk_filesystems.go:391-417` (getParentDisk) | 안정성 | `LastIndex("p")`로 nvme 분할 → 잘못된 파티션에 growpart |
| M8 | `internal/feature/logs/handler.go:373-379` (LogStreamWS) | 안정성 | 파일 부재 시 WS 업그레이드 전에 JSON 응답 → 클라가 101 대신 200 받음 |
| M9 | `internal/feature/files/handler.go:417-422` (WriteFile .bak) | 안정성 | 기존 파일 백업이 os.ReadFile 무제한 → 대형 파일 OOM |
| M10 | `internal/feature/cron/handler.go:198,235` | 안정성 | 라인-인덱스 주소 지정 → 클라 GET/PUT 사이 변경 시 엉뚱한 job 수정 (TOCTOU) |
| M11 | `internal/feature/network/tailscale.go:491`, `wireguard.go:240,418,458,484` | 보안 | err.Error() raw 반환 (SanitizeOutput 미적용) |
| M12 | `internal/feature/firewall/firewall_fail2ban.go:773` (writeFile) | 안정성 | jail 설정을 비원자적 os.WriteFile → 크래시 시 truncated .local |
| M13 | `internal/feature/firewall/firewall_fail2ban.go:616,671` (validLogPath) | 보안 | logpath가 `*`/`/` 허용, `..`만 차단 → 정보 노출 가능 |
| M14 | `internal/feature/network/tailscale.go:413` | 안정성 | auth-URL 추출이 임의 https 매칭 → 잘못된 URL 표면화 |
| M15 | `internal/feature/alert/container_rules.go:217-219` (recentRestartTimes) | 안정성 | rows.Scan/Err 무시 → restart-loop 오판 |
| M16 | `internal/feature/alert/handler.go:491` (ListHistory) | 안정성 | COUNT(*) Scan 에러 무시 → 페이저 total=0 |
| M17 | `internal/cluster/ws_relay.go:114-163` | 안정성 | 한쪽 종료 시 반대편 conn 미close → 최대 60s 세션 잔류 |
| M18 | `internal/cluster/grpc_client.go:131-154` (ConnPool.Get) | 안정성 | connMaxAge가 생성시각 기준 → 활성 연결도 5분마다 재핸드셰이크 |
| M19 | `internal/feature/cluster/handler.go:1099-1132,1164` (ClusterUpdate) | 안정성 | FSM 스냅샷 한 번 캡처 후 분 단위로 stale 인덱싱 |

## Low

| ID | 위치 | 카테고리 | 문제 |
|----|------|----------|------|
| L1 | `internal/feature/audit/handler.go:81-89`, `internal/feature/settings/handler.go:87-93` | 안정성 | rows.Err() 미확인 |
| L2 | `internal/feature/auth/handler.go:312-326` (insertSecurityAuditRow) | 안정성 | 큐 full 시 unbounded `go insert` |
| L3 | `internal/feature/logs/handler.go:167-188,306-323` (ReadLog) | 최적화 | 로그 파일 2~3회 재읽기(grep+wc+tail) |
| L4 | `internal/feature/disk/disk_blocks.go:32-82` | 안정성 | diskCache가 package-global mutable |
| L5 | `internal/feature/portmap/ss_parser.go:31` | 안정성 | ss 필드 인덱스 고정 가정 (형제 파서는 fallback 있음) |
| L6 | `internal/feature/firewall/firewall_fail2ban.go:643-646` | 안정성 | 루프변수 주소 취득(`&t`), break 의존 |
| L7 | `internal/cluster/manager.go:1042-1145`, `heartbeat.go:94-119` | 안정성 | bare `go func()` (비 safe.Go) |
| L8 | `internal/cluster/manager.go:265-267` (verifySelfAddress) | 안정성 | ctx 미인지 `time.Sleep(10s)` |
| L9 | `internal/cluster/grpc_client.go:45-60` (DialNodeInsecure) | 보안 | InsecureSkipVerify 클라가 join 외 재사용 방지 구조 없음 |

## 깨끗한(주요 결함 없음) 영역

`auth/bounds.go`, `network/network.go`, `internal/cluster/raft_fsm.go`, `internal/cluster/tls.go`,
`websocket/handler.go`, `packages` apt 스트리밍, `cron` 명령주입 방어, `channels/discord·telegram`,
`disk` LVM/RAID/partition/swap 파서, `files/upload_policy.go`, `disk/mountguard.go`,
`portmap/aggregator.go`.
