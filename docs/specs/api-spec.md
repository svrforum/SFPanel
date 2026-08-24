# SFPanel API 스펙

> 마지막 전체 동기화: 2026-04-19 · 기준 버전: v0.9.0 · 근거: `docs/superpowers/research/2026-04-19-docs-overhaul/api-inventory.md`
> v0.19.0–v0.40.0 캠페인 라우트 반영: 2026-06-03 (`internal/api/router.go` 기준 등록 라우트 279개)
>
> 권한 있는 출처는 `internal/api/router.go`이며, 변경 요약은 `CHANGELOG.md`를 참조하세요. 본 문서가 코드와 어긋날 경우 코드를 우선시합니다.

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
  "totp_code": "string",      // 선택 (2FA 활성화 시 필수)
  "recovery_code": "string"   // 선택 (totp_code 대신 1회용 복구 코드로 로그인, v0.34.0+)
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
| `INVALID_TOTP` | 401 | 잘못된 2FA 코드 또는 잘못된/소진된 복구 코드 |

> **복구 코드 로그인 (v0.34.0+)**: `recovery_code`가 제공되면 `totp_code` 대신 사용됩니다. 유효한 코드는 사용 시 1회 소진됩니다. 코드는 SHA-256 해시로 저장되며 클러스터 관리자의 경우 Raft FSM으로 복제됩니다 — 팔로워에서 코드 소진은 leader write이므로, 팔로워 노드에서의 복구 로그인은 "리더 노드를 사용하라"는 안내와 함께 거부될 수 있습니다.

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

### DELETE /api/v1/auth/2fa
2FA 비활성화. 비밀번호 + **현재 TOTP 코드**(2FA가 켜진 경우)를 재확인한 뒤 `totp_secret`을 제거하고, 함께 복구 코드도 비웁니다(2FA 없이는 무의미하므로). 세션만 탈취한 공격자가 계정을 비밀번호-only로 다운그레이드하는 것을 막기 위해 물리적 인증기(TOTP)가 요구됩니다 (v0.38.0).

- **인증 필요**: 예

**Request Body:**
```json
{
  "password": "string",
  "totp_code": "string"   // 2FA가 활성 상태이면 필수
}
```

**Response (200):** `{ "success": true, "data": { "message": "..." } }`

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `MISSING_FIELDS` | 400 | password 누락 |
| `INVALID_PASSWORD` | 401 | 비밀번호 불일치 |
| `TOTP_REQUIRED` | 400 | 2FA 활성 상태인데 totp_code 누락 |
| `INVALID_TOTP` | 401 | 잘못된 현재 2FA 코드 |
| `RATE_LIMITED` | 429 | per-IP 시도 제한 초과 |

> 클러스터 관리자 계정은 FSM 쓰기이므로 팔로워에서 호출 시 리더로 프록시됩니다.

---

### POST /api/v1/auth/2fa/recovery
2FA 복구 코드 한 세트를 새로 생성 (이전 세트는 무효화). 평문 코드는 **응답에서 1회만** 반환되며 이후 조회 불가. 2FA가 먼저 활성화되어 있어야 합니다.

