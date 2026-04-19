# SFPanel API 스펙

> 마지막 동기화: 2026-04-19 · 기준 버전: v0.9.0 · 근거: `docs/superpowers/research/2026-04-19-docs-overhaul/api-inventory.md`

## 개요

### 기본 URL
```
/api/v1
```

### 인증 방식
- **JWT Bearer Token**: 보호된 엔드포인트는 HTTP 헤더에 `Authorization: Bearer <JWT>` 필요
- **WebSocket 인증**: 쿼리 파라미터 `?token=<JWT>`로 인증
- 토큰은 로그인 또는 초기 셋업 시 발급
- 토큰 만료 시간은 서버 설정 `config.yaml`의 `auth.token_expiry`로 결정 (기본값: 24시간)

### 응답 형식
모든 REST API 응답은 통일된 JSON 형식을 따릅니다.

**성공 응답:**
```json
{
  "success": true,
  "data": { ... }
}
```

**실패 응답:**
```json
{
  "success": false,
  "error": {
    "code": "ERROR_CODE",
    "message": "사람이 읽을 수 있는 에러 메시지"
  }
}
```

### 공통 에러 코드
| 코드 | HTTP 상태 | 설명 |
|------|-----------|------|
| `MISSING_TOKEN` | 401 | Authorization 헤더 누락 |
| `INVALID_TOKEN` | 401 | 유효하지 않거나 만료된 JWT 토큰 |
| `INVALID_REQUEST` | 400 | 잘못된 요청 본문 |
| `MISSING_FIELDS` | 400 | 필수 필드 누락 |

(전체 150+ 에러 코드는 `internal/api/response/errors.go` 참조.)

### SSE (Server-Sent Events) 스트리밍

일부 POST 엔드포인트는 `Content-Type: text/event-stream`으로 장시간 작업의 진행률을 스트리밍합니다. 주로 설치/업데이트/이미지 풀/Compose up·update에 사용. 표준 `Authorization: Bearer` 헤더로 인증하며, 서버는 각 이벤트 후 명시적으로 flush합니다. 종료 마커는 `data: [DONE]` (평문) 또는 `{"step":"complete"}` / `{"phase":"complete"}` (JSON). 자세한 엔드포인트·스키마는 `docs/specs/websocket-spec.md`의 "Server-Sent Events 스트리밍" 섹션 참조.

### 조건부 라우트 등록

- **Docker 라우트** (`/api/v1/docker/*` 26개): 서버 시작 시 Docker 소켓(`/var/run/docker.sock`) 접속에 성공해야만 등록됩니다. 실패 시 해당 경로는 404를 반환합니다. `/api/v1/packages/docker-status`로 현재 상태 확인 가능.

### 클러스터 프록시

클러스터가 활성화되어 있으면 모든 보호 라우트가 `?node=<nodeID>` 쿼리 파라미터를 지원합니다. 대상 노드가 현재 노드가 아닐 때 `ClusterProxyMiddleware`(`internal/api/middleware/proxy.go`)가 요청을 해당 노드로 포워딩합니다.

| 요청 유형 | 전송 방식 | 타임아웃 |
|-----------|----------|----------|
| 일반 REST | gRPC `ClusterService.ProxyRequest` | 30초 |
| SSE(`-stream` 접미사 또는 `/system/update`, `/appstore/.../install` 등) | HTTP 직접 릴레이 | 5분 |
| WebSocket | `WrapEchoWSHandler` 양방향 프록시 | WS 생명주기 전체 |

노드 간 내부 트래픽은 JWT 대신 `X-SFPanel-Internal-Proxy` 헤더(클러스터 CA 인증서 SHA-256 해시, 상수시간 비교)로 인증됩니다. 원본 사용자는 `X-SFPanel-Original-User` 헤더로 감사 로그에 전파됩니다.

### 감사 로그 제외

AuditMiddleware는 **비-GET/HEAD/OPTIONS** 요청만 기록합니다. `/api/v1/auth/login`, `/api/v1/auth/setup`은 비밀번호 보호를 위해 제외됩니다.

---

## 인증 API (`/api/v1/auth`)

### POST /api/v1/auth/login
사용자 로그인 및 JWT 토큰 발급.

- **인증 필요**: 아니오 (공개 엔드포인트)

**Request Body:**
```json
{
  "username": "string",
  "password": "string",
  "totp_code": "string"  // 선택 (2FA 활성화 시 필수)
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIs..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | username 또는 password 누락 |
| `INVALID_CREDENTIALS` | 401 | 잘못된 사용자명/비밀번호 |
| `TOTP_REQUIRED` | 400 | 2FA가 활성화되어 있으나 totp_code 누락 |
| `INVALID_TOTP` | 401 | 잘못된 2FA 코드 |

---

### GET /api/v1/auth/setup-status
초기 셋업 필요 여부 확인 (관리자 계정 존재 여부).

- **인증 필요**: 아니오 (공개 엔드포인트)

**Query Parameters:** 없음

**Response (200):**
```json
{
  "success": true,
  "data": {
    "setup_required": true
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `setup_required` | boolean | `true`이면 관리자 계정이 없어 셋업 필요 |

---

### POST /api/v1/auth/setup
초기 관리자 계정 생성. 관리자 계정이 이미 존재하면 실패.

- **인증 필요**: 아니오 (공개 엔드포인트, 1회용)

**Request Body:**
```json
{
  "username": "string",
  "password": "string"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIs..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | username 또는 password 누락 |
| `WEAK_PASSWORD` | 400 | 비밀번호 8자 미만 |
| `ALREADY_SETUP` | 409 | 관리자 계정이 이미 존재 |

---

### POST /api/v1/auth/2fa/setup
2FA(TOTP) 시크릿 생성. QR 코드 등록용 시크릿과 URL 반환.

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "secret": "JBSWY3DPEHPK3PXP",
    "url": "otpauth://totp/SFPanel:admin?secret=JBSWY3DPEHPK3PXP&issuer=SFPanel"
  }
}
```

---

### POST /api/v1/auth/2fa/verify
2FA 활성화를 위한 코드 검증 및 시크릿 저장.

- **인증 필요**: 예

**Request Body:**
```json
{
  "secret": "string",
  "code": "string"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "2FA enabled successfully"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | secret 또는 code 누락 |
| `INVALID_TOTP` | 400 | 잘못된 2FA 코드 |

---

### POST /api/v1/auth/change-password
비밀번호 변경.

- **인증 필요**: 예

**Request Body:**
```json
{
  "current_password": "string",
  "new_password": "string"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Password changed successfully"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | 현재/새 비밀번호 누락 |
| `WEAK_PASSWORD` | 400 | 새 비밀번호 8자 미만 |
| `INVALID_PASSWORD` | 401 | 현재 비밀번호 불일치 |
| `USER_NOT_FOUND` | 404 | 사용자를 찾을 수 없음 |

---

## 설정 API (`/api/v1/settings`)

### GET /api/v1/settings
전체 설정 조회. 기본값과 DB에 저장된 값을 병합하여 반환.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "terminal_timeout": "30"
  }
}
```

설정 기본값:
| 키 | 기본값 | 설명 |
|----|--------|------|
| `terminal_timeout` | `"30"` | 터미널 세션 타임아웃 (분). `"0"`이면 무제한 |

---

### PUT /api/v1/settings
설정 업데이트. 키-값 쌍으로 전달하며, 기존 키는 덮어쓰기.

- **인증 필요**: 예

**Request Body:**
```json
{
  "settings": {
    "terminal_timeout": "60"
  }
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Settings updated"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `EMPTY_SETTINGS` | 400 | settings 객체가 비어있음 |

---

## 시스템 API (`/api/v1/system`)

### GET /api/v1/system/info
시스템 호스트 정보, 현재 메트릭, 패널 버전 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "host": {
      "hostname": "string",
      "os": "string",
      "platform": "string",
      "kernel": "string",
      "uptime": 123456,
      "num_cpu": 4
    },
    "metrics": {
      "cpu": 23.5,
      "mem_total": 8388608000,
      "mem_used": 4194304000,
      "mem_percent": 50.0,
      "swap_total": 2147483648,
      "swap_used": 0,
      "swap_percent": 0.0,
      "disk_total": 107374182400,
      "disk_used": 53687091200,
      "disk_percent": 50.0,
      "net_bytes_sent": 1234567,
      "net_bytes_recv": 7654321,
      "timestamp": 1740000000000
    },
    "version": "0.9.0"
  }
}
```

**최상위 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `host` | object | 호스트 시스템 정보 |
| `metrics` | object | 현재 시스템 메트릭 |
| `version` | string | SFPanel 버전 (예: "0.9.0"). `DashboardHandler.Version` 필드에서 제공 |

**host 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `hostname` | string | 호스트명 |
| `os` | string | 운영체제 (예: "linux") |
| `platform` | string | 플랫폼 (예: "ubuntu") |
| `kernel` | string | 커널 버전 |
| `uptime` | number | 가동 시간 (초) |
| `num_cpu` | number | CPU 코어 수 |

**metrics 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `cpu` | number | CPU 사용률 (%) |
| `mem_total` | number | 전체 메모리 (bytes) |
| `mem_used` | number | 사용 중인 메모리 (bytes) |
| `mem_percent` | number | 메모리 사용률 (%) |
| `swap_total` | number | 전체 스왑 (bytes) |
| `swap_used` | number | 사용 중인 스왑 (bytes) |
| `swap_percent` | number | 스왑 사용률 (%) |
| `disk_total` | number | 전체 디스크 (bytes, 루트 파티션) |
| `disk_used` | number | 사용 중인 디스크 (bytes) |
| `disk_percent` | number | 디스크 사용률 (%) |
| `net_bytes_sent` | number | 누적 네트워크 송신 (bytes) |
| `net_bytes_recv` | number | 누적 네트워크 수신 (bytes) |
| `timestamp` | number | 수집 시각 (Unix ms) |

---

### GET /api/v1/system/metrics-history
24시간 메트릭 히스토리 조회. 30초 간격으로 수집된 최대 2880개 데이터 포인트.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "time": 1740000000000,
      "cpu": 23.5,
      "mem_percent": 50.0
    }
  ]
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `time` | number | 수집 시각 (Unix ms) |
| `cpu` | number | CPU 사용률 (%) |
| `mem_percent` | number | 메모리 사용률 (%) |

---

## 프로세스 API (`/api/v1/system/processes`)

### GET /api/v1/system/processes
CPU 사용률 기준 상위 10개 프로세스 (대시보드용).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "pid": 1234,
      "name": "node",
      "cpu": 45.2,
      "memory": 12.3,
      "status": "S",
      "user": "root",
      "command": "/usr/bin/node server.js"
    }
  ]
}
```

---

