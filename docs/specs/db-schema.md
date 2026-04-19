# SFPanel DB 스키마

## 개요

- **DB 엔진**: SQLite (드라이버: `modernc.org/sqlite`, CGO-free 순수 Go 구현)
- **DB 파일 경로**: 설정 파일(`config.yaml`)의 `database.path` 값 (기본값: `./sfpanel.db`, 운영 권장: `/var/lib/sfpanel/sfpanel.db`). 환경 변수 `SFPANEL_DB_PATH`로 오버라이드 가능.
- **연결 DSN 프래그마** (`internal/db/sqlite.go`):
  - `journal_mode(WAL)` — Write-Ahead Logging, 동시 읽기 지원 (연결 후 `PRAGMA journal_mode;`로 명시적 검증, 다를 경우 `PRAGMA journal_mode=WAL` 재설정)
  - `busy_timeout(5000)` — 잠금 대기 5초
  - `foreign_keys(on)` — (현재 FK 제약 없음, 향후 대비)
  - `synchronous(NORMAL)` — 성능·안정성 균형
  - `mmap_size(268435456)` — 메모리 매핑 256MB
  - `cache_size(-8000)` — 페이지 캐시 8MB
- **연결 풀**: `db.SetMaxOpenConns(1)` — SQLite 단일 연결 정책 (쓰기 경합 최소화)
- **마이그레이션 방식**: `db.Open()` 호출 시 `RunMigrations()` 자동 실행. 모든 DDL에 `CREATE TABLE IF NOT EXISTS` 사용하여 멱등성(idempotent) 보장. 별도의 마이그레이션 버전 추적 테이블 없이 순차적으로 실행. `ALTER TABLE ADD COLUMN`의 "duplicate column" 에러는 무시 (재실행 안전).

### 소스 파일

| 파일 | 역할 |
|------|------|
| `internal/db/sqlite.go` | DB 연결 열기, WAL 모드 설정, 마이그레이션 실행 |
| `internal/db/migrations.go` | DDL 문 정의 및 순차 실행 |

---

## 테이블

총 10개 테이블 + SQLite 내부 테이블 1개 (sqlite_sequence)

> **참고**: `sites` 테이블은 v0.3에서 제거됨 (Nginx 가상호스트 기능 폐기). `metrics_history` 테이블이 v0.2에서 추가됨. `alert_channels`, `alert_rules`, `alert_history` 테이블이 v0.7에서 추가됨.

### admin

관리자 계정 정보. 단일 관리자(Single Admin) 모델로, 최초 셋업 위저드에서 1개의 계정만 생성됨. 이미 계정이 존재하면 추가 생성 불가.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 관리자 고유 ID |
| username | TEXT | O | - | UNIQUE | 관리자 로그인 아이디 |
| password | TEXT | O | - | - | bcrypt 해시된 비밀번호 |
| totp_secret | TEXT | - | NULL | - | TOTP 2FA 시크릿 키 (NULL이면 2FA 미설정) |
| created_at | DATETIME | - | CURRENT_TIMESTAMP | - | 계정 생성 시각 (UTC) |

**자동 인덱스:**
- `sqlite_autoindex_admin_1` — `username` UNIQUE 인덱스

**사용처:**
- `feature/auth/handler.go` — 로그인 인증 (`SELECT id, password, totp_secret`), 비밀번호 변경 (`UPDATE password`), 2FA 설정 (`UPDATE totp_secret`), 셋업 상태 확인 (`SELECT COUNT(*)`), 초기 계정 생성 (`INSERT`)

---

### sessions

JWT 토큰 세션 관리 테이블. 스키마는 정의되어 있으나, 현재 코드에서는 JWT 토큰을 stateless 방식으로 검증하고 있어 이 테이블에 대한 INSERT/SELECT/DELETE 로직은 아직 구현되지 않음 (향후 토큰 블랙리스트/리프레시 토큰 용도로 예약).

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 세션 고유 ID |
| token_hash | TEXT | O | - | UNIQUE | JWT 토큰의 해시값 (원본 저장 방지) |
| expires_at | DATETIME | O | - | - | 세션 만료 시각 |