- **인증 필요**: 예
- **Request Body:** 없음 (빈 POST)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "codes": ["ABCDE-FGHIJ", "..."]
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_REQUEST` | 400 | 2FA가 비활성 상태 (먼저 활성화 필요) |
| `USER_NOT_FOUND` | 404 | 사용자 없음 |

> 코드는 SHA-256 해시로 저장되고 클러스터 관리자는 Raft FSM으로 복제됩니다(팔로워는 리더로 프록시). 계정 record와 분리되어 있어 비밀번호/TOTP 변경이 복구 코드를 지우지 않습니다.

---

### GET /api/v1/auth/2fa/recovery/status
복구 코드 존재 여부와 남은 개수 조회.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "generated": true,
    "remaining": 8
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `generated` | boolean | 복구 코드가 1개 이상 존재하는지 |
| `remaining` | number | 미사용 복구 코드 수 |

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

### GET /api/v1/system/overview
대시보드 초기 로드용 **통합** 엔드포인트. 호스트 정보 + 현재 메트릭 + 메트릭 히스토리 + 패널 버전 + 업데이트 정보를 한 번에 반환하여 초기 요청 수를 줄인다. 서버는 host/metrics와 history를 병렬(goroutine 2개)로 수집하고, 한 소스가 실패해도 500 대신 해당 필드를 `null`로 둔 부분 응답을 돌려준다(WARN 로그만 남김).

- **인증 필요**: 예
- **쿼리 파라미터**: `range` — 히스토리 윈도우(예: `1h`/`4h`/`12h`/`24h`). 생략 시 기본 윈도우.

**Response (200):**
```json
{
  "success": true,
  "data": {
    "host": { "hostname": "…", "os": "linux", "platform": "ubuntu", "platform_version": "26.04", "kernel": "…", "uptime": 0, "num_cpu": 4 },
    "metrics": { "cpu": 23.5, "mem_percent": 50.0, "disk_percent": 34.0, "…": "…" },
    "version": "0.16.1",
    "metrics_history": [ { "time": 1740000000000, "cpu": 23.5, "mem_percent": 50.0 } ],
    "update_info": { "latest_version": "0.16.2", "…": "…" }
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `host` | object\|null | 호스트 시스템 정보 (수집 실패 시 null) |
| `metrics` | object\|null | 현재 시스템 메트릭 (수집 실패 시 null) |
| `version` | string | 패널 버전 (`v` 접두사 제거됨) |
| `metrics_history` | array | 메트릭 히스토리 포인트 (`range` 윈도우) |
| `update_info` | object | 최신 버전/업데이트 가용 정보 (`latest_version` 등) |

---

### GET /api/v1/system/portmap
호스트 포트별로 UFW 규칙 + Docker DNAT 컨테이너 + 호스트 프로세스를 통합한 포트 맵 (`ufw status numbered` + Docker DNAT + `ss -tlnp/-ulnp` 집계).

- **인증 필요**: 예

**Response (200):** `data`는 `PortMapRow` 배열:

| 필드 | 타입 | 설명 |
|------|------|------|
| `port` | number | 호스트 포트 |
| `proto` | string | `tcp` \| `udp` |
| `state` | string | `listening` \| `bound` |
| `firewall` | object\|null | `{action, scope, rule_id, source:"ufw"}` (UFW 규칙) |
| `containers` | array | `{id, name, stack}` (해당 포트를 발행한 컨테이너) |
| `process` | object\|null | `{pid, name}` (리스닝 호스트 프로세스) |

---

### GET /api/v1/system/tls
패널이 브라우저에 제시하는 인증서의 상태. 노드별 상태이므로 `?node=`로 대상 노드의 값을 조회합니다 — 노드마다 자체 CA를 운영합니다.

- **인증 필요**: 예

**Response (200):**

| 필드 | 타입 | 설명 |
|------|------|------|
| `enabled` | boolean | `server.tls.enabled` 여부. false면 나머지 필드 없음 |
| `managed` | boolean | 패널이 직접 CA를 운영하는지. 운영자가 인증서를 제공한 경우 false이며 CA 다운로드 불가 |
| `ca_not_after` | string | CA 만료 시각 (10년) |
| `ca_fingerprint` | string | CA SHA-256 지문 (콜론 구분 대문자 hex) — 기기에 설치한 것과 대조용 |
| `ca_subject` | string | CA CommonName |
| `not_after` | string | 서버 인증서 만료 시각 (1년) |
| `dns_names` | string[] | 인증서 DNS SAN |
| `ip_addresses` | string[] | 인증서 IP SAN |
| `days_until_renew` | number | 자동 갱신까지 남은 일수. 0 이하이면 다음 재시작에 갱신 |

### GET /api/v1/system/tls/ca.crt
로컬 CA **인증서**를 PEM으로 내려받습니다. 기기에 한 번 설치하면 브라우저 경고가 사라집니다.

- **인증 필요**: 예
- **Content-Type**: `application/x-x509-ca-cert`
- **Content-Disposition**: `attachment; filename=sfpanel-ca-<hostname>.crt`
- `managed`가 false이거나 TLS가 꺼져 있으면 **404 `TLS_DISABLED`**

> 인증서만 반환합니다. CA 개인키와 서버 개인키는 어떤 라우트로도 노출되지 않습니다.

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
| `ppid` | number | 부모 프로세스 ID (트리 뷰 구성용, v0.20.0+) |
| `name` | string | 프로세스 이름 |
| `cpu` | number | CPU 사용률 (%) |
| `memory` | number | 메모리 사용률 (%) |
| `rss` | number | 절대 상주 메모리 (bytes, v0.20.0+) |
| `nice` | number | nice 값 (스케줄링 우선순위, v0.20.0+) |
| `status` | string | 프로세스 상태 코드 (예: "S", "R", "Z") |
| `user` | string | 소유 사용자 |
| `command` | string | 전체 명령줄 |

> `ppid`/`rss`/`nice`는 `GET /system/processes`(상위 10)와 `GET /system/processes/list` 양쪽 응답에 포함됩니다. 프론트엔드는 `ppid`로 트리 뷰(순환 가드 포함)를 구성합니다.

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
| `signal` | string | 아니오 | 시그널 이름/번호. 기본값 `"TERM"`. 허용: `TERM`/`15`, `KILL`/`9`, `HUP`/`1`, `INT`/`2`, `STOP`/`19`(일시정지), `CONT`/`18`(재개) (STOP/CONT는 v0.20.0+) |

> 보호 PID(init, kthreadd, sfpanel 자신)와 패널이 스폰한 자식 프로세스(pgid 매칭)는 시그널 전송이 거부됩니다(403, `INVALID_PID`).

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

### POST /api/v1/system/processes/:pid/renice
프로세스의 스케줄링 우선순위(nice 값) 변경 (v0.20.0+).

- **인증 필요**: 예

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `pid` | 대상 프로세스 ID |

**Request Body:**
```json
{
  "nice": 10
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `nice` | number | 예 | nice 값. `-20`(최고 우선순위) ~ `19`(최저)로 클램프 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Process 1234 reniced to 10",
    "pid": 1234,
    "nice": 10
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PID` | 400/403 | 유효하지 않은 PID 또는 보호 PID/패널 자식 프로세스 |
| `INVALID_BODY` | 400 | nice 값 누락 |
| `INVALID_VALUE` | 400 | nice가 −20..19 범위 밖 |
| `PROCESS_NOT_FOUND` | 404 | 프로세스 없음 |
| `INTERNAL_ERROR` | 500 | setpriority 실패 |

> kill과 동일한 보호 가드 적용 — 보호 PID(init/kthreadd/sfpanel)와 패널이 스폰한 자식 프로세스의 renice는 거부됩니다.

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
      "kind": "dir",
      "owner": { "uid": 0, "gid": 0, "user": "root", "group": "root" },
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

> **삭제는 휴지통을 거칩니다.** `DELETE /api/v1/files`는 항목을 `/var/lib/sfpanel/trash`로 옮기고 7일간 보관합니다. 응답의 `trashed`가 실제로 휴지통에 들어갔는지 알려줍니다 — 다른 파일시스템의 항목은 `rename`으로 옮길 수 없어 영구 삭제로 떨어집니다(되돌릴 수 없다는 이유로 삭제를 거부하는 것보다 나은 선택). `?permanent=true`로 휴지통을 건너뜁니다.
>
> 시스템이 즉시 멈추는 디렉터리(`/bin` `/sbin` `/lib` `/lib64` `/boot` `/proc` `/sys` `/dev` `/run` `/usr`, `/`)는 **정확히 일치할 때만** 거부합니다. 보안 통제가 아니라 오조작 방지입니다 — 같은 패널이 옆 탭에서 root 터미널을 제공합니다.

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

### POST /api/v1/files/copy
파일 또는 디렉토리 트리 복사 (v0.21.0+). 기존 대상 덮어쓰기 거부, 자기 자신 하위로의 복사 거부, 비정규 파일(심볼릭 링크 등) 건너뜀.

- **인증 필요**: 예

**Request Body:**
```json
{
  "src": "/opt/src",
  "dst": "/opt/dst"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `src` | string | 예 | 원본 절대 경로 |
| `dst` | string | 예 | 대상 절대 경로 (존재해서는 안 됨) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "path copied",
    "src": "/opt/src",
    "dst": "/opt/dst"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PATH` | 400 | src/dst 경로 유효성 검증 실패 |
| `CRITICAL_PATH` | 403 | dst가 보호된 시스템 경로 |
| `INVALID_REQUEST` | 400 | 디렉토리를 자기 하위로 복사 |
| `NOT_FOUND` | 404 | 원본 없음 |
| `CONFLICT` | 409 | 대상이 이미 존재 |
| `PERMISSION_DENIED` | 403 | 권한 부족 |

---

### GET /api/v1/files/search
현재 디렉토리 하위에서 이름에 검색어가 포함된 항목을 재귀 검색 (대소문자 무시, v0.21.0+). 결과 수 상한(최대 1000)과 wall-clock 데드라인(10초)으로 제한되며, 잘림 여부를 응답에 표시.

- **인증 필요**: 예

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 기본값 | 설명 |
|----------|------|------|--------|------|
| `path` | string | 예 | - | 검색 루트 디렉토리 절대 경로 |
| `q` | string | 예 | - | 검색어 (부분 일치) |
| `limit` | number | 아니오 | `200` | 결과 상한 (최대 1000) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "results": [
      {
        "name": "config.yaml",
        "path": "/opt/app/config.yaml",
        "size": 1024,
        "mode": "-rw-r--r--",
        "modTime": "2026-01-15T10:30:00Z",
        "isDir": false
      }
    ],
    "count": 1,
    "truncated": false
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `results` | array | 매칭된 항목 (`FileEntry` 형식, 디렉토리 목록과 동일) |
| `count` | number | 반환된 결과 수 |
| `truncated` | boolean | 상한/데드라인에 도달해 결과가 잘렸는지 |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PATH` | 400 | 경로 유효성 검증 실패 |
| `INVALID_REQUEST` | 400 | 검색어 누락 또는 검색 경로가 디렉토리가 아님 |
| `NOT_FOUND` | 404 | 경로 없음 |
| `PERMISSION_DENIED` | 403 | 권한 부족 |

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

### GET /api/v1/files/thumbnail
이미지 축소본 (JPEG 바이너리). 파일 관리자 그리드 뷰가 사용합니다. (v0.66.0+)

- **인증 필요**: 예 — `<img>`는 헤더를 붙일 수 없으므로 `?token=` 쿼리 토큰도 허용됩니다.

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `path` | string | 예 | 이미지의 절대 경로 |
| `size` | int | 아니오 | 정사각 박스 한 변(기본 192, 최대 512). 종횡비는 유지되고 원본보다 커지지 않습니다 |

**Response:** JPEG 바이너리 + `Cache-Control: private, max-age=86400`. 캐시 키가 경로·mtime·크기를 모두 포함하므로 한 URL의 내용은 절대 바뀌지 않습니다 — 파일이 수정되면 URL 자체가 달라집니다.

지원 포맷은 JPEG · PNG · GIF. WebP/AVIF는 표준 라이브러리가 디코딩하지 못해 의도적으로 빠져 있고, UI는 아이콘으로 대체합니다.

**디코딩 상한.** 바이트 상한(25 MB)과 픽셀 상한(8천만)은 서로 다른 것을 막습니다. 20 MB JPEG이 20000×20000이면 디코딩 후 RGBA로 1.6 GB이므로, 헤더만 먼저 파싱해 **선언된 크기로 거부**합니다 — 픽셀을 한 줄도 할당하기 전에.

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PATH` | 400 | 경로가 유효하지 않거나 일반 파일이 아님 |
| `FILE_TOO_LARGE` | 400 | 원본이 25 MB 초과 |
| `FILE_ERROR` | 400 | 픽셀 상한 초과 · 디코딩 실패 |
| `READ_PROTECTED` | 403 | 읽기 보호 경로 |
| `PERMISSION_DENIED` | 403 | 파일을 읽을 권한 없음 |
| `NOT_FOUND` | 404 | 파일 없음 |
| `UNSUPPORTED_FORMAT` | 415 | 썸네일을 만들 수 없는 포맷 |

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

### POST /api/v1/files/chmod
권한 비트 변경.

- **인증 필요**: 예
- **Body**: `{ "path": "/opt/stacks/app/run.sh", "mode": "0755", "recursive": false }`

`mode`는 3~4자리 8진수만 받습니다. **setuid·setgid·sticky는 거부**합니다 — 텍스트 필드에 네 자리를 쳐서 setuid를 켜는 건 파일 관리자가 제공할 동작이 아닙니다.

**심볼릭 링크는 거부**합니다(400 `INVALID_PATH`). chmod는 링크를 따라가므로, 링크 행에서 실행하면 사용자가 지목하지 않은 파일의 권한이 바뀝니다. 리눅스는 링크 자체에 권한을 저장하지 않고 `lchmod`도 없습니다.

`recursive`는 트리를 순회하되 **심볼릭 링크는 건너뜁니다** — 트리에 심어진 링크가 chmod를 트리 밖으로 유도하지 못하게.

### POST /api/v1/files/chown
소유자·그룹 변경.

- **인증 필요**: 예
- **Body**: `{ "path": "/srv/data", "user": "www-data", "group": "www-data", "recursive": true }`

이름과 **숫자 id를 모두** 받습니다. 문제를 일으킨 컨테이너 uid는 대개 passwd 항목이 없는 쪽이라, 이름으로는 지정할 수 없습니다. 빈 필드는 "그대로 두기"(`chown`의 `-1`)입니다. 링크를 따라가지 않도록 `lchown`을 씁니다.

### POST /api/v1/files/archive
선택 항목을 `.tar.gz` 또는 `.zip`으로 묶습니다.

- **인증 필요**: 예
- **Body**: `{ "paths": ["/opt/stacks/app"], "dest": "/opt/backups/app.tar.gz", "format": "tar.gz" }`
- 목적지가 이미 있으면 **409 `DESTINATION_EXISTS`**

임시 이름으로 쓴 뒤 성공 시 rename합니다 — 반쯤 쓰인 아카이브가 최종 이름으로 남으면 **완성된 백업처럼 보이는** 최악의 실패가 됩니다. tar는 심볼릭 링크를 따라가지 않고 링크로 저장하며, zip은 이식 가능한 표현이 없어 건너뜁니다.

### POST /api/v1/files/extract
`.tar` / `.tar.gz` / `.tgz` / `.zip`을 지정한 디렉터리에 풉니다.

- **인증 필요**: 예
- **Body**: `{ "path": "/opt/backups/app.tar.gz", "dest": "/opt/restore" }`

**보안상 세 겹의 방어가 있습니다.** 압축 해제는 이 모듈에서 가장 위험한 방향입니다.

1. **항목 경로 봉쇄** — `../../etc/cron.d/x` 같은 이름은 거부합니다. 구분자를 붙여 비교하므로 `dest-evil`이 `dest`의 자식으로 통과하지 못합니다. 절대 경로 항목명은 거부가 아니라 **목적지 안으로 가둡니다**(GNU tar이 선행 `/`를 떼는 것과 동일).
2. **링크 항목 거부** — 심볼릭·하드 링크를 만들지 않습니다. 만들면 `evil -> /etc/sfpanel` + `evil/config.yaml` 두 항목으로 봉쇄 검사를 통과한 채 밖에 쓸 수 있습니다. 디렉터리 체인도 한 단계씩 `lstat`하며 만들어, **이미 존재하던** 링크를 통과하지 않습니다. 리프는 `unlink` 후 `O_NOFOLLOW`로 엽니다.
3. **팽창 상한** — 전체 4GB, 항목 20 000개. zip bomb은 수 KB가 수 GB로 펼쳐집니다. 항목별이 아니라 **누적** 예산이라 쪼개서 우회할 수 없습니다.

장치 노드·FIFO·소켓은 건너뜁니다.

### GET /api/v1/files/trash
휴지통 목록(최신순).

- **인증 필요**: 예

**Response (200):** `{ "entries": [...], "retentionDays": 7 }`

각 항목은 `id`, `originalPath`, `name`, `deletedAt`, `size`, `isDir`. `originalPath`가 복원을 의미 있게 만드는 유일한 정보입니다.

### POST /api/v1/files/trash/restore
휴지통 항목을 원래 자리로 되돌립니다.

- **인증 필요**: 예
- **Body**: `{ "id": "<trash-id>", "to": "/optional/override" }`
- 원래 경로에 이미 무언가 있으면 **409 `DESTINATION_EXISTS`** — 더 새 파일을 덮어써서 옛 파일을 되살리는 건 요청한 것의 반대입니다.

`id`에 경로 구분자가 있으면 거부합니다. id는 휴지통 디렉터리 안의 한 파일만 가리켜야 합니다.

### DELETE /api/v1/files/trash
`?id=` 로 한 항목을, 생략하면 전체를 영구 삭제합니다.

- **인증 필요**: 예

---

## 네트워크 공유 API (`/api/v1/disks/network-shares`)

SMB/CIFS·NFS 공유를 fstab에 등록해 네트워크 드라이브로 붙입니다 (v0.63.0+). 진실의 원천은 `/etc/fstab` 하나이며, 패널이 만든 항목은 앞줄의 `# sfpanel-netshare` 마커로 구분합니다 — 손으로 쓴 항목은 읽되 절대 고치거나 지우지 않습니다. SMB 비밀번호는 fstab에 넣지 않고 0600 자격증명 파일에 따로 둡니다.

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/disks/network-shares` | 등록된 공유 목록(마운트 상태 포함) |
| POST | `/disks/network-shares` | 공유 등록 + 마운트. Body: `{ type: "smb"\|"nfs", source, mount_point, username?, password?, options? }` |
| POST | `/disks/network-shares/remove` | fstab 항목까지 제거 |
| POST | `/disks/network-shares/mount` | 등록된 공유 마운트 |
| POST | `/disks/network-shares/unmount` | 언마운트(등록은 유지) |
| POST | `/disks/network-shares/test` | 등록 전 접속 확인 |
| GET | `/disks/network-shares/discover` | 호스트가 내보내는 공유 검색 |
| GET | `/disks/network-shares/tools` | `cifs-utils` / `nfs-common` 설치 여부 |
| POST | `/disks/network-shares/tools/install` | 위 패키지 설치 |

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

### POST /api/v1/logs/custom-sources
커스텀 로그 소스 추가. `custom_log_sources` 테이블에 저장, source_id는 `custom-<slug>`.

- **인증 필요**: 예
- **Request Body**: `{ "name": "string", "path": "/absolute/path.log" }`
- **경로 제약**: 절대 경로 + 허용 루트(`/var/log/`·`/opt/`)의 세그먼트 경계 내, 심볼릭 링크 해석 재검증.

**Response (200):** `{ "id": <int>, "source": "custom-<slug>" }`

**에러:** `INVALID_PATH`(400) 허용 목록 밖 또는 비절대 경로.

---

### DELETE /api/v1/logs/custom-sources/:id
커스텀 로그 소스 삭제 (built-in 소스는 삭제 불가).

- **인증 필요**: 예
- **Path**: `id` — 커스텀 소스 행 ID

**Response (200):** `{ "message": "deleted" }`

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

**응답은 SSE(`text/event-stream`)** 스트림입니다 — `apt-get` 출력이 `data:` 라인으로 실시간 전달되고 완료 시 `[DONE]`. (이전 버전의 unary JSON 응답에서 변경됨.)

**사전 실패 (스트림 시작 전):**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_PACKAGE_NAME` | 400 | 패키지 이름에 허용되지 않는 문자 포함 |
| `CONFLICT` | 409 | dpkg 프런트엔드 락 점유 중 |

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

#### POST /api/v1/docker/containers
독립(standalone) 컨테이너 생성 — compose 파일이나 셸 없이 UI에서 단일 컨테이너 실행 (v0.23.0+). 이미지가 로컬에 없으면 먼저 풀합니다. Docker create API의 검증된 부분집합이며 full HostConfig 패스스루가 아닙니다.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "my-app",
  "image": "nginx:latest",
  "command": ["nginx", "-g", "daemon off;"],
  "env": ["KEY=VALUE"],
  "ports": [
    { "host_ip": "", "host_port": "8080", "container_port": "80", "protocol": "tcp" }
  ],
  "volumes": ["/host/path:/container/path:ro"],
  "restart_policy": "unless-stopped",
  "network": "bridge",
  "auto_start": true
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `image` | string | 예 | 이미지 참조 (검증됨; 없으면 풀) |
| `name` | string | 아니오 | 컨테이너 이름 (검증됨) |
| `command` | string[] | 아니오 | 명령 오버라이드 |
| `env` | string[] | 아니오 | `["KEY=VALUE"]` 형식 환경변수 |
| `ports` | array | 아니오 | `{host_ip, host_port, container_port, protocol}`. `host_port`는 비우면 랜덤, 지정 시 1–65535 |
| `volumes` | string[] | 아니오 | `["/host:/container[:ro]"]` 바인드 마운트 |
| `restart_policy` | string | 아니오 | `no`\|`always`\|`unless-stopped`\|`on-failure` (기본 `no`) |
| `network` | string | 아니오 | 네트워크 이름/모드 |
| `auto_start` | boolean | 아니오 | 생성 후 즉시 시작 여부 |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "id": "abc123...",
    "message": "container created"
  }
}
```

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_BODY` | 400 | 잘못된 요청 본문 |
| `INVALID_REQUEST` | 400 | image 누락 또는 잘못된 이미지 참조 |
| `INVALID_NAME` | 400 | 잘못된 컨테이너 이름 |
| `INVALID_VALUE` | 400 | 잘못된 restart policy 또는 호스트 포트 |
| `DOCKER_ERROR` | 500 | 생성/풀 실패 |

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