### GET /api/v1/system/processes/list
전체 프로세스 목록 조회 (검색/정렬 지원).

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `q` | string | 아니오 | 검색어 (이름, 명령어, 사용자, PID 매칭) |
| `sort` | string | 아니오 | 정렬 기준: `cpu` (기본값), `memory`, `pid`, `name` |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "processes": [
      {
        "pid": 1234,
        "name": "node",
        "cpu": 45.2,
        "memory": 12.3,
        "status": "S",
        "user": "root",
        "command": "/usr/bin/node server.js"
      }
    ],
    "total": 150
  }
}
```

**프로세스 객체 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `pid` | number | 프로세스 ID |
| `name` | string | 프로세스 이름 |
| `cpu` | number | CPU 사용률 (%) |
| `memory` | number | 메모리 사용률 (%) |
| `status` | string | 프로세스 상태 코드 (예: "S", "R", "Z") |
| `user` | string | 소유 사용자 |
| `command` | string | 전체 명령줄 |

---

### POST /api/v1/system/processes/:pid/kill
프로세스에 시그널 전송.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `pid` | 대상 프로세스 ID |

**Request Body:**
```json
{
  "signal": "TERM"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `signal` | string | 아니오 | 시그널 이름/번호. 기본값 `"TERM"`. 허용: `TERM`/`15`, `KILL`/`9`, `HUP`/`1`, `INT`/`2` |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Signal TERM sent to process 1234",
    "pid": 1234,
    "signal": "TERM"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PID` | 400 | 유효하지 않은 PID 형식 |
| `INVALID_SIGNAL` | 400 | 지원하지 않는 시그널 |
| `PROCESS_NOT_FOUND` | 404 | 프로세스를 찾을 수 없음 |
| `KILL_FAILED` | 500 | 시그널 전송 실패 |

---

## 파일 관리 API (`/api/v1/files`)

### GET /api/v1/files
디렉토리 내용 목록 조회.

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 기본값 | 설명 |
|----------|------|------|--------|------|
| `path` | string | 아니오 | `"/"` | 대상 디렉토리의 절대 경로 |

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "name": "etc",
      "path": "/etc",
      "size": 4096,
      "mode": "drwxr-xr-x",
      "modTime": "2026-01-15T10:30:00Z",
      "isDir": true
    },
    {
      "name": "config.yaml",
      "path": "/etc/config.yaml",
      "size": 1024,
      "mode": "-rw-r--r--",
      "modTime": "2026-01-15T10:30:00Z",
      "isDir": false
    }
  ]
}
```

정렬 순서: 디렉토리 우선, 이름 알파벳순 (대소문자 무시).

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PATH` | 400 | 절대 경로가 아니거나 `..` 포함 |
| `NOT_FOUND` | 404 | 디렉토리 없음 |
| `PERMISSION_DENIED` | 403 | 권한 부족 |

---

### GET /api/v1/files/read
파일 텍스트 내용 읽기 (최대 5 MB).

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `path` | string | 예 | 파일의 절대 경로 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "content": "파일 내용...",
    "size": 1024
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PATH` | 400 | 경로 유효성 검증 실패 |
| `IS_DIRECTORY` | 400 | 경로가 디렉토리 |
| `FILE_TOO_LARGE` | 400 | 파일 크기가 5 MB 초과 |
| `NOT_FOUND` | 404 | 파일 없음 |
| `PERMISSION_DENIED` | 403 | 권한 부족 |

---

### POST /api/v1/files/write
파일 작성/덮어쓰기. 상위 디렉토리가 없으면 자동 생성.

- **인증 필요**: 예

**Request Body:**
```json
{
  "path": "/etc/example.conf",
  "content": "파일 내용..."
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "file written",
    "path": "/etc/example.conf"
  }
}
```

---

### POST /api/v1/files/mkdir
디렉토리 생성 (부모 디렉토리 포함 재귀 생성).

- **인증 필요**: 예

**Request Body:**
```json
{
  "path": "/opt/myapp/data"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "directory created",
    "path": "/opt/myapp/data"
  }
}
```

---

### DELETE /api/v1/files
파일 또는 디렉토리 삭제 (디렉토리는 재귀 삭제).

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `path` | string | 예 | 삭제 대상 절대 경로 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "path deleted",
    "path": "/tmp/old-file"
  }
}
```

**보호 경로 (삭제 불가):**
`/`, `/etc`, `/usr`, `/bin`, `/sbin`, `/var`, `/boot`, `/proc`, `/sys`, `/dev`

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `CRITICAL_PATH` | 403 | 보호된 시스템 경로 |
| `NOT_FOUND` | 404 | 경로 없음 |

---

### POST /api/v1/files/rename
파일 또는 디렉토리 이름 변경/이동.

- **인증 필요**: 예

**Request Body:**
```json
{
  "old_path": "/tmp/old-name",
  "new_path": "/tmp/new-name"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "path renamed",
    "old_path": "/tmp/old-name",
    "new_path": "/tmp/new-name"
  }
}
```

---

### GET /api/v1/files/download
파일 다운로드 (바이너리 첨부).

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `path` | string | 예 | 파일의 절대 경로 |

**Response:** 파일 바이너리 데이터 (`Content-Disposition: attachment`). 표준 JSON 응답이 아닌 파일 다운로드.

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `IS_DIRECTORY` | 400 | 디렉토리는 다운로드 불가 |
| `NOT_FOUND` | 404 | 파일 없음 |

---

### POST /api/v1/files/upload
파일 업로드 (최대 100 MB). multipart/form-data 사용.

- **인증 필요**: 예
- **Content-Type**: `multipart/form-data`

**Form Fields:**
| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `file` | File | 예 | 업로드할 파일 |
| `path` | string | 예 | 저장할 디렉토리의 절대 경로 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "file uploaded",
    "path": "/opt/uploads/example.txt",
    "filename": "example.txt",
    "size": 2048
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FILE` | 400 | 'file' 필드에 파일 없음 |
| `INVALID_FILENAME` | 400 | 유효하지 않은 파일명 |

---

## Cron 작업 API (`/api/v1/cron`)

### GET /api/v1/cron
root 사용자의 crontab 전체 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "id": 0,
      "schedule": "0 * * * *",
      "command": "/usr/bin/backup.sh",
      "enabled": true,
      "raw": "0 * * * * /usr/bin/backup.sh",
      "type": "job"
    },
    {
      "id": 1,
      "schedule": "",
      "command": "SHELL=/bin/bash",
      "enabled": true,
      "raw": "SHELL=/bin/bash",
      "type": "env"
    }
  ]
}
```

**CronJob 객체 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `id` | number | 줄 번호 기반 인덱스 (0부터 시작) |
| `schedule` | string | 크론 스케줄 표현식 (job 타입만) |
| `command` | string | 실행 명령어 또는 줄 내용 |
| `enabled` | boolean | 활성화 여부 (주석 처리 = 비활성) |
| `raw` | string | 원본 줄 텍스트 |
| `type` | string | `"job"` \| `"env"` \| `"comment"` |

---

### POST /api/v1/cron
새 cron 작업 추가.

- **인증 필요**: 예

**Request Body:**
```json
{
  "schedule": "0 2 * * *",
  "command": "/usr/bin/backup.sh"
}
```

**Response (200):** 생성된 CronJob 객체

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | schedule 또는 command 누락 |
| `INVALID_SCHEDULE` | 400 | 유효하지 않은 크론 스케줄 형식 |

지원하는 스케줄 형식:
- 5필드 표준: `분 시 일 월 요일` (예: `0 2 * * *`)
- 예약 키워드: `@reboot`, `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`, `@midnight`, `@hourly`

---

### PUT /api/v1/cron/:id
기존 cron 작업 수정.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 줄 번호 기반 인덱스 |

**Request Body:**
```json
{
  "schedule": "0 3 * * *",
  "command": "/usr/bin/backup.sh --full",
  "enabled": true
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `schedule` | string | 예 | 크론 스케줄 |
| `command` | string | 예 | 실행 명령어 |
| `enabled` | boolean | 아니오 | `false`이면 주석 처리하여 비활성화 (기본값: `true`) |

**Response (200):** 수정된 CronJob 객체

---

### DELETE /api/v1/cron/:id
cron 작업 삭제 (crontab에서 해당 줄 제거).

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 줄 번호 기반 인덱스 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "job deleted"
  }
}
```

---

## 로그 뷰어 API (`/api/v1/logs`)

### GET /api/v1/logs/sources
사용 가능한 로그 소스 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "id": "syslog",
      "name": "System Log",
      "path": "/var/log/syslog",
      "size": 1048576,
      "exists": true
    }
  ]
}
```

**지원하는 로그 소스:**
| ID | 이름 | 경로 |
|----|------|------|
| `syslog` | System Log | `/var/log/syslog` |
| `auth` | Auth Log | `/var/log/auth.log` |
| `kern` | Kernel Log | `/var/log/kern.log` |
| `nginx-access` | Nginx Access | `/var/log/nginx/access.log` |
| `nginx-error` | Nginx Error | `/var/log/nginx/error.log` |
| `sfpanel` | SFPanel | `/var/log/sfpanel.log` |
| `dpkg` | Package Manager | `/var/log/dpkg.log` |
| `ufw` | Firewall (UFW) | `/var/log/ufw.log` |

---

### GET /api/v1/logs/read
로그 파일의 마지막 N줄 읽기.

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 기본값 | 설명 |
|----------|------|------|--------|------|
| `source` | string | 예 | - | 로그 소스 ID (위 테이블 참조) |
| `lines` | number | 아니오 | `100` | 읽을 줄 수 (최대 5000) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "source": "syslog",
    "lines": [
      "Feb 25 10:00:00 server kernel: ...",
      "Feb 25 10:00:01 server sshd: ..."
    ],
    "total_lines": 2
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_SOURCE` | 400 | source 파라미터 누락 |
| `INVALID_SOURCE` | 400 | 알 수 없는 로그 소스 |
| `INVALID_LINES` | 400 | lines가 양의 정수가 아님 |
| `LOG_NOT_FOUND` | 404 | 로그 파일이 디스크에 존재하지 않음 |

---

## 패키지 관리 API (`/api/v1/packages`)

### GET /api/v1/packages/updates
업데이트 가능한 패키지 목록 조회 (`apt list --upgradable`).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "updates": [
      {
        "name": "nginx",
        "current_version": "1.24.0-1",
        "new_version": "1.24.0-2",
        "arch": "amd64"
      }
    ],
    "total": 1,
    "last_checked": "2026-02-26T10:00:00Z"
  }
}
```

---

### POST /api/v1/packages/upgrade
패키지 업그레이드 실행. 특정 패키지 지정 가능.

- **인증 필요**: 예

**Request Body:**
```json
{
  "packages": ["nginx", "curl"]
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `packages` | string[] | 아니오 | 업그레이드할 패키지 목록. 비어있으면 전체 업그레이드 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Packages upgraded successfully",
    "update_output": "...",
    "upgrade_output": "..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PACKAGE_NAME` | 400 | 패키지 이름에 허용되지 않는 문자 포함 |
| `APT_UPDATE_ERROR` | 500 | apt-get update 실패 |
| `APT_UPGRADE_ERROR` | 500 | apt-get upgrade 실패 |

---

### POST /api/v1/packages/install
단일 패키지 설치.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "nginx"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Package nginx installed successfully",
    "output": "..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | 패키지 이름 누락 |
| `INVALID_PACKAGE_NAME` | 400 | 허용 문자: `a-zA-Z0-9._+-` |
| `APT_INSTALL_ERROR` | 500 | 설치 실패 |

---

### POST /api/v1/packages/remove
단일 패키지 제거.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "nginx"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Package nginx removed successfully",
    "output": "..."
  }
}
```

---

### GET /api/v1/packages/search
패키지 검색 (`apt-cache search`). 최대 50개 결과 반환.

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `q` | string | 예 | 검색어 (허용 문자: `a-zA-Z0-9._+-`) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "packages": [
      {
        "name": "nginx-core",
        "description": "nginx web/proxy server (standard version)"
      }
    ],
    "total": 1,
    "query": "nginx"
  }
}
```

---

### GET /api/v1/packages/docker-status
Docker 및 Docker Compose 설치/실행 상태 확인.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "installed": true,
    "version": "Docker version 27.5.1, build abcdef",
    "running": true,
    "compose_available": true
  }
}
```

---

### POST /api/v1/packages/install-docker
Docker Engine 설치 (get.docker.com 스크립트 사용). SSE(Server-Sent Events)로 설치 진행 상황 실시간 스트리밍.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Response:** SSE 스트림
```
data: >>> Downloading Docker install script from https://get.docker.com ...

data: >>> Running install script (this may take a few minutes) ...

data: [설치 로그 줄...]

data: >>> Docker installation completed successfully!

data: [DONE]
```

마지막 줄 `[DONE]`이 설치 완료를 나타냅니다. 에러 발생 시 `ERROR:` 접두사가 붙은 메시지 후 `[DONE]`.

---

## Docker API (`/api/v1/docker`)

> Docker 소켓에 연결할 수 없는 경우 이 그룹의 모든 라우트가 등록되지 않습니다.

### 컨테이너

#### GET /api/v1/docker/containers
전체 컨테이너 목록 조회 (실행 중 + 중지된 것 모두).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "Id": "abc123...",
      "Names": ["/my-container"],
      "Image": "nginx:latest",
      "State": "running",
      "Status": "Up 3 hours",
      "Ports": [
        {
          "PrivatePort": 80,
          "PublicPort": 8080,
          "Type": "tcp"
        }
      ],
      "Created": 1740000000
    }
  ]
}
```

