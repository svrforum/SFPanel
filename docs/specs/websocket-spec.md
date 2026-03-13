# SFPanel WebSocket 스펙

## 개요

### 라이브러리
- **백엔드**: `gorilla/websocket` (Go)
- **프론트엔드**: 네이티브 `WebSocket` API (브라우저)
- **터미널 렌더링**: `@xterm/xterm` (컨테이너 로그, 컨테이너 셸, 시스템 터미널)

### 인증 방식

모든 WebSocket 엔드포인트는 **쿼리 파라미터** 방식으로 JWT 토큰을 전달한다.

```
ws://host/ws/metrics?token=<JWT>
```

- WebSocket 프로토콜은 HTTP 핸드셰이크 시 커스텀 헤더 전송이 제한적이므로, `Authorization` 헤더 대신 `token` 쿼리 파라미터를 사용한다.
- JWT는 HS256 알고리즘으로 서명되며, `username`과 `exp`(만료시간) 클레임을 포함한다.
- 토큰 검증 실패 시 WebSocket 업그레이드 전에 HTTP 401 응답을 반환한다.

**인증 실패 응답** (업그레이드 전 HTTP):
```json
{"error": "missing token"}
```
```json
{"error": "invalid or expired token"}
```

### Origin 검사

서버측 `websocket.Upgrader`의 `CheckOrigin`은 모든 origin을 허용한다 (`return true`).

### 프론트엔드 토큰 획득

`api.getToken()` 메서드로 `localStorage`에 저장된 JWT 토큰을 조회하여 쿼리 파라미터에 포함한다.

### 프로토콜 자동 감지

프론트엔드에서 현재 페이지 프로토콜에 따라 `ws:` / `wss:`를 자동으로 결정한다:

