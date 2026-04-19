# R1 — 신규 설치 → 첫 로그인 플로우

검토 일시: 2026-04-19
범위: `scripts/install.sh`, `cmd/sfpanel/main.go`, `internal/config/config.go`, `internal/db/sqlite.go`, `internal/db/migrations.go`, `internal/feature/auth/handler.go`, `internal/api/middleware/auth.go`, `internal/auth/*`

## 요약
- install.sh 구조는 합리적이지만 **바이너리 다운로드 체크섬 검증이 전무** (README의 "SHA-256 checksum verification" 광고와 상충)
- 서비스 중단 순서가 잘못되어 **검증 실패 시 서비스 다운 + 교체 불가** 상태 가능
- `ALTER TABLE` 에러 메시지 문자열 매칭이 SQLite 드라이버 업그레이드 시 깨질 수 있음 (부팅 불가 위험)
- Setup TOCTOU/bcrypt/HS256 pinning/만료 검증 모두 양호
- **로그인 감사 기록이 전혀 없음** — 침해 사고 조사 시 로그 증적 없음

## Phase 1 — install.sh

### P1-1 (P1) 바이너리 다운로드 체크섬 검증 없음
`scripts/install.sh:80-113` `download_binary`
GitHub Releases의 `checksums.txt`는 이미 제공되고 `updatePanel()` Go 코드는 검증하는데 install.sh는 무시. MITM + `curl | bash` 패턴 결합 시 공급망 공격 노출.
**수정**:
```bash
curl -fsSL "${CHECKSUMS_URL}" -o "${tmp_dir}/checksums.txt"
expected=$(grep "sfpanel_${version}_linux_${arch}.tar.gz" "${tmp_dir}/checksums.txt" | awk '{print $1}')
actual=$(sha256sum "${tmp_dir}/sfpanel.tar.gz" | awk '{print $1}')
[ "$expected" = "$actual" ] || { log_error "Checksum mismatch"; exit 1; }
```

### P1-2 (P1) 서비스 중단 후 검증 실패 시 다운 상태
`scripts/install.sh:104-112`
현재: download → tar → systemctl stop → install. 중간 실패 시 서비스 다운 + 새 바이너리 미배치.
**수정**: 모든 검증 완료 후에만 `systemctl stop`.

### P1-3 (P1) `/var/lib/sfpanel` 디렉토리 0755
`scripts/install.sh:115-117` `setup_dirs`
DB 파일(bcrypt 해시, TOTP 시크릿, 감사 로그)이 world-readable.
**수정**: `chmod 700 "$DATA_DIR"`.

### P1-4 (P2) JWT 시크릿 생성 방식 불일치
`scripts/install.sh:125-126`
`head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 32` → 제거 문자 비율에 따라 엣지 케이스 가능. Go `generateRandomSecret()`은 `crypto/rand` + `hex.EncodeToString`으로 일관성 유지.
**수정**: `od -vN 32 -An -tx1 /dev/urandom | tr -d ' \n'`.

### P1-5 (P2) 기존 config 신규 필드 누락 침묵 처리
`scripts/install.sh:119-151`. 구버전 config 존재 시 "skipping"만 출력. Go가 기본값으로 채우지만 운영자는 모름.

### P1-6 (P1) 설치 직후 서비스 상태 확인 없음
`scripts/install.sh:194-198`. `systemctl start` 직후 `print_success`. 포트 충돌/config 오류 시에도 "installed successfully" 출력.
**수정**: `sleep 3 && systemctl is-active --quiet $SERVICE_NAME || fail`.

### P1-7 (P2) uninstall 시 시크릿 잔류 경고 부족
`scripts/install.sh:241-250`. `/etc/sfpanel/config.yaml` (JWT secret), `/var/lib/sfpanel/sfpanel.db` (bcrypt, TOTP) 잔류 명시 부족.

### P1-8 (P2) 네트워크 오류 메시지 불명확
IPv6-only/프록시 환경 진단 안내 부족.

## Phase 2 — 첫 부팅

### P2-1 (P1) `ALTER TABLE` 에러 메시지 문자열 매칭이 취약
`internal/db/migrations.go:95-99`
```go
if strings.Contains(m, "ALTER TABLE") && strings.Contains(err.Error(), "duplicate column") { continue }
```
`modernc.org/sqlite` 업그레이드 시 메시지 변경되면 마이그레이션 실패 → `db.Open()` 실패 → 부팅 불가.
**수정**: `PRAGMA table_info`로 컬럼 존재 사전 확인. 장기: `schema_migrations` 테이블 기반 버전 마이그레이션.

### P2-2 (P2) `main.go`가 `log` 패키지 사용
`cmd/sfpanel/main.go:81, 269, 278, 302`. CLAUDE.md "slog exclusively" 규칙 위반. CLI 서브커맨드 전반.