> 참고: Docker SDK의 원본 구조체를 반환하므로 필드명이 PascalCase입니다.

---

#### GET /api/v1/docker/containers/:id/inspect
컨테이너 상세 정보 조회.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 컨테이너 ID 또는 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "id": "abc123def456...",
    "name": "my-container",
    "image": "nginx:latest",
    "state": "running",
    "started_at": "2026-02-25T10:00:00Z",
    "finished_at": "0001-01-01T00:00:00Z",
    "restart_count": 0,
    "platform": "linux",
    "cmd": "nginx -g daemon off;",
    "entrypoint": "/docker-entrypoint.sh",
    "working_dir": "/",
    "hostname": "abc123",
    "ports": [
      {
        "container_port": "80",
        "protocol": "tcp",
        "host_ip": "0.0.0.0",
        "host_port": "8080"
      }
    ],
    "env": [
      "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
      "NGINX_VERSION=1.24.0"
    ],
    "mounts": [
      {
        "type": "bind",
        "source": "/host/path",
        "destination": "/container/path",
        "mode": "rw",
        "rw": "true"
      }
    ],
    "networks": [
      {
        "name": "bridge",
        "ip_address": "172.17.0.2",
        "gateway": "172.17.0.1",
        "mac_address": "02:42:ac:11:00:02"
      }
    ]
  }
}
```

---

#### GET /api/v1/docker/containers/:id/stats
컨테이너 CPU/메모리 사용량 조회 (단일 스냅샷).

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 컨테이너 ID 또는 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "cpu_percent": 2.5,
    "mem_usage": 52428800,
    "mem_limit": 8388608000,
    "mem_percent": 0.625
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `cpu_percent` | number | CPU 사용률 (%) |
| `mem_usage` | number | 메모리 사용량 (bytes) |
| `mem_limit` | number | 메모리 제한 (bytes) |
| `mem_percent` | number | 메모리 사용률 (%) |

---

#### POST /api/v1/docker/containers/:id/start
컨테이너 시작.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "container started"
  }
}
```

---

#### POST /api/v1/docker/containers/:id/stop
컨테이너 중지.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "container stopped"
  }
}
```

---

#### POST /api/v1/docker/containers/:id/restart
컨테이너 재시작.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "container restarted"
  }
}
```

---

#### DELETE /api/v1/docker/containers/:id
컨테이너 삭제 (강제).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "container removed"
  }
}
```

---

### 이미지

#### GET /api/v1/docker/images
로컬 Docker 이미지 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "Id": "sha256:abc123...",
      "RepoTags": ["nginx:latest"],
      "Size": 142000000,
      "Created": 1740000000
    }
  ]
}
```

---

#### POST /api/v1/docker/images/pull
이미지 풀(pull). 동기식으로 완료될 때까지 대기.

- **인증 필요**: 예

**Request Body:**
```json
{
  "image": "nginx:latest"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "image pulled",
    "image": "nginx:latest"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | image 필드 누락 |

---

#### DELETE /api/v1/docker/images/:id
이미지 삭제.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 이미지 ID 또는 태그 (URL 인코딩 필요) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "image removed"
  }
}
```

---

### 볼륨

#### GET /api/v1/docker/volumes
Docker 볼륨 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "Name": "my-volume",
      "Driver": "local",
      "Mountpoint": "/var/lib/docker/volumes/my-volume/_data",
      "CreatedAt": "2026-02-25T10:00:00Z"
    }
  ]
}
```

---

#### POST /api/v1/docker/volumes
볼륨 생성.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "my-volume"
}
```

**Response (200):** 생성된 볼륨 객체 (Docker SDK 형식)

---

#### DELETE /api/v1/docker/volumes/:name
볼륨 삭제.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `name` | 볼륨 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "volume removed"
  }
}
```

---

### 네트워크

#### GET /api/v1/docker/networks
Docker 네트워크 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "Id": "abc123...",
      "Name": "bridge",
      "Driver": "bridge",
      "Scope": "local"
    }
  ]
}
```

---

#### POST /api/v1/docker/networks
네트워크 생성.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "my-network",
  "driver": "bridge"
}
```

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `name` | string | 예 | - | 네트워크 이름 |
| `driver` | string | 아니오 | `"bridge"` | 네트워크 드라이버 |

**Response (200):** 생성된 네트워크 객체 (Docker SDK 형식)

---

#### DELETE /api/v1/docker/networks/:id
네트워크 삭제.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 네트워크 ID 또는 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "network removed"
  }
}
```

---

### Docker Compose

#### GET /api/v1/docker/compose
전체 Compose 프로젝트 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "name": "my-project",
      "yaml_path": "/var/lib/sfpanel/compose/my-project/docker-compose.yml",
      "status": "running",
      "created_at": "2026-02-25T10:00:00Z"
    }
  ]
}
```

---

#### POST /api/v1/docker/compose
새 Compose 프로젝트 생성.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "my-project",
  "yaml": "version: '3'\nservices:\n  web:\n    image: nginx:latest\n    ports:\n      - '8080:80'"
}
```

**Response (200):** 생성된 ComposeProject 객체

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | name 또는 yaml 누락 |

---

#### GET /api/v1/docker/compose/:project
특정 Compose 프로젝트 상세 정보 및 YAML 내용 조회.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "project": {
      "id": 1,
      "name": "my-project",
      "yaml_path": "/var/lib/sfpanel/compose/my-project/docker-compose.yml",
      "status": "running",
      "created_at": "2026-02-25T10:00:00Z"
    },
    "yaml": "version: '3'\nservices:\n  web:\n    image: nginx:latest"
  }
}
```

---

#### PUT /api/v1/docker/compose/:project
Compose 프로젝트 YAML 업데이트.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Request Body:**
```json
{
  "yaml": "version: '3'\nservices:\n  web:\n    image: nginx:alpine"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "project updated"
  }
}
```

---

#### DELETE /api/v1/docker/compose/:project
Compose 프로젝트 삭제.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "project deleted"
  }
}
```

---

#### POST /api/v1/docker/compose/:project/up
Compose 프로젝트 시작 (`docker compose up -d`).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "output": "Creating network... Creating container..."
  }
}
```

---

#### POST /api/v1/docker/compose/:project/down
Compose 프로젝트 중지 (`docker compose down`).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "output": "Stopping container... Removing container..."
  }
}
```

---

## 방화벽 API — UFW (`/api/v1/firewall`)

### GET /api/v1/firewall/status
UFW 방화벽 현재 상태 조회 (활성 여부, 기본 정책).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "active": true,
    "default_income": "deny",
    "default_out": "allow"
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `active` | boolean | UFW 활성화 상태 |
| `default_income` | string | 기본 인바운드 정책 (예: "deny") |
| `default_out` | string | 기본 아웃바운드 정책 (예: "allow") |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `UFW_ERROR` | 500 | UFW 상태 조회 실패 |

---

### POST /api/v1/firewall/enable
UFW 방화벽 활성화 (`ufw --force enable`).

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "UFW enabled successfully",
    "output": "Firewall is active and enabled on system startup"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `UFW_ENABLE_ERROR` | 500 | UFW 활성화 실패 |

---

### POST /api/v1/firewall/disable
UFW 방화벽 비활성화 (`ufw disable`).

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "UFW disabled successfully",
    "output": "Firewall stopped and disabled on system startup"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `UFW_DISABLE_ERROR` | 500 | UFW 비활성화 실패 |

---

### GET /api/v1/firewall/rules
현재 UFW 규칙 목록 조회 (번호 포함).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "rules": [
      {
        "number": 1,
        "to": "22/tcp",
        "action": "ALLOW IN",
        "from": "Anywhere",
        "comment": "SSH",
        "v6": false
      }
    ],
    "total": 1
  }
}
```

**UFWRule 객체 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `number` | number | 규칙 번호 (삭제 시 사용) |
| `to` | string | 대상 포트/주소 (예: "22/tcp", "80,443/tcp") |
| `action` | string | 동작 (예: "ALLOW IN", "DENY IN") |
| `from` | string | 소스 주소 (예: "Anywhere", "192.168.1.0/24") |
| `comment` | string | 규칙 코멘트 (선택) |
| `v6` | boolean | IPv6 규칙 여부 |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `UFW_ERROR` | 500 | 규칙 목록 조회 실패 |

---

### POST /api/v1/firewall/rules
새 UFW 방화벽 규칙 추가.

- **인증 필요**: 예

**Request Body:**
```json
{
  "action": "allow",
  "port": "22",
  "protocol": "tcp",
  "from": "any",
  "to": "",
  "comment": "SSH"
}
```

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `action` | string | 아니오 | `"allow"` | 동작: `allow`, `deny`, `reject`, `limit` |
| `port` | string | 예 | - | 포트 번호, 범위(예: "8000:8080"), 서비스명 |
| `protocol` | string | 아니오 | `"any"` | 프로토콜: `tcp`, `udp`, `any` |
| `from` | string | 아니오 | `""` | 소스 IP/CIDR (빈 값 또는 "any" = 전체) |
| `to` | string | 아니오 | `""` | 대상 IP/CIDR (빈 값 또는 "any" = 전체) |
| `comment` | string | 아니오 | `""` | 규칙 설명 (영숫자, 공백, 기본 구두점만 허용) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Rule added successfully",
    "output": "Rule added"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 잘못된 요청 본문 |
| `INVALID_ACTION` | 400 | 허용되지 않는 action 값 |
| `MISSING_FIELDS` | 400 | port 누락 |
| `INVALID_PORT` | 400 | 유효하지 않은 포트 형식 |
| `INVALID_PROTOCOL` | 400 | 허용되지 않는 protocol 값 |
| `INVALID_FROM_ADDRESS` | 400 | 유효하지 않은 소스 IP/CIDR |
| `INVALID_TO_ADDRESS` | 400 | 유효하지 않은 대상 IP/CIDR |
| `INVALID_COMMENT` | 400 | 코멘트에 허용되지 않는 문자 |
| `UFW_ADD_RULE_ERROR` | 500 | 규칙 추가 실패 |

---

### DELETE /api/v1/firewall/rules/:number
UFW 규칙을 번호로 삭제 (`ufw --force delete <number>`).

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `number` | 삭제할 규칙 번호 (양의 정수) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Rule 1 deleted successfully",
    "output": "Rule deleted"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_RULE_NUMBER` | 400 | 유효하지 않은 규칙 번호 (양의 정수가 아님) |
| `UFW_DELETE_ERROR` | 500 | 규칙 삭제 실패 |

---

### GET /api/v1/firewall/ports
시스템에서 리스닝 중인 TCP/UDP 포트 목록 조회 (`ss -tlnp` + `ss -ulnp`).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "ports": [
      {
        "protocol": "tcp",
        "address": "0.0.0.0",
        "port": 22,
        "pid": 1234,
        "process": "sshd"
      },
      {
        "protocol": "tcp",
        "address": "::",
        "port": 8443,
        "pid": 5678,
        "process": "sfpanel"
      }
    ],
    "total": 2
  }
}
```

**ListeningPort 객체 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `protocol` | string | 프로토콜 ("tcp" 또는 "udp") |
| `address` | string | 바인딩 주소 (예: "0.0.0.0", "::", "127.0.0.1") |
| `port` | number | 리스닝 포트 번호 |
| `pid` | number | 프로세스 ID (0이면 권한 부족으로 감지 불가) |
| `process` | string | 프로세스 이름 (빈 문자열이면 감지 불가) |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `SS_ERROR` | 500 | ss 명령어 실행 실패 |

---

## Fail2ban API (`/api/v1/fail2ban`)

### GET /api/v1/fail2ban/status
Fail2ban 설치 및 실행 상태 확인.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "installed": true,
    "running": true,
    "version": "0.11.2"
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `installed` | boolean | fail2ban-client 바이너리 존재 여부 |
| `running` | boolean | fail2ban 서비스 실행 중 여부 (ping/pong 확인) |
| `version` | string | fail2ban 버전 (미설치 시 빈 문자열) |

---

### POST /api/v1/fail2ban/install
Fail2ban 패키지 설치 및 서비스 시작 (`apt-get install -y fail2ban` + `systemctl enable/start`).

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Fail2ban installed and started successfully",
    "install_output": "...",
    "start_output": "..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `APT_UPDATE_ERROR` | 500 | apt-get update 실패 |
| `FAIL2BAN_INSTALL_ERROR` | 500 | fail2ban 설치 실패 |
| `FAIL2BAN_START_ERROR` | 500 | 설치 성공했으나 서비스 시작 실패 |

---

### GET /api/v1/fail2ban/jails
전체 Fail2ban jail 목록 및 상태 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "jails": [
      {
        "name": "sshd",
        "enabled": true,
        "filter": "/var/log/auth.log",
        "banned_count": 1,
        "total_banned": 5,
        "max_retry": 5,
        "ban_time": "600",
        "find_time": "600",
        "banned_ips": ["192.168.1.100"]
      }
    ],
    "total": 1
  }
}
```