```typescript
const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
const wsUrl = `${protocol}//${window.location.host}/ws/...?token=${token}`
```

---

## 엔드포인트

### 1. `/ws/metrics` -- 실시간 시스템 메트릭

| 항목 | 값 |
|------|-----|
| **용도** | 대시보드 실시간 시스템 리소스 모니터링 |
| **인증** | `?token=<JWT>` (필수) |
| **통신 방향** | 서버 -> 클라이언트 (단방향 푸시) |
| **전송 주기** | **2초** (`time.NewTicker(2 * time.Second)`) |
| **메시지 타입** | JSON (TextMessage) |
| **사용 페이지** | `Dashboard.tsx` |
| **라우트 등록** | `e.GET("/ws/metrics", ...)` (인증 미들웨어 바깥, 토큰은 쿼리 파라미터로 자체 검증) |
| **Docker 의존성** | 없음 (항상 등록) |

**메시지 형식 (서버 -> 클라이언트):**

```json
{
  "cpu": 23.5,
  "mem_total": 8589934592,
  "mem_used": 4294967296,
  "mem_percent": 50.0,
  "swap_total": 2147483648,
  "swap_used": 536870912,
  "swap_percent": 25.0,
  "disk_total": 107374182400,
  "disk_used": 53687091200,
  "disk_percent": 50.0,
  "net_bytes_sent": 123456789,
  "net_bytes_recv": 987654321,
  "timestamp": 1740500000000
}
```

**데이터 필드 설명:**

| 필드 | 타입 | 설명 |
|------|------|------|
| `cpu` | `float64` | CPU 사용률 (%) -- 전체 CPU 평균, 1초 샘플링 |
| `mem_total` | `uint64` | 전체 물리 메모리 (bytes) |
| `mem_used` | `uint64` | 사용 중 메모리 (bytes) |
| `mem_percent` | `float64` | 메모리 사용률 (%) |
| `swap_total` | `uint64` | 전체 스왑 (bytes) |
| `swap_used` | `uint64` | 사용 중 스왑 (bytes) |
| `swap_percent` | `float64` | 스왑 사용률 (%) |
| `disk_total` | `uint64` | 루트(`/`) 파티션 전체 크기 (bytes) |
| `disk_used` | `uint64` | 루트 파티션 사용량 (bytes) |
| `disk_percent` | `float64` | 디스크 사용률 (%) |
| `net_bytes_sent` | `uint64` | 부팅 이후 누적 송신 바이트 |
| `net_bytes_recv` | `uint64` | 부팅 이후 누적 수신 바이트 |
| `timestamp` | `int64` | Unix 밀리초 타임스탬프 |

**서버 구현 세부사항:**

- `monitor.GetMetrics()` 호출 시 CPU 측정에 1초(`cpu.Percent(1*time.Second, false)`)가 소요된다.
- 따라서 실제 전송 간격은 약 3초이다 (2초 ticker + 1초 CPU 샘플링).
- 메트릭 수집 에러 발생 시 해당 틱은 건너뛴다 (`continue`).
- 클라이언트 연결 해제 감지를 위한 별도 고루틴이 `ReadMessage()`를 루프한다.

**프론트엔드 처리:**

- `useWebSocket` 훅을 통해 연결하며, 자동 재연결이 활성화되어 있다.
- 수신된 메트릭을 파싱하여 실시간 카드(CPU, 메모리, 디스크, 네트워크) 업데이트 및 차트 데이터를 누적한다.
- 네트워크 전송률은 이전 메트릭과의 누적 바이트 차이를 시간차로 나누어 계산한다.
- 차트 데이터는 최대 2880개 포인트(24시간 기준, 30초 간격)로 제한된다.

---

### 2. `/ws/logs` -- 시스템 로그 실시간 스트리밍

| 항목 | 값 |
|------|-----|
| **용도** | 시스템 로그 파일의 실시간 `tail -f` 스트리밍 |
| **인증** | `?token=<JWT>` (필수) |
| **쿼리 파라미터** | `source` (필수): 로그 소스 키 |
| **통신 방향** | 서버 -> 클라이언트 (단방향 푸시) |
| **메시지 타입** | 텍스트 (TextMessage) -- 한 줄씩 |
| **사용 페이지** | `Logs.tsx` |
| **라우트 등록** | `e.GET("/ws/logs", ...)` (인증 미들웨어 바깥) |
| **Docker 의존성** | 없음 (항상 등록) |

**쿼리 파라미터:**

| 파라미터 | 필수 | 설명 |
|----------|------|------|
| `token` | 예 | JWT 토큰 |
| `source` | 예 | 로그 소스 식별자 (아래 허용 목록 참조) |

**허용된 로그 소스 (`source` 값):**

| source 키 | 이름 | 파일 경로 |
|-----------|------|-----------|
| `syslog` | System Log | `/var/log/syslog` |
| `auth` | Auth Log | `/var/log/auth.log` |
| `kern` | Kernel Log | `/var/log/kern.log` |
| `nginx-access` | Nginx Access | `/var/log/nginx/access.log` |
| `nginx-error` | Nginx Error | `/var/log/nginx/error.log` |
| `sfpanel` | SFPanel | `/var/log/sfpanel.log` |
| `dpkg` | Package Manager | `/var/log/dpkg.log` |
| `ufw` | Firewall (UFW) | `/var/log/ufw.log` |

**메시지 형식 (서버 -> 클라이언트):**

```
Mar 15 10:23:45 server sshd[12345]: Accepted publickey for admin from 192.168.1.1
```

- 각 메시지는 로그 파일의 한 줄이다 (줄바꿈 없이 `scanner.Bytes()` 그대로 전송).
- JSON이 아닌 **평문 텍스트**로 전송된다.

**에러 처리 (업그레이드 전):**

| 상황 | HTTP 코드 | 응답 |
|------|-----------|------|
| `source` 파라미터 누락 | 400 | `"missing source parameter"` |
| 알 수 없는 source 키 | 400 | `"unknown log source: {key}"` |
| 로그 파일 미존재 | 404 | `"log file does not exist: {path}"` |

**에러 처리 (업그레이드 후):**

- `tail -f` 시작 실패 시 에러 메시지를 WebSocket TextMessage로 전송 후 연결 종료.

**서버 구현 세부사항:**

- `tail -f <path>` 프로세스를 생성하여 stdout을 WebSocket으로 스트리밍한다.
- 핸들러 종료 시 `cmd.Process.Kill()`로 tail 프로세스를 정리한다.
- 클라이언트 연결 해제 감지를 위한 별도 고루틴이 `ReadMessage()`를 루프한다.

**프론트엔드 처리:**

- `Logs.tsx`에서 직접 WebSocket을 생성/관리한다 (`useWebSocket` 훅 미사용).
- 수신 메시지를 먼저 JSON 파싱 시도 (`{line: string}` 또는 `{lines: string[]}` 형식)하고, 실패 시 평문 텍스트로 처리한다.
- "Live" 버튼 토글로 스트리밍 시작/중지를 제어한다.
- 자동 재연결은 없다 (수동 Live 토글 방식).

---

### 3. `/ws/docker/containers/:id/logs` -- 컨테이너 로그 스트리밍

| 항목 | 값 |
|------|-----|
| **용도** | 특정 Docker 컨테이너의 로그 실시간 스트리밍 |
| **인증** | `?token=<JWT>` (필수) |
| **경로 파라미터** | `:id` -- Docker 컨테이너 ID 또는 이름 |
| **통신 방향** | 서버 -> 클라이언트 (단방향 푸시) |
| **메시지 타입** | 텍스트 (TextMessage) -- 줄 단위, 끝에 `\n` 포함 |
| **사용 컴포넌트** | `ContainerLogs.tsx` |
| **라우트 등록** | `e.GET("/ws/docker/containers/:id/logs", ...)` (Docker 가용 시에만 등록) |
| **Docker 의존성** | 필수 (`dockerClient != nil` 조건) |

**쿼리 파라미터:**

| 파라미터 | 필수 | 설명 |
|----------|------|------|
| `token` | 예 | JWT 토큰 |

**메시지 형식 (서버 -> 클라이언트):**

```
2026-02-25T10:23:45.123456Z stdout: Starting server on port 8080\n
```

- Docker SDK의 `ContainerLogs` API로 스트림을 받아 줄 단위로 전송한다.
- Docker 멀티플렉스 로그 프레임의 8바이트 헤더를 자동 제거한다 (stdout=0x01, stderr=0x02).
- 각 메시지 끝에 `\n`을 추가하여 터미널 줄바꿈을 보장한다.
- `tail: "100"` 옵션으로 최근 100줄부터 시작하여 실시간 follow 한다.

**서버측 Docker API 호출 옵션:**

```go
container.LogsOptions{
    ShowStdout: true,
    ShowStderr: true,
    Follow:     true,
    Tail:       "100",
}
```

**에러 처리:**

- 컨테이너 로그 스트림 획득 실패 시: `"error: {메시지}"` 텍스트 메시지 전송 후 연결 종료.

**프론트엔드 처리:**

- `ContainerLogs.tsx`에서 직접 WebSocket을 생성한다 (`useWebSocket` 훅 미사용).
- xterm.js Terminal에 `disableStdin: true`, `convertEol: true` 옵션으로 읽기 전용 표시.
- 로그 라인을 `logLinesRef`에 누적하여 다운로드 기능 지원.
- 자동 재연결은 없다 (컴포넌트 마운트 시 1회 연결, 언마운트 시 종료).

---

### 4. `/ws/docker/containers/:id/exec` -- 컨테이너 인터랙티브 셸

| 항목 | 값 |
|------|-----|
| **용도** | Docker 컨테이너 내 인터랙티브 셸 (`/bin/sh`) 접속 |
| **인증** | `?token=<JWT>` (필수) |
| **경로 파라미터** | `:id` -- Docker 컨테이너 ID 또는 이름 |
| **통신 방향** | 양방향 (클라이언트 <-> 서버) |
| **메시지 타입** | 텍스트 (TextMessage) |
| **사용 컴포넌트** | `ContainerShell.tsx` |
| **라우트 등록** | `e.GET("/ws/docker/containers/:id/exec", ...)` (Docker 가용 시에만 등록) |
| **Docker 의존성** | 필수 (`dockerClient != nil` 조건) |

**쿼리 파라미터:**

| 파라미터 | 필수 | 설명 |
|----------|------|------|
| `token` | 예 | JWT 토큰 |

**메시지 형식:**

**서버 -> 클라이언트 (셸 출력):**
- 타입: `TextMessage`
- 내용: 셸 프로세스의 stdout/stderr raw 바이트 (최대 4096바이트 청크)

**클라이언트 -> 서버 (키보드 입력):**
- 타입: `TextMessage`
- 내용: xterm.js `onData` 이벤트에서 발생한 문자열 (키 입력, 제어 문자 포함)

**클라이언트 -> 서버 (터미널 리사이즈):**
```json
{
  "type": "resize",
  "cols": 120,
  "rows": 40
}
```

**리사이즈 메시지 처리:**

서버에서 수신 메시지를 JSON 파싱 시도하여 `type: "resize"` 여부를 확인하고, 해당하면 Docker `ExecResize` API로 TTY 크기를 조정한다. 일반 텍스트 입력은 exec stdin에 직접 전달한다.

**Docker Exec 설정:**

```go
container.ExecOptions{
    Cmd:          []string{"/bin/sh"},
    AttachStdin:  true,
    AttachStdout: true,
    AttachStderr: true,
    Tty:          true,
}
```

**에러 처리:**

- exec 생성 실패 시: `"error: {메시지}"` 텍스트 메시지 전송 후 연결 종료.

**프론트엔드 처리:**

- `ContainerShell.tsx`에서 직접 WebSocket을 생성한다.
- xterm.js Terminal에 `cursorBlink: true`, `convertEol: true` 옵션.
- `term.onData()` -- 키 입력을 WebSocket으로 전송.
- `term.onResize()` -- 터미널 크기 변경 시 resize JSON 메시지 전송.
- WebLinksAddon으로 URL 자동 하이라이트.
- 자동 재연결은 없다.

---

### 5. `/ws/terminal` -- 시스템 터미널 (PTY)

| 항목 | 값 |
|------|-----|
| **용도** | 호스트 서버의 인터랙티브 셸 접속 (PTY 기반) |
| **인증** | `?token=<JWT>` (필수) |
| **통신 방향** | 양방향 (클라이언트 <-> 서버) |
| **메시지 타입** | Binary (BinaryMessage) -- 서버->클라, 입력 데이터도 Binary 가능 |
| **사용 페이지** | `Terminal.tsx` |
| **라우트 등록** | `e.GET("/ws/terminal", ...)` (인증 미들웨어 바깥) |
| **Docker 의존성** | 없음 (항상 등록) |

**쿼리 파라미터:**

| 파라미터 | 필수 | 설명 |
|----------|------|------|
| `token` | 예 | JWT 토큰 |
| `session_id` | 아니오 | 세션 식별자 (기본값: `"default"`) |

**세션 관리:**

시스템 터미널은 **영속적 세션(persistent session)** 을 지원한다:

- `session_id`가 동일하면 기존 PTY 세션에 재접속한다.
- 재접속 시 **스크롤백 버퍼**(256KB 링 버퍼)에 저장된 이전 출력이 먼저 전송된다.
- 세션당 하나의 PTY 프로세스가 유지되며, 여러 WebSocket 클라이언트가 동시 접속 가능하다 (fan-out 브로드캐스트).
- 프로세스 종료 감지: `cmd.ProcessState != nil`이면 세션을 폐기하고 새로 생성한다.

**세션 타임아웃:**

- `CleanupTerminalSessions` 고루틴이 1분마다 유휴 세션을 검사한다.
- 타임아웃 값은 DB `settings` 테이블의 `terminal_timeout` 키에서 읽는다 (분 단위, 기본 30분).
- `terminal_timeout = 0`이면 자동 만료 없음.

**셸 선택:**

서버에서 `/bin/bash` -> `/bin/sh` 순서로 존재하는 셸을 자동 선택한다. 환경 변수:
- `TERM=xterm-256color`
- `LANG=en_US.UTF-8`

**메시지 형식:**

**서버 -> 클라이언트 (셸 출력):**
- 타입: `BinaryMessage`
- 내용: PTY stdout raw 바이트 (최대 4096바이트 청크)
- 동시에 스크롤백 버퍼에도 기록된다.

**클라이언트 -> 서버 (키보드 입력):**
- 타입: `BinaryMessage` (프론트엔드에서 `TextEncoder.encode(data)`로 변환)
- 내용: 키 입력 바이트

**클라이언트 -> 서버 (터미널 리사이즈):**
- 타입: `TextMessage`
- 내용:
```json
{
  "type": "resize",
  "cols": 120,
  "rows": 40
}
```

**리사이즈 메시지 처리:**

서버는 메시지 타입이 `TextMessage`인 경우에만 JSON 파싱을 시도한다. `type: "resize"`이면 `pty.Setsize()`로 PTY 윈도우 크기를 조정한다. 그 외의 경우 PTY stdin에 직접 전달한다.

**에러 처리:**

- PTY 시작 실패 시: `"Failed to start shell: {메시지}"` 텍스트 메시지 전송 후 연결 종료.

**프론트엔드 처리:**

- `Terminal.tsx`의 `TerminalSession` 컴포넌트에서 직접 WebSocket을 생성한다.
- `ws.binaryType = 'arraybuffer'`로 설정하여 바이너리 데이터를 `ArrayBuffer`로 수신.
- `onmessage`에서 `ArrayBuffer`이면 `Uint8Array`로 변환하여 xterm.js에 쓴다.
- 키 입력은 `TextEncoder.encode(data)`로 바이너리 전송한다.
- 연결 직후 초기 resize 메시지를 전송하여 서버측 PTY 크기를 동기화한다.
- 탭 관리: 복수 탭 지원, `localStorage`에 탭 목록 및 활성 탭 저장.
- 폰트 크기 조절 (10~24px, 기본 14px), 검색(SearchAddon), WebLinksAddon 지원.
- 자동 재연결은 없다 (세션 영속성으로 수동 재접속 시 이전 출력 복원).

---

## 프론트엔드 훅: `useWebSocket`

### 위치

`web/src/hooks/useWebSocket.ts`

### 용도

범용 WebSocket 연결 훅. 현재 **Dashboard의 메트릭 스트리밍**에서만 사용된다. 다른 WebSocket 사용처(로그, 컨테이너, 터미널)는 직접 WebSocket을 생성한다.

### 파라미터

```typescript
interface UseWebSocketOptions {
  url: string                   // WebSocket 경로 (예: '/ws/metrics')
  onMessage?: (data: any) => void  // 메시지 수신 콜백
  autoReconnect?: boolean       // 자동 재연결 여부 (기본: true)
  reconnectInterval?: number    // 재연결 간격 ms (기본: 3000)
}
```

### 반환값

```typescript
{
  connected: boolean           // 현재 연결 상태
  send: (data: any) => void    // 메시지 전송 함수
  ws: React.MutableRefObject<WebSocket | null>  // WebSocket 인스턴스 ref
}
```

### 연결 로직

1. `api.getToken()`으로 JWT 토큰 획득. 없으면 연결하지 않는다.
2. 현재 페이지 프로토콜에 따라 `ws:` / `wss:` 결정.
3. URL 구성: `${protocol}//${window.location.host}${url}?token=${token}`
4. 네이티브 `new WebSocket(wsUrl)` 생성.

