# SFPanel Modular Architecture Migration Plan

## 목표

현재 모놀리식 핸들러 구조를 **기능별 모듈 + 공통 모듈** 구조로 점진적 마이그레이션.
52,500줄 코드를 안전하게 이동하며, 각 Phase마다 빌드+동작 검증.

## 현재 구조 → 목표 구조

```
현재:                                    목표:
internal/                               internal/
├── api/                                ├── common/
│   ├── router.go                       │   ├── exec/         (시스템 명령 추상화)
│   ├── handlers/ (33파일, 15,144줄)     │   ├── response/     (API 응답 타입)
│   │   ├── auth.go                     │   └── middleware/    (JWT, Audit, Cluster)
│   │   ├── docker.go                   ├── feature/
│   │   ├── packages.go                 │   ├── auth/         (handler + service)
│   │   ├── firewall_*.go               │   ├── docker/       (handler + service)
│   │   ├── disk_*.go                   │   ├── compose/      (handler + service)
│   │   ├── network.go                  │   ├── firewall/     (handler + service)
│   │   ├── ...                         │   ├── disk/         (handler + service)
│   │   └── (33개 전부 여기)              │   ├── network/      (handler + service)
│   ├── middleware/                      │   ├── packages/     (handler + service)
│   └── response/                       │   ├── services/     (handler + service)
├── auth/                               │   ├── files/        (handler + service)
├── cluster/                            │   ├── terminal/     (handler + service)
├── config/                             │   ├── logs/         (handler + service)
├── db/                                 │   ├── cron/         (handler + service)
├── docker/                             │   ├── process/      (handler + service)
├── monitor/                            │   ├── appstore/     (handler + service)
└── release/                            │   ├── monitor/      (handler + service)
                                        │   ├── settings/     (handler + service)
                                        │   ├── audit/        (handler + service)
                                        │   ├── system/       (handler + service)
                                        │   └── cluster/      (handler + service)
                                        ├── auth/             (기존 유지, JWT/TOTP 유틸)
                                        ├── config/           (기존 유지)
                                        ├── db/               (기존 유지)
                                        ├── docker/           (기존 유지, SDK 래퍼)
                                        ├── monitor/          (기존 유지, 메트릭 수집)
                                        ├── cluster/          (기존 유지, Raft/gRPC)
                                        ├── release/          (기존 유지)
                                        └── api/
                                            └── router.go     (라우트 등록만)
```

## 핵심 원칙

1. **한 번에 하나의 기능 모듈만 이동** — 전체를 한꺼번에 바꾸지 않음
2. **매 Phase마다 빌드+동작 검증** — 깨지면 즉시 발견
3. **import 경로만 변경, 로직 변경 없음** — 기능 변경과 구조 변경을 섞지 않음
4. **기존 테스트/E2E가 있으면 반드시 통과 확인**
5. **router.go는 마지막에 정리** — 핸들러가 다 이동된 후 import 경로만 갱신

---

## Phase 0: 공통 모듈 생성 (의존성 없음, 안전)

### 0-1. common/exec 패키지 생성

시스템 명령 실행을 추상화하는 인터페이스 생성. 113개 exec.Command() 호출의 공통 진입점.

```go
// internal/common/exec/exec.go
package exec

import (
    "context"
    "os/exec"
)

type Result struct {
    Stdout   string
    Stderr   string
    ExitCode int
}

type Commander interface {
    Run(ctx context.Context, name string, args ...string) (*Result, error)
    RunWithInput(ctx context.Context, input string, name string, args ...string) (*Result, error)
}

type SystemCommander struct{}

func NewCommander() Commander {
    return &SystemCommander{}
}

func (c *SystemCommander) Run(ctx context.Context, name string, args ...string) (*Result, error) {
    cmd := exec.CommandContext(ctx, name, args...)
    // ... 구현
}
```

**변경 범위**: 새 파일 1개 생성. 기존 코드 수정 없음.

### 0-2. common/response, common/middleware 이동

기존 `internal/api/response/`와 `internal/api/middleware/`를 `internal/common/`으로 이동.

```bash
mv internal/api/response internal/common/response
mv internal/api/middleware internal/common/middleware
```

**변경 범위**: 디렉토리 이동 + import 경로 변경 (router.go, 각 핸들러의 response import).

### 0-3. 빌드 검증

```bash
make build  # 또는 go build ./...
```

---

## Phase 1: DB 의존성 없는 stateless 핸들러 이동 (가장 안전)

Stateless 핸들러 18개 중 가장 독립적인 것부터 이동.

### 1-1. feature/services (services.go → 337줄)

가장 단순한 핸들러. systemctl 명령만 사용, DB 없음.

```
internal/feature/services/
├── handler.go    ← services.go 내용 이동
└── service.go    ← exec.Command() 호출을 서비스 레이어로 추출
```

**작업 순서**:
1. `internal/feature/services/` 디렉토리 생성
2. `service.go` 생성 — systemctl 관련 함수 추출
3. `handler.go` 생성 — HTTP 핸들러가 service를 호출하도록 변경
4. `router.go`에서 import 경로 변경
5. 기존 `handlers/services.go` 삭제
6. `go build ./...` 검증

### 1-2. feature/cron (cron.go → 399줄)

crontab 명령만 사용. DB 없음.

### 1-3. feature/process (processes.go → 216줄)

프로세스 목록 조회. DB 없음, 순수 gopsutil.

### 1-4. feature/packages (packages.go → 1,243줄)

apt 명령 래핑. DB 없음. 가장 큰 stateless 핸들러.

### 1-5. 빌드 + 동작 검증

