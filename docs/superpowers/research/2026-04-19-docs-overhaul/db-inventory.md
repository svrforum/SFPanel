# SFPanel DB 스키마 인벤토리

## 1. 엔진 / 파일 / 연결

| 항목 | 값 |
|------|-----|
| 엔진 | SQLite (`modernc.org/sqlite`, CGO-free 순수 Go) |
| 경로 | `config.yaml` `database.path`, env `SFPANEL_DB_PATH` 오버라이드 |
| 기본값 | `./sfpanel.db` (install.sh 설치 시 `/var/lib/sfpanel/sfpanel.db`) |
| 마이그레이션 | `db.Open()` → `RunMigrations()`, 순차 DDL 배치, 모두 `CREATE TABLE IF NOT EXISTS` (버전 테이블 없음, 멱등) |
| 연결 풀 | `SetMaxOpenConns(1)` — 단일 연결 정책 |

## 2. SQLite 프래그마 (internal/db/sqlite.go)

| 프래그마 | 값 | 목적 |
|---------|-----|------|
| `journal_mode` | WAL | 동시 읽기 |
| `busy_timeout` | 5000ms | 잠금 대기 |
| `foreign_keys` | on | (현재 FK 관계 없음) |
| `synchronous` | NORMAL | 성능/안정성 균형 |
| `mmap_size` | 256MB | 메모리 매핑 |
| `cache_size` | -8000 (= 8MB) | 캐시 |

## 3. 테이블 카탈로그 (10개)

### 3.1 admin — 관리자 계정 (단일 사용자 모델)
- `id` INTEGER PK AUTOINCREMENT
- `username` TEXT UNIQUE NOT NULL
- `password` TEXT NOT NULL — bcrypt 해시
- `totp_secret` TEXT NULL — 2FA 미설정 시 NULL, **평문 저장** (DB 파일 권한 중요)
- `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
- 사용처: `auth/handler.go` (로그인/2FA/초기 생성), `cluster/handler.go` (클러스터 초기화 시 조회), `cluster/join.go` (비밀번호 동기화)

### 3.2 sessions — JWT 세션 (예약, **현재 미사용**)
- `id` INTEGER PK AUTOINCREMENT
- `token_hash` TEXT UNIQUE NOT NULL
- `expires_at` DATETIME NOT NULL
- 사용처: 없음. JWT는 stateless 검증. 향후 블랙리스트/리프레시용으로 예약.

### 3.3 compose_projects — Docker Compose 프로젝트 메타
- `id` INTEGER PK AUTOINCREMENT
- `name` TEXT UNIQUE NOT NULL (디렉토리명 겸용)
- `yaml_path` TEXT NOT NULL (절대경로, YAML 본체는 디스크)
- `status` TEXT DEFAULT 'stopped' ('running'/'stopped')
- `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
- 사용처: `compose/handler.go` CRUD + 상태 업데이트

### 3.4 settings — KV 설정 저장소
- `key` TEXT PK
- `value` TEXT NOT NULL
- 알려진 키: `terminal_timeout` (분, 기본 30, 0=무제한), `max_upload_size` (MB, 기본 1024), `appstore_cache` (JSON), `appstore_installed_{appID}` (JSON, 동적 키)
- 사용처: `settings/handler.go` UPSERT, `appstore/handler.go` 캐시/설치기록, `compose/handler.go` 설치기록 정리, `logs/handler.go` 참조
- UPSERT 패턴: `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`

### 3.5 custom_log_sources — 사용자 정의 로그 경로
- `id` INTEGER PK AUTOINCREMENT
- `source_id` TEXT UNIQUE NOT NULL (내부 ID, 예: `custom_myapp`)
- `name` TEXT NOT NULL (UI 표시명)
- `path` TEXT NOT NULL (절대경로)
- `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
- 사용처: `logs/handler.go` — 시스템 기본 소스(`defaultLogSources`)와 병합

### 3.6 metrics_history — CPU/메모리 시계열
- `time` INTEGER PK — Unix ms
- `cpu` REAL NOT NULL (0~100)
- `mem_percent` REAL NOT NULL (0~100)
- 사용처: `monitor/history.go` 수집/삭제, `feature/monitor/handler.go` `/api/v1/system/metrics-history`
- 수집 간격: **60초** (⚠️ 기존 문서의 "30초"는 오류, 코드가 정확)
- 보존: 최대 2880 포인트 (~24h), 24h 초과 DELETE

### 3.7 audit_logs — API 요청 감사
- `id` INTEGER PK AUTOINCREMENT
- `username` TEXT NOT NULL DEFAULT ''
- `method`, `path` TEXT NOT NULL
- `status` INTEGER NOT NULL DEFAULT 0
- `ip` TEXT NOT NULL DEFAULT ''
- `node_id` TEXT NOT NULL DEFAULT '' (v0.5.5 추가, 클러스터 환경)
- `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
- 인덱스: `idx_audit_logs_created_at`
- 기록: 비-GET/HEAD/OPTIONS 요청만, `/api/v1/auth/login`·`/api/v1/auth/setup` 제외 (middleware/audit.go 비동기 INSERT)
- 보존: 최대 50,000행, 초과 시 가장 오래된 10,000행 삭제 (5분 주기)