### 자동 재연결 로직

```
[연결 성공] -> connected = true
[연결 끊김 (onclose)] -> connected = false
    -> autoReconnect = true 이면:
        -> setTimeout(connect, reconnectInterval) // 기본 3초
        -> 재연결 시도
    -> autoReconnect = false 이면:
        -> 재연결 하지 않음
```

- 재연결 시도 횟수 제한은 없다 (무한 재시도).
- 지수 백오프(exponential backoff)는 적용되지 않는다 (고정 간격).
- `onclose` 이벤트에서만 재연결 (오류 코드 구분 없음).

### 메시지 수신 처리

```typescript
ws.onmessage = (event) => {
  try {
    const data = JSON.parse(event.data)  // JSON 파싱 시도
    onMessage?.(data)
  } catch {
    onMessage?.(event.data)               // 실패 시 raw 데이터 전달
  }
}
```

### 메시지 전송

```typescript
const send = (data: any) => {
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(typeof data === 'string' ? data : JSON.stringify(data))
  }
}
```

- 연결이 `OPEN` 상태일 때만 전송.
- 문자열은 그대로, 객체는 `JSON.stringify`로 직렬화하여 전송.

### 생명주기

- 컴포넌트 마운트 시 `connect()` 호출.
- 컴포넌트 언마운트 시 `ws.close()` 호출.
- `url`, `onMessage`, `autoReconnect`, `reconnectInterval` 의존성이 변경되면 기존 연결을 닫고 재연결한다.