**자동 인덱스:**
- `sqlite_autoindex_sessions_1` — `token_hash` UNIQUE 인덱스

**사용처:**
- 현재 미사용 (스키마만 존재). 향후 토큰 무효화(revocation) 또는 리프레시 토큰 구현 시 활용 예정.

---

### compose_projects

Docker Compose 프로젝트 메타데이터. YAML 파일은 디스크(`/var/lib/sfpanel/compose/{name}/docker-compose.yml`)에 저장되며, DB에는 프로젝트 메타정보만 기록.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 프로젝트 고유 ID |
| name | TEXT | O | - | UNIQUE | 프로젝트명 (디렉토리명으로도 사용) |
| yaml_path | TEXT | O | - | - | docker-compose.yml 파일의 절대 경로 |
| status | TEXT | - | 'stopped' | - | 프로젝트 상태 (`running`, `stopped`) |
| created_at | DATETIME | - | CURRENT_TIMESTAMP | - | 프로젝트 등록 시각 (UTC) |

**자동 인덱스:**
- `sqlite_autoindex_compose_projects_1` — `name` UNIQUE 인덱스

**사용처:**
- `feature/compose/handler.go` — 프로젝트 CRUD (`INSERT`, `SELECT`, `DELETE`), 상태 업데이트 (`UPDATE status` to `running`/`stopped`), 목록 조회 (`SELECT ... ORDER BY id`)

---

### settings

키-값 기반 애플리케이션 설정 저장소. 런타임에 변경 가능한 설정을 관리.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| key | TEXT | - | - | PK | 설정 키 (예: `terminal_timeout`) |
| value | TEXT | O | - | - | 설정 값 (문자열로 저장) |

**자동 인덱스:**
- `sqlite_autoindex_settings_1` — `key` PRIMARY KEY 인덱스

**알려진 설정 키:**

| key | 기본값 (코드) | 설명 |
|-----|---------------|------|
| `terminal_timeout` | `"30"` | 터미널 세션 유휴 타임아웃 (분 단위, 0=무제한) |
| `max_upload_size` | `"1024"` | 파일 업로드 최대 크기 (MB 단위). `feature/files/handler.go`에서 참조 |
| `appstore_cache` | — | 앱 스토어 카탈로그 캐시 (JSON 문자열) |
| `appstore_installed_<appID>` | — | 설치된 앱별 메타데이터 (동적 키, 앱스토어 설치 시 INSERT, 제거 시 DELETE). `<appID>`는 앱스토어 카탈로그의 앱 식별자 |

**사용처:**
- `feature/settings/handler.go` — 전체 설정 조회 (`SELECT key, value`), 설정 저장 (`INSERT ... ON CONFLICT DO UPDATE`, UPSERT 패턴), 개별 설정 읽기 (`SELECT value WHERE key = ?`)
- `feature/appstore/handler.go` — 카탈로그 캐시 저장/조회, 설치 기록 관리
- `feature/compose/handler.go` — Compose 스택 삭제 시 매칭되는 `appstore_installed_*` 엔트리 정리
- `feature/files/handler.go` — 업로드 크기 제한 조회

---

### custom_log_sources

사용자 정의 로그 소스 관리 테이블. 기본 제공 시스템 로그 소스 외에 사용자가 직접 추가한 커스텀 로그 파일 경로를 저장.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 로그 소스 고유 ID |
| source_id | TEXT | O | - | UNIQUE | 로그 소스 식별자 (내부 키, 예: `custom_myapp`) |
| name | TEXT | O | - | - | 로그 소스 표시명 (예: `My App Log`) |
| path | TEXT | O | - | - | 로그 파일 절대 경로 (예: `/var/log/myapp/app.log`) |
| created_at | DATETIME | - | CURRENT_TIMESTAMP | - | 로그 소스 등록 시각 (UTC) |

**자동 인덱스:**
- `sqlite_autoindex_custom_log_sources_1` — `source_id` UNIQUE 인덱스

**사용처:**
- `feature/logs/handler.go` — 커스텀 로그 소스 목록 조회 (`SELECT source_id, name, path`), 로그 소스 추가 (`INSERT INTO custom_log_sources`), 로그 소스 삭제 (`DELETE FROM custom_log_sources WHERE id = ?`), 기본 시스템 로그 소스와 병합하여 전체 소스 목록 제공