**Fail2banJail 객체 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `name` | string | jail 이름 (예: "sshd", "apache-auth") |
| `enabled` | boolean | 활성화 상태 (jail 목록에 존재하면 true) |
| `filter` | string | 모니터링 대상 로그 파일 경로 |
| `banned_count` | number | 현재 차단된 IP 수 |
| `total_banned` | number | 총 누적 차단 횟수 |
| `max_retry` | number | 최대 재시도 횟수 (이 횟수 초과 시 차단) |
| `ban_time` | string | 차단 시간 (초) |
| `find_time` | string | 감시 윈도우 시간 (초) |
| `banned_ips` | string[] | 현재 차단된 IP 주소 목록 |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `FAIL2BAN_ERROR` | 500 | fail2ban-client 실행 실패 |

---

### GET /api/v1/fail2ban/jails/:name
특정 Fail2ban jail의 상세 정보 조회.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `name` | jail 이름 (허용 문자: `a-zA-Z0-9_-`) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "name": "sshd",
    "enabled": true,
    "filter": "/var/log/auth.log",
    "banned_count": 1,
    "total_banned": 5,
    "max_retry": 5,
    "ban_time": "600",
    "find_time": "600",
    "banned_ips": ["192.168.1.100"]
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_JAIL_NAME` | 400 | jail 이름 누락 |
| `INVALID_JAIL_NAME` | 400 | jail 이름에 허용되지 않는 문자 |
| `FAIL2BAN_JAIL_ERROR` | 500 | jail 상태 조회 실패 |

---

### POST /api/v1/fail2ban/jails/:name/enable
Fail2ban jail 시작 (활성화).

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `name` | jail 이름 (허용 문자: `a-zA-Z0-9_-`) |

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Jail sshd enabled successfully",
    "output": "..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_JAIL_NAME` | 400 | jail 이름 누락 |
| `INVALID_JAIL_NAME` | 400 | jail 이름에 허용되지 않는 문자 |
| `FAIL2BAN_ENABLE_ERROR` | 500 | jail 활성화 실패 |

---

### POST /api/v1/fail2ban/jails/:name/disable
Fail2ban jail 중지 (비활성화).

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `name` | jail 이름 (허용 문자: `a-zA-Z0-9_-`) |

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Jail sshd disabled successfully",
    "output": "..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_JAIL_NAME` | 400 | jail 이름 누락 |
| `INVALID_JAIL_NAME` | 400 | jail 이름에 허용되지 않는 문자 |
| `FAIL2BAN_DISABLE_ERROR` | 500 | jail 비활성화 실패 |

---

### POST /api/v1/fail2ban/jails/:name/unban
특정 jail에서 차단된 IP 해제.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `name` | jail 이름 (허용 문자: `a-zA-Z0-9_-`) |

**Request Body:**
```json
{
  "ip": "192.168.1.100"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `ip` | string | 예 | 차단 해제할 IPv4 또는 IPv6 주소 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "IP 192.168.1.100 unbanned from jail sshd",
    "output": "..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_JAIL_NAME` | 400 | jail 이름 누락 |
| `INVALID_JAIL_NAME` | 400 | jail 이름에 허용되지 않는 문자 |
| `INVALID_REQUEST` | 400 | 잘못된 요청 본문 |
| `MISSING_FIELDS` | 400 | IP 주소 누락 |
| `INVALID_IP` | 400 | 유효하지 않은 IP 주소 형식 |
| `FAIL2BAN_UNBAN_ERROR` | 500 | IP 차단 해제 실패 |

---

## 앱스토어 API (`/api/v1/appstore`)

### GET /api/v1/appstore/categories
앱스토어 카테고리 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "id": "media",
      "name": { "ko": "미디어", "en": "Media" },
      "icon": "Film"
    }
  ]
}
```

---

### GET /api/v1/appstore/apps
앱 목록 조회. 카테고리별 필터링 가능.

- **인증 필요**: 예

**Query Parameters:**
| 이름 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `category` | string | X | 카테고리 ID로 필터링 |

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "id": "uptime-kuma",
      "name": "Uptime Kuma",
      "description": { "ko": "셀프 호스팅 모니터링 도구", "en": "Self-hosted monitoring tool" },
      "category": "monitoring",
      "version": "1",
      "website": "https://github.com/louislam/uptime-kuma",
      "source": "louislam/uptime-kuma",
      "ports": ["3001"],
      "env": [
        {
          "key": "PORT",
          "label": { "ko": "포트", "en": "Port" },
          "type": "number",
          "default": "3001",
          "required": true,
          "generate": ""
        }
      ],
      "installed": false
    }
  ]
}
```

---

### GET /api/v1/appstore/apps/:id
앱 상세 정보 및 Compose YAML 조회.

- **인증 필요**: 예

**Path Parameters:**
| 이름 | 설명 |
|------|------|
| `id` | 앱 ID (예: `uptime-kuma`) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "app": {
      "id": "uptime-kuma",
      "name": "Uptime Kuma",
      "description": { "ko": "...", "en": "..." },
      "category": "monitoring",
      "version": "1",
      "website": "...",
      "source": "...",
      "ports": ["3001"],
      "env": [{ "key": "PORT", "label": {"ko": "포트", "en": "Port"}, "type": "number", "default": "3001", "required": true, "generate": "" }],
      "installed": false
    },
    "compose": "version: '3'\nservices:\n  uptime-kuma:\n    image: louislam/uptime-kuma:1\n    ...",
    "installed": false
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `APP_NOT_FOUND` | 404 | 존재하지 않는 앱 ID |

---

### POST /api/v1/appstore/apps/:id/install
앱 설치 (Docker Compose 프로젝트로 배포).

- **인증 필요**: 예

**Path Parameters:**
| 이름 | 설명 |
|------|------|
| `id` | 앱 ID (예: `uptime-kuma`) |

**Request Body:**
```json
{
  "env": {
    "PORT": "3001",
    "PASSWORD": "my-secret"
  }
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "앱이 설치되었습니다",
    "id": "uptime-kuma",
    "output": "docker compose up -d 출력..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `APP_NOT_FOUND` | 404 | 존재하지 않는 앱 ID |
| `APP_ALREADY_INSTALLED` | 409 | 이미 설치된 앱 |
| `APP_INSTALL_ERROR` | 500 | 설치 실패 |

---

### GET /api/v1/appstore/installed
설치된 앱 목록 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "id": "uptime-kuma",
      "details": {
        "version": "1",
        "installed_at": "2026-03-10T12:00:00Z"
      }
    }
  ]
}
```

---

### POST /api/v1/appstore/refresh
앱스토어 캐시 갱신 (GitHub 레포에서 최신 앱 목록 다시 로드).

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "앱스토어 캐시가 갱신되었습니다",
    "apps": 8,
    "categories": 5
  }
}
```

---

## 시스템 관리 API (`/api/v1/system`)

### GET /api/v1/system/update-check
GitHub 릴리즈 API에서 최신 버전 확인.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "current_version": "0.5.5",
    "latest_version": "0.5.6",
    "update_available": true,
    "release_notes": "## 변경사항\n- ...",
    "published_at": "2026-03-15T10:00:00Z"
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `current_version` | string | 현재 설치된 버전 |
| `latest_version` | string | GitHub 최신 릴리즈 버전 |
| `update_available` | boolean | 업데이트 가능 여부 |
| `release_notes` | string | 릴리즈 노트 (Markdown) |
| `published_at` | string | 릴리즈 일시 (ISO 8601) |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `UPDATE_CHECK_FAILED` | 502 | GitHub API 요청 실패 |

---

### POST /api/v1/system/update
최신 버전 다운로드 및 바이너리 교체. SSE(Server-Sent Events)로 진행 상황 실시간 스트리밍. 체크섬(SHA-256) 검증 포함.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Response:** SSE 스트림 (각 이벤트는 JSON)
```
data: {"step":"downloading","message":"Downloading v0.5.6 (amd64)..."}

data: {"step":"verifying","message":"Downloading checksums..."}

data: {"step":"extracting","message":"Extracting binary..."}

data: {"step":"replacing","message":"Replacing binary..."}

data: {"step":"restarting","message":"Restarting service..."}

data: {"step":"complete","message":"Updated to v0.5.6. Restarting..."}
```

에러 발생 시 `step`이 `"error"`인 이벤트가 전송됩니다.

---

### POST /api/v1/system/backup
시스템 설정 백업 파일 다운로드 (tar.gz 아카이브). DB, 설정 파일, Docker Compose 프로젝트 파일 포함.

- **인증 필요**: 예
- **응답 형식**: `application/gzip` (표준 JSON 응답이 아님)

**Response:** 바이너리 tar.gz 파일 (`Content-Disposition: attachment; filename=sfpanel-backup-20260317-120000.tar.gz`)

**아카이브 내용:**
| 파일 | 설명 |
|------|------|
| `sfpanel.db` | SQLite 데이터베이스 |
| `config.yaml` | 서버 설정 파일 |
| `compose/<project>/<file>` | Docker Compose 프로젝트 파일 (docker-compose.yml, .env 등) |

---

### POST /api/v1/system/restore
백업 파일로 시스템 설정 복원. multipart/form-data로 tar.gz 파일 업로드. 복원 후 서비스 자동 재시작.

- **인증 필요**: 예
- **Content-Type**: `multipart/form-data`

**Form Fields:**
| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `backup` | File | 예 | 백업 tar.gz 파일 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Backup restored. Service restarting..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `RESTORE_FAILED` | 400 | 백업 파일 미제공, 유효하지 않은 gzip/tar, sfpanel.db 누락 |
| `RESTORE_FAILED` | 500 | DB 또는 설정 파일 복원 실패 |

---

## 시스템 튜닝 API (`/api/v1/system/tuning`)

커널 파라미터(sysctl) 튜닝 관리. 시스템 사양(CPU, RAM)에 따라 동적으로 최적 값을 추천하며, 적용 후 60초 이내 확인하지 않으면 자동 롤백됩니다.

### GET /api/v1/system/tuning
현재 sysctl 값과 추천 값 비교. 카테고리별(network, memory, filesystem, security) 파라미터 목록 반환.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "categories": [
      {
        "name": "network",
        "benefit": "benefit_network",
        "caution": "caution_network",
        "params": [
          {
            "key": "net.core.default_qdisc",
            "current": "pfifo_fast",
            "recommended": "fq",
            "description": "Fair Queue scheduler (required for BBR)",
            "applied": false
          }
        ],
        "applied": 0,
        "total": 16
      }
    ],
    "total_params": 37,
    "applied": 0,
    "pending_rollback": false,
    "rollback_remaining": 0,
    "system_info": {
      "cpu_cores": 4,
      "total_ram": 8388608000,
      "kernel": "6.17.0-14-generic"
    }
  }
}
```