---

## 엔드포인트 요약 비교

| 엔드포인트 | 방향 | 메시지 타입 | 프론트엔드 훅 | 자동 재연결 | 세션 유지 |
|-----------|------|------------|--------------|------------|----------|
| `/ws/metrics` | 서버->클라 | JSON Text | `useWebSocket` | 있음 (3초) | 없음 |
| `/ws/logs` | 서버->클라 | 평문 Text | 직접 관리 | 없음 (수동) | 없음 |
| `/ws/docker/containers/:id/logs` | 서버->클라 | Text (+`\n`) | 직접 관리 | 없음 | 없음 |
| `/ws/docker/containers/:id/exec` | 양방향 | Text | 직접 관리 | 없음 | 없음 |
| `/ws/terminal` | 양방향 | Binary + Text(resize) | 직접 관리 | 없음 | 있음 (PTY 영속) |

---

## 라우트 등록 구조

```
e (Echo 루트)
├── /ws/metrics                           <- 항상 등록, 자체 JWT 검증
├── /ws/logs                              <- 항상 등록, 자체 JWT 검증
├── /ws/terminal                          <- 항상 등록, 자체 JWT 검증
├── /ws/docker/containers/:id/logs        <- Docker 가용 시에만 등록
├── /ws/docker/containers/:id/exec        <- Docker 가용 시에만 등록
└── /api/v1/...                           <- REST API 라우트
```