---

### metrics_history

시스템 메트릭 히스토리. **60초** 간격으로 CPU/메모리 사용률을 기록하여 24시간 시계열 차트에 활용. `monitor/history.go`에서 관리 (`historyInterval = 60 * time.Second`).

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| time | INTEGER | - | - | PK | Unix timestamp (**밀리초** 단위, `time.Now().UnixMilli()`) |
| cpu | REAL | O | - | - | CPU 사용률 (0.0~100.0) |
| mem_percent | REAL | O | - | - | 메모리 사용률 (0.0~100.0) |

**사용처:**
- `monitor/history.go` — 60초 간격 메트릭 기록 (`INSERT OR REPLACE`), 24시간 이전 데이터 자동 삭제 (`DELETE ... WHERE time <= ?`), 히스토리 조회 (`SELECT ... ORDER BY time ASC`)
- `feature/monitor/handler.go` — `/api/v1/system/metrics-history` 응답 데이터

**보존 정책:** 24시간 롤링 윈도우 (~1,440 포인트 @ 60초 간격). 24시간보다 오래된 행은 수집 시점에 `DELETE` 수행. 별도의 포인트 수 상한은 없음 (시간 기반 정리만 사용).

---

### audit_logs

API 요청 감사 로그 테이블. 모든 인증된 API 요청의 메서드, 경로, 상태 코드, 클라이언트 IP, 사용자명을 기록. 클러스터 환경에서는 노드 ID도 추적.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 로그 고유 ID |
| username | TEXT | O | `''` | - | 요청한 사용자명 (JWT에서 추출) |
| method | TEXT | O | - | - | HTTP 메서드 (GET, POST, PUT, DELETE, PATCH) |
| path | TEXT | O | - | - | 요청 경로 (예: `/api/v1/docker/containers`) |
| status | INTEGER | O | `0` | - | HTTP 응답 상태 코드 (200, 400, 500 등) |
| ip | TEXT | O | `''` | - | 클라이언트 IP 주소 |
| node_id | TEXT | O | `''` | - | 클러스터 노드 ID (단일 서버 시 빈 문자열) |
| created_at | DATETIME | - | CURRENT_TIMESTAMP | - | 로그 기록 시각 (UTC) |

**인덱스:**
- `idx_audit_logs_created_at` — `created_at` 컬럼 인덱스 (시간 기반 조회 성능 최적화)

**마이그레이션 이력:**
- v0.5: 초기 테이블 생성 (id, username, method, path, status, ip, created_at)
- v0.5.5: `node_id` 컬럼 추가 (`ALTER TABLE audit_logs ADD COLUMN node_id TEXT NOT NULL DEFAULT ''`)

**사용처:**
- `api/middleware/audit.go` — AuditMiddleware에서 인증된 **비-GET/HEAD/OPTIONS** 요청만 자동 기록 (`INSERT`, 비동기). `/api/v1/auth/login`, `/api/v1/auth/setup`은 제외 (비밀번호 보호)
- `feature/audit/handler.go` — 감사 로그 목록 조회 (`SELECT ... ORDER BY id DESC LIMIT ? OFFSET ?`), 페이지네이션 지원 (page, limit 파라미터)
- `feature/audit/handler.go` — 감사 로그 전체 삭제 (`DELETE FROM audit_logs`)

**보존 정책:** 최대 50,000행. 초과 시 가장 오래된 10,000행을 삭제 (AuditMiddleware가 5분 주기로 정리).

---

### alert_channels

알림 채널 설정 테이블. Discord, Telegram 등 알림을 전송할 대상 채널을 관리.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 채널 고유 ID |
| type | TEXT | O | - | - | 채널 유형 (`discord`, `telegram`) |
| name | TEXT | O | - | - | 채널 표시명 |
| config | TEXT | O | - | - | 채널 설정 (JSON, 유형별 다름) |
| enabled | INTEGER | - | 1 | - | 활성화 여부 (0=비활성, 1=활성) |
| created_at | DATETIME | - | CURRENT_TIMESTAMP | - | 생성 시각 (UTC) |