**카테고리:**
| 이름 | 설명 | 파라미터 수 |
|------|------|------------|
| `network` | 네트워크 버퍼, TCP 최적화, BBR 등 | 16 |
| `memory` | swappiness, dirty ratio, cache pressure 등 | 5 |
| `filesystem` | file-max, inotify, aio 등 | 4 |
| `security` | SYN cookies, rp_filter, ICMP 보호 등 | 12 |

**TuningParam 객체 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `key` | string | sysctl 키 (예: "net.core.default_qdisc") |
| `current` | string | 현재 시스템 값 |
| `recommended` | string | SFPanel 추천 값 (시스템 사양 기반) |
| `description` | string | 파라미터 설명 (영문) |
| `applied` | boolean | SFPanel 설정 파일에 이 키가 포함되어 있는지 여부 |

---

### POST /api/v1/system/tuning/apply
추천 sysctl 값을 적용. 적용 후 60초 이내에 `/system/tuning/confirm`을 호출하지 않으면 자동 롤백.

- **인증 필요**: 예

**Request Body:**
```json
{
  "categories": ["network", "memory"]
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `categories` | string[] | 아니오 | 적용할 카테고리 목록. 비어있으면 전체 적용 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Tuning applied — confirm within 60 seconds or changes will be rolled back",
    "output": "net.core.default_qdisc = fq\n...",
    "timeout": 60
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `TUNING_ERROR` | 500 | sysctl 설정 적용 실패 |

---

### POST /api/v1/system/tuning/confirm
적용된 튜닝 변경사항을 확인하고 영구 저장. 롤백 타이머를 취소합니다.

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Tuning confirmed and saved permanently"
  }
}
```

대기 중인 변경사항이 없을 경우:
```json
{
  "success": true,
  "data": {
    "message": "No pending changes to confirm"
  }
}
```

---

### POST /api/v1/system/tuning/reset
SFPanel 튜닝 설정 파일(`/etc/sysctl.d/99-sfpanel-tuning.conf`)을 삭제하고 시스템 기본값으로 복원.

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Tuning reset to system defaults"
  }
}
```

설정 파일이 없을 경우:
```json
{
  "success": true,
  "data": {
    "message": "No tuning configuration to reset"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `TUNING_ERROR` | 500 | 설정 파일 삭제 실패 |

---

## 감사 로그 API (`/api/v1/audit`)

API 요청 기록을 조회하고 관리합니다. 감사 로그는 인증된 모든 API 요청에 대해 자동 기록됩니다.

### GET /api/v1/audit/logs
감사 로그 목록 조회 (최신순 정렬, 페이지네이션 지원).

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 기본값 | 설명 |
|----------|------|------|--------|------|
| `page` | number | 아니오 | `1` | 페이지 번호 (1부터 시작) |
| `limit` | number | 아니오 | `50` | 페이지당 항목 수 (최대 100) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "logs": [
      {
        "id": 1234,
        "username": "admin",
        "method": "POST",
        "path": "/api/v1/docker/containers/abc123/restart",
        "status": 200,
        "ip": "192.168.1.10",
        "node_id": "",
        "created_at": "2026-03-17T10:30:00Z"
      }
    ],
    "total": 5000
  }
}
```

**AuditLogEntry 객체 필드:**
| 필드 | 타입 | 설명 |
|------|------|------|
| `id` | number | 로그 항목 ID |
| `username` | string | 요청한 사용자명 |
| `method` | string | HTTP 메서드 (GET, POST, PUT, DELETE, PATCH) |
| `path` | string | 요청 경로 |
| `status` | number | HTTP 응답 상태 코드 |
| `ip` | string | 클라이언트 IP 주소 |
| `node_id` | string | 클러스터 노드 ID (클러스터 미사용 시 빈 문자열) |
| `created_at` | string | 기록 일시 (ISO 8601) |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `DB_ERROR` | 500 | 데이터베이스 조회 실패 |

---

### DELETE /api/v1/audit/logs
감사 로그 전체 삭제.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Audit logs cleared"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `DB_ERROR` | 500 | 데이터베이스 삭제 실패 |

---

## 헬스체크 API

### GET /api/v1/health
서버 상태 확인.

- **인증 필요**: 아니오 (공개 엔드포인트)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "status": "ok"
  }
}
```

---

## 패키지 관리 — Node.js API (`/api/v1/packages`)

### GET /api/v1/packages/node-status
Node.js 및 NVM(Node Version Manager) 설치 상태 확인.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "installed": true,
    "version": "v22.12.0",
    "nvm_installed": true,
    "npm_version": "10.9.0"
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `installed` | boolean | Node.js 설치 여부 |
| `version` | string | Node.js 버전 (미설치 시 빈 문자열) |
| `nvm_installed` | boolean | NVM 설치 여부 |
| `npm_version` | string | npm 버전 (미설치 시 빈 문자열) |

---

### POST /api/v1/packages/install-node
NVM을 통해 Node.js LTS 설치. NVM이 없으면 먼저 설치. SSE(Server-Sent Events)로 진행 상황 스트리밍.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Response:** SSE 스트림
```
data: >>> Installing NVM (Node Version Manager) ...

data: >>> Installing Node.js LTS via NVM ...

data: >>> Creating symlinks in /usr/local/bin ...

data: >>> Node.js installation completed successfully!

data: [DONE]
```

---

### GET /api/v1/packages/node-versions
NVM으로 설치된 Node.js 버전 목록 및 활성 버전 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "versions": [
      {
        "version": "v20.18.0",
        "active": false,
        "lts": true
      },
      {
        "version": "v22.12.0",
        "active": true,
        "lts": true
      }
    ],
    "current": "v22.12.0",
    "remote_lts": ["v18.20.5", "v20.18.0", "v22.12.0"]
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `versions` | array | 설치된 버전 목록 |
| `current` | string | 현재 활성 버전 |
| `remote_lts` | string[] | 원격 LTS 최신 버전 목록 (최대 5개) |

---

### POST /api/v1/packages/node-switch
활성 Node.js 버전 전환. `/usr/local/bin` 심볼릭 링크도 함께 업데이트.

- **인증 필요**: 예

**Request Body:**
```json
{
  "version": "v20.18.0"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `version` | string | 예 | 전환할 버전 (예: "v20.18.0", "20") |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "switched": "v20.18.0",
    "output": "Now using node v20.18.0 (npm v10.8.2)"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_BODY` | 400 | version 누락 또는 형식 오류 |
| `COMMAND_FAILED` | 500 | NVM 미설치 또는 버전 전환 실패 |

---

### POST /api/v1/packages/node-install-version
특정 Node.js 버전 설치. SSE(Server-Sent Events)로 진행 상황 스트리밍.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Request Body:**
```json
{
  "version": "v18.20.5"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `version` | string | 예 | 설치할 버전 (예: "v18.20.5", "18") |

**Response:** SSE 스트림
```
data: >>> Installing Node.js v18.20.5 ...

data: Downloading and installing node v18.20.5...

data: >>> Node.js v18.20.5 installed successfully!

data: [DONE]
```

---

### POST /api/v1/packages/node-uninstall-version
특정 Node.js 버전 삭제.

- **인증 필요**: 예

**Request Body:**
```json
{
  "version": "v18.20.5"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `version` | string | 예 | 삭제할 버전 (예: "v18.20.5") |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "removed": "v18.20.5",
    "output": "Uninstalled node v18.20.5"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_BODY` | 400 | version 누락 또는 형식 오류 |
| `COMMAND_FAILED` | 500 | NVM 미설치 또는 삭제 실패 |

---

## 패키지 관리 — AI CLI API (`/api/v1/packages`)

### GET /api/v1/packages/claude-status
Claude Code CLI 설치 상태 확인.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "installed": true,
    "version": "1.0.0"
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `installed` | boolean | Claude CLI 설치 여부 |
| `version` | string | Claude CLI 버전 (미설치 시 빈 문자열) |

---

### POST /api/v1/packages/install-claude
Claude Code CLI 설치 (공식 설치 스크립트 사용). SSE(Server-Sent Events)로 진행 상황 스트리밍.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Response:** SSE 스트림
```
data: >>> Installing Claude Code CLI ...

data: [설치 로그...]

data: >>> Claude Code CLI installed successfully!

data: [DONE]
```

---

### GET /api/v1/packages/codex-status
OpenAI Codex CLI 설치 상태 확인.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "installed": true,
    "version": "0.1.0"
  }
}
```

---

### POST /api/v1/packages/install-codex
OpenAI Codex CLI 설치 (`npm install -g @openai/codex`). Node.js가 먼저 설치되어 있어야 합니다. SSE(Server-Sent Events)로 진행 상황 스트리밍.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Response:** SSE 스트림
```
data: >>> Installing OpenAI Codex CLI via npm ...

data: [npm 로그...]

data: >>> OpenAI Codex CLI installed successfully!

data: [DONE]
```

npm 미설치 시:
```
data: ERROR: npm is not installed. Please install Node.js first.

data: [DONE]
```

---

### GET /api/v1/packages/gemini-status
Google Gemini CLI 설치 상태 확인.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "installed": false,
    "version": ""
  }
}
```

---

### POST /api/v1/packages/install-gemini
Google Gemini CLI 설치 (`npm install -g @google/gemini-cli`). Node.js가 먼저 설치되어 있어야 합니다. SSE(Server-Sent Events)로 진행 상황 스트리밍.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Response:** SSE 스트림
```
data: >>> Installing Google Gemini CLI via npm ...

data: [npm 로그...]

data: >>> Google Gemini CLI installed successfully!

data: [DONE]
```

---

## Docker 이미지 — 업데이트 확인 API

### GET /api/v1/docker/images/updates
실행 중인 컨테이너에서 사용하는 이미지의 업데이트 가능 여부 확인. Docker Hub의 최신 다이제스트와 로컬 다이제스트를 비교합니다.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Response (200):**
```json
{
  "success": true,
  "data": [
    {
      "image": "nginx:latest",
      "current_digest": "sha256:abc123...",
      "latest_digest": "sha256:def456...",
      "update_available": true
    }
  ]
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `image` | string | 이미지 이름:태그 |
| `current_digest` | string | 현재 로컬 이미지 다이제스트 |
| `latest_digest` | string | 레지스트리 최신 다이제스트 |
| `update_available` | boolean | 업데이트 가능 여부 |

---

## Docker 네트워크 — 상세 조회 API

### GET /api/v1/docker/networks/:id/inspect
Docker 네트워크 상세 정보 조회. 연결된 컨테이너 목록, 서브넷, 게이트웨이 정보 포함.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 네트워크 ID 또는 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "id": "abc123def456...",
    "name": "my-network",
    "driver": "bridge",
    "scope": "local",
    "internal": false,
    "subnet": "172.18.0.0/16",
    "gateway": "172.18.0.1",
    "containers": [
      {
        "id": "abc123def456",
        "name": "my-container",
        "ipv4_address": "172.18.0.2/16",
        "ipv6_address": "",
        "mac_address": "02:42:ac:12:00:02"
      }
    ],
    "created": "2026-03-15T10:00:00Z"
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `id` | string | 네트워크 ID |
| `name` | string | 네트워크 이름 |
| `driver` | string | 네트워크 드라이버 (bridge, overlay 등) |
| `scope` | string | 범위 (local, swarm) |
| `internal` | boolean | 내부 네트워크 여부 |
| `subnet` | string | 서브넷 CIDR |
| `gateway` | string | 게이트웨이 IP |
| `containers` | array | 연결된 컨테이너 목록 |
| `created` | string | 생성 일시 (ISO 8601) |

---

## Docker Compose — 추가 API

### POST /api/v1/docker/compose/:project/up-stream
Compose 프로젝트 시작 (SSE 스트리밍). 배포 진행 상황을 실시간으로 전달합니다.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Response:** SSE 스트림 (각 이벤트는 JSON)
```
data: {"phase":"deploy","line":"Starting deployment..."}

data: {"phase":"deploy","line":"Creating network my-project_default"}

data: {"phase":"complete","line":"Deployment completed successfully"}
```

에러 발생 시 `phase`가 `"error"`인 이벤트가 전송됩니다.

---

### POST /api/v1/docker/compose/:project/validate
Compose 설정 파일 검증 (`docker compose config`).

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "valid": true,
    "message": "Configuration is valid"
  }
}
```

검증 실패 시:
```json
{
  "success": true,
  "data": {
    "valid": false,
    "message": "services.web.image must be a string"
  }
}
```

---

### POST /api/v1/docker/compose/:project/check-updates
Compose 프로젝트의 서비스 이미지 업데이트 가능 여부 확인.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Request Body:** 없음 (빈 POST)

**Response (200):** Docker ComposeManager.CheckStackUpdates 반환값

---

### POST /api/v1/docker/compose/:project/update
Compose 스택 업데이트 (이미지 풀 + 컨테이너 재생성). 업데이트 전 현재 이미지 정보를 저장하여 롤백을 지원합니다.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "output": "Pulling images... Recreating containers..."
  }
}
```