모든 WebSocket 라우트는 Echo의 JWT 미들웨어(`mw.JWTMiddleware`) **바깥**에 등록된다. 각 핸들러가 쿼리 파라미터의 토큰을 자체적으로 검증한다.

---

## 연결 해제 감지 패턴

서버에서 클라이언트 연결 해제를 감지하는 공통 패턴:

```go
done := make(chan struct{})
go func() {
    defer close(done)
    for {
        if _, _, err := ws.ReadMessage(); err != nil {
            return  // 클라이언트 연결 해제
        }
    }
}()

// ... 메인 로직 ...

<-done  // 클라이언트 연결 해제까지 블로킹
```

- `/ws/metrics`: ticker 루프에서 `select` 문으로 `done` 채널 감시
- `/ws/logs`: `<-done`으로 블로킹 후 tail 프로세스 정리
- `/ws/docker/containers/:id/logs`: `<-done`으로 블로킹 후 logReader 닫기
- `/ws/docker/containers/:id/exec`: 입력 고루틴에서 ReadMessage 에러 시 hijacked 연결 닫기
- `/ws/terminal`: for 루프에서 `ReadMessage` 에러 시 직접 return

---

## 클러스터 WebSocket 릴레이

### 개요

클러스터 환경에서 모든 WebSocket 엔드포인트는 `?node=X` 쿼리 파라미터를 지원한다. `node`가 로컬 노드가 아닌 경우, `WrapEchoWSHandler`가 클라이언트 연결을 업그레이드한 뒤 원격 노드의 WebSocket에 양방향으로 릴레이한다.

