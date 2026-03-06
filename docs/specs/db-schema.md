# SFPanel DB 스키마

## 개요

- **DB 엔진**: SQLite (드라이버: `modernc.org/sqlite`, CGO-free 순수 Go 구현)
- **DB 파일 경로**: 설정 파일(`config.yaml`)의 `database.path` 값 (기본값: `./sfpanel.db`, 운영 권장: `/var/lib/sfpanel/sfpanel.db`)
- **연결 옵션**: `?_journal_mode=WAL&_busy_timeout=5000` (WAL 모드, 동시 읽기 지원, 5초 busy timeout)
- **마이그레이션 방식**: `db.Open()` 호출 시 `RunMigrations()` 자동 실행. 모든 DDL에 `CREATE TABLE IF NOT EXISTS` 사용하여 멱등성(idempotent) 보장. 별도의 마이그레이션 버전 추적 테이블 없이 순차적으로 실행.

### 소스 파일

| 파일 | 역할 |
|------|------|
| `internal/db/sqlite.go` | DB 연결 열기, WAL 모드 설정, 마이그레이션 실행 |
| `internal/db/migrations.go` | DDL 문 정의 및 순차 실행 |

---

## 테이블

총 6개 테이블 + SQLite 내부 테이블 1개 (sqlite_sequence)

> **참고**: `sites` 테이블은 v0.3에서 제거됨 (Nginx 가상호스트 기능 폐기). `metrics_history` 테이블이 v0.2에서 추가됨.

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
- `handlers/auth.go` — 로그인 인증 (`SELECT id, password, totp_secret`), 비밀번호 변경 (`UPDATE password`), 2FA 설정 (`UPDATE totp_secret`), 셋업 상태 확인 (`SELECT COUNT(*)`), 초기 계정 생성 (`INSERT`)

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
- `docker/compose.go` — 프로젝트 CRUD (`INSERT`, `SELECT`, `DELETE`), 상태 업데이트 (`UPDATE status` to `running`/`stopped`), 목록 조회 (`SELECT ... ORDER BY id`)

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

**사용처:**
- `handlers/settings.go` — 전체 설정 조회 (`SELECT key, value`), 설정 저장 (`INSERT ... ON CONFLICT DO UPDATE`, UPSERT 패턴), 개별 설정 읽기 (`SELECT value WHERE key = ?`)

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
- `handlers/logs.go` — 커스텀 로그 소스 목록 조회 (`SELECT source_id, name, path`), 로그 소스 추가 (`INSERT INTO custom_log_sources`), 로그 소스 삭제 (`DELETE FROM custom_log_sources WHERE id = ?`), 기본 시스템 로그 소스와 병합하여 전체 소스 목록 제공

---

### metrics_history

시스템 메트릭 히스토리. 30초 간격으로 CPU/메모리 사용률을 기록하여 24시간 시계열 차트에 활용. `monitor/history.go`에서 관리.

| 컬럼 | 타입 | NOT NULL | 기본값 | 제약조건 | 설명 |
|------|------|----------|--------|----------|------|
| time | INTEGER | - | - | PK | Unix timestamp (초 단위) |
| cpu | REAL | O | - | - | CPU 사용률 (0.0~100.0) |
| mem_percent | REAL | O | - | - | 메모리 사용률 (0.0~100.0) |

**사용처:**
- `monitor/history.go` — 30초 간격 메트릭 기록 (`INSERT`), 24시간 이전 데이터 자동 삭제, 히스토리 조회 (`SELECT ... ORDER BY time`)
- `handlers/dashboard.go` — `/api/v1/system/metrics-history` 응답 데이터

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

단일 마이그레이션 배치로 6개 테이블 생성:

1. **admin** — 관리자 계정 (단일 사용자)
2. **compose_projects** — Docker Compose 프로젝트 메타데이터
3. **sessions** — JWT 세션 관리 (예약)
4. **settings** — 키-값 설정 저장소
5. **custom_log_sources** — 사용자 정의 로그 소스
6. **metrics_history** — 시스템 메트릭 24시간 히스토리

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
```

> 참고: 현재 테이블 간 외래 키(FK) 관계는 없음. 모든 테이블이 독립적으로 운영됨.

---

## 참고사항

- **WAL 모드**: 연결 문자열에 `_journal_mode=WAL`을 지정하나, SQLite 드라이버 특성상 실제 저널 모드는 연결마다 다를 수 있음. 현재 실제 DB는 `delete` 모드로 확인됨.
- **동시성**: `_busy_timeout=5000`으로 5초간 lock 대기. 단일 바이너리 서버이므로 동시 쓰기 경합은 제한적.
- **AUTOINCREMENT vs ROWID**: 모든 id 컬럼에 `AUTOINCREMENT` 사용. SQLite에서 AUTOINCREMENT는 id 재사용을 방지하여 삭제된 행의 id가 재할당되지 않음을 보장.
- **시간대**: 모든 DATETIME 컬럼은 `CURRENT_TIMESTAMP` (UTC) 기준. 클라이언트에서 로컬 시간으로 변환.
- **비밀번호 보안**: `admin.password`는 bcrypt 해시로 저장 (`auth.HashPassword` 사용).
- **TOTP 시크릿**: `admin.totp_secret`은 평문으로 저장됨. TOTP 검증 시 필요하므로 복호화 가능한 형태여야 하지만, DB 파일 자체의 접근 제어가 중요.