### P2-3 (P1) `config.Validate()` 빈 JWT secret 허용 (R0 S-11 재확인)
`internal/config/config.go:60-68`. env `SFPANEL_JWT_SECRET=""`로 빈 값 덮어씀 후 `Validate()` 통과 → 공격자 임의 JWT 발급 가능.
**수정**: `if len(c.Auth.JWTSecret) < 16 { return err }`.

### P2-4 (P2) `AtomicWriteFile` 상위 디렉토리 부재 시 시크릿 메모리 전용
`internal/config/config.go:125-133`. `/etc/sfpanel` 없으면 저장 실패만 Warn, 시크릿이 메모리에만 → 재시작 시 기존 토큰 전부 무효화. `setup_dirs`가 먼저 실행되면 안전.

### P2-5 (P2) 포트 충돌 시 systemd 재시작 루프
`cmd/sfpanel/main.go:237-240`. `os.Exit(1)` + `Restart=always` + `RestartSec=5`. P1-6과 결합 시 사용자가 인지 못하는 루프.

## Phase 3 — 셋업 위저드

### P3-1 (P2) `SetupAdmin` rate limiter 구조가 `Login`과 다름
전역 뮤텍스 vs `sync.Map`. 기능 OK.

### P3-2 양호 TOCTOU 방어 재확인 (R0 F-14)

### P3-3 (P2) 유저네임 길이/문자 검증 없음 (R2 A-06과 동일)

### P3-4 (P2) `SetupAdmin` 응답에 JWT 즉시 포함 — 프런트 `SetupGuard` 상태 전환 확인 필요 (R10 I-1 관련)

## Phase 4 — 최초 로그인

### P4-1 (P1) **로그인 감사 로그 미기록**
`internal/api/middleware/audit.go:33-35`. `/api/v1/auth/login`과 `/api/v1/auth/setup`이 감사 미들웨어에서 의도적 제외(비밀번호 로깅 방지). 결과적으로 **성공/실패 로그인 이벤트 미기록**. 메모리 `loginAttempts`는 재시작 시 초기화. 침해 조사 시 로그인 이력 전혀 없음.
**수정**: Login 핸들러 성공/실패 경로에서 username/IP/status만 `audit_logs`에 INSERT.

### P4-2 (P2) TOTP 브루트포스 실질 위협 낮음
Rate limit이 로그인 시도 전체를 카운트하므로 시간당 약 60회. 100만 경우의 수 → 16,667시간. 이론상 안전. 별도 TOTP 실패 rate limit은 강화 옵션.

### P4-3 Rate limit "5회 실패 → 6번째 차단" (R0 P0-B 재검증 완료)

## Phase 5 — 첫 인증 요청

### P5-1 (P1) `?token=` 쿼리 파라미터가 모든 보호 라우트에 허용
`internal/api/middleware/auth.go:67-69`. 파일 다운로드 폴백으로 의도됐으나 전역 허용. JWT가 액세스 로그/Referer/브라우저 히스토리 노출 (R0 F-05 재확인).
**수정**: `/files/download` 경로로 한정.

### P5-2 (P1) CORS 정책에 리버스 프록시 오리진 없음
`internal/api/router.go:53-62`. `http://localhost:5173` + Tauri만 허용. `https://panel.example.com` 같은 프로덕션 오리진 차단.
**수정**: `config.yaml`에 `server.allowed_origins: []` 추가.

### P5-3 클러스터 TLS 비활성화 — 단일 노드 무관, 클러스터 추가 시 발현 (R0 P0-A)

## UX 트랩
1. 방화벽 포트 개방 안내 없음
2. http 기본값 브라우저 경고
3. 첫 방문 URL(`/setup`) 미표시
4. 서비스 시작 실패 시 침묵
5. `sfpanel reset`이 WAL 파일 미삭제 (기능 문제 없음, 데이터 잔류)
6. 업그레이드 후 기존 토큰 유지 여부 문서에 없음

## R0 교차
| R0 항목 | 이 플로우에서 |
|---------|-------------|
| P0-A 클러스터 TLS | 단일 노드 잠재, 클러스터 즉시 발현 |
| P0-D alert shutdown | `router.go:182`에서 발현 |
| S-11 빈 JWT | `Validate()` 체크 없음 (P2-3 재확인) |
| S-01 `/opt/stacks` 하드코딩 | install.sh config에 `stacks_path` 없어도 Go 기본값 처리 |
| 묶음 β WS 인증 | 최초 로그인 플로우에서 WS 미사용 (R7 범위) |

## 권장 즉시 수행 PR
1. install.sh 체크섬 검증 추가 (P1-1) — 최우선
2. 서비스 중단 순서 수정 (P1-2)
3. 설치 후 서비스 상태 확인 (P1-6)
4. `/var/lib/sfpanel` 권한 700 (P1-3)
5. `config.Validate()`에 JWT secret 길이 체크 (P2-3)
6. 로그인 감사 기록 (P4-1)