#### GET /api/v1/docker/containers/:id/metrics
컨테이너 CPU/메모리 **히스토리** 조회(관측성 탭). 백그라운드 수집기가 `container_metrics_history` 테이블에 적재한 시계열을 윈도우 단위로 반환. 관측성(observability)이 비활성이면 빈 데이터.

- **인증 필요**: 예
- **Path**: `id` — 컨테이너 ID 또는 이름
- **쿼리 파라미터**: `range` — `1h`(기본)/`6h`/`24h`

**Response (200):** `data`는 `{ ts, cpu_percent, mem_percent, mem_bytes }` 포인트 배열.

---

#### GET /api/v1/docker/containers/:id/events
컨테이너 **수명주기 이벤트** 타임라인(die/oom/restart/health 등). `container_events` 테이블에 적재된 이벤트를 최신순으로 반환.

- **인증 필요**: 예
- **Path**: `id` — 컨테이너 ID 또는 이름
- **쿼리 파라미터**: `limit`(기본 50), `before`(Unix ms 커서, 페이지네이션)

**Response (200):** `data`는 `{ ts, event_type, exit_code, detail }` 이벤트 배열.

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
- **쿼리 파라미터**: `force` — SSH(22)/패널 포트 허용 규칙이 없을 때 잠금(lockout)을 막기 위해 활성화를 거부합니다. `?force=true`로 오버라이드. (v0.19.x 잠금 가드)

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
        "port": 3628,
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

