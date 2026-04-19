# SFPanel 기능 모듈 인벤토리

생성 일시: 2026-04-19 | 총 22개 모듈 분석 | 상태: 모든 모듈이 최근 활동 있음

---

## 1. auth

**목적**: 관리자 인증, 2FA(TOTP), 비밀번호 관리, JWT 토큰 발급

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/auth/handler.go` (15KB)

**외부 의존성**:
- 라이브러리: `database/sql`, `sync`, `time`
- 클러스터 내부: `internal/cluster.Manager`, `internal/auth`, `internal/config`

**데이터 모델**:
- 메모리: `loginAttempts` (sync.Map - 로그인 시도 추적), `setupLimiter` (설정 제한)
- DB: 암묵적으로 사용자 테이블 (auth 패키지에서 관리)
- 설정 파일: config.yaml 읽음

**엔드포인트**: 7개
- `/auth/login` (POST), `/auth/setup-status` (GET), `/auth/setup` (POST)
- `/auth/2fa/status`, `/auth/2fa/setup`, `/auth/2fa/verify`, `/auth/change-password` (각 POST)

**특이 동작**:
- 로그인 속도 제한: 60초 내 5회 실패 시 5분 블록
- 클러스터 모드: Raft FSM 계정 동기화 (`SetClusterMgr` 콜백)
- 최근 커밋: `0a9cb6a` - Raft FSM 계정 동기화 추가

**패턴 준수**:
- ✓ Handler 구조 OK (DB, Config, ClusterMgr 필드)
- ✗ **handler_test.go 누락**
- ✓ exec.Commander 불사용 (외부 명령 없음)

---

## 2. docker

**목적**: 컨테이너, 이미지, 볼륨, 네트워크 관리 (Docker 클라이언트 래퍼)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/docker/handler.go` (18KB)

**외부 의존성**:
- 라이브러리: Docker SDK (`internal/docker.Client`)
- 명령어: Docker daemon (socket 통신)

**데이터 모델**:
- 메모리: 없음 (Docker daemon 쿼리)
- DB: 없음
- 설정: `cfg.Docker.Socket` (docker socket 경로)

**엔드포인트**: 30개
- 컨테이너: start, stop, restart, pause, unpause, inspect, remove, list, stats
- 이미지: pull, list, remove, search, check updates
- 볼륨: create, list, remove
- 네트워크: create, list, remove, inspect
- Prune: containers, images, volumes, networks, all

**특이 동작**:
- 컨텍스트 기반 타임아웃 (요청 컨텍스트 사용)
- 에러 새니타이제이션 (`SanitizeOutput` - 민감 정보 제거)

**패턴 준수**:
- ✓ Handler 구조 OK (Docker 필드)
- ✗ **handler_test.go 누락**
- ✓ exec.Commander 불사용

---

## 3. compose

**목적**: Docker Compose 프로젝트 관리 (YAML 저장, up/down, 환경변수)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/compose/handler.go` (12KB)

**외부 의존성**:
- 라이브러리: `internal/docker.ComposeManager`, `database/sql`
- 명령어: `docker compose` (Manager 내부에서 호출)

**데이터 모델**:
- 메모리: 없음 (파일 기반)
- DB: compose 프로젝트 메타데이터 (DB 참조)
- 파일: `/opt/stacks/<project>/docker-compose.yml`

**엔드포인트**: 25개
- 프로젝트: list, create, get, update, delete
- 스택: up, down, up-stream, restart
- 서비스: list, restart, stop, start, logs
- 유틸리티: validate, check-updates, update, update-stream, rollback, has-rollback
- 환경: get, update

**특이 동작**:
- 롤백 지원 (자동 백업 유지)
- 스트림 업데이트 (SSE를 통한 진행 상태)
- YAML 검증

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ exec.Commander 불사용

---

## 4. firewall

**목적**: UFW 방화벽 규칙 관리, Fail2ban 설치 및 감옥(jail) 관리

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/firewall/firewall.go` (3.6KB) - 핸들러 정의
- `/opt/stacks/SFPanel/internal/feature/firewall/firewall_ufw.go` (13KB) - UFW 명령어
- `/opt/stacks/SFPanel/internal/feature/firewall/firewall_fail2ban.go` (23KB) - Fail2ban 명령어
- `/opt/stacks/SFPanel/internal/feature/firewall/firewall_docker.go` (15KB) - Docker USER 규칙