**config JSON 형식 (유형별):**

| type | config 예시 |
|------|-------------|
| `discord` | `{"webhook_url":"https://discord.com/api/webhooks/..."}` |
| `telegram` | `{"bot_token":"123456:ABC-DEF...","chat_id":"-1001234567890"}` |

**사용처:**
- `feature/alert/handler.go` — 채널 CRUD (`INSERT`, `SELECT`, `UPDATE`, `DELETE`), 테스트 전송 (`POST /alerts/channels/:id/test`)
- `feature/alert/manager.go` — 알림 발송 시 활성화된 채널 조회 (`SELECT ... WHERE enabled = 1`)

---

### alert_rules

알림 규칙 정의 테이블. CPU/메모리/디스크 사용률 등의 조건을 감시하여 임계치 초과 시 지정된 채널로 알림을 전송.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 규칙 고유 ID |
| name | TEXT | O | - | - | 규칙명 |
| type | TEXT | O | - | - | 감시 대상 (`cpu`, `memory`, `disk`) |
| condition | TEXT | O | - | - | 조건 (JSON, 예: `{"operator":">","threshold":90}`). 필드는 `operator`(>/</>=/<=)와 `threshold`(float, 0~100) 두 개. `manager.go:ruleCondition` 구조 |
| channel_ids | TEXT | O | `'[]'` | - | 알림 대상 채널 ID 배열 (JSON) |
| severity | TEXT | - | `'warning'` | - | 심각도 (`info`, `warning`, `critical`) |
| cooldown | INTEGER | - | 300 | - | 재알림 방지 쿨다운 (초) |
| node_scope | TEXT | - | `'all'` | - | 노드 범위 (`all`, `specific`) |
| node_ids | TEXT | - | `'[]'` | - | 대상 노드 ID 배열 (JSON, `node_scope=specific` 시) |
| enabled | INTEGER | - | 1 | - | 활성화 여부 |
| created_at | DATETIME | - | CURRENT_TIMESTAMP | - | 생성 시각 (UTC) |

**사용처:**
- `feature/alert/handler.go` — 규칙 CRUD. `condition` JSON 유효성은 `json.Valid()`로만 검증
- `feature/alert/manager.go` — **60초 주기**로 조건 평가 (`Start()` 고루틴), 활성 규칙 조회 (`SELECT ... WHERE enabled = 1`), 임계치 초과 시 매칭 채널로 발송 및 `alert_history` 기록. `cooldown` 내 중복 발송 억제 (메모리 상태)

---

### alert_history

알림 발송 이력 테이블. 규칙 조건이 트리거될 때마다 발송 내역을 기록.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| id | INTEGER | - | 자동 | PK, AUTOINCREMENT | 이력 고유 ID |
| rule_id | INTEGER | - | - | - | 트리거된 규칙 ID |
| rule_name | TEXT | - | - | - | 규칙명 (규칙 삭제 후에도 이력 보존) |
| type | TEXT | - | - | - | 알림 유형 (`cpu`, `memory`, `disk`) |
| severity | TEXT | - | - | - | 심각도 |
| message | TEXT | - | - | - | 알림 메시지 본문 |
| node_id | TEXT | - | `''` | - | 트리거된 노드 ID (클러스터 환경) |
| sent_channels | TEXT | - | `'[]'` | - | 실제 전송된 채널 ID 배열 (JSON) |
| created_at | DATETIME | - | CURRENT_TIMESTAMP | - | 발송 시각 (UTC) |

**인덱스:**
- `idx_alert_history_created_at` — `created_at` 컬럼 인덱스 (시간 기반 조회 성능 최적화)

**사용처:**
- `feature/alert/manager.go` — 알림 발송 시 이력 기록 (`INSERT`). `sent_channels`에는 **실제 전송에 성공한** 채널 ID 배열만 저장
- `feature/alert/handler.go` — 이력 조회 (`SELECT ... ORDER BY id DESC LIMIT ? OFFSET ?`), 이력 전체 삭제 (`DELETE FROM alert_history`)

**보존 정책:** 자동 정리 없음. 관리자가 UI에서 수동으로 삭제해야 함.