### 릴레이 동작

```
클라이언트 ←→ 로컬 노드 (WS 업그레이드) ←→ 원격 노드 (WS 다이얼)
```

1. 클라이언트가 `ws://host/ws/terminal?token=JWT&node=REMOTE_ID`로 연결
2. 로컬 노드가 `node` 파라미터 감지 → 클라이언트 WS 업그레이드
3. 로컬 노드가 원격 노드의 WS 엔드포인트에 연결 (JWT 대신 `X-SFPanel-Internal-Proxy` 헤더 사용)
4. 양방향 메시지 포워딩 (메시지 타입 보존)
5. 어느 한쪽이 닫히면 반대쪽도 닫기

### 내부 프록시 인증

클러스터 노드 간 WebSocket 릴레이 시 JWT 토큰 대신 내부 프록시 인증을 사용한다:

- **헤더**: `X-SFPanel-Internal-Proxy`
- **값**: 클러스터 CA 인증서의 SHA-256 해시 (hex)
- **검증**: `crypto/subtle.ConstantTimeCompare`로 상수 시간 비교
- `authenticateWS()` 헬퍼가 내부 프록시 헤더를 우선 확인하고, 없으면 JWT 토큰 검증

### 릴레이 대상 엔드포인트

| 엔드포인트 | 설명 |
|-----------|------|
| `/ws/metrics?node=X` | 원격 노드 실시간 메트릭 |
| `/ws/logs?node=X` | 원격 노드 로그 스트리밍 |
| `/ws/terminal?node=X` | 원격 노드 터미널 접속 |
| `/ws/docker/containers/:id/logs?node=X` | 원격 노드 컨테이너 로그 |
| `/ws/docker/containers/:id/exec?node=X` | 원격 노드 컨테이너 셸 |

### `node` 파라미터 처리

- `node` 미지정 또는 로컬 노드 ID → 로컬 핸들러 실행
- `node`가 원격 노드 → 릴레이 (원격 연결 시 `node` 파라미터 제거)
- 노드를 찾을 수 없음 → HTTP 400
- 노드가 오프라인 → HTTP 503

---

## 보안 고려사항

1. **Origin 검사 미비**: `CheckOrigin`이 모든 origin을 허용하므로 CSRF 공격에 노출될 수 있다. 프로덕션 환경에서는 허용 origin 목록 설정을 권장한다.
2. **토큰 URL 노출**: JWT가 쿼리 파라미터에 포함되어 서버 접근 로그에 기록될 수 있다.
3. **로그 소스 제한**: `/ws/logs`의 로그 소스는 화이트리스트 방식으로 제한되어 있어 임의 파일 접근은 차단된다.
4. **시스템 터미널 보안**: `/ws/terminal`은 호스트 셸에 직접 접근하므로 JWT 인증이 유일한 방어선이다.