```bash
make build
# 수동 테스트: 서비스 목록, 크론 목록, 프로세스 목록, 패키지 검색
```

---

## Phase 2: 네트워크/방화벽 핸들러 이동

### 2-1. feature/firewall (firewall.go + firewall_ufw.go + firewall_fail2ban.go + firewall_docker.go → 1,782줄)

4개 파일을 하나의 모듈로 묶기.

```
internal/feature/firewall/
├── handler.go        ← 라우트 핸들러 (기존 firewall.go 집약)
├── ufw.go            ← UFW 서비스 로직
├── fail2ban.go       ← Fail2ban 서비스 로직
└── docker_rules.go   ← Docker 방화벽 규칙
```

### 2-2. feature/network (network.go + tailscale.go + wireguard.go → 2,235줄)

```
internal/feature/network/
├── handler.go        ← 라우트 핸들러
├── interface.go      ← 인터페이스 설정 (netplan, DHCP)
├── wireguard.go      ← WireGuard 관리
└── tailscale.go      ← Tailscale 관리
```

### 2-3. 빌드 + 동작 검증

---

## Phase 3: 디스크 핸들러 이동 (가장 큰 그룹)

### 3-1. feature/disk (disk*.go 6파일 → 3,111줄)

```
internal/feature/disk/
├── handler.go        ← 라우트 핸들러 (진입점)
├── types.go          ← 기존 disk.go (타입 정의)
├── blocks.go         ← 블록 디바이스
├── partitions.go     ← 파티션 관리
├── filesystems.go    ← 파일시스템
├── lvm.go            ← LVM
├── raid.go           ← RAID
└── swap.go           ← 스왑
```

### 3-2. 빌드 + 동작 검증

---

## Phase 4: DB 의존 핸들러 이동

### 4-1. feature/auth (auth.go → 361줄)

```
internal/feature/auth/
├── handler.go        ← HTTP 핸들러
└── service.go        ← 로그인/2FA/세션 비즈니스 로직 (DB 쿼리 포함)
```

### 4-2. feature/settings (settings.go → 85줄)

### 4-3. feature/audit (audit.go → 80줄)

### 4-4. feature/logs (logs.go → 488줄)

WebSocket 핸들러 포함. `LogStreamWS` 이동 시 주의.

### 4-5. feature/files (files.go → 535줄)

### 4-6. feature/appstore (appstore.go → 867줄)

DB + Docker Compose 의존. 가장 복잡한 핸들러.

### 4-7. 빌드 + 동작 검증

---

## Phase 5: Docker 핸들러 이동

### 5-1. feature/docker (docker.go → 587줄)

```
internal/feature/docker/
├── handler.go        ← HTTP + WebSocket 핸들러
└── service.go        ← docker.Client 래핑 비즈니스 로직
```

주의: `internal/docker/` (SDK 래퍼)와 `internal/feature/docker/` (핸들러)는 별개.

### 5-2. feature/compose (compose.go → 375줄)

### 5-3. 빌드 + 동작 검증

---

## Phase 6: 나머지 핸들러 이동

### 6-1. feature/monitor (dashboard.go → 97줄 + ws.go 메트릭 부분)
### 6-2. feature/terminal (terminal.go → 304줄)
### 6-3. feature/system (system.go → 424줄 + tuning.go → 509줄)
### 6-4. feature/cluster (cluster.go → 707줄)

### 6-5. 빌드 + 동작 검증

---

## Phase 7: router.go 정리 + handlers/ 디렉토리 삭제

### 7-1. router.go 리팩토링

모든 핸들러가 feature/ 하위로 이동된 후, router.go의 import를 정리.

```go
import (
    "sfpanel/internal/feature/auth"
    "sfpanel/internal/feature/docker"
    "sfpanel/internal/feature/firewall"
    // ...
)
```

### 7-2. 빈 handlers/ 디렉토리 삭제

### 7-3. 최종 빌드 + 전체 동작 검증

---

## Phase 8: 테스트 + 문서

### 8-1. common/exec 단위 테스트
### 8-2. 각 feature service 레이어 단위 테스트 (mock Commander 주입)
### 8-3. E2E 테스트 (있는 경우)
### 8-4. README, ARCHITECTURE.md 업데이트

---

## 일정 추정

| Phase | 핸들러 수 | 코드량 | 위험도 |
|-------|----------|--------|--------|
| Phase 0 | 0 (인프라) | ~200줄 신규 | 매우 낮음 |
| Phase 1 | 4 (stateless) | 2,195줄 이동 | 낮음 |
| Phase 2 | 5 (네트워크/방화벽) | 4,017줄 이동 | 낮음 |
| Phase 3 | 6 (디스크) | 3,111줄 이동 | 낮음 |
| Phase 4 | 6 (DB 의존) | 2,416줄 이동 | 중간 |
| Phase 5 | 2 (Docker) | 962줄 이동 | 중간 |
| Phase 6 | 4 (나머지) | 2,532줄 이동 | 중간 |
| Phase 7 | 0 (정리) | router.go 수정 | 낮음 |
| Phase 8 | 0 (테스트) | 신규 테스트 | 낮음 |

## 주의사항

1. **WebSocket 핸들러** (ws.go)는 여러 기능에 걸쳐 있음 → 각 feature로 분산 이동
2. **exec.go** (65줄, 공통 헬퍼)는 common/exec로 통합
3. **cluster 관련 미들웨어**는 Phase 6까지 기존 위치 유지 후 이동
4. **go:embed** (web.go)는 루트에 유지 — 프론트엔드 임베딩은 구조 변경과 무관
5. **proto/** (gRPC 정의)는 기존 위치 유지