### GET /api/v1/firewall/docker
Docker가 발행한 포트(NAT `DOCKER` 체인)와 사용자가 추가한 `DOCKER-USER` 체인 규칙을 함께 조회. `iptables`가 없으면 빈 배열을 반환. NAT `DOCKER` 체인을 **1회만** 읽어 두 뷰를 모두 파생합니다 (v0.29.0 dedup). `?node=`로 원격 노드 조회 시 iptables가 없는 노드는 빈 결과.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "ports": [
      { "container_name": "web", "container_ip": "172.17.0.2", "host_port": 8080, "container_port": 80, "protocol": "tcp", "host_ip": "0.0.0.0" }
    ],
    "rules": [
      { "number": 1, "port": 8080, "protocol": "tcp", "source": "192.168.1.0/24", "action": "drop" }
    ]
  }
}
```

| 필드 | 설명 |
|------|------|
| `ports` | Docker가 발행한 published 포트 (`DockerPublishedPort`) |
| `rules` | `DOCKER-USER` 체인의 사용자 규칙 (`DockerUserRule`) |

---

### POST /api/v1/firewall/docker/rules
`DOCKER-USER` 체인에 규칙 추가 (컨테이너 published 포트에 대한 소스 기반 허용/차단).

- **인증 필요**: 예

**Request Body:**
```json
{
  "port": 8080,
  "protocol": "tcp",
  "source": "192.168.1.0/24",
  "action": "drop"
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `port` | number | 예 | 포트 (1–65535) |
| `protocol` | string | 아니오 | `tcp`(기본) \| `udp` |
| `source` | string | 아니오 | 소스 IP/CIDR |
| `action` | string | 아니오 | `drop`(기본) \| `accept` 등 |

**에러 응답:** `INVALID_PORT`(400), `INVALID_PROTOCOL`(400), `TOOL_NOT_INSTALLED`(503 iptables 없음).

---

### DELETE /api/v1/firewall/docker/rules/:number
`DOCKER-USER` 체인에서 줄 번호로 규칙 삭제.

- **인증 필요**: 예
- **Path**: `number` — `GET /firewall/docker`의 `rules[].number`

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
앱 상세 정보 + Compose YAML + README + 포트 상태 조회.

- **인증 필요**: 예
- **응답 추가 필드**: `compose`(docker-compose.yml), `readme`(브랜치 main/master/develop 자동 탐색), `readme_base_url`(README 상대 링크 기준), `port_status[]`(`{port, in_use, suggested}` — 선언 포트 및 env `type:"port"` 기본값의 사용 여부와 대체 포트 제안).

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

**응답은 JSON이 아니라 SSE(`text/event-stream`) 스트림입니다.** 각 `data:` 라인은 `{stage, message, done, success}` (stage: `prepare`/`fetch` → `pull` → `start` → `done`).

**Request Body (심플 모드):** `{ "env": { "PORT": "3001", "PASSWORD": "my-secret" } }`

**Request Body (고급 모드):** `{ "advanced": true, "compose": "<yaml>", "env_raw": "<.env>", "password": "<재인증>" }` — 비밀번호 bcrypt 재확인 + `privileged`/`pid:host`/hostfs/docker.sock 차단 검증. 요청 바디 1MB 캡.

**스트림 종료/에러** (스트림 시작 전 사전 검사):
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_ID` | 400 | 잘못된 앱 ID |
| `NOT_FOUND` | 404 | 존재하지 않는 앱 |
| `PORT_CONFLICT` | 409 | 선언 포트가 이미 사용 중 |
| `CONTAINER_CONFLICT` | 409 | 컨테이너 이름 충돌 |
| `APPSTORE_ERROR` | 500 | 고급 검증 실패 등 |
(스트림 시작 후 실패는 `{stage:"...",success:false}` 이벤트로 전달)

---

### GET /api/v1/appstore/installed
설치된 앱 목록 조회.

- **인증 필요**: 예

**Response (200):** `data`는 평탄(flat)한 설치 기록 배열 (`settings.appstore_installed_<id>` 기반):
```json
{
  "success": true,
  "data": [
    {
      "id": "uptime-kuma",
      "version": "1",
      "installed_at": "2026-03-10T12:00:00Z",
      "name": "Uptime Kuma",
      "description": "...",
      "icon": "..."
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

### GET /api/v1/system/backup/schedule
예약 로컬 백업 설정 + 디스크에 저장된 아카이브 목록 조회 (v0.26.0+). 백그라운드 러너가 10분마다 점검하고 due 시 타임스탬프 `tar.gz`(DB + config + compose 파일)를 DB 옆 `backups/` 디렉토리에 기록, 보관 한도(retention)로 prune.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "schedule": {
      "enabled": true,
      "interval_hours": 24,
      "retention": 7,
      "last_run": "2026-06-03T02:00:00Z",
      "last_status": "success",
      "last_error": ""
    },
    "files": [
      { "name": "sfpanel-backup-20260603-020000.tar.gz", "size": 1048576, "mod_time": "2026-06-03T02:00:00Z" }
    ]
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `schedule.enabled` | boolean | 예약 백업 활성화 여부 |
| `schedule.interval_hours` | number | 백업 주기 (시간) |
| `schedule.retention` | number | 보관할 아카이브 수 |
| `schedule.last_run` | string\|null | 마지막 실행 시각 |
| `schedule.last_status` | string | 마지막 실행 상태 |
| `schedule.last_error` | string | 마지막 실행 오류 (있으면) |
| `files[]` | array | `{name, size, mod_time}` 아카이브 목록 |

---

### PUT /api/v1/system/backup/schedule
예약 백업 설정 변경.

- **인증 필요**: 예

**Request Body:**
```json
{
  "enabled": true,
  "interval_hours": 24,
  "retention": 7
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `enabled` | boolean | 예 | 예약 백업 활성화 여부 |
| `interval_hours` | number | 예 | 주기 (1–168) |
| `retention` | number | 예 | 보관 수 (1–100) |

**Response (200):** `{ "message": "backup schedule updated" }`

**에러 응답:** `INVALID_BODY`(400), `INVALID_VALUE`(400 범위 초과).

---

### POST /api/v1/system/backup/schedule/run
즉시 백업 실행 (설정된 retention 적용).

- **인증 필요**: 예
- **Request Body:** 없음 (빈 POST)

**Response (200):** `{ "message": "backup created", "name": "sfpanel-backup-...tar.gz" }`

---

### GET /api/v1/system/backup/files/download
저장된 아카이브를 이름으로 다운로드 (`Content-Disposition: attachment`).

- **인증 필요**: 예
- **쿼리 파라미터**: `name` — 아카이브 파일명 (traversal 방지 패턴 검증)

**에러 응답:** `INVALID_NAME`(400), `NOT_FOUND`(404).

---

### DELETE /api/v1/system/backup/files
저장된 아카이브를 이름으로 삭제.

- **인증 필요**: 예
- **쿼리 파라미터**: `name` — 아카이브 파일명 (패턴 검증)

**Response (200):** `{ "message": "backup deleted", "name": "..." }`

**에러 응답:** `INVALID_NAME`(400), `NOT_FOUND`(404).

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
| `protected` | bool | 보호 행(tombstone 등) 여부 — wipe에서 제외 |
| `created_at` | string | 기록 일시 (ISO 8601) |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `DB_ERROR` | 500 | 데이터베이스 조회 실패 |

---

### DELETE /api/v1/audit/logs
감사 로그 삭제 (범위 지정 + tombstone 기록).

- **인증 필요**: 예
- **쿼리 파라미터** (택일, 둘 다 지정 시 400 `INVALID_VALUE`): `days=N`(N일 이전 삭제) | `before=ISO8601|YYYY-MM-DD`(해당 시점 이전 삭제). 미지정 시 보호되지 않은 전체 삭제.
- **동작**: 삭제 전 `protected=1` tombstone 행을 먼저 INSERT(수행자/IP/노드/삭제 건수)한 뒤 `DELETE … WHERE protected=0 [+ 날짜필터]`를 같은 트랜잭션으로 실행 — tombstone·보호 행은 면역.

**Response (200):**
```json
{
  "success": true,
  "data": {
    "message": "Audit logs cleared",
    "deleted": 0,
    "tombstone_id": 0
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

### POST /api/v1/docker/compose/:project/migrate/preflight
스택을 다른 노드로 이관하기 전 사전 점검(dry-run). 실제 이관은 하지 않고 차단 사유(`blocks`)와 경고(`warnings`)만 반환합니다. 클러스터가 활성화되어 있어야 합니다 (v0.43.0+).

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 소스 스택(프로젝트) 이름 |

**Request Body:**
```json
{
  "targetNodeId": "node-b",
  "overwriteAcked": false
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `targetNodeId` | string | 예 | 이관 대상 노드 ID |
| `overwriteAcked` | boolean | 아니오 | 대상에 동일 id 스택이 존재할 때 덮어쓰기를 명시적으로 승인 (기본 `false`) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "blocks": [
      { "code": "arch-mismatch", "message": "source (arm64) and target (amd64) architectures differ" }
    ],
    "warnings": [
      { "code": "system-bind", "message": "stack uses host/system bind mounts (e.g. docker.sock); these are not copied and must exist on the target" }
    ]
  }
}
```

**차단 코드 (`blocks` — 이관 거부):**
| 코드 | 조건 |
|------|------|
| `same-node` | 소스와 대상이 같은 노드 |
| `arch-mismatch` | 소스/대상 CPU 아키텍처 불일치 |
| `insufficient-disk` | 대상 여유 공간이 추정 이관 크기보다 작음 |
| `port-conflict` | 대상이 스택의 호스트 포트를 이미 사용 중 (disposition이 `clone`이 아닐 때) |
| `stack-exists` | 대상에 동일 id 스택이 이미 존재 (`overwriteAcked`로 승인 필요) |

**경고 코드 (`warnings` — 승인 후 진행 가능):**
| 코드 | 조건 |
|------|------|
| `port-conflict` | `clone` 시 대상이 일부 호스트 포트를 사용 중 — 클론 시작 전 리맵 필요 |
| `system-bind` | 호스트/시스템 바인드 마운트(예: docker.sock)는 복사되지 않으며 대상에 존재해야 함 |
| `external-volume` | external 볼륨 데이터는 복사되지 않음 |
| `device-required` | 디바이스/GPU 요청 — 대상에 동등 하드웨어 필요 |
| `target-unreachable` | 대상 노드 조회 실패 — 디스크/포트/아키텍처 점검이 생략됨 |

**에러 응답:**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_NAME` | 400 | 유효하지 않은 프로젝트 id |
| `MISSING_FIELDS` | 400 | targetNodeId 누락 |
| `INTERNAL_ERROR` | 400 | 클러스터 미활성 |
| `COMPOSE_ERROR` | 400 | compose 설정 해석 실패 |

---

### POST /api/v1/docker/compose/:project/migrate
스택을 `targetNodeId` 노드로 **콜드 이관**(SSE 스트리밍). 소스를 정지시켜 일관된 스냅샷을 만든 뒤 번들로 패키징해 대상에 전송하고, 대상이 정상(healthy)으로 확인되면 disposition을 적용합니다. finalize 이전 단계에서 실패하면 소스는 다시 실행 상태로 복구됩니다. 클러스터가 활성화되어 있어야 합니다 (v0.43.0+).

- **인증 필요**: 예
- **Docker 사용 가능 시에만 등록**
- **응답 형식**: `text/event-stream` (표준 JSON 응답이 아님)

**Path Parameters:**
| 파라미터 | 설명 |
|----------|------|
| `project` | 소스 스택(프로젝트) 이름 |

**Request Body:**
```json
{
  "targetNodeId": "node-b",
  "disposition": "retain",
  "overwriteAcked": false,
  "rateLimitMbps": 0
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `targetNodeId` | string | 예 | 이관 대상 노드 ID |
| `disposition` | string | 예 | 이관 성공 후 소스 처리: `retain` \| `delete` \| `clone` |
| `overwriteAcked` | boolean | 아니오 | 대상 동일 id 스택 덮어쓰기 승인 (기본 `false`) |
| `rateLimitMbps` | number | 아니오 | 전송 대역폭 상한(MB/s). 0 또는 미지정 = 무제한. (v0.51.0) |

**disposition 의미:**
| 값 | 동작 |
|----|------|
| `retain` | 소스는 정지 상태로 남김 (파일/볼륨 보존) |
| `delete` | 대상이 정상 확인된 **후** 소스 제거 |
| `clone` | 소스를 다시 실행 (소스·대상 양쪽 모두 실행) |

**Response:** SSE 스트림 (각 이벤트는 JSON)
```
data: {"phase":"preflight","message":"Running pre-flight checks...","done":false}

data: {"phase":"quiesce","message":"Stopping source stack...","done":false}

data: {"phase":"package","message":"Packaging stack...","done":false}

data: {"phase":"transfer","message":"Transferring to target...","done":false}

data: {"phase":"finalize","message":"Applying source disposition (retain)...","done":false}

data: {"phase":"done","message":"Migration complete.","done":true}
```

이벤트 필드는 `{phase, message, done}`. 정상 진행 순서: `preflight` → `quiesce` → `package` → `transfer` → `finalize` → 종료 마커 `done`. 실패 시: 패키징/전송 단계 실패는 소스를 복구한 뒤 `rollback` 이벤트(`done:true`)를, 사전 점검 차단이나 그 외 실패는 `error` 이벤트(`done:true`)를 전송합니다.

> `transfer` 단계는 진행률 메시지를 주기적으로 전송합니다 — 예: `{"phase":"transfer","message":"Transferring to target... 512 / 2048 MiB (25%)","done":false}`

**사전 실패 (스트림 시작 전):**
| 코드 | HTTP 상태 | 조건 |
|------|-----------|------|
| `INVALID_NAME` | 400 | 유효하지 않은 프로젝트 id |
| `INVALID_BODY` | 400 | 잘못된 요청 본문 |
| `INTERNAL_ERROR` | 400 | 클러스터 미활성 |
| `MISSING_FIELDS` | 400 | targetNodeId 누락 또는 유효하지 않은 disposition |

---

### GET /api/v1/docker/compose/migrate/target-info
**클러스터 내부 전용** — 이관 사전 점검 시 소스 노드가 대상 노드의 사실(arch, 여유 공간, 사용 중 포트, 스택 존재 여부)을 조회하기 위해 노드 간(internal-proxy 인증)으로만 호출됩니다. 일반 클라이언트가 직접 호출하면 거부됩니다 (v0.43.0+).

- **인증**: 내부 프록시 전용 (`X-SFPanel-Internal-Proxy`). 외부 호출 시 403 `PERMISSION_DENIED`.
- **Docker 사용 가능 시에만 등록**

**Query Parameters:**
| 파라미터 | 타입 | 필수 | 설명 |
|----------|------|------|------|
| `stackId` | string | 아니오 | 대상에 동일 id 스택 존재 여부를 확인할 스택 id |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "arch": "amd64",
    "freeBytes": 0,
    "dockerFreeBytes": 0,
    "sameDevice": true,
    "portsInUse": [8096],
    "stackExists": false,
    "stackRunning": false
  }
}
```

- `dockerFreeBytes`: docker 스토리지 FS의 여유 바이트.
- `sameDevice`: stacks FS와 docker FS가 동일 디바이스인지 여부.
- `stackRunning`: 대상에 동일 id 스택이 실행 중인지 여부.

---

### POST /api/v1/docker/compose/migrate-import
**클러스터 내부 전용** — 소스 노드가 이관 번들(tar 스트림)을 대상 노드로 푸시하는 바이너리 릴레이 엔드포인트. 운영자가 직접 호출하는 용도가 아닙니다. `X-SFPanel-Migration-Sha256` 요청 헤더의 체크섬으로 번들을 검증한 뒤, compose 안전성 검증(원문 + .env 주입 후 resolved 재검증) → 정의 복원 → 이미지 `docker load` + 명명 볼륨/바인드 데이터 복원 → `up` → 헬스체크를 수행하며, 실패 시 부분 복원분을 정리합니다. 덮어쓰기 시 기존 정의(`.migbak`)와 기존 볼륨 데이터를 백업해 두고 실패하면 원복합니다. 동일 스택 동시 import 또는 공유 볼륨 동시 복원은 409로 거부합니다 (v0.43.0+, v0.50.0/v0.51.0).

- **인증**: 내부 프록시 전용 (`X-SFPanel-Internal-Proxy`). 외부 호출 시 403 `PERMISSION_DENIED`.
- **Docker 사용 가능 시에만 등록**
- **요청 헤더**: `X-SFPanel-Migration-Sha256` (번들 SHA-256 체크섬, 필수)
- **요청 바디**: 이관 번들 tar 스트림 (바이너리)

**Response (200):**
```json
{
  "success": true,
  "data": {
    "status": "ok",
    "stackId": "jellyfin"
  }
}
```

---

## 클러스터 — 추가 API

### GET /api/v1/cluster/overview
클러스터 개요 — 노드 목록 + 집계 메트릭 (Raft FSM 기반). 클러스터 미구성 시 빈 개요(`node_count:0`, 빈 배열)를 반환. 리더만 최신 상태를 보장하므로 팔로워는 503(또는 `stale` 플래그)으로 응답할 수 있습니다.

- **인증 필요**: 예

**Response (200):** `data`에 `{ name, node_count, leader_id, nodes[], metrics[] }`. (실시간 갱신은 `/ws/cluster/overview` WebSocket 사용 — v0.31.0+.)

---

### GET /api/v1/cluster/tokens
대기 중(pending)인 참가 토큰 목록 조회 (v0.19.0+). 값은 **마스킹**되어 반환되며 전체 토큰은 노출되지 않으므로 오발급된 초대를 사용 전에 무효화할 수 있습니다. 토큰은 리더 디스크에 존재하므로 팔로워에서 호출 시 리더로 프록시됩니다.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "tokens": [
      { "id": "string", "masked": "join-****…", "expires_at": "2026-06-04T00:00:00Z", "created_by": "admin" }
    ]
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `id` | string | 토큰 식별자 (revoke에 사용) |
| `masked` | string | 마스킹된 토큰 값 (전체 비노출) |
| `expires_at` | string | 만료 시각 |
| `created_by` | string | 발급한 사용자 |

---

### DELETE /api/v1/cluster/tokens/:id
대기 중인 참가 토큰 폐기 (v0.19.0+). 리더 전용 — 팔로워는 자동 프록시.

- **인증 필요**: 예
- **Path**: `id` — `GET /cluster/tokens`의 `tokens[].id`

**Response (200):** `{ "revoked": "<id>" }`

---

### POST /api/v1/cluster/init
새 클러스터 초기화. CA 인증서를 생성하고 Raft를 부트스트랩합니다. 이미 클러스터에 참여 중이면 실패합니다.

- **인증 필요**: 예

**Request Body:**
```json
{
  "name": "sfpanel",
  "advertise_address": "192.168.1.10",
  "grpc_port": 3629,
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
  "leader_address": "192.168.1.5:3629",
  "token": "join-token-string",
  "advertise_address": "192.168.1.10",
  "grpc_port": 3629,
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
클러스터에서 자발적으로 탈퇴. 리더에게 탈퇴를 통보한 후 로컬 클러스터 데이터를 정리하고 서비스를 재시작합니다. (로컬 노드 row에서 탈퇴 UI는 v0.19.0+.)

- **인증 필요**: 예
- **쿼리 파라미터**: `force` — 탈퇴 후 잔여 voter가 quorum을 형성할 수 없으면(live heartbeat 기준) 탈퇴를 거부합니다. `?force=true`로 긴급 drain 시 오버라이드.

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
| `CLUSTER_QUORUM` | 409 | 탈퇴 시 quorum 상실 (`?force=true`로 오버라이드) |
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
  "api_address": "192.168.1.10:3628",
  "grpc_address": "192.168.1.10:3629"
}
```

> `api_address`는 스킴 없는 `호스트:포트` 형태로 저장합니다. `https://` 접두사를 넣어도
> **무시하고 제거**합니다 — 피어를 HTTP로 부를지 HTTPS로 부를지는 해당 노드의 패널 CA가
> 복제 상태에 있는지로 판단합니다. (예전에는 접두사를 존중하도록 되어 있었으나, 부팅 10초 뒤
> `verifySelfAddress`가 스킴 없는 형태로 덮어쓰기 때문에 실제로는 동작한 적이 없습니다.)

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
    "api_address": "https://192.168.1.10:3628",
    "grpc_address": "192.168.1.10:3629"
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

### POST /api/v1/cluster/panel-ca
노드의 **패널 CA 인증서**(공개분)를 Raft FSM 복제 상태에 기록합니다. 리더에서만 적용되며,
팔로워는 자신을 대신해 적용해 달라고 리더에게 이 엔드포인트를 호출합니다.

- **인증 필요**: 예 (클러스터 내부 프록시 경로)

**Request Body:**
```json
{
  "node_id": "<node-uuid>",
  "ca_cert": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n"
}
```

**왜 이런 모양인가**: FSM 쓰기는 리더만 가능한데 패널 CA는 노드마다 다릅니다. 리더로 선출된 적 없는
노드는 자기 인증서를 영영 게시할 수 없으므로, 주소 수정(`PATCH .../address`)과 같은 위임 구조를 씁니다.

**검증**: `ca_cert`를 파싱해 **CA 인증서가 아니면 거부**합니다 (리프 인증서·개인키·비-PEM 모두 400).
여기에 들어온 값은 다른 노드들의 신뢰 앵커가 되므로, 형식 검증이 유일한 방어선입니다.

**Response (200):** `{"node_id": "<node-uuid>"}`
**Errors**: 리더가 아니면 503, 알 수 없는 노드면 404, 인증서가 부적합하면 400

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

## WireGuard — 피어 관리 API (v0.28.0+)

UI에서 raw 설정을 직접 편집하지 않고 피어를 관리. 설정 파일(`.conf`)이 진실의 원천(reboot 후에도 유지)이고, 인터페이스가 up이면 `wg set`으로 즉시 라이브 반영(best-effort)됩니다. 클라이언트 설정과 QR 코드는 **브라우저에서** 조립/렌더링되며 서버는 클라이언트 private key를 저장하지 않습니다.

### POST /api/v1/network/wireguard/keypair
WireGuard 키페어 생성 (`wg genkey` + `wg pubkey`).

- **인증 필요**: 예
- **Request Body:** 없음 (빈 POST)

**Response (200):** `{ "private_key": "...", "public_key": "..." }`

---

### POST /api/v1/network/wireguard/configs/:name/peers
인터페이스 설정에 `[Peer]` 블록을 추가하고, up 상태면 라이브 반영.

- **인증 필요**: 예
- **Path**: `name` — 인터페이스/설정 이름

**Request Body:**
```json
{
  "public_key": "string",
  "preshared_key": "string",
  "allowed_ips": ["10.0.0.2/32"],
  "endpoint": "host:port",
  "persistent_keepalive": 25
}
```

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `public_key` | string | 예 | 피어 공개키 (base64, 서버 검증) |
| `preshared_key` | string | 아니오 | PSK (검증됨; 라이브 PSK 적용은 생략하고 설정 파일에 의존) |
| `allowed_ips` | string[] | 예 | 1개 이상의 CIDR (예: `10.0.0.2/32`) |
| `endpoint` | string | 아니오 | `host:port` |
| `persistent_keepalive` | number | 아니오 | 0–65535 |

**Response (200):** `{ "message": "peer added", "public_key": "..." }`

**에러 응답:** `INVALID_NAME`(400), `INVALID_VALUE`(400 키/CIDR/엔드포인트), `NOT_FOUND`(404 설정 없음), `CONFLICT`(409 동일 public_key 중복).

---

### DELETE /api/v1/network/wireguard/configs/:name/peers
공개키로 `[Peer]` 블록 삭제 후, up 상태면 라이브 제거. 키는 **쿼리 파라미터**로 전달합니다 (WireGuard 키는 base64라 `/`·`+`를 포함해 path 라우팅을 깨뜨림).

- **인증 필요**: 예
- **Path**: `name` — 인터페이스/설정 이름
- **쿼리 파라미터**: `public_key` — 삭제할 피어 공개키 (필수)

**Response (200):** `{ "message": "peer removed", "public_key": "..." }`

**에러 응답:** `INVALID_NAME`(400), `INVALID_VALUE`(400 잘못된 키), `NOT_FOUND`(404 설정/피어 없음).

---

### POST /api/v1/network/wireguard/configs/:name/autostart
부팅 시 자동 시작(`wg-quick@<name>` systemd enable/disable) 토글.

- **인증 필요**: 예
- **Path**: `name` — 인터페이스/설정 이름

**Request Body:** `{ "enabled": true }`

**Response (200):** `{ "message": "autostart updated", "enabled": true }`

---

## 터미널 — 세션 목록 API

### GET /api/v1/terminal/sessions
현재 사용자가 소유한 영속 PTY 세션 목록 (v0.19.0+). 서버는 연결이 끊겨도 세션과 스크롤백을 유지하므로, 이 목록으로 기존 세션에 재접속(reattach)할 수 있습니다. `/ws/terminal`에 `?session_id=<id>`로 재접속하면 스크롤백이 재생됩니다.

- **인증 필요**: 예

**Response (200):**
```json
{
  "success": true,
  "data": {
    "sessions": [
      { "session_id": "string", "last_use": "2026-06-03T10:00:00Z", "attached": false, "reader_count": 0 }
    ]
  }
}
```

| 필드 | 타입 | 설명 |
|------|------|------|
| `session_id` | string | 세션 식별자 (재접속에 사용) |
| `last_use` | string | 마지막 사용 시각 |
| `attached` | boolean | 현재 연결된 리더가 있는지 |
| `reader_count` | number | 연결된 리더 수 |

---

## 알림 — Webhook 채널 (v0.25.0+)

`POST/PUT /api/v1/alerts/channels`의 `type`에 기존 `discord`/`telegram`에 더해 **`webhook`**(Slack/Mattermost 호환)이 추가되었습니다. `config`는 `{"webhook_url":"https://…"}` 형식이며, 임의의 http(s) 대상이 허용됩니다(홈랩 수신기 대응). webhook 채널은 JSON 본문에 Slack 호환 `text` 필드 + 구조화 필드(`title`, `message`, `severity`, `source:"SFPanel"`, `timestamp`)를 POST합니다. 채널 라우트 자체는 기존과 동일(아래 요약 표 참조)하며 `type` 검증과 페이로드만 확장되었습니다.

---

## WebSocket API

모든 WebSocket 엔드포인트는 쿼리 파라미터 `?token=<JWT>`로 인증합니다.

### WS /ws/metrics
시스템 메트릭 실시간 스트리밍 (2초 간격).

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:3628/ws/metrics?token=<JWT>`

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
- **URL 예시**: `ws://host:3628/ws/docker/containers/abc123/logs?token=<JWT>`
- **Docker 사용 가능 시에만 등록**

**서버 -> 클라이언트 메시지:** 텍스트 메시지 (각 줄이 개별 메시지, 개행 포함)

---

### WS /ws/docker/containers/:id/exec
컨테이너 내부 인터랙티브 쉘 (`/bin/sh`).

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:3628/ws/docker/containers/abc123/exec?token=<JWT>`
- **Docker 사용 가능 시에만 등록**

**클라이언트 -> 서버:**
- 일반 텍스트: 쉘 stdin으로 전달
- JSON 리사이즈: `{"type": "resize", "cols": 80, "rows": 24}`

**서버 -> 클라이언트:** 텍스트 메시지 (쉘 stdout/stderr)

---

### WS /ws/logs
시스템 로그 실시간 스트리밍 (`tail -f`).

- **인증**: 쿼리 파라미터 `token`
- **URL 예시**: `ws://host:3628/ws/logs?token=<JWT>&source=syslog`

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
- **URL 예시**: `ws://host:3628/ws/terminal?token=<JWT>&session_id=default`

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

`internal/api/router.go` 기준 등록 라우트 총 279개 (REST/SSE + WebSocket 7개). 이 외에 SSE 스트리밍 엔드포인트는 REST 숫자에 포함됩니다. Docker 소켓 미사용 시 `/api/v1/docker/*` 라우트는 미등록. 실제 등록 라우트는 서버 시작 로그 또는 `internal/api/router.go`에서 확인.

### 인증/설정 (15개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/health` | X | 헬스체크 |
| POST | `/api/v1/auth/login` | X | 로그인 (refresh + CSRF 쿠키 발급, v0.13.3+; recovery_code 로그인 v0.34.0+) |
| GET | `/api/v1/auth/setup-status` | X | 셋업 필요 여부 |
| POST | `/api/v1/auth/setup` | X | 초기 관리자 생성 |
| POST | `/api/v1/auth/refresh` | X | 액세스 토큰 갱신 (쿠키 우선, body fallback) |
| POST | `/api/v1/auth/logout` | O | 로그아웃 — 쿠키 만료 + DB row 삭제 (v0.13.3+) |
| GET | `/api/v1/auth/2fa/status` | O | 2FA 상태 확인 |
| POST | `/api/v1/auth/2fa/setup` | O | 2FA 시크릿 생성 |
| POST | `/api/v1/auth/2fa/verify` | O | 2FA 활성화 |
| DELETE | `/api/v1/auth/2fa` | O | 2FA 비활성화 (비밀번호 + 현재 TOTP, v0.38.0) |
| POST | `/api/v1/auth/2fa/recovery` | O | 2FA 복구 코드 생성 (v0.34.0+) |
| GET | `/api/v1/auth/2fa/recovery/status` | O | 복구 코드 존재/잔여 수 조회 (v0.34.0+) |
| POST | `/api/v1/auth/change-password` | O | 비밀번호 변경 |
| POST | `/api/v1/auth/ws-ticket` | O | WebSocket 단발성 ticket 발급 (v0.13.2+) |
| GET | `/api/v1/settings` | O | 설정 조회 |
| PUT | `/api/v1/settings` | O | 설정 업데이트 |

**v0.13.3 보안 흐름**:
- 로그인 성공 시 `sfpanel_refresh`(HttpOnly + Secure + SameSite=Strict, Path=`/api/v1/auth`)와 `sfpanel_csrf`(JS-readable) 쿠키가 함께 발급됩니다.
- 모든 POST/PUT/PATCH/DELETE 요청에 `X-CSRF-Token: <sfpanel_csrf 값>` 헤더 필수 (`CSRFProtect` 미들웨어가 검증). 로그인/Setup/Refresh 경로 및 mTLS 클러스터 프록시는 자동 면제.
- WebSocket 인증은 `/auth/ws-ticket`에서 60초짜리 ticket을 받아 `?ticket=` 쿼리로 사용. 레거시 `?token=` JWT 경로는 한 릴리스(v0.14.0까지) 호환성 유지.

### 시스템 (29개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/system/info` | O | 시스템 정보 + 메트릭 + 버전 |
| GET | `/api/v1/system/metrics-history` | O | 24시간 메트릭 히스토리 |
| GET | `/api/v1/system/overview` | O | 대시보드 통합 엔드포인트 |
| GET | `/api/v1/system/portmap` | O | 포트 맵 (UFW+Docker DNAT+프로세스 통합) |
| GET | `/api/v1/system/update-check` | O | 업데이트 확인 |
| POST | `/api/v1/system/update` | O | 업데이트 실행 (SSE) |
| POST | `/api/v1/system/backup` | O | 시스템 백업 다운로드 |
| POST | `/api/v1/system/restore` | O | 시스템 백업 복원 |
| GET | `/api/v1/system/backup/schedule` | O | 예약 백업 설정 + 아카이브 목록 (v0.26.0+) |
| PUT | `/api/v1/system/backup/schedule` | O | 예약 백업 설정 변경 (v0.26.0+) |
| POST | `/api/v1/system/backup/schedule/run` | O | 즉시 백업 실행 (v0.26.0+) |
| GET | `/api/v1/system/backup/files/download` | O | 백업 아카이브 다운로드 (v0.26.0+) |
| DELETE | `/api/v1/system/backup/files` | O | 백업 아카이브 삭제 (v0.26.0+) |
| GET | `/api/v1/system/tuning` | O | 시스템 튜닝 상태 조회 |
| POST | `/api/v1/system/tuning/apply` | O | 시스템 튜닝 적용 |
| POST | `/api/v1/system/tuning/confirm` | O | 시스템 튜닝 확인 |
| POST | `/api/v1/system/tuning/reset` | O | 시스템 튜닝 초기화 |
| GET | `/api/v1/system/processes` | O | 상위 10 프로세스 |
| GET | `/api/v1/system/processes/list` | O | 전체 프로세스 목록 |
| POST | `/api/v1/system/processes/:pid/kill` | O | 프로세스 시그널 전송 (STOP/CONT 포함, v0.20.0+) |
| POST | `/api/v1/system/processes/:pid/renice` | O | 프로세스 nice 값 변경 (v0.20.0+) |
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

### 파일 관리 (18개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/files` | O | 디렉토리 목록 |
| GET | `/api/v1/files/read` | O | 파일 읽기 |
| POST | `/api/v1/files/write` | O | 파일 쓰기 |
| POST | `/api/v1/files/mkdir` | O | 디렉토리 생성 |
| DELETE | `/api/v1/files` | O | 파일/디렉토리 삭제 (휴지통 경유) |
| POST | `/api/v1/files/rename` | O | 이름 변경/이동 |
| POST | `/api/v1/files/copy` | O | 파일/디렉토리 트리 복사 (v0.21.0+) |
| GET | `/api/v1/files/search` | O | 재귀 이름 검색 (v0.21.0+) |
| GET | `/api/v1/files/download` | O | 파일 다운로드 |
| POST | `/api/v1/files/upload` | O | 파일 업로드 |
| POST | `/api/v1/files/chmod` | O | 권한 변경 |
| POST | `/api/v1/files/chown` | O | 소유자 변경 |
| POST | `/api/v1/files/archive` | O | 압축 생성 |
| POST | `/api/v1/files/extract` | O | 압축 해제 |
| GET | `/api/v1/files/thumbnail` | O | 이미지 썸네일 (v0.66.0+) |
| GET | `/api/v1/files/trash` | O | 휴지통 목록 |
| POST | `/api/v1/files/trash/restore` | O | 휴지통 복원 |
| DELETE | `/api/v1/files/trash` | O | 휴지통 비우기 |

### 그 밖의 라우트

문서화가 밀려 있던 것들. 각 절의 상세 설명은 아직 없고, 형태만 기록합니다.

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/api/v1/appstore/status` | 앱스토어 카탈로그 동기화 상태 |
| POST | `/api/v1/cron/:id/run` | cron 작업 즉시 실행 |
| GET | `/api/v1/cron/logs` | cron 실행 로그 |
| GET | `/api/v1/docker/events/recent` | 최근 컨테이너 이벤트(observability). 꺼져 있으면 빈 배열 |
| GET | `/api/v1/system/services/:name/cat` | systemd 유닛 파일 원문 |
| GET | `/api/v1/terminal/info` | 터미널 세션이 쓸 셸·홈·사용자 정보 |
| POST | `/api/v1/network/apply/confirm` | netplan 적용 확정(60초 자동 되돌리기 취소) (v0.69.0+) |
| GET | `/api/v1/network/apply/status` | 확정 대기 중인 적용이 있는지 (v0.69.0+) |
| POST | `/api/v1/system/restore/file` | 서버에 이미 있는 백업 아카이브로 복원. Body: `{ name }` (v0.70.0+) |

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

### WireGuard VPN (14개)

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
| POST | `/api/v1/network/wireguard/keypair` | O | 키페어 생성 (v0.28.0+) |
| POST | `/api/v1/network/wireguard/configs/:name/peers` | O | 피어 추가 (v0.28.0+) |
| DELETE | `/api/v1/network/wireguard/configs/:name/peers` | O | 피어 삭제 (`?public_key=`, v0.28.0+) |
| POST | `/api/v1/network/wireguard/configs/:name/autostart` | O | 부팅 자동 시작 토글 (v0.28.0+) |

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

### 디스크 (10개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/disks/overview` | O | 디스크 목록 |
| GET | `/api/v1/disks/iostat` | O | I/O 통계 |
| POST | `/api/v1/disks/usage` | O | 디스크 사용량 |
| GET | `/api/v1/disks/smartmontools-status` | O | smartmontools 설치 상태 |
| POST | `/api/v1/disks/install-smartmontools` | O | smartmontools 설치 |
| GET | `/api/v1/disks/:device/smart` | O | SMART 정보 (self-test 로그 포함, v0.27.0+) |
| POST | `/api/v1/disks/:device/smart/test` | O | SMART self-test 트리거 (`{type:short\|long}`, v0.27.0+) |
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

### Docker - 컨테이너 (11개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/docker/containers` | O | 컨테이너 목록 |
| POST | `/api/v1/docker/containers` | O | 독립 컨테이너 생성 (v0.23.0+) |
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
| PUT | `/api/v1/docker/compose/:project/healthcheck/:service` | O | 서비스 헬스체크 추가/수정 |
| DELETE | `/api/v1/docker/compose/:project/healthcheck/:service` | O | 서비스 헬스체크 제거 |
| POST | `/api/v1/docker/compose/:project/healthcheck/:service/test` | O | 서비스 헬스체크 테스트 실행 |

### 앱스토어 (6개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/appstore/categories` | O | 앱스토어 카테고리 목록 |
| GET | `/api/v1/appstore/apps` | O | 앱 목록 (카테고리 필터) |
| GET | `/api/v1/appstore/apps/:id` | O | 앱 상세 정보 + Compose YAML |
| POST | `/api/v1/appstore/apps/:id/install` | O | 앱 설치 |
| GET | `/api/v1/appstore/installed` | O | 설치된 앱 목록 |
| POST | `/api/v1/appstore/refresh` | O | 앱스토어 캐시 갱신 |

### 클러스터 (17개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/cluster/status` | O | 클러스터 상태 (활성화 여부, 노드 수, 리더) |
| GET | `/api/v1/cluster/overview` | O | 클러스터 개요 (노드 목록 + 집계 메트릭) |
| GET | `/api/v1/cluster/nodes` | O | 노드 목록 (상태, 역할, 라벨) |
| POST | `/api/v1/cluster/token` | O | 참가 토큰 생성 (TTL 지정 가능) |
| GET | `/api/v1/cluster/tokens` | O | 대기 중 참가 토큰 목록 (마스킹, v0.19.0+) |
| DELETE | `/api/v1/cluster/tokens/:id` | O | 참가 토큰 폐기 (v0.19.0+) |
| DELETE | `/api/v1/cluster/nodes/:id` | O | 노드 제거 (리더만 가능, `?force=`) |
| PATCH | `/api/v1/cluster/nodes/:id/labels` | O | 노드 라벨 수정 |
| PATCH | `/api/v1/cluster/nodes/:id/address` | O | 노드 주소 수정 |
| POST | `/api/v1/cluster/panel-ca` | O | 노드의 패널 CA를 복제 상태에 등록 (리더 전용, 팔로워가 대신 요청) |
| GET | `/api/v1/cluster/events` | O | 클러스터 이벤트 로그 |
| POST | `/api/v1/cluster/leader-transfer` | O | 리더십 이전 |
| POST | `/api/v1/cluster/init` | O | 클러스터 초기화 (CA 생성, Raft 부트스트랩) |
| POST | `/api/v1/cluster/join` | O | 기존 클러스터 참가 (pre-flight 검증 포함) |
| POST | `/api/v1/cluster/leave` | O | 클러스터 탈퇴 (서비스 재시작, quorum 가드 `?force=`) |
| POST | `/api/v1/cluster/disband` | O | 클러스터 해산 (리더 전용) |
| GET | `/api/v1/cluster/interfaces` | O | 네트워크 인터페이스 목록 (클러스터 초기화용) |
| POST | `/api/v1/cluster/update` | O | 클러스터 전체 업데이트 오케스트레이션 (SSE, 리더 전용) |

### 알림 시스템 (11개)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/api/v1/alerts/channels` | O | 알림 채널 목록 (Discord/Telegram/Webhook) |
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

**스키마 요약:**
- **채널** (`alert_channels`): `{ id, type:"discord"|"telegram"|"webhook", name, config, enabled }`. `config`는 Discord `{"webhook_url":"…"}` / Telegram `{"bot_token":"…","chat_id":"…"}` / Webhook `{"webhook_url":"https://…"}`(v0.25.0+, Slack/Mattermost 호환 — `text` + 구조화 필드 POST). 목록 응답에서 `config`의 시크릿 키는 마스킹됩니다. `…/test`는 해당 채널로 테스트 알림 1건 발송.
- **규칙** (`alert_rules`): `{ id, name, type, condition(JSON), channel_ids(JSON 배열), severity:"info"|"warning"|"critical", cooldown(초), node_scope:"all"|"specific", node_ids(JSON), enabled }`. `type`은 호스트형(`cpu`/`memory`/`disk`)이면 `condition={"operator":">","threshold":90}`, 컨테이너형(`container_down`/`container_oom`/`container_restart_loop`/`container_unhealthy`)이면 `condition={"container_pattern":"*","threshold_count":N,"window_seconds":N}`.
- **이력** (`alert_history`): `GET`은 `?page=&limit=` 페이지네이션 → `{items[], total, page, limit}`. 각 항목 `{ rule_id, rule_name, type, severity, message, sent_channels(JSON), created_at }`. `DELETE`는 전체 삭제.

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

### WebSocket (7개)

모두 `?token=<JWT>` 쿼리 파라미터 인증. `?node=<nodeID>` 파라미터로 클러스터 원격 릴레이 지원.

| 프로토콜 | 경로 | 인증 | 설명 |
|----------|------|------|------|
| WS | `/ws/metrics` | O (query) | 실시간 메트릭 (단일 sampler 공유, v0.24.0+) |
| WS | `/ws/cluster/overview` | O (query) | 클러스터 status+overview+이벤트 스냅샷 푸시 (v0.31.0+) |
| WS | `/ws/logs` | O (query) | 실시간 로그 스트리밍 |
| WS | `/ws/terminal` | O (query) | 호스트 PTY 터미널 (영속, 256KB 스크롤백, 최대 20 세션; `?session_id=`로 재접속) |
| WS | `/ws/docker/containers/:id/logs` | O (query) | 컨테이너 로그 |
| WS | `/ws/docker/containers/:id/exec` | O (query) | 컨테이너 셸 exec |
| WS | `/ws/docker/compose/:project/logs` | O (query) | Compose 프로젝트 로그 (서비스 필터 가능) |

> **터미널 세션 목록 (REST)**: `GET /api/v1/terminal/sessions`로 현재 사용자의 영속 PTY 세션을 조회한 뒤 `/ws/terminal?session_id=<id>`로 재접속(reattach)합니다 (v0.19.0+).