**외부 의존성**:
- 라이브러리: `internal/common/exec.Commander`
- 명령어: `ufw`, `systemctl`, `apt-get`, `fail2ban-client`, `ss` (포트 수신 대기)

**데이터 모델**:
- 메모리: 없음
- DB: 없음
- 파일: `/etc/ufw/` (UFW 규칙), `/etc/fail2ban/jail.d/` (Fail2ban 감옥)

**엔드포인트**: 18개
- UFW: status, enable, disable, list rules, add rule, delete rule, list ports
- Docker: get Docker firewall, add rule, delete rule
- Fail2ban: status, install, list templates, list jails, create jail, delete jail, get detail, enable/disable, update config, unban IP

**특이 동작**:
- 포트 검증: 범위(8000:8080), CIDR 표기법 지원
- IP/CIDR 유효성 검사 (`net.ParseIP`, `net.ParseCIDR`)
- Fail2ban 감옥 동적 생성/삭제

**패턴 준수**:
- ✓ Handler 구조 OK (`Cmd exec.Commander`)
- ✗ **handler_test.go 누락**
- ✓ 올바른 exec 패턴 (Commander 사용)

---

## 5. disk

**목적**: 디스크, 파일시스템, LVM, RAID, Swap, SMART 관리

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/disk/disk.go` (11KB) - 핸들러
- `/opt/stacks/SFPanel/internal/feature/disk/disk_filesystems.go` (26KB) - 파일시스템 확장
- `/opt/stacks/SFPanel/internal/feature/disk/disk_swap.go` (19KB) - Swap 관리
- `/opt/stacks/SFPanel/internal/feature/disk/disk_lvm.go` (13KB) - LVM
- `/opt/stacks/SFPanel/internal/feature/disk/disk_raid.go` (12KB) - RAID
- `/opt/stacks/SFPanel/internal/feature/disk/disk_blocks.go` (11KB) - 블록 장치
- `/opt/stacks/SFPanel/internal/feature/disk/disk_partitions.go` (3.9KB) - 파티션

**외부 의존성**:
- 라이브러리: `internal/common/exec.Commander`
- 명령어: `lsblk`, `df`, `smartctl`, `fdisk`, `parted`, `growpart`, `lvm`, `mdadm`, `mkfs.*`, `mount`, `umount`, `resize2fs`

**데이터 모델**:
- 메모리: 없음
- DB: 없음
- 파일: `/dev/*` (장치), `/etc/fstab` (마운트 포인트)
- 외부: SMART 정보는 디바이스 펌웨어에서 읽음

**엔드포인트**: 35개
- 블록: overview, iostat, usage, smartmontools status/install
- SMART: get SMART info, check smartmontools
- 파티션: list, create, delete
- 파일시스템: list, format, mount, unmount, resize, expand check, expand
- LVM: list pvs/vgs/lvs, create pvs/vgs/lvs, remove, resize
- RAID: list, get detail, create, delete, add device, remove device
- Swap: get info, create, remove, set swappiness, resize check, resize

**특이 동작**:
- 확장 후보 체인 계산: `growpart` → `pvresize` → `lvextend` → `resize2fs`
- SMART 미지원 디바이스 감지 (healthy=nil)
- Swap 동적 생성/삭제

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ 올바른 exec 패턴

---

## 6. network

**목적**: 네트워크 인터페이스, DNS, 라우트, 본딩, Netplan, WireGuard, Tailscale

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/network/network.go` (37KB) - 메인 핸들러
- `/opt/stacks/SFPanel/internal/feature/network/wireguard.go` (15KB) - WireGuard VPN
- `/opt/stacks/SFPanel/internal/feature/network/tailscale.go` (18KB) - Tailscale VPN

**외부 의존성**:
- 라이브러리: `internal/common/exec.Commander`, `gopkg.in/yaml.v3` (Netplan YAML)
- 명령어: `ip`, `nmcli`, `ss`, `netstat`, `ethtool`, `systemctl`, `wg`, `tailscale`

**데이터 모델**:
- 메모리: 인터페이스 캐시 (시간 기반, `sync.RWMutex`)
- DB: 없음
- 파일: `/etc/netplan/*.yaml`, `/etc/wg/` (WireGuard 설정)

**엔드포인트**: 29개
- 기본: status, list interfaces, get interface, configure interface, apply netplan
- DNS: get, configure
- 라우트: list routes
- 본딩: list, create, delete
- WireGuard: status, install, list/get/create/update/delete interfaces & configs
- Tailscale: status, install, up, down, logout, list peers, set preferences, check update

**특이 동작**:
- 인터페이스 이름 검증: 정규식 `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$`
- Netplan YAML 읽음/쓰기/적용
- WireGuard 공개키/개인키 생성
- Tailscale 버전 체크 및 자동 업데이트

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ 올바른 exec 패턴
- **주목**: `network.go`에 `go func()` 고루틴 2개 (캐시 갱신)

---

## 7. packages

**목적**: APT 패키지 관리, Docker, Node.js, Claude, Codex, Gemini 설치/업데이트

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/packages/handler.go` (37KB)

**외부 의존성**:
- 라이브러리: `internal/common/exec.Commander`, `os/exec` (중복 임포트 `osExec`)
- 명령어: `apt-get`, `apt`, `apt-mark`, `dpkg`, `curl`, `wget`, `docker install`, `nvm`, `npm`

**데이터 모델**:
- 메모리: 없음
- DB: 없음
- 파일: `/usr/local/bin/`, `/home/`, `~/.nvm/` (Node 버전)

**엔드포인트**: 21개
- 기본: check updates, upgrade, install, remove, search
- Docker: status, install
- Node.js: status, versions, switch version, install version, uninstall version
- Claude, Codex, Gemini: status, install (각 3개씩)

**특이 동작**:
- `apt list --upgradable` 파싱
- `npm list -g` (전역 패키지)
- NVM 버전 관리 (`nvm ls-remote`)
- SSE 스트림으로 설치 진행 상황 전송 (`Start()` 호출, 백그라운드 실행)
- ⚠️ **os/exec 직접 임포트** (`osExec` 별칭) - 일부 코드는 exec.Commander 사용

**패턴 준수**:
- ✗ **os/exec 임포트** (프로젝트 규칙 위반 - 별칭으로 회피)
- ✓ 대부분 `exec.Commander` 사용
- ✗ **handler_test.go 누락**

---

## 8. services

**목적**: systemd 서비스 관리 (start, stop, restart, enable, disable)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/services/handler.go` (9.2KB)

**외부 의존성**:
- 라이브러리: `internal/common/exec.Commander`
- 명령어: `systemctl`

**데이터 모델**:
- 메모리: 서비스 캐시 (`serviceCache`, 3초 TTL)
- DB: 없음
- 파일: `/etc/systemd/system/` (서비스 정의)

**엔드포인트**: 8개
- list, start, stop, restart, enable, disable, get logs, get dependencies

**특이 동작**:
- 서비스 이름 검증: `^[a-zA-Z0-9@._:-]+\.service$`
- 캐시 무효화: 상태 변경 시 TTL 초기화
- 의존성 조회: `systemctl show` (Requires, RequiredBy, WantedBy)

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ 올바른 exec 패턴

---

## 9. files

**목적**: 서버측 파일 관리 (읽기, 쓰기, 삭제, 업로드, 다운로드)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/files/handler.go` (19KB)

**외부 의존성**:
- 라이브러리: `database/sql`, `internal/feature/settings`
- 명령어: 없음 (순수 파일시스템 I/O)

**데이터 모델**:
- 메모리: 없음
- DB: 커스텀 로그 소스 저장 (settings 모듈)
- 파일: 관리 대상 파일들

**엔드포인트**: 7개
- list, read, write, mkdir, delete, rename, download, upload

**특이 동작**:
- 경로 검증: 절대 경로, `..` 불허, 심링크 해석
- 중요 경로 보호: `/, /etc, /usr, /bin, /var, /home, /root, /boot, /dev, /proc, /sys` 등
- 파일 크기 제한:
  - 읽기: 5MB
  - 쓰기: 10MB
  - 다운로드: 2GB
- 심링크 공격 방지 (쓰기 전 `filepath.EvalSymlinks`)
- **크로스 모듈**: settings 모듈 임포트 (커스텀 로그 소스)

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ exec 불사용
- ⚠️ **크로스 모듈 의존성**: settings 모듈

---

## 10. cron

**목적**: 루트 crontab 관리 (작업 추가, 편집, 삭제)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/cron/handler.go` (11KB)

**외부 의존성**:
- 라이브러리: `internal/common/exec.Commander`
- 명령어: `crontab`

**데이터 모델**:
- 메모리: 없음
- DB: 없음
- 파일: 루트 crontab (시스템 관리)

**엔드포인트**: 4개
- list jobs, create job, update job, delete job

**특이 동작**:
- Cron 스케줄 검증: `@reboot, @yearly, @daily` 등 또는 5-필드 형식
- 환경 변수 라인 인식 (`^[A-Za-z_][A-Za-z0-9_]*=`)
- 코멘트 라인 처리

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ 올바른 exec 패턴

---

## 11. logs

**목적**: 시스템 로그 조회 및 실시간 스트림 (WebSocket)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/logs/handler.go` (15KB)

**외부 의존성**:
- 라이브러리: `database/sql`, `gorilla/websocket`, `internal/auth`
- 명령어: `grep`, `tail`, `wc`
- ⚠️ **os/exec 직접 임포트** (규칙 위반)

**데이터 모델**:
- 메모리: 없음
- DB: 커스텀 로그 소스 (저장)
- 파일: `/var/log/syslog`, `/var/log/auth.log`, `/var/log/kern.log`, `/var/log/sfpanel/sfpanel.log` 등

**엔드포인트**: 5개
- list sources, read log, add custom source, delete custom source, log stream (WebSocket)

**특이 동작**:
- 기본 소스: 7개 (syslog, auth, kern, sfpanel, dpkg, firewall=kern grep, fail2ban)
- 동적 필터: grep 정규식
- 라인 수 제한 없음 (클라이언트 주도)
- WebSocket 실시간: `tail -F | grep -E` 파이프

**패턴 준수**:
- ✗ **os/exec 직접 임포트** (규칙 위반)
- ✗ **handler_test.go 누락**

---

## 12. process

**목적**: 프로세스 목록 및 CPU 사용량 상위 10개 조회

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/process/handler.go` (5.8KB)

**외부 의존성**:
- 라이브러리: `github.com/shirou/gopsutil/v4/process`
- 명령어: 없음 (gopsutil 사용)

**데이터 모델**:
- 메모리: 프로세스 캐시 (`processCache`, 3초 TTL, `sync.RWMutex`)
- DB: 없음
- 파일: `/proc/` (Linux proc fs)

**엔드포인트**: 3개
- top processes (top 10 by CPU), list all processes, kill process

**특이 동작**:
- CPU 측정 비용이 높음 (200ms) → 3초 캐시
- 이중 확인 잠금 (double-check after lock acquire)

**패턴 준수**:
- ✓ Handler 구조 OK (빈 구조체)
- ✗ **handler_test.go 누락**
- ✓ exec 불사용

---

## 13. monitor

**목적**: 시스템 정보, 메트릭 조회 (CPU, 메모리, 디스크, 네트워크 I/O)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/monitor/handler.go` (2.7KB)

**외부 의존성**:
- 라이브러리: `internal/monitor` (메인 로직), `sync`
- 명령어: 없음 (gopsutil, OS 호출)

**데이터 모델**:
- 메모리: 메트릭 히스토리 (internal/monitor에서 관리)
- DB: 없음
- 파일: `/proc/`, `/sys/` (시스템 정보)

**엔드포인트**: 3개
- get system info, get metrics history, get overview (combined)

**특이 동작**:
- 병렬 조회: `sync.WaitGroup` 사용 (hostInfo + metrics)
- 히스토리 범위: 1h, 4h, 12h, 24h (기본)
- 업데이트 정보 포함 (현재 버전 vs. 최신)

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ exec 불사용

---

## 14. settings

**목적**: 애플리케이션 설정 조회/갱신 (key-value)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/settings/handler.go` (2.6KB)

**외부 의존성**:
- 라이브러리: `database/sql`
- 명령어: 없음

**데이터 모델**:
- 메모리: 기본값 맵 (terminal_timeout=30s, max_upload_size=1024MB)
- DB: `settings` 테이블 (key, value)

**엔드포인트**: 2개
- get all settings, update settings

**특이 동작**:
- 기본값 병합 (DB 쿼리 전)
- 키/값 길이 제한 (키 200, 값 1000자)
- UPSERT 패턴 (INSERT ... ON CONFLICT)

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ exec 불사용

---

## 15. audit

**목적**: 감사 로그 조회 및 삭제 (모든 API 요청 기록)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/audit/handler.go` (2.1KB)

**외부 의존성**:
- 라이브러리: `database/sql`
- 명령어: 없음

**데이터 모델**:
- 메모리: 없음
- DB: `audit_logs` 테이블 (username, method, path, status, ip, node_id, created_at)

**엔드포인트**: 2개
- list audit logs (페이지네이션: page, limit), clear all

**특이 동작**:
- 페이지네이션: 기본 50, 최대 100
- 클러스터 모드: node_id 포함 (다중 노드 추적)

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ exec 불사용

---

## 16. system

**목적**: 시스템 업데이트, 백업/복원, 튜닝

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/system/handler.go` (14KB)
- `/opt/stacks/SFPanel/internal/feature/system/tuning.go` (15KB)

**외부 의존성**:
- 라이브러리: `archive/tar`, `compress/gzip`, `crypto/sha256`, `os/exec` (직접 임포트!), `internal/common/exec.Commander`
- 명령어: `systemctl`, `curl` (GitHub releases API), `tar`, `gzip`

**데이터 모델**:
- 메모리: 없음
- DB: 없음
- 파일: 백업 tarball (gzip), `/opt/stacks/` (compose 프로젝트)

**엔드포인트**: 3개
- check update, run update, create backup, restore backup + 튜닝 4개

**특이 동작**:
- GitHub API 쿼리: releases/latest
- 바이너리 다운로드 및 교체 (SHA256 검증)
- SSE 진행률 스트림
- ⚠️ **os/exec 직접 사용**:
  ```go
  _ = exec.Command("systemctl", "restart", "sfpanel").Start()
  ```
  현재 프로세스 교체 (비블로킹)

**패턴 준수**:
- ✗ **os/exec 직접 사용** (규칙 위반!)
- ✓ commonExec.Commander 별도 사용
- ✗ **handler_test.go 누락**

---

## 17. cluster

**목적**: Raft 클러스터 관리 (초기화, 노드 추가/제거, 리더십 이전, 상태 조회)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/cluster/handler.go` (26KB)

**외부 의존성**:
- 라이브러리: `database/sql`, `sync`, `internal/cluster.Manager`, `gopkg.in/yaml.v3`
- 명령어: 없음 (gRPC로 통신)
- gRPC: `internal/cluster/proto` (protocol buffers)

**데이터 모델**:
- 메모리: 핸들러 뮤텍스 (Manager 참조, 런타임 업데이트)
- DB: Raft 상태, 클러스터 구성
- 파일: 클러스터 설정 (config.yaml)

**엔드포인트**: 13개
- status, overview, nodes list, create token, remove node
- update node labels/address, leader transfer
- init, join, leave, disband, get events
- get network interfaces, cluster update

**특이 동작**:
- 토큰 기반 조인 (일시 토큰, 사용 후 소멸)
- Raft FSM: 상태 변경은 모든 노드에 동기화
- 리더만 쓰기 가능 (리더가 아니면 ServiceUnavailable)
- 동시성 보호: `joiningMu` (init/join 경합 방지), `configMu` (설정 쓰기)
- 고루틴 4개: 병렬 노드 조회, 설정 동기화 등

**패턴 준수**:
- ✓ Handler 구조 OK
- ✗ **handler_test.go 누락**
- ✓ exec 불사용
- **주목**: 최근 활발한 개발 (data race, 클러스터 재설계)

---

## 18. appstore

**목적**: 앱 스토어 (Docker Compose 템플릿 설치)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/appstore/handler.go` (22KB)

**외부 의존성**:
- 라이브러리: `database/sql`, `crypto/rand`, `internal/common/exec.Commander`, `os/exec` (별칭 `osExec`)
- 명령어: `docker compose`, `git`
- 외부 API: GitHub 앱 스토어 저장소 (메타데이터)

**데이터 모델**:
- 메모리: 앱 카테고리/메타데이터 캐시
- DB: 설치된 앱 레코드, 설정 (settings 테이블)
- 파일: `/opt/stacks/<app>/docker-compose.yml`

**엔드포인트**: 6개
- get categories, list apps, get app detail, install app, get installed, refresh cache

**특이 동작**:
- GitHub 저장소에서 메타데이터 동적 로드
- 앱 설치: Compose + 환경변수 생성 + 포트 충돌 확인
- SSE 스트림: 설치 진행 상황
- 포트 제안 (충돌 시 자동 대안)
- 병렬 설치 가능 (고루틴)

**패턴 준수**:
- ✗ **os/exec 임포트** (규칙 위반)
- ✓ 대부분 exec.Commander 사용
- ✗ **handler_test.go 누락**

---

## 19. terminal

**목적**: 웹 기반 터미널 (WebSocket, PTY 관리)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/terminal/handler.go` (8.5KB)

**외부 의존성**:
- 라이브러리: `os/exec` (직접 사용), `creack/pty` (PTY 관리), `gorilla/websocket`
- 명령어: `/bin/bash` 또는 쉘 설정

**데이터 모델**:
- 메모리: 터미널 세션 (최대 20개), 스크롤백 버퍼 (256KB 링 버퍼 per 세션)
- DB: 없음
- 파일: 없음 (메모리 스트림)

**엔드포인트**: 1개
- WebSocket `/ws/terminal` (쿼리 파라미터 토큰 기반 인증)

**특이 동작**:
- PTY 할당: `creack/pty.Start()`
- 링 버퍼: 가장 최근 256KB만 유지
- 여러 클라이언트: 동일 세션에 다중 연결 가능
- 세션 타임아웃: 1분 무활동
- 뮤텍스: PTY 쓰기 보호, 리더 관리

**패턴 준수**:
- ✗ **os/exec 직접 사용** (규칙 위반)
- ✗ **handler_test.go 누락**
- ⚠️ **고루틴**: 백그라운드 PTY 리더, 세션 정리 타이머

---

## 20. websocket

**목적**: WebSocket 핸들러 (실시간 메트릭, 컨테이너 로그, Compose 로그)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/websocket/handler.go` (8.0KB)

**외부 의존성**:
- 라이브러리: `gorilla/websocket`, `internal/docker`, `internal/monitor`
- 명령어: 없음 (내부 API 쿼리)

**데이터 모델**:
- 메모리: 없음 (실시간 조회)
- DB: 없음

**엔드포인트**: 4개
- Metrics WS: 2초마다 메트릭 스트림
- Container Logs WS: 컨테이너 로그 실시간
- Container Exec WS: 컨테이너 exec 세션
- Compose Logs WS: Compose 서비스 로그

**특이 동작**:
- JWT 토큰 검증 (쿼리 파라미터)
- 안전한 WebSocket 쓰기: `safeWSWriter` 뮤텍스 보호
- CORS: Origin 검증 없음 (JWT 토큰이 인증 수단)
- 클라이언트 연결 해제 감지 (읽기 고루틴)

**패턴 준수**:
- ✓ exec 불사용
- ✗ **handler_test.go 누락**
- **주목**: 고루틴 기반 (메트릭 2초 틱, 로그 스트림)

---

## 21. alert

**목적**: 경고 시스템 (규칙 기반, Discord/Telegram 통지)

**핵심 파일**:
- `/opt/stacks/SFPanel/internal/feature/alert/handler.go` (14KB)
- `/opt/stacks/SFPanel/internal/feature/alert/manager.go` (5.2KB)
- `/opt/stacks/SFPanel/internal/feature/alert/channels/` (Discord, Telegram 구현)

**외부 의존성**:
- 라이브러리: `database/sql`, `internal/feature/alert/channels` (Discord/Telegram 클라이언트)
- 명령어: 없음 (HTTP API 호출)
- 외부 API: Discord webhooks, Telegram Bot API

**데이터 모델**:
- 메모리: 마지막 전송 시간 (쿨다운 추적, `map[int]time.Time`)
- DB: `alert_rules`, `alert_channels`, `alert_history` 테이블
- 파일: 없음

**엔드포인트**: 12개
- Channels: list, create, update, delete, test
- Rules: list, create, update, delete
- History: list, clear

**특이 동작**:
- 60초 간격 평가 (고루틴, 시작 시)
- 규칙 조건: JSON 형식 (`operator`, `threshold`)
- 쿨다운: 규칙별 중복 알림 방지
- 메트릭 수집: `monitor.GetMetrics()` 호출
- CPU, 메모리, 디스크 기반 경고
- 클러스터 범위: node_scope (all, specific nodes)

**패턴 준수**:
- ✓ exec 불사용
- ✗ **handler_test.go 누락**
- ⚠️ **고루틴 미관리**: alertManager.Start()는 router.go에서 시작되지만, Stop() 호출 없음 (코멘트 주석)

---

## 22. websocket (재검토)

이미 20번에서 다룸.

---

## 크로스 모듈 의존성 맵

```
files → settings (커스텀 로그 소스, settings 테이블 사용)
terminal → settings (터미널 타임아웃 설정)
alert → alert/channels (Discord, Telegram 클라이언트)
auth → cluster (Raft FSM 동기화 콜백)
cluster → auth (SetClusterMgr 콜백으로 cluster manager 전파)
appstore → (자체 settings 테이블 사용)
router.go → 모든 모듈 (엔드포인트 등록)
```

**정리**: 대부분 독립적. 몇 개 모듈만 settings 또는 cluster에 의존.

---

## 패턴 & 안티패턴

### ✓ 준수 사항

1. **Handler 구조 일관성**
   - 대부분 `type Handler struct { Cmd exec.Commander }` 패턴 준수
   - 또는 Database, Docker, ComposeManager 등 명확한 의존성

2. **exec.Commander 사용**
   - 대부분 `internal/common/exec.Commander` 인터페이스 사용
   - 직접 os/exec 호출 최소화

3. **동시성 보호**
   - sync.Mutex, sync.RWMutex 광범위 사용
   - 캐시 이중 확인 잠금 (process, services)
   - 클러스터: joiningMu, configMu로 경합 방지

4. **입력 검증**
   - 정규식 (서비스명, 패키지명, 인터페이스명)
   - CIDR/IP 검증 (firewall)
   - 경로 검증 (files)

### ✗ 규칙 위반 및 문제

1. **os/exec 직접 임포트 (3개 모듈)**
   - **system**: `os/exec.Command("systemctl", "restart", "sfpanel").Start()`
   - **terminal**: `os/exec` 직접 사용 (PTY 구성)
   - **logs**: `os/exec` 직접 사용 (grep, tail)
   - 권장: `internal/common/exec.Commander` 래핑 또는 명확한 정당성

2. **패키지 모듈의 os/exec 회피**
   - `osExec` 별칭으로 임포트 (규칙 우회 시도)
   - 프로젝트 표준과 불일치

3. **모든 모듈 handler_test.go 누락**
   - 22개 모듈 모두 테스트 파일 없음
   - 단위 테스트 커버리지 0%
   - 권장: 최소한 핵심 로직(입력 검증, 에러 처리) 테스트

4. **고루틴 수명 관리 미흡**
   - **alert**: manager.Start() 시작하지만 Stop() 호출 없음 (코멘트 주석)
   - **terminal**: 세션 정리 타이머는 있으나, 서버 종료 시 PTY 정리 불명확
   - **websocket**: 메트릭 틱, 읽기 루프 - 클라이언트 연결 해제 시만 종료

5. **에러 처리**
   - 일관성: 어떤 모듈은 errors.Is() 체크, 어떤 건 문자열 비교
   - 클러스터: clusterErrResponse로 집중화하는 게 좋음

6. **설정 경로 하드코딩**
   - 많은 모듈이 `/opt/stacks`, `/var/log` 등 절대 경로 사용
   - config.yaml에서 주입받지 않음 (system은 받음)

### ⚠️ 설계 우려

1. **로그 모듈의 WebSocket 에러 처리**
   - tail -F 프로세스 시작 후 에러 무시 (goroutine 해제 시에만)
   - 파이프 연결 실패 시 클라이언트에 알림 없음

2. **디스크 확장 체인**
   - disk_filesystems.go의 단계별 명령어 실행
   - 중간 단계 실패 시 부분 확장 위험 (롤백 메커니즘 없음)

3. **Firewall UFW 규칙 검증**
   - CIDR 범위 문법 ('192.168.1.0/24') 지원하지만, UFW 호환성 검증 부족

4. **Terminal 링 버퍼 오버플로우**
   - 256KB 고정 → 대용량 출력 시 스크롤백 손실
   - 설정 불가능 (settings에 추가 가능했을 듯)

5. **Alert 규칙 조건 JSON 유효성**
   - 런타임 Unmarshal 실패 시 로그만 남김
   - API 응답에 제약 없음 (앞단에서 검증 해야함)

---

## 최근 커밋 활동

| 모듈 | 최신 커밋 | 변화 |
|------|----------|------|
| auth | 0a9cb6a | Raft FSM 계정 동기화 (**활발**) |
| cluster | ba5cd60 | 클러스터 조인 재설계 (**매우 활발**) |
| system | 87d5919 | 클러스터 재시작 엣지 케이스 (**활발**) |
| disk | ec56fcc | 모듈식 아키텍처 리팩터링 |
| network | ec56fcc | 모듈식 아키텍처 리팩터링 |
| appstore | ec56fcc | 모듈식 아키텍처 리팩터링 |
| alert | 5ee67cc | Discord/Telegram 알림 추가 (**활발**) |
| websocket | 2dcbaae | 컨테이너 로그 옵션 강화 |

**경향**: 최근 3주 내 거의 모든 모듈이 활동. Raft 클러스터, 알림 시스템, 부팅 수명주기 중심.

---

## 요약 테이블

| 모듈 | 파일 수 | 엔드포인트 | 고루틴 | exec 패턴 | 테스트 | 상태 |
|------|--------|----------|--------|-----------|--------|------|
| auth | 1 | 7 | ✗ | ✓ | ✗ | ✓ |
| docker | 1 | 30 | ✗ | N/A | ✗ | ✓ |
| compose | 1 | 25 | ✗ | N/A | ✗ | ✓ |
| firewall | 4 | 18 | ✗ | ✓ | ✗ | ✓ |
| disk | 7 | 35 | ✗ | ✓ | ✗ | ✓ |
| network | 3 | 29 | 2 | ✓ | ✗ | ✓ |
| packages | 1 | 21 | 5+ | ⚠️ | ✗ | ⚠️ |
| services | 1 | 8 | ✗ | ✓ | ✗ | ✓ |
| files | 1 | 7 | ✗ | N/A | ✗ | ✓ |
| cron | 1 | 4 | ✗ | ✓ | ✗ | ✓ |
| logs | 1 | 5 | 1+ | ✗ | ✗ | ⚠️ |
| process | 1 | 3 | ✗ | N/A | ✗ | ✓ |
| monitor | 1 | 3 | 2 | N/A | ✗ | ✓ |
| settings | 1 | 2 | ✗ | N/A | ✗ | ✓ |
| audit | 1 | 2 | ✗ | N/A | ✗ | ✓ |
| system | 2 | 3 | ✗ | ✗ | ✗ | ⚠️ |
| cluster | 1 | 13 | 4 | N/A | ✗ | ✓ |
| appstore | 1 | 6 | 3+ | ⚠️ | ✗ | ⚠️ |
| terminal | 1 | 1 | 2 | ✗ | ✗ | ⚠️ |
| websocket | 1 | 4 | 3 | N/A | ✗ | ✓ |
| alert | 2 | 12 | 1+ | N/A | ✗ | ⚠️ |

**범례**: ✓=준수, ✗=위반, ⚠️=부분/경고, N/A=해당사항없음

---

## 우선순위 개선 목록

### P0 (긴급)
1. **os/exec 직접 사용 제거** (system, terminal, logs, packages)
   - `internal/common/exec.Commander` 래핑 or exec.RealCommander 구현
2. **handler_test.go 추가** (22개 모듈 모두)
   - 최소: 엔드포인트 기본 동작 + 에러 경우의 수

### P1 (중요)
3. **Alert 고루틴 수명 관리** (router.go에 Stop() 추가)
4. **Terminal 링 버퍼 설정화** (settings 테이블에 추가)
5. **Disk 확장 롤백 메커니즘** (단계별 실패 처리)

### P2 (개선)
6. **에러 타입 일관화** (모든 모듈이 같은 에러 인터페이스)
7. **설정 경로 중앙화** (config.yaml에서 주입)
8. **고루틴 추적** (http pprof endpoint로 모니터링)

---

**end of report**