**주의:** `rule_id`는 `alert_rules.id`를 논리적으로 참조하지만 FK 제약은 없음 — 규칙이 삭제되어도 이력은 보존됨 (`rule_name` 컬럼에 규칙명 스냅샷).

---

### sqlite_sequence (SQLite 내부)

SQLite의 AUTOINCREMENT 시퀀스를 추적하는 내부 시스템 테이블. `AUTOINCREMENT`를 사용하는 테이블마다 자동 생성됨.

| 컬럼 | 타입 | 설명 |
|------|------|------|
| name | TEXT | 테이블명 |
| seq | INTEGER | 해당 테이블의 마지막 AUTOINCREMENT 값 |

---

## 마이그레이션 이력

마이그레이션은 `internal/db/migrations.go`의 `migrations` 슬라이스에 DDL 문이 순서대로 정의됨. 별도의 버전 추적 메커니즘 없이 `CREATE TABLE IF NOT EXISTS`로 멱등성 보장.

### v1: 초기 스키마

단일 마이그레이션 배치로 7개 테이블 생성:

1. **admin** — 관리자 계정 (단일 사용자)
2. **compose_projects** — Docker Compose 프로젝트 메타데이터
3. **sessions** — JWT 세션 관리 (예약)
4. **settings** — 키-값 설정 저장소
5. **custom_log_sources** — 사용자 정의 로그 소스
6. **metrics_history** — 시스템 메트릭 24시간 히스토리
7. **audit_logs** — API 요청 감사 로그 (메서드/경로/상태/IP/사용자명/노드ID)

### v2: 클러스터 지원

- `audit_logs` 테이블에 `node_id TEXT NOT NULL DEFAULT ''` 컬럼 추가
- `idx_audit_logs_created_at` 인덱스 생성

### v3: 알림 시스템

- `alert_channels` 테이블 생성 (이메일, Webhook, Discord 채널)
- `alert_rules` 테이블 생성 (조건 기반 알림 규칙)
- `alert_history` 테이블 생성 (알림 발송 이력)
- `idx_alert_history_created_at` 인덱스 생성

---

## ER 다이어그램 (텍스트)

```
┌─────────────────┐
│     admin        │
├─────────────────┤
│ id (PK, AUTO)   │
│ username (UQ)    │
│ password         │
│ totp_secret      │
│ created_at       │
└─────────────────┘

┌─────────────────┐
│    sessions      │
├─────────────────┤
│ id (PK, AUTO)   │
│ token_hash (UQ)  │
│ expires_at       │
└─────────────────┘

┌─────────────────────┐
│  compose_projects    │
├─────────────────────┤
│ id (PK, AUTO)       │
│ name (UQ)            │
│ yaml_path            │
│ status               │
│ created_at           │
└─────────────────────┘

┌─────────────────┐
│    settings      │
├─────────────────┤
│ key (PK)         │
│ value            │
└─────────────────┘

┌─────────────────────┐
│ custom_log_sources   │
├─────────────────────┤
│ id (PK, AUTO)       │
│ source_id (UQ)       │
│ name                 │
│ path                 │
│ created_at           │
└─────────────────────┘

┌─────────────────────┐
│  metrics_history     │
├─────────────────────┤
│ time (PK)            │
│ cpu (REAL)           │
│ mem_percent (REAL)   │
└─────────────────────┘

┌─────────────────────┐
│    audit_logs        │
├─────────────────────┤
│ id (PK, AUTO)       │
│ username             │
│ method               │
│ path                 │
│ status (INT)         │
│ ip                   │
│ node_id              │
│ created_at           │
└─────────────────────┘
  ↑ idx_audit_logs_created_at

┌─────────────────────┐
│  alert_channels      │
├─────────────────────┤
│ id (PK, AUTO)       │
│ type                 │
│ name                 │
│ config (JSON)        │
│ enabled (INT)        │
│ created_at           │
└─────────────────────┘

┌─────────────────────┐
│   alert_rules        │
├─────────────────────┤
│ id (PK, AUTO)       │
│ name                 │
│ type                 │
│ condition (JSON)     │
│ channel_ids (JSON)   │
│ severity             │
│ cooldown (INT)       │
│ node_scope           │
│ node_ids (JSON)      │
│ enabled (INT)        │
│ created_at           │
└─────────────────────┘

┌─────────────────────┐
│  alert_history       │
├─────────────────────┤
│ id (PK, AUTO)       │
│ rule_id (INT)        │
│ rule_name            │
│ type                 │
│ severity             │
│ message              │
│ node_id              │
│ sent_channels (JSON) │
│ created_at           │
└─────────────────────┘
  ↑ idx_alert_history_created_at
```