### 3.8 alert_channels — Discord/Telegram 등 알림 채널
- `id` PK AUTOINCREMENT
- `type` TEXT NOT NULL (`discord`/`telegram`)
- `name` TEXT NOT NULL
- `config` TEXT NOT NULL (JSON, 유형별)
  - discord: `{"webhook_url":"..."}`
  - telegram: `{"bot_token":"...","chat_id":"..."}`
- `enabled` INTEGER DEFAULT 1
- `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
- 사용처: `alert/handler.go` CRUD + 테스트, `alert/manager.go` 발송

### 3.9 alert_rules — 알림 규칙 정의
- `id` PK
- `name` TEXT NOT NULL
- `type` TEXT NOT NULL (`cpu`/`memory`/`disk`)
- `condition` TEXT NOT NULL — JSON `{"operator":">","threshold":90}`
- `channel_ids` TEXT NOT NULL DEFAULT '[]' — JSON 배열
- `severity` TEXT DEFAULT 'warning' (info/warning/critical)
- `cooldown` INTEGER DEFAULT 300 — 재알림 쿨다운(초)
- `node_scope` TEXT DEFAULT 'all' (all/specific)
- `node_ids` TEXT DEFAULT '[]' — `specific` 시 대상 노드 JSON 배열
- `enabled` INTEGER DEFAULT 1
- `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
- 사용처: `alert/manager.go`가 60초마다 `WHERE enabled=1` 조회 → 조건 평가 → 임계치 초과 시 발송

### 3.10 alert_history — 알림 발송 이력
- `id` PK
- `rule_id` INTEGER (FK 없음, 규칙 삭제 후에도 보존)
- `rule_name`, `type`, `severity`, `message` TEXT
- `node_id` TEXT DEFAULT ''
- `sent_channels` TEXT DEFAULT '[]' — 실제 전송 성공 채널 ID JSON 배열
- `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
- 인덱스: `idx_alert_history_created_at`
- 정리: 자동 정리 없음, 관리자 수동 DELETE만

## 4. 마이그레이션 순서 (internal/db/migrations.go)

```
1. admin
2. compose_projects
3. sessions
4. settings
5. custom_log_sources
6. metrics_history
7. audit_logs
8. idx_audit_logs_created_at
9. ALTER TABLE audit_logs ADD COLUMN node_id TEXT NOT NULL DEFAULT ''  (v0.5.5)
10. alert_channels
11. alert_rules
12. alert_history
13. idx_alert_history_created_at
```
- `ALTER ADD COLUMN`의 "duplicate column" 에러는 무시 (idempotent)

## 5. 백업/복구

**현재 미구현.** `internal/feature/system/`에는 디스크/스왑 등 시스템 기능만 있고 DB 백업 코드 없음. `install.sh`도 설정/데이터 디렉토리만 만들고 백업은 미설치.

→ 권장: WAL 체크포인트 후 3파일 백업(`sfpanel.db`, `-shm`, `-wal`), 또는 단일 `sfpanel.db` 복제 전에 `PRAGMA wal_checkpoint(RESTART)`.

## 6. 스펙 문서와의 편차

| 항목 | 실제 코드 | docs/specs/db-schema.md | 조치 |
|------|---------|------------------------|------|
| metrics 수집 간격 | 60초 | "30초" | 문서 수정 필요 |
| `max_upload_size` 설정키 | 존재 (기본 1024MB) | 미기재 | 문서 추가 |
| `appstore_installed_*` 동적키 | 설치 앱별 생성/삭제 | 간략 언급만 | 상세 기술 |
| 백업/복구 | 미구현 | (문서에도 없음) | 어느 쪽 문서화할지 결정 |
| `admin.totp_secret` 암호화 | 평문 저장 | 언급 없음 | 문서에 명시 + 권한(0600) 주의 |

## 7. 주의사항

- `sessions` 테이블은 스키마만 존재, 런타임 미사용
- FK 제약은 전혀 사용하지 않음 (`alert_history.rule_id` 등)
- `admin.totp_secret` 평문 저장 — DB 파일 권한(0600) 필수
- `metrics_history` PK가 INTEGER(time)이지만 실제 값은 ms (LONG처럼 취급)