---

### POST /api/v1/docker/compose/:project/update-stream
Compose 스택 업데이트 (SSE 스트리밍). 풀 및 재생성 진행 상황을 실시간으로 전달합니다.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Response:** SSE 스트림 (각 이벤트는 JSON)
```
data: {"phase":"pull","line":"Starting update..."}

data: {"phase":"update","line":"Pulling nginx:latest..."}

data: {"phase":"complete","line":"Update completed successfully"}
```

---

### POST /api/v1/docker/compose/:project/rollback
이전 이미지 버전으로 롤백. `update` 또는 `update-stream` 실행 시 저장된 이전 이미지 정보를 사용합니다.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "output": "Rolling back images... Recreating containers..."
  }
}
```

---

### GET /api/v1/docker/compose/:project/has-rollback
프로젝트에 롤백 데이터가 존재하는지 확인.

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 프로젝트 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "has_rollback": true
  }
}
```

---

## 클러스터 — 추가 API

### POST /api/v1/cluster/init
새 클러스터 초기화. CA 인증서를 생성하고 Raft를 부트스트랩합니다. 이미 클러스터에 참여 중이면 실패합니다.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "sfpanel",
  "advertise_address": "192.168.1.10",
  "grpc_port": 9444,
  "raft_tls": true
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `name` | string | 아니오 | 클러스터 이름 (기본값: "sfpanel") |
| `advertise_address` | string | 아니오 | Advertise 주소. 미지정 시 자동 감지 |
| `grpc_port` | number | 아니오 | gRPC 포트 (기본값: API 포트 + 1) |
| `raft_tls` | boolean | 아니오 | Raft TLS 사용 여부 (기본값: true) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Cluster initialized successfully",
    "name": "sfpanel",
    "node_id": "node-abc123",
    "live": true
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `message` | string | 결과 메시지 |
| `name` | string | 클러스터 이름 |
| `node_id` | string | 이 노드의 ID |
| `live` | boolean | 재시작 없이 활성화 성공 여부 (`true`이면 즉시 사용 가능) |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 이미 클러스터에 참여 중 |
| `INVALID_REQUEST` | 400 | Advertise 주소를 감지할 수 없음 |
| `INTERNAL_ERROR` | 500 | 초기화 실패 또는 설정 저장 실패 |

---

### POST /api/v1/cluster/join
기존 클러스터에 참가. 리더 노드에 사전 검증(pre-flight) 후 참가를 수행합니다.

- **인증 필요**: 예