> 참고: 현재 테이블 간 외래 키(FK) 관계는 없음. 모든 테이블이 독립적으로 운영됨. `alert_history.rule_id`는 논리적으로 `alert_rules.id`를 참조하지만 FK 제약은 설정되어 있지 않음 (규칙 삭제 후에도 이력 보존).

---

## 데이터 보존 정책 요약

| 테이블 | 보존 | 정리 주기 | 구현 위치 |
|--------|------|----------|-----------|
| `audit_logs` | 최대 50,000행 | 5분 주기, 초과 시 오래된 10,000행 `DELETE` | `api/middleware/audit.go` |
| `metrics_history` | 24시간 (~1,440 포인트 @ 60초 간격) | 60초 주기, `time <= cutoff` `DELETE` | `internal/monitor/history.go` |
| `alert_history` | 제한 없음 | 수동 (UI에서 전체 삭제) | — |
| 그 외 모든 테이블 | 제한 없음 | 수동 (CRUD 엔드포인트 경유) | — |

## 백업/복구

**현재 백업/복구 자동화 기능은 구현되어 있지 않음.** `scripts/install.sh`는 데이터 디렉토리(`/var/lib/sfpanel/`)만 생성하며 백업 로직은 포함하지 않음. 운영자가 필요 시 다음 방식 중 선택:

- **파일 복사 기반**: 서비스 정지 후 `sfpanel.db`, `sfpanel.db-shm`, `sfpanel.db-wal` 세 파일 함께 복사
- **온라인 백업**: `PRAGMA wal_checkpoint(RESTART)` 실행 후 `sfpanel.db` 단일 파일 복사 (프로세스 중단 없이 가능, 다만 `-wal` 파일에 부분 변경이 남아 있을 수 있음)

## 참고사항

- **WAL 모드**: 연결 시 DSN에서 `journal_mode(WAL)`을 지정하고, `Open()` 직후 `PRAGMA journal_mode;`로 실제 모드를 검증. `wal`이 아니면 명시적 `PRAGMA journal_mode=WAL`로 재설정 후 실패 시 에러 반환. 따라서 정상 시동된 프로세스는 항상 WAL 모드로 동작.
- **동시성**: `busy_timeout(5000)`으로 5초간 잠금 대기 + `SetMaxOpenConns(1)`로 단일 쓰기 경합. 읽기는 WAL 덕분에 쓰기와 병행 가능.
- **AUTOINCREMENT vs ROWID**: 모든 id 컬럼에 `AUTOINCREMENT` 사용. SQLite에서 AUTOINCREMENT는 id 재사용을 방지하여 삭제된 행의 id가 재할당되지 않음을 보장.
- **시간대**: 모든 DATETIME 컬럼은 `CURRENT_TIMESTAMP` (UTC) 기준. 클라이언트에서 로컬 시간으로 변환.
- **비밀번호 보안**: `admin.password`는 bcrypt 해시로 저장 (`auth.HashPassword` 사용).
- **TOTP 시크릿**: `admin.totp_secret`은 **평문으로 저장됨** (TOTP 검증 시 복호화 필요). DB 파일 자체의 권한을 `0600` 이하로 유지할 것. `scripts/install.sh`는 `/etc/sfpanel/config.yaml`에는 `chmod 600`을 적용하지만 DB 파일 권한은 명시적으로 설정하지 않으므로 운영자가 주기적으로 확인 권장.
- **외래 키**: 스키마 간 FK 제약은 전혀 설정되어 있지 않음 (`foreign_keys(on)` 프래그마는 향후 대비). `alert_history.rule_id` 등은 논리적 참조만 존재.