**Request Body:**
```json
{
  "leader_address": "192.168.1.5:9444",
  "token": "join-token-string",
  "advertise_address": "192.168.1.10",
  "grpc_port": 9444,
  "node_name": "worker-01"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `leader_address` | string | 예 | 리더 노드의 gRPC 주소 (host:port) |
| `token` | string | 예 | 참가 토큰 |
| `advertise_address` | string | 아니오 | Advertise 주소. 미지정 시 리더 네트워크 기반 자동 감지 |
| `grpc_port` | number | 아니오 | gRPC 포트 (기본값: API 포트 + 1) |
| `node_name` | string | 아니오 | 노드 표시 이름 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Joined cluster successfully",
    "cluster_name": "sfpanel",
    "node_id": "node-def456",
    "live": true
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `message` | string | 결과 메시지 |
| `cluster_name` | string | 참가한 클러스터 이름 |
| `node_id` | string | 이 노드에 할당된 ID |
| `live` | boolean | 재시작 없이 활성화 성공 여부 |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 이미 클러스터에 참여 중 |
| `INVALID_REQUEST` | 400 | leader_address 또는 token 누락 |
| `INTERNAL_ERROR` | 502 | 리더 노드 연결 실패 (pre-flight 검증 실패) |
| `INTERNAL_ERROR` | 500 | 참가 실행 실패 |

---

### POST /api/v1/cluster/leave
클러스터에서 자발적으로 탈퇴. 리더에게 탈퇴를 통보한 후 로컬 클러스터 데이터를 정리하고 서비스를 재시작합니다.

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Left cluster. Service restarting in standalone mode..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 클러스터가 구성되지 않음 |
| `INTERNAL_ERROR` | 500 | 설정 저장 실패 |

---

### POST /api/v1/cluster/disband
전체 클러스터 해산 (리더 전용). 클러스터 데이터 및 TLS 인증서를 정리하고 서비스를 재시작합니다.

- **인증 필요**: 예

**Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Cluster disbanded. Service restarting..."
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 클러스터가 구성되지 않음 |
| `INTERNAL_ERROR` | 500 | 설정 저장 실패 |

---

### POST /api/v1/cluster/leader-transfer
Raft 리더십을 지정한 노드로 이전. 리더 노드에서만 실행 가능.

- **인증 필요**: 예

**Request Body:**
```json
{
  "target_node_id": "node-abc123"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `target_node_id` | string | 예 | 리더십을 이전할 대상 노드 ID |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Leadership transfer initiated",
    "target_node_id": "node-abc123"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 클러스터 미구성 또는 target_node_id 누락 |
| `INTERNAL_ERROR` | 500 | 리더십 이전 실패 |

---

### PATCH /api/v1/cluster/nodes/:id/labels
노드 라벨 업데이트. 리더 노드에서만 실행 가능.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 노드 ID |

**Request Body:**
```json
{
  "labels": {
    "role": "worker",
    "region": "kr-1"
  }
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `labels` | object | 예 | 키-값 라벨 맵 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "node_id": "node-abc123",
    "labels": {
      "role": "worker",
      "region": "kr-1"
    }
  }
}
```

---

### PATCH /api/v1/cluster/nodes/:id/address
노드 API 및 gRPC 주소 업데이트. 리더 노드에서만 실행 가능.

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `id` | 노드 ID |

**Request Body:**
```json
{
  "api_address": "https://192.168.1.10:8443",
  "grpc_address": "192.168.1.10:9444"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `api_address` | string | 예 | 새 API 주소 (URL) |
| `grpc_address` | string | 예 | 새 gRPC 주소 (host:port) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "node_id": "node-abc123",
    "api_address": "https://192.168.1.10:8443",
    "grpc_address": "192.168.1.10:9444"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 클러스터 미구성 또는 노드 ID 누락 |
| `MISSING_FIELDS` | 400 | api_address 또는 grpc_address 누락 |
| `INTERNAL_ERROR` | 500 | 주소 업데이트 실패 |

---

### GET /api/v1/cluster/interfaces
클러스터 초기화 시 Advertise Address 선택을 위한 네트워크 인터페이스 목록. 활성(UP) 상태의 비-루프백 인터페이스만 반환.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "interfaces": [
      {
        "name": "eth0",
        "address": "192.168.1.10"
      },
      {
        "name": "wlan0",
        "address": "192.168.1.20"
      }
    ]
  }
}
```

---

### POST /api/v1/cluster/update
클러스터 전체 SFPanel 업데이트 오케스트레이션 (리더 전용). SSE 스트리밍으로 각 노드의 업데이트 진행 상황을 실시간 전달합니다.

- **인증 필요**: 예
- **응답 형식**: `text/event-stream` (SSE)

**Request Body:**
```json
{
  "mode": "rolling"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `mode` | string | 아니오 | 업데이트 모드: `rolling` (순차, 기본값) 또는 `simultaneous` (동시) |

**SSE 이벤트 예시:**
```
data: {"overall":"started","mode":"rolling","total_nodes":3}

data: {"node_id":"node-abc","node_name":"worker-01","step":"updating","message":"Starting update..."}

data: {"node_id":"node-abc","node_name":"worker-01","step":"complete","message":"Updated successfully"}

data: {"overall":"complete","success_count":3,"fail_count":0}
```

| SSE 필드 | 설명 |
|----------|------|
| `overall` | 전체 진행 상태: `started`, `complete` |
| `node_id` | 업데이트 대상 노드 ID |
| `node_name` | 업데이트 대상 노드 이름 |
| `step` | 노드별 진행 단계: `updating`, `complete`, `failed`, `skipped` |
| `message` | 사람이 읽을 수 있는 상태 메시지 |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 클러스터가 구성되지 않음 |
| `INTERNAL_ERROR` | 503 | 리더가 아닌 노드에서 요청 |

---

## WebSocket API

모든 WebSocket 엔드포인트는 쿼리 파라미터 `?token=<JWT>`로 인증합니다.

### WS /ws/metrics
시스템 메트릭 실시간 스트리밍 (2초 간격).

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:8443/ws/metrics?token=<JWT>`

**서버 -> 클라이언트 메시지 (JSON):**
```json
{
  "cpu": 23.5,
  "mem_total": 8388608000,
  "mem_used": 4194304000,
  "mem_percent": 50.0,
  "swap_total": 2147483648,
  "swap_used": 0,
  "swap_percent": 0.0,
  "disk_total": 107374182400,
  "disk_used": 53687091200,
  "disk_percent": 50.0,
  "net_bytes_sent": 1234567,
  "net_bytes_recv": 7654321,
  "timestamp": 1740000000000
}
```

---

### WS /ws/docker/containers/:id/logs
컨테이너 로그 실시간 스트리밍.

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:8443/ws/docker/containers/abc123/logs?token=<JWT>`
- **Docker 사용 가능 시에만 등록**

**서버 -> 클라이언트 메시지:** 텍스트 메시지 (각 줄이 개별 메시지, 개행 포함)

---

### WS /ws/docker/containers/:id/exec
컨테이너 내부 인터랙티브 쉘 (`/bin/sh`).

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:8443/ws/docker/containers/abc123/exec?token=<JWT>`
- **Docker 사용 가능 시에만 등록**

**클라이언트 -> 서버:**
- 일반 텍스트: 쉘 stdin으로 전달
- JSON 리사이즈: `{"type": "resize", "cols": 80, "rows": 24}`

**서버 -> 클라이언트:** 텍스트 메시지 (쉘 stdout/stderr)

---

### WS /ws/logs
시스템 로그 실시간 스트리밍 (`tail -f`).

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:8443/ws/logs?token=<JWT>&source=syslog`

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `token` | string | 예 | JWT 토큰 |
| `source` | string | 예 | 로그 소스 ID (`syslog`, `auth`, `kern` 등) |

**서버 -> 클라이언트:** 텍스트 메시지 (새 로그 줄)

---

### WS /ws/terminal
서버 호스트 터미널 (PTY) 세션. 재연결 시 스크롤백 버퍼(256 KB) 재생.

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:8443/ws/terminal?token=<JWT>&session_id=default`

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 기본값 | 설명 |
|----------|------|------|--------|------|
| `token` | string | 예 | - | JWT 토큰 |
| `session_id` | string | 아니오 | `"default"` | 세션 식별자 (같은 ID로 재연결 가능) |

**클라이언트 -> 서버:**
- 바이너리/텍스트: 쉘 stdin으로 전달
- JSON 리사이즈 (TextMessage): `{"type": "resize", "cols": 80, "rows": 24}`

**서버 -> 클라이언트:** 바이너리 메시지 (쉘 출력). 재연결 시 스크롤백 히스토리가 먼저 전송됨.

**세션 관리:**
- 세션은 `session_id`로 식별되며, 같은 ID로 재연결하면 기존 PTY 세션 유지
- 유휴 세션은 `terminal_timeout` 설정값(기본 30분)에 따라 자동 정리
- `terminal_timeout`이 `"0"`이면 자동 정리 비활성화

---

## 전체 엔드포인트 요약

REST API 230+개 + WebSocket 6개 = 총 236+개 엔드포인트. 이 외에 8개의 SSE 스트리밍 엔드포인트가 존재 (REST 숫자에 포함). Docker 소켓 미사용 시 `/api/v1/docker/*` 26개는 미등록. 실제 등록 라우트는 서버 시작 로그 또는 `internal/api/router.go`에서 확인.

### 인증/설정 (10개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/health` | X | 헬스체크 |
| POST | `/api/v1/auth/login` | X | 로그인 |
| GET | `/api/v1/auth/setup-status` | X | 셋업 필요 여부 |
| POST | `/api/v1/auth/setup` | X | 초기 관리자 생성 |
| GET | `/api/v1/auth/2fa/status` | O | 2FA 상태 확인 |
| POST | `/api/v1/auth/2fa/setup` | O | 2FA 시크릿 생성 |
| POST | `/api/v1/auth/2fa/verify` | O | 2FA 활성화 |
| POST | `/api/v1/auth/change-password` | O | 비밀번호 변경 |
| GET | `/api/v1/settings` | O | 설정 조회 |
| PUT | `/api/v1/settings` | O | 설정 업데이트 |

### 시스템 (18개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/system/info` | O | 시스템 정보 + 메트릭 + 버전 |
| GET | `/api/v1/system/metrics-history` | O | 24시간 메트릭 히스토리 |
| GET | `/api/v1/system/overview` | O | 대시보드 통합 엔드포인트 |
| GET | `/api/v1/system/update-check` | O | 업데이트 확인 |
| POST | `/api/v1/system/update` | O | 업데이트 실행 (SSE) |
| POST | `/api/v1/system/backup` | O | 시스템 백업 다운로드 |
| POST | `/api/v1/system/restore` | O | 시스템 백업 복원 |
| GET | `/api/v1/system/tuning` | O | 시스템 튜닝 상태 조회 |
| POST | `/api/v1/system/tuning/apply` | O | 시스템 튜닝 적용 |
| POST | `/api/v1/system/tuning/confirm` | O | 시스템 튜닝 확인 |
| POST | `/api/v1/system/tuning/reset` | O | 시스템 튜닝 초기화 |
| GET | `/api/v1/system/processes` | O | 상위 10 프로세스 |
| GET | `/api/v1/system/processes/list` | O | 전체 프로세스 목록 |
| POST | `/api/v1/system/processes/:pid/kill` | O | 프로세스 시그널 전송 |
| GET | `/api/v1/system/services` | O | Systemd 서비스 목록 |
| GET | `/api/v1/system/services/:name/logs` | O | 서비스 로그 조회 |
| GET | `/api/v1/system/services/:name/deps` | O | 서비스 의존성 조회 |
| POST | `/api/v1/system/services/:name/start` | O | 서비스 시작 |
| POST | `/api/v1/system/services/:name/stop` | O | 서비스 중지 |
| POST | `/api/v1/system/services/:name/restart` | O | 서비스 재시작 |
| POST | `/api/v1/system/services/:name/enable` | O | 서비스 활성화 |
| POST | `/api/v1/system/services/:name/disable` | O | 서비스 비활성화 |

### 감사 로그 (2개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/audit/logs` | O | 감사 로그 목록 |
| DELETE | `/api/v1/audit/logs` | O | 감사 로그 전체 삭제 |

### 파일 관리 (8개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/files` | O | 디렉토리 목록 |
| GET | `/api/v1/files/read` | O | 파일 읽기 |
| POST | `/api/v1/files/write` | O | 파일 쓰기 |
| POST | `/api/v1/files/mkdir` | O | 디렉토리 생성 |
| DELETE | `/api/v1/files` | O | 파일/디렉토리 삭제 |
| POST | `/api/v1/files/rename` | O | 이름 변경/이동 |
| GET | `/api/v1/files/download` | O | 파일 다운로드 |
| POST | `/api/v1/files/upload` | O | 파일 업로드 |

### Cron (4개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/cron` | O | cron 작업 목록 |
| POST | `/api/v1/cron` | O | cron 작업 생성 |
| PUT | `/api/v1/cron/:id` | O | cron 작업 수정 |
| DELETE | `/api/v1/cron/:id` | O | cron 작업 삭제 |

### 로그 (4개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/logs/sources` | O | 로그 소스 목록 |
| GET | `/api/v1/logs/read` | O | 로그 읽기 |
| POST | `/api/v1/logs/custom-sources` | O | 커스텀 로그 소스 추가 |
| DELETE | `/api/v1/logs/custom-sources/:id` | O | 커스텀 로그 소스 삭제 |

### 네트워크 (11개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/network/status` | O | 네트워크 통합 상태 |
| GET | `/api/v1/network/interfaces` | O | 네트워크 인터페이스 목록 |
| GET | `/api/v1/network/interfaces/:name` | O | 인터페이스 상세 |
| PUT | `/api/v1/network/interfaces/:name` | O | 인터페이스 설정 변경 |
| POST | `/api/v1/network/apply` | O | Netplan 적용 |
| GET | `/api/v1/network/dns` | O | DNS 설정 조회 |
| PUT | `/api/v1/network/dns` | O | DNS 설정 변경 |
| GET | `/api/v1/network/routes` | O | 라우팅 테이블 조회 |
| GET | `/api/v1/network/bonds` | O | 본딩 목록 |
| POST | `/api/v1/network/bonds` | O | 본딩 생성 |
| DELETE | `/api/v1/network/bonds/:name` | O | 본딩 삭제 |

### WireGuard VPN (10개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/network/wireguard/status` | O | WireGuard 설치 상태 확인 |
| POST | `/api/v1/network/wireguard/install` | O | WireGuard 설치 |
| GET | `/api/v1/network/wireguard/interfaces` | O | WireGuard 인터페이스 목록 |
| GET | `/api/v1/network/wireguard/interfaces/:name` | O | WireGuard 인터페이스 상세 |
| POST | `/api/v1/network/wireguard/interfaces/:name/up` | O | WireGuard 인터페이스 활성화 |
| POST | `/api/v1/network/wireguard/interfaces/:name/down` | O | WireGuard 인터페이스 비활성화 |
| POST | `/api/v1/network/wireguard/configs` | O | WireGuard 설정 파일 생성 |
| GET | `/api/v1/network/wireguard/configs/:name` | O | WireGuard 설정 파일 조회 |
| PUT | `/api/v1/network/wireguard/configs/:name` | O | WireGuard 설정 파일 수정 |
| DELETE | `/api/v1/network/wireguard/configs/:name` | O | WireGuard 설정 파일 삭제 |

### Tailscale VPN (8개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/network/tailscale/status` | O | Tailscale 상태 확인 |
| POST | `/api/v1/network/tailscale/install` | O | Tailscale 설치 |
| POST | `/api/v1/network/tailscale/up` | O | Tailscale 연결 |
| POST | `/api/v1/network/tailscale/down` | O | Tailscale 연결 해제 |
| POST | `/api/v1/network/tailscale/logout` | O | Tailscale 로그아웃 |
| GET | `/api/v1/network/tailscale/peers` | O | Tailscale 피어 목록 |
| GET | `/api/v1/network/tailscale/update-check` | O | Tailscale 업데이트 확인 |
| PUT | `/api/v1/network/tailscale/preferences` | O | Tailscale 설정 변경 |

### 디스크 (9개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/disks/overview` | O | 디스크 목록 |
| GET | `/api/v1/disks/iostat` | O | I/O 통계 |
| POST | `/api/v1/disks/usage` | O | 디스크 사용량 |
| GET | `/api/v1/disks/smartmontools-status` | O | smartmontools 설치 상태 |
| POST | `/api/v1/disks/install-smartmontools` | O | smartmontools 설치 |
| GET | `/api/v1/disks/:device/smart` | O | SMART 정보 |
| GET | `/api/v1/disks/:device/partitions` | O | 파티션 목록 |
| POST | `/api/v1/disks/:device/partitions` | O | 파티션 생성 |
| DELETE | `/api/v1/disks/:device/partitions/:number` | O | 파티션 삭제 |

### 파일시스템 (7개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/filesystems` | O | 파일시스템 목록 |
| POST | `/api/v1/filesystems/format` | O | 파티션 포맷 |
| POST | `/api/v1/filesystems/mount` | O | 마운트 |
| POST | `/api/v1/filesystems/unmount` | O | 언마운트 |
| POST | `/api/v1/filesystems/resize` | O | 파일시스템 리사이즈 |
| GET | `/api/v1/filesystems/expand-check` | O | 파일시스템 확장 가능 여부 |
| POST | `/api/v1/filesystems/expand` | O | 파일시스템 확장 |

### LVM (10개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/lvm/pvs` | O | PV 목록 |
| GET | `/api/v1/lvm/vgs` | O | VG 목록 |
| GET | `/api/v1/lvm/lvs` | O | LV 목록 |
| POST | `/api/v1/lvm/pvs` | O | PV 생성 |
| POST | `/api/v1/lvm/vgs` | O | VG 생성 |
| POST | `/api/v1/lvm/lvs` | O | LV 생성 |
| DELETE | `/api/v1/lvm/pvs/:name` | O | PV 제거 |
| DELETE | `/api/v1/lvm/vgs/:name` | O | VG 제거 |
| DELETE | `/api/v1/lvm/lvs/:vg/:name` | O | LV 제거 |
| POST | `/api/v1/lvm/lvs/resize` | O | LV 리사이즈 |

### RAID (6개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/raid` | O | RAID 어레이 목록 |
| GET | `/api/v1/raid/:name` | O | RAID 어레이 상세 |
| POST | `/api/v1/raid` | O | RAID 어레이 생성 |
| DELETE | `/api/v1/raid/:name` | O | RAID 어레이 삭제 |
| POST | `/api/v1/raid/:name/add` | O | RAID 디스크 추가 |
| POST | `/api/v1/raid/:name/remove` | O | RAID 디스크 제거 |

### Swap (6개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/swap` | O | 스왑 정보 조회 |
| POST | `/api/v1/swap` | O | 스왑 생성 |
| DELETE | `/api/v1/swap` | O | 스왑 제거 |
| PUT | `/api/v1/swap/swappiness` | O | swappiness 설정 |
| GET | `/api/v1/swap/resize-check` | O | 스왑 리사이즈 가능 여부 |
| PUT | `/api/v1/swap/resize` | O | 스왑 리사이즈 |

### 방화벽 - UFW (10개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/firewall/status` | O | UFW 상태 조회 |
| POST | `/api/v1/firewall/enable` | O | UFW 활성화 |
| POST | `/api/v1/firewall/disable` | O | UFW 비활성화 |
| GET | `/api/v1/firewall/rules` | O | UFW 규칙 목록 |
| POST | `/api/v1/firewall/rules` | O | UFW 규칙 추가 |
| DELETE | `/api/v1/firewall/rules/:number` | O | UFW 규칙 삭제 |
| GET | `/api/v1/firewall/ports` | O | 리스닝 포트 목록 |
| GET | `/api/v1/firewall/docker` | O | Docker 방화벽 규칙 목록 |
| POST | `/api/v1/firewall/docker/rules` | O | Docker 방화벽 규칙 추가 |
| DELETE | `/api/v1/firewall/docker/rules/:number` | O | Docker 방화벽 규칙 삭제 |

### 방화벽 - Fail2ban (11개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/fail2ban/status` | O | Fail2ban 상태 확인 |
| POST | `/api/v1/fail2ban/install` | O | Fail2ban 설치 |
| GET | `/api/v1/fail2ban/templates` | O | Jail 템플릿 목록 |
| GET | `/api/v1/fail2ban/jails` | O | Jail 목록 |
| POST | `/api/v1/fail2ban/jails` | O | Jail 생성 |
| DELETE | `/api/v1/fail2ban/jails/:name` | O | Jail 삭제 |
| GET | `/api/v1/fail2ban/jails/:name` | O | Jail 상세 |
| POST | `/api/v1/fail2ban/jails/:name/enable` | O | Jail 활성화 |
| POST | `/api/v1/fail2ban/jails/:name/disable` | O | Jail 비활성화 |
| PUT | `/api/v1/fail2ban/jails/:name/config` | O | Jail 설정 변경 |
| POST | `/api/v1/fail2ban/jails/:name/unban` | O | IP 차단 해제 |

### 패키지 관리 (19개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/packages/updates` | O | 업데이트 확인 |
| POST | `/api/v1/packages/upgrade` | O | 패키지 업그레이드 |
| POST | `/api/v1/packages/install` | O | 패키지 설치 |
| POST | `/api/v1/packages/remove` | O | 패키지 제거 |
| GET | `/api/v1/packages/search` | O | 패키지 검색 |
| GET | `/api/v1/packages/docker-status` | O | Docker 상태 확인 |
| POST | `/api/v1/packages/install-docker` | O | Docker 설치 (SSE) |
| GET | `/api/v1/packages/node-status` | O | Node.js 설치 상태 |
| POST | `/api/v1/packages/install-node` | O | Node.js 설치 (SSE) |
| GET | `/api/v1/packages/node-versions` | O | Node.js 설치된 버전 목록 |
| POST | `/api/v1/packages/node-switch` | O | Node.js 버전 전환 |
| POST | `/api/v1/packages/node-install-version` | O | Node.js 특정 버전 설치 (SSE) |
| POST | `/api/v1/packages/node-uninstall-version` | O | Node.js 특정 버전 삭제 |
| GET | `/api/v1/packages/claude-status` | O | Claude CLI 설치 상태 |
| POST | `/api/v1/packages/install-claude` | O | Claude CLI 설치 (SSE) |
| GET | `/api/v1/packages/codex-status` | O | Codex CLI 설치 상태 |
| POST | `/api/v1/packages/install-codex` | O | Codex CLI 설치 (SSE) |
| GET | `/api/v1/packages/gemini-status` | O | Gemini CLI 설치 상태 |
| POST | `/api/v1/packages/install-gemini` | O | Gemini CLI 설치 (SSE) |

### Docker - 컨테이너 (10개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/docker/containers` | O | 컨테이너 목록 |
| GET | `/api/v1/docker/containers/stats/batch` | O | 컨테이너 배치 stats |
| GET | `/api/v1/docker/containers/:id/inspect` | O | 컨테이너 상세 |
| GET | `/api/v1/docker/containers/:id/stats` | O | 컨테이너 리소스 |
| POST | `/api/v1/docker/containers/:id/start` | O | 컨테이너 시작 |
| POST | `/api/v1/docker/containers/:id/stop` | O | 컨테이너 중지 |
| POST | `/api/v1/docker/containers/:id/restart` | O | 컨테이너 재시작 |
| POST | `/api/v1/docker/containers/:id/pause` | O | 컨테이너 일시정지 |
| POST | `/api/v1/docker/containers/:id/unpause` | O | 컨테이너 일시정지 해제 |
| DELETE | `/api/v1/docker/containers/:id` | O | 컨테이너 삭제 |

### Docker - 이미지 (5개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/docker/images` | O | 이미지 목록 |
| GET | `/api/v1/docker/images/search` | O | Docker Hub 이미지 검색 |
| POST | `/api/v1/docker/images/pull` | O | 이미지 풀 |
| GET | `/api/v1/docker/images/updates` | O | 이미지 업데이트 확인 |
| DELETE | `/api/v1/docker/images/:id` | O | 이미지 삭제 |

### Docker - 볼륨 (3개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/docker/volumes` | O | 볼륨 목록 |
| POST | `/api/v1/docker/volumes` | O | 볼륨 생성 |
| DELETE | `/api/v1/docker/volumes/:name` | O | 볼륨 삭제 |

### Docker - 네트워크 (4개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/docker/networks` | O | 네트워크 목록 |
| POST | `/api/v1/docker/networks` | O | 네트워크 생성 |
| DELETE | `/api/v1/docker/networks/:id` | O | 네트워크 삭제 |
| GET | `/api/v1/docker/networks/:id/inspect` | O | 네트워크 상세 조회 |

### Docker - Prune (5개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/api/v1/docker/prune/containers` | O | 중지된 컨테이너 정리 |
| POST | `/api/v1/docker/prune/images` | O | 미사용 이미지 정리 |
| POST | `/api/v1/docker/prune/volumes` | O | 미사용 볼륨 정리 |
| POST | `/api/v1/docker/prune/networks` | O | 미사용 네트워크 정리 |
| POST | `/api/v1/docker/prune/all` | O | 전체 정리 |

### Docker - Compose (20개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/docker/compose` | O | Compose 프로젝트 목록 |
| POST | `/api/v1/docker/compose` | O | Compose 프로젝트 생성 |
| GET | `/api/v1/docker/compose/:project` | O | Compose 프로젝트 상세 |
| PUT | `/api/v1/docker/compose/:project` | O | Compose YAML 수정 |
| DELETE | `/api/v1/docker/compose/:project` | O | Compose 프로젝트 삭제 |
| POST | `/api/v1/docker/compose/:project/up` | O | Compose 시작 |
| POST | `/api/v1/docker/compose/:project/up-stream` | O | Compose 시작 (SSE 스트리밍) |
| POST | `/api/v1/docker/compose/:project/down` | O | Compose 중지 |
| GET | `/api/v1/docker/compose/:project/env` | O | 환경변수 파일 조회 |
| PUT | `/api/v1/docker/compose/:project/env` | O | 환경변수 파일 수정 |
| GET | `/api/v1/docker/compose/:project/services` | O | 서비스 목록 |
| POST | `/api/v1/docker/compose/:project/services/:service/restart` | O | 서비스 재시작 |
| POST | `/api/v1/docker/compose/:project/services/:service/stop` | O | 서비스 중지 |
| POST | `/api/v1/docker/compose/:project/services/:service/start` | O | 서비스 시작 |
| GET | `/api/v1/docker/compose/:project/services/:service/logs` | O | 서비스 로그 |
| POST | `/api/v1/docker/compose/:project/validate` | O | Compose 설정 검증 |
| POST | `/api/v1/docker/compose/:project/check-updates` | O | 스택 이미지 업데이트 확인 |
| POST | `/api/v1/docker/compose/:project/update` | O | 스택 업데이트 (풀 + 재생성) |
| POST | `/api/v1/docker/compose/:project/update-stream` | O | 스택 업데이트 (SSE 스트리밍) |
| POST | `/api/v1/docker/compose/:project/rollback` | O | 스택 롤백 |
| GET | `/api/v1/docker/compose/:project/has-rollback` | O | 롤백 가능 여부 확인 |

### 앱스토어 (6개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/appstore/categories` | O | 앱스토어 카테고리 목록 |
| GET | `/api/v1/appstore/apps` | O | 앱 목록 (카테고리 필터) |
| GET | `/api/v1/appstore/apps/:id` | O | 앱 상세 정보 + Compose YAML |
| POST | `/api/v1/appstore/apps/:id/install` | O | 앱 설치 |
| GET | `/api/v1/appstore/installed` | O | 설치된 앱 목록 |
| POST | `/api/v1/appstore/refresh` | O | 앱스토어 캐시 갱신 |

### 클러스터 (15개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/cluster/status` | O | 클러스터 상태 (활성화 여부, 노드 수, 리더) |
| GET | `/api/v1/cluster/overview` | O | 클러스터 개요 (노드 목록 + 집계 메트릭) |
| GET | `/api/v1/cluster/nodes` | O | 노드 목록 (상태, 역할, 라벨) |
| POST | `/api/v1/cluster/token` | O | 참가 토큰 생성 (TTL 지정 가능) |
| DELETE | `/api/v1/cluster/nodes/:id` | O | 노드 제거 (리더만 가능) |
| PATCH | `/api/v1/cluster/nodes/:id/labels` | O | 노드 라벨 수정 |
| PATCH | `/api/v1/cluster/nodes/:id/address` | O | 노드 주소 수정 |
| GET | `/api/v1/cluster/events` | O | 클러스터 이벤트 로그 |
| POST | `/api/v1/cluster/leader-transfer` | O | 리더십 이전 |
| POST | `/api/v1/cluster/init` | O | 클러스터 초기화 (CA 생성, Raft 부트스트랩) |
| POST | `/api/v1/cluster/join` | O | 기존 클러스터 참가 (pre-flight 검증 포함) |
| POST | `/api/v1/cluster/leave` | O | 클러스터 탈퇴 (서비스 재시작) |
| POST | `/api/v1/cluster/disband` | O | 클러스터 해산 (리더 전용) |
| GET | `/api/v1/cluster/interfaces` | O | 네트워크 인터페이스 목록 (클러스터 초기화용) |
| POST | `/api/v1/cluster/update` | O | 클러스터 전체 업데이트 오케스트레이션 (SSE, 리더 전용) |

### 알림 시스템 (11개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/alerts/channels` | O | 알림 채널 목록 (Discord/Telegram) |
| POST | `/api/v1/alerts/channels` | O | 알림 채널 생성 |
| PUT | `/api/v1/alerts/channels/:id` | O | 채널 편집 |
| DELETE | `/api/v1/alerts/channels/:id` | O | 채널 삭제 |
| POST | `/api/v1/alerts/channels/:id/test` | O | 테스트 알림 발송 |
| GET | `/api/v1/alerts/rules` | O | 알림 규칙 목록 |
| POST | `/api/v1/alerts/rules` | O | 알림 규칙 생성 |
| PUT | `/api/v1/alerts/rules/:id` | O | 규칙 편집 |
| DELETE | `/api/v1/alerts/rules/:id` | O | 규칙 삭제 |
| GET | `/api/v1/alerts/history` | O | 알림 발송 이력 |
| DELETE | `/api/v1/alerts/history` | O | 이력 전체 삭제 |

### SSE 스트리밍 (8개)

위 표들의 라우트 중 `Content-Type: text/event-stream`으로 응답하는 엔드포인트 목록. 자세한 이벤트 스키마는 `docs/specs/websocket-spec.md` 참조.

| 메서드 | 경로 | 용도 |
|--------|------|------|
| POST | `/api/v1/system/update` | SFPanel 자체 업데이트 |
| POST | `/api/v1/docker/images/pull` | Docker 이미지 풀 |
| POST | `/api/v1/docker/compose/:project/up-stream` | Compose 시작 스트리밍 |
| POST | `/api/v1/docker/compose/:project/update-stream` | Compose 업데이트 스트리밍 |
| POST | `/api/v1/packages/install-docker` | Docker 엔진 설치 |
| POST | `/api/v1/packages/install-node` | Node.js/NVM 설치 |
| POST | `/api/v1/network/tailscale/install` | Tailscale 설치 |
| POST | `/api/v1/cluster/update` | 클러스터 멀티노드 업데이트 |

### WebSocket (6개)

모두 `?token=<JWT>` 쿼리 파라미터 인증. `?node=<nodeID>` 파라미터로 클러스터 원격 릴레이 지원.

| 프로토콜 | 경로 | 인증 | 설명 |
|----------|------|------|------|
| WS | `/ws/metrics` | O (query) | 실시간 메트릭 |
| WS | `/ws/logs` | O (query) | 실시간 로그 스트리밍 |
| WS | `/ws/terminal` | O (query) | 호스트 PTY 터미널 (영속, 256KB 스크롤백, 최대 20 세션) |
| WS | `/ws/docker/containers/:id/logs` | O (query) | 컨테이너 로그 |
| WS | `/ws/docker/containers/:id/exec` | O (query) | 컨테이너 셸 exec |
| WS | `/ws/docker/compose/:project/logs` | O (query) | Compose 프로젝트 로그 (서비스 필터 가능) |
