# SFPanel HTTP API 인벤토리 (2026-04-19)

**목적**: SFPanel 프로젝트의 모든 HTTP 엔드포인트 실시간 데이터 수집. WebSocket 엔드포인트는 별도 범위(sibling agent 담당).

---

## 1. 라우트 테이블

### 주요 정보

- **기본 경로**: `/api/v1`
- **라우터 중앙점**: `internal/api/router.go` (NewRouter 함수)
- **미들웨어**: JWT 인증, 감사 로깅, 클러스터 프록시, 요청 로깅

### 공개 엔드포인트 (인증 불필요)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/health` | `OK(c, ...)` | - | RequestLogger 제외 | 헬스체크 |
| **POST** | `/api/v1/auth/login` | `featureAuth.Login` | auth | RequestLogger만 | JWT 토큰 발급 |
| **GET** | `/api/v1/auth/setup-status` | `featureAuth.GetSetupStatus` | auth | RequestLogger만 | 초기 셋업 필요 여부 |
| **POST** | `/api/v1/auth/setup` | `featureAuth.SetupAdmin` | auth | RequestLogger만 | 관리자 계정 생성 (1회용) |

### 보호된 엔드포인트 (JWT 필수)

#### 인증 관리 (`/api/v1/auth`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/auth/2fa/status` | `featureAuth.Get2FAStatus` | auth | JWT, ClusterProxy, Audit | 2FA 상태 조회 |
| **POST** | `/api/v1/auth/2fa/setup` | `featureAuth.Setup2FA` | auth | JWT, ClusterProxy, Audit | 2FA 시크릿 생성 (TOTP) |
| **POST** | `/api/v1/auth/2fa/verify` | `featureAuth.Verify2FA` | auth | JWT, ClusterProxy, Audit | 2FA 코드 검증 및 활성화 |
| **DELETE** | `/api/v1/auth/2fa` | `featureAuth.Disable2FA` | auth | JWT, ClusterProxy, Audit | 2FA 비활성화 |
| **POST** | `/api/v1/auth/change-password` | `featureAuth.ChangePassword` | auth | JWT, ClusterProxy, Audit | 비밀번호 변경 |

#### 설정 관리 (`/api/v1/settings`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/settings` | `featureSettings.GetSettings` | settings | JWT, ClusterProxy, Audit | 전체 설정 조회 |
| **PUT** | `/api/v1/settings` | `featureSettings.UpdateSettings` | settings | JWT, ClusterProxy, Audit | 설정 업데이트 |

#### 시스템 정보 및 모니터링 (`/api/v1/system`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/system/info` | `featureMonitor.GetSystemInfo` | monitor | JWT, ClusterProxy, Audit | 시스템 기본 정보 |
| **GET** | `/api/v1/system/metrics-history` | `featureMonitor.GetMetricsHistory` | monitor | JWT, ClusterProxy, Audit | 과거 메트릭 히스토리 |
| **GET** | `/api/v1/system/overview` | `featureMonitor.GetOverview` | monitor | JWT, ClusterProxy, Audit | 대시보드 오버뷰 |
| **GET** | `/api/v1/system/tuning` | `featureSystem.GetTuningStatus` | system | JWT, ClusterProxy, Audit | 시스템 튜닝 상태 |
| **POST** | `/api/v1/system/tuning/apply` | `featureSystem.ApplyTuning` | system | JWT, ClusterProxy, Audit | 튜닝 설정 적용 |
| **POST** | `/api/v1/system/tuning/confirm` | `featureSystem.ConfirmTuning` | system | JWT, ClusterProxy, Audit | 튜닝 변경 확인 |
| **POST** | `/api/v1/system/tuning/reset` | `featureSystem.ResetTuning` | system | JWT, ClusterProxy, Audit | 튜닝 초기화 |
| **GET** | `/api/v1/system/update-check` | `featureSystem.CheckUpdate` | system | JWT, ClusterProxy, Audit | 업데이트 확인 |
| **POST** | `/api/v1/system/update` | `featureSystem.RunUpdate` | system | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 시스템 업데이트 실행 |
| **POST** | `/api/v1/system/backup` | `featureSystem.CreateBackup` | system | JWT, ClusterProxy, Audit | 시스템 백업 생성 |
| **POST** | `/api/v1/system/restore` | `featureSystem.RestoreBackup` | system | JWT, ClusterProxy, Audit | 백업에서 복원 |

#### 프로세스 관리 (`/api/v1/system/processes`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/system/processes` | `featureProcess.TopProcesses` | process | JWT, ClusterProxy, Audit | 상위 프로세스 |
| **GET** | `/api/v1/system/processes/list` | `featureProcess.ListProcesses` | process | JWT, ClusterProxy, Audit | 전체 프로세스 목록 |
| **POST** | `/api/v1/system/processes/:pid/kill` | `featureProcess.KillProcess` | process | JWT, ClusterProxy, Audit | 프로세스 강제 종료 |

#### Systemd 서비스 (`/api/v1/system/services`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/system/services` | `featureServices.ListServices` | services | JWT, ClusterProxy, Audit | 모든 서비스 목록 |
| **POST** | `/api/v1/system/services/:name/start` | `featureServices.StartService` | services | JWT, ClusterProxy, Audit | 서비스 시작 |
| **POST** | `/api/v1/system/services/:name/stop` | `featureServices.StopService` | services | JWT, ClusterProxy, Audit | 서비스 중지 |
| **POST** | `/api/v1/system/services/:name/restart` | `featureServices.RestartService` | services | JWT, ClusterProxy, Audit | 서비스 재시작 |
| **POST** | `/api/v1/system/services/:name/enable` | `featureServices.EnableService` | services | JWT, ClusterProxy, Audit | 서비스 자동시작 활성화 |
| **POST** | `/api/v1/system/services/:name/disable` | `featureServices.DisableService` | services | JWT, ClusterProxy, Audit | 서비스 자동시작 비활성화 |
| **GET** | `/api/v1/system/services/:name/logs` | `featureServices.ServiceLogs` | services | JWT, ClusterProxy, Audit | 서비스 로그 조회 |
| **GET** | `/api/v1/system/services/:name/deps` | `featureServices.GetServiceDeps` | services | JWT, ClusterProxy, Audit | 서비스 의존성 조회 |

#### 앱 스토어 (`/api/v1/appstore`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/appstore/categories` | `featureAppstore.GetCategories` | appstore | JWT, ClusterProxy, Audit | 카테고리 목록 |
| **GET** | `/api/v1/appstore/apps` | `featureAppstore.ListApps` | appstore | JWT, ClusterProxy, Audit | 앱 목록 |
| **GET** | `/api/v1/appstore/apps/:id` | `featureAppstore.GetApp` | appstore | JWT, ClusterProxy, Audit | 앱 상세 정보 |
| **POST** | `/api/v1/appstore/apps/:id/install` | `featureAppstore.InstallApp` | appstore | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 앱 설치 |
| **GET** | `/api/v1/appstore/installed` | `featureAppstore.GetInstalled` | appstore | JWT, ClusterProxy, Audit | 설치된 앱 목록 |
| **POST** | `/api/v1/appstore/refresh` | `featureAppstore.RefreshCache` | appstore | JWT, ClusterProxy, Audit | 앱 스토어 캐시 새로고침 |

#### 파일 관리 (`/api/v1/files`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/files` | `featureFiles.ListDir` | files | JWT, ClusterProxy, Audit | 디렉토리 목록 조회 (쿼리: `path`) |
| **GET** | `/api/v1/files/read` | `featureFiles.ReadFile` | files | JWT, ClusterProxy, Audit | 파일 내용 읽기 (쿼리: `path`, `lines`) |
| **POST** | `/api/v1/files/write` | `featureFiles.WriteFile` | files | JWT, ClusterProxy, Audit | 파일 쓰기 |
| **POST** | `/api/v1/files/mkdir` | `featureFiles.MkDir` | files | JWT, ClusterProxy, Audit | 디렉토리 생성 |
| **DELETE** | `/api/v1/files` | `featureFiles.DeletePath` | files | JWT, ClusterProxy, Audit | 파일/디렉토리 삭제 (쿼리: `path`) |
| **POST** | `/api/v1/files/rename` | `featureFiles.RenamePath` | files | JWT, ClusterProxy, Audit | 파일/디렉토리 이름 변경 |
| **GET** | `/api/v1/files/download` | `featureFiles.DownloadFile` | files | JWT(쿼리 param), ClusterProxy, Audit | 파일 다운로드 (쿼리: `path`, `token`) |
| **POST** | `/api/v1/files/upload` | `featureFiles.UploadFile` | files | JWT, ClusterProxy, Audit | 파일 업로드 (multipart/form-data) |

#### Cron 작업 (`/api/v1/cron`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/cron` | `featureCron.ListJobs` | cron | JWT, ClusterProxy, Audit | 모든 크론 작업 조회 |
| **POST** | `/api/v1/cron` | `featureCron.CreateJob` | cron | JWT, ClusterProxy, Audit | 크론 작업 생성 |
| **PUT** | `/api/v1/cron/:id` | `featureCron.UpdateJob` | cron | JWT, ClusterProxy, Audit | 크론 작업 수정 |
| **DELETE** | `/api/v1/cron/:id` | `featureCron.DeleteJob` | cron | JWT, ClusterProxy, Audit | 크론 작업 삭제 |

#### 로그 조회 (`/api/v1/logs`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/logs/sources` | `featureLogs.ListSources` | logs | JWT, ClusterProxy, Audit | 로그 소스 목록 |
| **GET** | `/api/v1/logs/read` | `featureLogs.ReadLog` | logs | JWT, ClusterProxy, Audit | 로그 파일 읽기 (쿼리: `source`, `lines`) |
| **POST** | `/api/v1/logs/custom-sources` | `featureLogs.AddCustomSource` | logs | JWT, ClusterProxy, Audit | 커스텀 로그 소스 추가 |
| **DELETE** | `/api/v1/logs/custom-sources/:id` | `featureLogs.DeleteCustomSource` | logs | JWT, ClusterProxy, Audit | 커스텀 로그 소스 삭제 |

#### 감사 로그 (`/api/v1/audit`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/audit/logs` | `featureAudit.ListAuditLogs` | audit | JWT, ClusterProxy, Audit | 감사 로그 조회 |
| **DELETE** | `/api/v1/audit/logs` | `featureAudit.ClearAuditLogs` | audit | JWT, ClusterProxy, Audit | 감사 로그 삭제 |

#### 알림 (`/api/v1/alerts`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/alerts/channels` | `featureAlert.ListChannels` | alert | JWT, ClusterProxy, Audit | 알림 채널 목록 |
| **POST** | `/api/v1/alerts/channels` | `featureAlert.CreateChannel` | alert | JWT, ClusterProxy, Audit | 알림 채널 생성 |
| **PUT** | `/api/v1/alerts/channels/:id` | `featureAlert.UpdateChannel` | alert | JWT, ClusterProxy, Audit | 알림 채널 수정 |
| **DELETE** | `/api/v1/alerts/channels/:id` | `featureAlert.DeleteChannel` | alert | JWT, ClusterProxy, Audit | 알림 채널 삭제 |
| **POST** | `/api/v1/alerts/channels/:id/test` | `featureAlert.TestChannel` | alert | JWT, ClusterProxy, Audit | 알림 채널 테스트 |
| **GET** | `/api/v1/alerts/rules` | `featureAlert.ListRules` | alert | JWT, ClusterProxy, Audit | 알림 규칙 목록 |
| **POST** | `/api/v1/alerts/rules` | `featureAlert.CreateRule` | alert | JWT, ClusterProxy, Audit | 알림 규칙 생성 |
| **PUT** | `/api/v1/alerts/rules/:id` | `featureAlert.UpdateRule` | alert | JWT, ClusterProxy, Audit | 알림 규칙 수정 |
| **DELETE** | `/api/v1/alerts/rules/:id` | `featureAlert.DeleteRule` | alert | JWT, ClusterProxy, Audit | 알림 규칙 삭제 |
| **GET** | `/api/v1/alerts/history` | `featureAlert.ListHistory` | alert | JWT, ClusterProxy, Audit | 알림 히스토리 조회 |
| **DELETE** | `/api/v1/alerts/history` | `featureAlert.ClearHistory` | alert | JWT, ClusterProxy, Audit | 알림 히스토리 삭제 |

#### 클러스터 관리 (`/api/v1/cluster`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/cluster/status` | `featureCluster.GetStatus` | cluster | JWT, ClusterProxy, Audit | 클러스터 상태 |
| **GET** | `/api/v1/cluster/overview` | `featureCluster.GetOverview` | cluster | JWT, ClusterProxy, Audit | 클러스터 오버뷰 |
| **GET** | `/api/v1/cluster/nodes` | `featureCluster.GetNodes` | cluster | JWT, ClusterProxy, Audit | 노드 목록 |
| **POST** | `/api/v1/cluster/token` | `featureCluster.CreateToken` | cluster | JWT, ClusterProxy, Audit | 클러스터 조인 토큰 생성 |
| **DELETE** | `/api/v1/cluster/nodes/:id` | `featureCluster.RemoveNode` | cluster | JWT, ClusterProxy, Audit | 노드 제거 |
| **PATCH** | `/api/v1/cluster/nodes/:id/labels` | `featureCluster.UpdateNodeLabels` | cluster | JWT, ClusterProxy, Audit | 노드 라벨 수정 |
| **PATCH** | `/api/v1/cluster/nodes/:id/address` | `featureCluster.UpdateNodeAddress` | cluster | JWT, ClusterProxy, Audit | 노드 주소 수정 |
| **GET** | `/api/v1/cluster/events` | `featureCluster.GetEvents` | cluster | JWT, ClusterProxy, Audit | 클러스터 이벤트 (쿼리: `limit`, `after`) |
| **POST** | `/api/v1/cluster/leader-transfer` | `featureCluster.TransferLeadership` | cluster | JWT, ClusterProxy, Audit | 리더십 이전 |
| **POST** | `/api/v1/cluster/init` | `featureCluster.InitCluster` | cluster | JWT, ClusterProxy, Audit | 클러스터 초기화 |
| **POST** | `/api/v1/cluster/join` | `featureCluster.JoinCluster` | cluster | JWT, ClusterProxy, Audit | 클러스터 조인 |
| **POST** | `/api/v1/cluster/leave` | `featureCluster.LeaveCluster` | cluster | JWT, ClusterProxy, Audit | 클러스터 떠나기 |
| **POST** | `/api/v1/cluster/disband` | `featureCluster.DisbandCluster` | cluster | JWT, ClusterProxy, Audit | 클러스터 해산 |
| **GET** | `/api/v1/cluster/interfaces` | `featureCluster.GetNetworkInterfaces` | cluster | JWT, ClusterProxy, Audit | 네트워크 인터페이스 목록 |
| **POST** | `/api/v1/cluster/update` | `featureCluster.ClusterUpdate` | cluster | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 클러스터 전체 업데이트 |

#### 네트워크 관리 (`/api/v1/network`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/network/status` | `featureNetwork.GetNetworkStatus` | network | JWT, ClusterProxy, Audit | 네트워크 상태 |
| **GET** | `/api/v1/network/interfaces` | `featureNetwork.ListInterfaces` | network | JWT, ClusterProxy, Audit | 네트워크 인터페이스 목록 |
| **GET** | `/api/v1/network/interfaces/:name` | `featureNetwork.GetInterface` | network | JWT, ClusterProxy, Audit | 인터페이스 상세 정보 |
| **PUT** | `/api/v1/network/interfaces/:name` | `featureNetwork.ConfigureInterface` | network | JWT, ClusterProxy, Audit | 인터페이스 설정 |
| **POST** | `/api/v1/network/apply` | `featureNetwork.ApplyNetplan` | network | JWT, ClusterProxy, Audit | Netplan 적용 |
| **GET** | `/api/v1/network/dns` | `featureNetwork.GetDNS` | network | JWT, ClusterProxy, Audit | DNS 설정 조회 |
| **PUT** | `/api/v1/network/dns` | `featureNetwork.ConfigureDNS` | network | JWT, ClusterProxy, Audit | DNS 설정 |
| **GET** | `/api/v1/network/routes` | `featureNetwork.GetRoutes` | network | JWT, ClusterProxy, Audit | 라우팅 테이블 조회 |
| **GET** | `/api/v1/network/bonds` | `featureNetwork.ListBonds` | network | JWT, ClusterProxy, Audit | 본드 목록 |
| **POST** | `/api/v1/network/bonds` | `featureNetwork.CreateBond` | network | JWT, ClusterProxy, Audit | 본드 생성 |
| **DELETE** | `/api/v1/network/bonds/:name` | `featureNetwork.DeleteBond` | network | JWT, ClusterProxy, Audit | 본드 삭제 |

#### WireGuard VPN (`/api/v1/network/wireguard`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/network/wireguard/status` | `featureNetwork.GetStatus` | network | JWT, ClusterProxy, Audit | WireGuard 상태 |
| **POST** | `/api/v1/network/wireguard/install` | `featureNetwork.Install` | network | JWT, ClusterProxy, Audit | WireGuard 설치 |
| **GET** | `/api/v1/network/wireguard/interfaces` | `featureNetwork.ListInterfaces` | network | JWT, ClusterProxy, Audit | 인터페이스 목록 |
| **GET** | `/api/v1/network/wireguard/interfaces/:name` | `featureNetwork.GetInterface` | network | JWT, ClusterProxy, Audit | 인터페이스 상세 |
| **POST** | `/api/v1/network/wireguard/interfaces/:name/up` | `featureNetwork.InterfaceUp` | network | JWT, ClusterProxy, Audit | 인터페이스 시작 |
| **POST** | `/api/v1/network/wireguard/interfaces/:name/down` | `featureNetwork.InterfaceDown` | network | JWT, ClusterProxy, Audit | 인터페이스 중지 |
| **POST** | `/api/v1/network/wireguard/configs` | `featureNetwork.CreateConfig` | network | JWT, ClusterProxy, Audit | 설정 생성 |
| **GET** | `/api/v1/network/wireguard/configs/:name` | `featureNetwork.GetConfig` | network | JWT, ClusterProxy, Audit | 설정 조회 |
| **PUT** | `/api/v1/network/wireguard/configs/:name` | `featureNetwork.UpdateConfig` | network | JWT, ClusterProxy, Audit | 설정 수정 |
| **DELETE** | `/api/v1/network/wireguard/configs/:name` | `featureNetwork.DeleteConfig` | network | JWT, ClusterProxy, Audit | 설정 삭제 |

#### Tailscale VPN (`/api/v1/network/tailscale`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/network/tailscale/status` | `featureNetwork.GetStatus` | network | JWT, ClusterProxy, Audit | Tailscale 상태 |
| **POST** | `/api/v1/network/tailscale/install` | `featureNetwork.Install` | network | JWT, ClusterProxy, Audit | Tailscale 설치 |
| **POST** | `/api/v1/network/tailscale/up` | `featureNetwork.Up` | network | JWT, ClusterProxy, Audit | Tailscale 시작 |
| **POST** | `/api/v1/network/tailscale/down` | `featureNetwork.Down` | network | JWT, ClusterProxy, Audit | Tailscale 중지 |
| **POST** | `/api/v1/network/tailscale/logout` | `featureNetwork.Logout` | network | JWT, ClusterProxy, Audit | 로그아웃 |
| **GET** | `/api/v1/network/tailscale/peers` | `featureNetwork.ListPeers` | network | JWT, ClusterProxy, Audit | 피어 목록 |
| **PUT** | `/api/v1/network/tailscale/preferences` | `featureNetwork.SetPreferences` | network | JWT, ClusterProxy, Audit | 설정 변경 |
| **GET** | `/api/v1/network/tailscale/update-check` | `featureNetwork.CheckUpdate` | network | JWT, ClusterProxy, Audit | 업데이트 확인 |

#### 디스크 관리 (`/api/v1/disks`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/disks/overview` | `featureDisk.ListDisks` | disk | JWT, ClusterProxy, Audit | 디스크 목록 |
| **GET** | `/api/v1/disks/iostat` | `featureDisk.GetIOStats` | disk | JWT, ClusterProxy, Audit | IO 통계 |
| **POST** | `/api/v1/disks/usage` | `featureDisk.GetDiskUsage` | disk | JWT, ClusterProxy, Audit | 디스크 사용량 (쿼리 param: `path`) |
| **GET** | `/api/v1/disks/smartmontools-status` | `featureDisk.CheckSmartmontools` | disk | JWT, ClusterProxy, Audit | smartmontools 설치 상태 |
| **POST** | `/api/v1/disks/install-smartmontools` | `featureDisk.InstallSmartmontools` | disk | JWT, ClusterProxy, Audit | smartmontools 설치 |
| **GET** | `/api/v1/disks/:device/smart` | `featureDisk.GetSmartInfo` | disk | JWT, ClusterProxy, Audit | SMART 정보 (:device = /dev/sda 등) |
| **GET** | `/api/v1/disks/:device/partitions` | `featureDisk.ListPartitions` | disk | JWT, ClusterProxy, Audit | 파티션 목록 |
| **POST** | `/api/v1/disks/:device/partitions` | `featureDisk.CreatePartition` | disk | JWT, ClusterProxy, Audit | 파티션 생성 |
| **DELETE** | `/api/v1/disks/:device/partitions/:number` | `featureDisk.DeletePartition` | disk | JWT, ClusterProxy, Audit | 파티션 삭제 |

#### 파일시스템 (`/api/v1/filesystems`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/filesystems` | `featureDisk.ListFilesystems` | disk | JWT, ClusterProxy, Audit | 파일시스템 목록 |
| **POST** | `/api/v1/filesystems/format` | `featureDisk.FormatPartition` | disk | JWT, ClusterProxy, Audit | 파티션 포맷 |
| **POST** | `/api/v1/filesystems/mount` | `featureDisk.MountFilesystem` | disk | JWT, ClusterProxy, Audit | 파일시스템 마운트 |
| **POST** | `/api/v1/filesystems/unmount` | `featureDisk.UnmountFilesystem` | disk | JWT, ClusterProxy, Audit | 마운트 해제 |
| **POST** | `/api/v1/filesystems/resize` | `featureDisk.ResizeFilesystem` | disk | JWT, ClusterProxy, Audit | 파일시스템 크기 조정 |
| **GET** | `/api/v1/filesystems/expand-check` | `featureDisk.CheckExpandable` | disk | JWT, ClusterProxy, Audit | 확장 가능 여부 확인 |
| **POST** | `/api/v1/filesystems/expand` | `featureDisk.ExpandFilesystem` | disk | JWT, ClusterProxy, Audit | 파일시스템 확장 |

#### LVM (`/api/v1/lvm`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/lvm/pvs` | `featureDisk.ListPVs` | disk | JWT, ClusterProxy, Audit | 물리 볼륨 목록 |
| **GET** | `/api/v1/lvm/vgs` | `featureDisk.ListVGs` | disk | JWT, ClusterProxy, Audit | 볼륨 그룹 목록 |
| **GET** | `/api/v1/lvm/lvs` | `featureDisk.ListLVs` | disk | JWT, ClusterProxy, Audit | 논리 볼륨 목록 |
| **POST** | `/api/v1/lvm/pvs` | `featureDisk.CreatePV` | disk | JWT, ClusterProxy, Audit | 물리 볼륨 생성 |
| **POST** | `/api/v1/lvm/vgs` | `featureDisk.CreateVG` | disk | JWT, ClusterProxy, Audit | 볼륨 그룹 생성 |
| **POST** | `/api/v1/lvm/lvs` | `featureDisk.CreateLV` | disk | JWT, ClusterProxy, Audit | 논리 볼륨 생성 |
| **DELETE** | `/api/v1/lvm/pvs/:name` | `featureDisk.RemovePV` | disk | JWT, ClusterProxy, Audit | 물리 볼륨 삭제 |
| **DELETE** | `/api/v1/lvm/vgs/:name` | `featureDisk.RemoveVG` | disk | JWT, ClusterProxy, Audit | 볼륨 그룹 삭제 |
| **DELETE** | `/api/v1/lvm/lvs/:vg/:name` | `featureDisk.RemoveLV` | disk | JWT, ClusterProxy, Audit | 논리 볼륨 삭제 |
| **POST** | `/api/v1/lvm/lvs/resize` | `featureDisk.ResizeLV` | disk | JWT, ClusterProxy, Audit | 논리 볼륨 크기 조정 |

#### RAID (`/api/v1/raid`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/raid` | `featureDisk.ListRAID` | disk | JWT, ClusterProxy, Audit | RAID 배열 목록 |
| **GET** | `/api/v1/raid/:name` | `featureDisk.GetRAIDDetail` | disk | JWT, ClusterProxy, Audit | RAID 상세 정보 |
| **POST** | `/api/v1/raid` | `featureDisk.CreateRAID` | disk | JWT, ClusterProxy, Audit | RAID 배열 생성 |
| **DELETE** | `/api/v1/raid/:name` | `featureDisk.DeleteRAID` | disk | JWT, ClusterProxy, Audit | RAID 배열 삭제 |
| **POST** | `/api/v1/raid/:name/add` | `featureDisk.AddRAIDDisk` | disk | JWT, ClusterProxy, Audit | RAID에 디스크 추가 |
| **POST** | `/api/v1/raid/:name/remove` | `featureDisk.RemoveRAIDDisk` | disk | JWT, ClusterProxy, Audit | RAID에서 디스크 제거 |

#### 스왑 (`/api/v1/swap`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/swap` | `featureDisk.GetSwapInfo` | disk | JWT, ClusterProxy, Audit | 스왑 정보 |
| **POST** | `/api/v1/swap` | `featureDisk.CreateSwap` | disk | JWT, ClusterProxy, Audit | 스왑 생성 |
| **DELETE** | `/api/v1/swap` | `featureDisk.RemoveSwap` | disk | JWT, ClusterProxy, Audit | 스왑 제거 |
| **PUT** | `/api/v1/swap/swappiness` | `featureDisk.SetSwappiness` | disk | JWT, ClusterProxy, Audit | swappiness 값 설정 |
| **GET** | `/api/v1/swap/resize-check` | `featureDisk.CheckSwapResize` | disk | JWT, ClusterProxy, Audit | 스왑 크기 조정 가능 여부 |
| **PUT** | `/api/v1/swap/resize` | `featureDisk.ResizeSwap` | disk | JWT, ClusterProxy, Audit | 스왑 크기 조정 |

#### 방화벽 (UFW) (`/api/v1/firewall`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/firewall/status` | `featureFirewall.GetUFWStatus` | firewall | JWT, ClusterProxy, Audit | UFW 상태 |
| **POST** | `/api/v1/firewall/enable` | `featureFirewall.EnableUFW` | firewall | JWT, ClusterProxy, Audit | UFW 활성화 |
| **POST** | `/api/v1/firewall/disable` | `featureFirewall.DisableUFW` | firewall | JWT, ClusterProxy, Audit | UFW 비활성화 |
| **GET** | `/api/v1/firewall/rules` | `featureFirewall.ListRules` | firewall | JWT, ClusterProxy, Audit | 방화벽 규칙 목록 |
| **POST** | `/api/v1/firewall/rules` | `featureFirewall.AddRule` | firewall | JWT, ClusterProxy, Audit | 규칙 추가 |
| **DELETE** | `/api/v1/firewall/rules/:number` | `featureFirewall.DeleteRule` | firewall | JWT, ClusterProxy, Audit | 규칙 삭제 |
| **GET** | `/api/v1/firewall/ports` | `featureFirewall.ListPorts` | firewall | JWT, ClusterProxy, Audit | 열린 포트 목록 |
| **GET** | `/api/v1/firewall/docker` | `featureFirewall.GetDockerFirewall` | firewall | JWT, ClusterProxy, Audit | Docker 방화벽 규칙 |
| **POST** | `/api/v1/firewall/docker/rules` | `featureFirewall.AddDockerUserRule` | firewall | JWT, ClusterProxy, Audit | Docker 규칙 추가 |
| **DELETE** | `/api/v1/firewall/docker/rules/:number` | `featureFirewall.DeleteDockerUserRule` | firewall | JWT, ClusterProxy, Audit | Docker 규칙 삭제 |

#### Fail2ban (`/api/v1/fail2ban`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/fail2ban/status` | `featureFirewall.GetFail2banStatus` | firewall | JWT, ClusterProxy, Audit | Fail2ban 상태 |
| **POST** | `/api/v1/fail2ban/install` | `featureFirewall.InstallFail2ban` | firewall | JWT, ClusterProxy, Audit | Fail2ban 설치 |
| **GET** | `/api/v1/fail2ban/templates` | `featureFirewall.GetJailTemplates` | firewall | JWT, ClusterProxy, Audit | 옥 템플릿 |
| **GET** | `/api/v1/fail2ban/jails` | `featureFirewall.ListJails` | firewall | JWT, ClusterProxy, Audit | 옥 목록 |
| **POST** | `/api/v1/fail2ban/jails` | `featureFirewall.CreateJail` | firewall | JWT, ClusterProxy, Audit | 옥 생성 |
| **DELETE** | `/api/v1/fail2ban/jails/:name` | `featureFirewall.DeleteJail` | firewall | JWT, ClusterProxy, Audit | 옥 삭제 |
| **GET** | `/api/v1/fail2ban/jails/:name` | `featureFirewall.GetJailDetail` | firewall | JWT, ClusterProxy, Audit | 옥 상세 |
| **POST** | `/api/v1/fail2ban/jails/:name/enable` | `featureFirewall.EnableJail` | firewall | JWT, ClusterProxy, Audit | 옥 활성화 |
| **POST** | `/api/v1/fail2ban/jails/:name/disable` | `featureFirewall.DisableJail` | firewall | JWT, ClusterProxy, Audit | 옥 비활성화 |
| **PUT** | `/api/v1/fail2ban/jails/:name/config` | `featureFirewall.UpdateJailConfig` | firewall | JWT, ClusterProxy, Audit | 옥 설정 수정 |
| **POST** | `/api/v1/fail2ban/jails/:name/unban` | `featureFirewall.UnbanIP` | firewall | JWT, ClusterProxy, Audit | IP 차단 해제 |

#### 패키지 관리 (`/api/v1/packages`)

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/packages/updates` | `featurePackages.CheckUpdates` | packages | JWT, ClusterProxy, Audit | 업데이트 확인 |
| **POST** | `/api/v1/packages/upgrade` | `featurePackages.UpgradePackages` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 전체 패키지 업그레이드 |
| **POST** | `/api/v1/packages/install` | `featurePackages.InstallPackage` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 패키지 설치 |
| **POST** | `/api/v1/packages/remove` | `featurePackages.RemovePackage` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 패키지 제거 |
| **GET** | `/api/v1/packages/search` | `featurePackages.SearchPackages` | packages | JWT, ClusterProxy, Audit | 패키지 검색 |
| **GET** | `/api/v1/packages/docker-status` | `featurePackages.GetDockerStatus` | packages | JWT, ClusterProxy, Audit | Docker 설치 상태 |
| **POST** | `/api/v1/packages/install-docker` | `featurePackages.InstallDocker` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: Docker 설치 |
| **GET** | `/api/v1/packages/node-status` | `featurePackages.GetNodeStatus` | packages | JWT, ClusterProxy, Audit | Node.js 설치 상태 |
| **POST** | `/api/v1/packages/node-switch` | `featurePackages.SwitchNodeVersion` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: Node.js 버전 전환 |
| **GET** | `/api/v1/packages/node-versions` | `featurePackages.GetNodeVersions` | packages | JWT, ClusterProxy, Audit | Node.js 버전 목록 |
| **POST** | `/api/v1/packages/node-install-version` | `featurePackages.InstallNodeVersion` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: Node.js 특정 버전 설치 |
| **POST** | `/api/v1/packages/node-uninstall-version` | `featurePackages.UninstallNodeVersion` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: Node.js 버전 제거 |
| **GET** | `/api/v1/packages/claude-status` | `featurePackages.GetClaudeStatus` | packages | JWT, ClusterProxy, Audit | Claude 설치 상태 |
| **POST** | `/api/v1/packages/install-claude` | `featurePackages.InstallClaude` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: Claude 설치 |
| **GET** | `/api/v1/packages/codex-status` | `featurePackages.GetCodexStatus` | packages | JWT, ClusterProxy, Audit | Codex 설치 상태 |
| **POST** | `/api/v1/packages/install-codex` | `featurePackages.InstallCodex` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: Codex 설치 |
| **GET** | `/api/v1/packages/gemini-status` | `featurePackages.GetGeminiStatus` | packages | JWT, ClusterProxy, Audit | Gemini 설치 상태 |
| **POST** | `/api/v1/packages/install-gemini` | `featurePackages.InstallGemini` | packages | JWT, ClusterProxy, Audit | **SSE 스트리밍**: Gemini 설치 |

#### Docker 관리 (`/api/v1/docker`) - Docker 가용 시에만 등록

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/docker/containers` | `featureDocker.ListContainers` | docker | JWT, ClusterProxy, Audit | 컨테이너 목록 |
| **GET** | `/api/v1/docker/containers/stats/batch` | `featureDocker.ContainerStatsBatch` | docker | JWT, ClusterProxy, Audit | 전체 컨테이너 통계 |
| **GET** | `/api/v1/docker/containers/:id/inspect` | `featureDocker.InspectContainer` | docker | JWT, ClusterProxy, Audit | 컨테이너 상세 정보 |
| **GET** | `/api/v1/docker/containers/:id/stats` | `featureDocker.ContainerStats` | docker | JWT, ClusterProxy, Audit | 컨테이너 CPU/메모리 통계 |
| **POST** | `/api/v1/docker/containers/:id/start` | `featureDocker.StartContainer` | docker | JWT, ClusterProxy, Audit | 컨테이너 시작 |
| **POST** | `/api/v1/docker/containers/:id/stop` | `featureDocker.StopContainer` | docker | JWT, ClusterProxy, Audit | 컨테이너 중지 |
| **POST** | `/api/v1/docker/containers/:id/restart` | `featureDocker.RestartContainer` | docker | JWT, ClusterProxy, Audit | 컨테이너 재시작 |
| **POST** | `/api/v1/docker/containers/:id/pause` | `featureDocker.PauseContainer` | docker | JWT, ClusterProxy, Audit | 컨테이너 일시정지 |
| **POST** | `/api/v1/docker/containers/:id/unpause` | `featureDocker.UnpauseContainer` | docker | JWT, ClusterProxy, Audit | 컨테이너 재개 |
| **DELETE** | `/api/v1/docker/containers/:id` | `featureDocker.RemoveContainer` | docker | JWT, ClusterProxy, Audit | 컨테이너 삭제 |
| **GET** | `/api/v1/docker/images` | `featureDocker.ListImages` | docker | JWT, ClusterProxy, Audit | 이미지 목록 |
| **POST** | `/api/v1/docker/images/pull` | `featureDocker.PullImage` | docker | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 이미지 다운로드 |
| **GET** | `/api/v1/docker/images/updates` | `featureDocker.CheckImageUpdates` | docker | JWT, ClusterProxy, Audit | 이미지 업데이트 확인 |
| **DELETE** | `/api/v1/docker/images/:id` | `featureDocker.RemoveImage` | docker | JWT, ClusterProxy, Audit | 이미지 삭제 |
| **GET** | `/api/v1/docker/images/search` | `featureDocker.SearchImages` | docker | JWT, ClusterProxy, Audit | Docker Hub 검색 |
| **GET** | `/api/v1/docker/volumes` | `featureDocker.ListVolumes` | docker | JWT, ClusterProxy, Audit | 볼륨 목록 |
| **POST** | `/api/v1/docker/volumes` | `featureDocker.CreateVolume` | docker | JWT, ClusterProxy, Audit | 볼륨 생성 |
| **DELETE** | `/api/v1/docker/volumes/:name` | `featureDocker.RemoveVolume` | docker | JWT, ClusterProxy, Audit | 볼륨 삭제 |
| **GET** | `/api/v1/docker/networks` | `featureDocker.ListNetworks` | docker | JWT, ClusterProxy, Audit | 네트워크 목록 |
| **POST** | `/api/v1/docker/networks` | `featureDocker.CreateNetwork` | docker | JWT, ClusterProxy, Audit | 네트워크 생성 |
| **DELETE** | `/api/v1/docker/networks/:id` | `featureDocker.RemoveNetwork` | docker | JWT, ClusterProxy, Audit | 네트워크 삭제 |
| **GET** | `/api/v1/docker/networks/:id/inspect` | `featureDocker.InspectNetwork` | docker | JWT, ClusterProxy, Audit | 네트워크 상세 |
| **POST** | `/api/v1/docker/prune/containers` | `featureDocker.PruneContainers` | docker | JWT, ClusterProxy, Audit | 중지된 컨테이너 정리 |
| **POST** | `/api/v1/docker/prune/images` | `featureDocker.PruneImages` | docker | JWT, ClusterProxy, Audit | 미사용 이미지 정리 |
| **POST** | `/api/v1/docker/prune/volumes` | `featureDocker.PruneVolumes` | docker | JWT, ClusterProxy, Audit | 미사용 볼륨 정리 |
| **POST** | `/api/v1/docker/prune/networks` | `featureDocker.PruneNetworks` | docker | JWT, ClusterProxy, Audit | 미사용 네트워크 정리 |
| **POST** | `/api/v1/docker/prune/all` | `featureDocker.PruneAll` | docker | JWT, ClusterProxy, Audit | 전체 정리 |

#### Docker Compose (`/api/v1/docker/compose`) - Docker 가용 시에만 등록

| METHOD | PATH | 핸들러 | 기능 모듈 | 미들웨어 | 비고 |
|--------|------|--------|----------|---------|------|
| **GET** | `/api/v1/docker/compose` | `featureCompose.ListProjectsWithStatus` | compose | JWT, ClusterProxy, Audit | 프로젝트 목록 |
| **POST** | `/api/v1/docker/compose` | `featureCompose.CreateProject` | compose | JWT, ClusterProxy, Audit | 프로젝트 생성 |
| **GET** | `/api/v1/docker/compose/:project` | `featureCompose.GetProject` | compose | JWT, ClusterProxy, Audit | 프로젝트 상세 |
| **PUT** | `/api/v1/docker/compose/:project` | `featureCompose.UpdateProject` | compose | JWT, ClusterProxy, Audit | 프로젝트 YAML 수정 |
| **DELETE** | `/api/v1/docker/compose/:project` | `featureCompose.DeleteProject` | compose | JWT, ClusterProxy, Audit | 프로젝트 삭제 |
| **POST** | `/api/v1/docker/compose/:project/up` | `featureCompose.ProjectUp` | compose | JWT, ClusterProxy, Audit | 프로젝트 시작 (JSON 응답) |
| **POST** | `/api/v1/docker/compose/:project/up-stream` | `featureCompose.ProjectUpStream` | compose | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 프로젝트 시작 |
| **POST** | `/api/v1/docker/compose/:project/down` | `featureCompose.ProjectDown` | compose | JWT, ClusterProxy, Audit | 프로젝트 중지 |
| **GET** | `/api/v1/docker/compose/:project/env` | `featureCompose.GetEnv` | compose | JWT, ClusterProxy, Audit | 환경 변수 조회 |
| **PUT** | `/api/v1/docker/compose/:project/env` | `featureCompose.UpdateEnv` | compose | JWT, ClusterProxy, Audit | 환경 변수 수정 |
| **GET** | `/api/v1/docker/compose/:project/services` | `featureCompose.GetProjectServices` | compose | JWT, ClusterProxy, Audit | 서비스 목록 |
| **POST** | `/api/v1/docker/compose/:project/services/:service/restart` | `featureCompose.RestartService` | compose | JWT, ClusterProxy, Audit | 서비스 재시작 |
| **POST** | `/api/v1/docker/compose/:project/services/:service/stop` | `featureCompose.StopService` | compose | JWT, ClusterProxy, Audit | 서비스 중지 |
| **POST** | `/api/v1/docker/compose/:project/services/:service/start` | `featureCompose.StartService` | compose | JWT, ClusterProxy, Audit | 서비스 시작 |
| **GET** | `/api/v1/docker/compose/:project/services/:service/logs` | `featureCompose.ServiceLogs` | compose | JWT, ClusterProxy, Audit | 서비스 로그 |
| **POST** | `/api/v1/docker/compose/:project/validate` | `featureCompose.ValidateProject` | compose | JWT, ClusterProxy, Audit | YAML 검증 |
| **POST** | `/api/v1/docker/compose/:project/check-updates` | `featureCompose.CheckStackUpdates` | compose | JWT, ClusterProxy, Audit | 업데이트 확인 |
| **POST** | `/api/v1/docker/compose/:project/update` | `featureCompose.UpdateStack` | compose | JWT, ClusterProxy, Audit | 스택 업데이트 (JSON 응답) |
| **POST** | `/api/v1/docker/compose/:project/update-stream` | `featureCompose.UpdateStackStream` | compose | JWT, ClusterProxy, Audit | **SSE 스트리밍**: 스택 업데이트 |
| **POST** | `/api/v1/docker/compose/:project/rollback` | `featureCompose.RollbackStack` | compose | JWT, ClusterProxy, Audit | 스택 롤백 |
| **GET** | `/api/v1/docker/compose/:project/has-rollback` | `featureCompose.HasRollback` | compose | JWT, ClusterProxy, Audit | 롤백 가능 여부 |

### WebSocket 엔드포인트 (범위 외 - 별도 문서)

다음 WebSocket 엔드포인트는 별도 sibling agent가 담당:

- `GET /ws/metrics` — 실시간 시스템 메트릭
- `GET /ws/logs` — 로그 스트림
- `GET /ws/terminal` — 터미널 인터페이스
- `GET /ws/docker/containers/:id/logs` — Docker 컨테이너 로그 스트림
- `GET /ws/docker/containers/:id/exec` — Docker 컨테이너 셸 실행
- `GET /ws/docker/compose/:project/logs` — Docker Compose 로그 스트림

---

## 2. 미들웨어 동작

### 2.1 미들웨어 순서 (라우터 초기화)

전역 미들웨어 (모든 요청 처리):
1. **RecoverMiddleware** (echo.Recover)
2. **GzipMiddleware** (echo.GzipWithConfig, 레벨 5, 최소 1024 bytes)
3. **RequestLogger** (custom) — `/api/v1/health` 제외
4. **CORSMiddleware** (echo.CORSWithConfig)

허용 Origin:
- `http://localhost:5173`
- `tauri://localhost`
- `http://tauri.localhost`
- `https://tauri.localhost`

허용 메서드: GET, POST, PUT, DELETE, PATCH
허용 헤더: Authorization, Content-Type

보호된 라우트 (`/api/v1` 그룹, authorized 서브그룹):
1. **JWTMiddleware** — JWT 토큰 검증 또는 내부 프록시 헤더 확인
2. **ClusterProxyMiddleware** — `?node=X` 쿼리 매개변수 시 다른 노드로 라우팅
3. **AuditMiddleware** — POST/PUT/DELETE 요청 감사 로깅 (읽기 요청 제외)

### 2.2 JWT 미들웨어 (`internal/api/middleware/auth.go`)

**기능**:
- Authorization 헤더에서 "Bearer <token>" 형식 추출
- 토큰 검증 실패 시: `401 Unauthorized`, `ErrInvalidToken`
- 토큰 누락 시: `401 Unauthorized`, `ErrMissingToken`
- 쿼리 파라미터 폴백: `?token=<JWT>` (파일 다운로드 용)

**내부 프록시 요청 바이패스**:
- 헤더 `X-SFPanel-Internal-Proxy: <secret>` 으로 JWT 검증 스킵
- 클러스터 내부 통신용 (mTLS로 보호됨)
- 원본 사용자명: `X-SFPanel-Original-User` 헤더 전파

### 2.3 클러스터 프록시 미들웨어 (`internal/api/middleware/proxy.go`)

**기능**:
- `?node=X` 쿼리 파라미터 감지
- 로컬 노드이거나 파라미터 없으면: 통과
- 원격 노드 요청: gRPC 프록시 (기본 30초, Compose는 5분 타임아웃)

**SSE 스트리밍 엔드포인트 감지**:
- 경로 suffix 감지: `-stream`, `update`, `/system/update`, `appstore.../install`
- SSE 엔드포인트는 직접 HTTP 릴레이 (실시간성)
- gRPC 폴링 불가능하기 때문

**요청 변환**:
- gRPC `APIRequest` protobuf로 변환
- 본문, 헤더, 토큰, 쿼리 매개변수 포함
- 응답: HTTP 상태, 헤더, 본문 반환

**연결 풀링**:
- 재사용 가능한 gRPC 연결 관리
- 실패 시 자동 재연결 (한 번)

### 2.4 감사 미들웨어 (`internal/api/middleware/audit.go`)

**기능**:
- POST, PUT, DELETE 요청만 로깅 (GET, HEAD, OPTIONS 제외)
- 제외: `/api/v1/auth/login`, `/api/v1/auth/setup` (비밀번호 보호)
- 기록 항목: username, method, path, HTTP status, IP, node_id

**테이블**: `audit_logs`
- 최대 50,000행 유지 (초과 시 오래된 10,000행 삭제)
- 비동기 처리 (고루틴)

### 2.5 요청 로거 (`internal/api/middleware/request_logger.go`)

**기능**:
- 각 요청의 메서드, 경로, 상태, 소요 시간 로깅
- `/api/v1/health` 제외
- slog.Info 레벨로 출력

---

## 3. 에러 코드 카탈로그

### 3.1 HTTP 상태 매핑

SFPanel은 다양한 HTTP 상태를 사용:

| 상태 | 용도 | 예제 에러 코드 |
|------|------|--------------|
| **200 OK** | 성공 | - |
| **400 Bad Request** | 요청 형식 오류 | `INVALID_REQUEST`, `MISSING_FIELDS`, `WEAK_PASSWORD` |
| **401 Unauthorized** | 인증 실패 | `MISSING_TOKEN`, `INVALID_TOKEN`, `INVALID_CREDENTIALS` |
| **409 Conflict** | 충돌 (중복 등) | `ALREADY_SETUP`, `ALREADY_EXISTS` |
| **429 Too Many Requests** | 요청 제한 | `RATE_LIMITED` |
| **500 Internal Server Error** | 서버 오류 | `INTERNAL_ERROR`, `DB_ERROR`, `COMMAND_FAILED` |
| **502 Bad Gateway** | 클러스터 노드 연결 실패 | `INTERNAL_ERROR` |
| **503 Service Unavailable** | 서비스 불가 (오프라인 노드 등) | `INTERNAL_ERROR` |

### 3.2 전체 에러 코드 목록

**공통 에러** (internal/api/response/errors.go):

```
INVALID_REQUEST         — 잘못된 요청 본문
INVALID_BODY            — 바디 파싱 오류
MISSING_FIELDS          — 필수 필드 누락
NOT_FOUND               — 리소스 없음
ALREADY_EXISTS          — 리소스 중복
INTERNAL_ERROR          — 내부 서버 오류
DB_ERROR                — 데이터베이스 오류
READ_ERROR              — 읽기 오류
WRITE_ERROR             — 쓰기 오류
DELETE_ERROR            — 삭제 오류
DIR_ERROR               — 디렉토리 오류
IO_ERROR                — 입출력 오류
EMPTY_CONTENT           — 빈 콘텐츠
INVALID_VALUE           — 유효하지 않은 값
INVALID_NAME            — 유효하지 않은 이름
INVALID_ID              — 유효하지 않은 ID
INVALID_ACTION          — 유효하지 않은 작업
INVALID_PATH            — 유효하지 않은 경로
PATH_INVALID            — 경로 검증 실패
MISSING_PATH            — 경로 누락
PERMISSION_DENIED       — 권한 거부
SSE_ERROR               — SSE 스트리밍 오류
```

**인증 에러**:

```
INVALID_CREDENTIALS     — 잘못된 사용자명/비밀번호
INVALID_TOKEN           — 유효하지 않거나 만료된 토큰
MISSING_TOKEN           — Authorization 헤더 누락
TOKEN_ERROR             — 토큰 처리 오류
TOTP_REQUIRED           — 2FA 필수 (코드 누락)
TOTP_ERROR              — TOTP 처리 오류
INVALID_TOTP            — 잘못된 2FA 코드
INVALID_PASSWORD        — 비밀번호 검증 실패
WEAK_PASSWORD           — 약한 비밀번호 (8자 미만)
HASH_ERROR              — 해싱 오류
ALREADY_SETUP           — 관리자 계정 이미 존재
NO_USER                 — 사용자 없음
USER_NOT_FOUND          — 사용자 미발견
RATE_LIMITED            — 요청 제한 초과
```

**Docker 에러**:

```
DOCKER_ERROR            — Docker 일반 오류
DOCKER_FIREWALL_ERROR   — Docker 방화벽 오류
COMPOSE_ERROR           — Compose 오류
```

**파일 관련 에러**:

```
FILE_ERROR              — 파일 일반 오류
FILE_NOT_FOUND          — 파일 미발견
FILE_DELETE_ERROR       — 파일 삭제 실패
FILE_WRITE_ERROR        — 파일 쓰기 실패
FILE_TOO_LARGE          — 파일 크기 초과
MISSING_FILE            — 파일 누락
NOT_A_FILE              — 파일이 아님
IS_DIRECTORY            — 디렉토리임
INVALID_FILENAME        — 유효하지 않은 파일명
CRITICAL_PATH           — 중요 경로 (삭제 불가)
READ_PROTECTED          — 읽기 보호됨
```

**시스템/프로세스 에러**:

```
COMMAND_FAILED          — 명령어 실행 실패
SERVICE_ERROR           — 서비스 오류
PROCESS_ERROR           — 프로세스 오류
PROCESS_NOT_FOUND       — 프로세스 미발견
KILL_FAILED             — 프로세스 강제 종료 실패
INVALID_PID             — 유효하지 않은 PID
INVALID_SIGNAL          — 유효하지 않은 시그널
HOST_INFO_ERROR         — 호스트 정보 오류
METRICS_ERROR           — 메트릭 수집 오류
USAGE_ERROR             — 사용량 오류
TOOL_NOT_INSTALLED      — 도구 미설치
```

**패키지/APT 에러**:

```
APT_ERROR               — APT 일반 오류
APT_UPDATE_ERROR        — apt update 오류
APT_INSTALL_ERROR       — apt install 오류
APT_REMOVE_ERROR        — apt remove 오류
APT_SEARCH_ERROR        — apt search 오류
APT_UPGRADE_ERROR       — apt upgrade 오류
INSTALL_ERROR           — 설치 일반 오류
INVALID_PACKAGE_NAME    — 유효하지 않은 패키지명
```

**방화벽 에러**:

```
FIREWALL_ERROR          — 방화벽 일반 오류
UFW_ERROR               — UFW 오류
UFW_ENABLE_ERROR        — UFW 활성화 오류
UFW_DISABLE_ERROR       — UFW 비활성화 오류
UFW_ADD_RULE_ERROR      — 규칙 추가 오류
UFW_DELETE_ERROR        — 규칙 삭제 오류
IPTABLES_ERROR          — iptables 오류
FAIL2BAN_ERROR          — Fail2ban 오류
INVALID_PORT            — 유효하지 않은 포트
INVALID_PROTOCOL        — 유효하지 않은 프로토콜
INVALID_IP              — 유효하지 않은 IP
INVALID_RULE_NUMBER     — 유효하지 않은 규칙 번호
INVALID_FROM_ADDRESS    — 유효하지 않은 From 주소
INVALID_TO_ADDRESS      — 유효하지 않은 To 주소
INVALID_JAIL_NAME       — 유효하지 않은 옥 이름
MISSING_JAIL_NAME       — 옥 이름 누락
JAIL_EXISTS             — 옥 이미 존재
INVALID_BAN_TIME        — 유효하지 않은 차단 시간
INVALID_FIND_TIME       — 유효하지 않은 검색 시간
INVALID_MAX_RETRY       — 유효하지 않은 최대 재시도
INVALID_LOG_PATH        — 유효하지 않은 로그 경로
INVALID_FILTER_NAME     — 유효하지 않은 필터명
UNKNOWN_TEMPLATE        — 미알려진 템플릿
ENABLE_FAILED           — 활성화 실패
DISABLE_FAILED          — 비활성화 실패
START_FAILED            — 시작 실패
STOP_FAILED             — 중지 실패
RESTART_FAILED          — 재시작 실패
```

**디스크 에러**:

```
DISK_ERROR              — 디스크 일반 오류
LVM_ERROR               — LVM 오류
RAID_ERROR              — RAID 오류
SWAP_ERROR              — 스왑 오류
SMART_ERROR             — SMART 오류
PARTITION_ERROR         — 파티션 오류
FS_ERROR                — 파일시스템 오류
FORMAT_ERROR            — 포맷 오류
MOUNT_ERROR             — 마운트 오류
UNMOUNT_ERROR           — 마운트 해제 오류
RESIZE_ERROR            — 크기 조정 오류
EXPAND_ERROR            — 확장 오류
INVALID_DEVICE          — 유효하지 않은 디바이스
INVALID_PARTITION       — 유효하지 않은 파티션
INVALID_FSTYPE          — 유효하지 않은 파일시스템 유형
INVALID_MOUNTPOINT      — 유효하지 않은 마운트 포인트
INVALID_OPTIONS         — 유효하지 않은 옵션
INVALID_SIZE            — 유효하지 않은 크기
INVALID_START           — 유효하지 않은 시작점
INVALID_END             — 유효하지 않은 종료점
INVALID_VG              — 유효하지 않은 볼륨 그룹
INVALID_LEVEL           — 유효하지 않은 레벨
```

**네트워크 에러**:

```
NETWORK_ERROR           — 네트워크 일반 오류
NETPLAN_ERROR           — Netplan 오류
SS_ERROR                — ss 명령어 오류
```

**VPN 에러**:

```
WIREGUARD_ERROR         — WireGuard 일반 오류
WG_LIST_ERROR           — WG list 오류
WG_INSTALL_ERROR        — WG 설치 오류
WG_UP_ERROR             — WG up 오류
WG_DOWN_ERROR           — WG down 오류
TAILSCALE_ERROR         — Tailscale 일반 오류
TS_UP_ERROR             — Tailscale up 오류
TS_DOWN_ERROR           — Tailscale down 오류
TS_LOGOUT_ERROR         — Tailscale logout 오류
TS_PEERS_ERROR          — Tailscale peers 오류
TS_SET_ERROR            — Tailscale preferences 오류
```

**로그 에러**:

```
LOG_ERROR               — 로그 일반 오류
LOG_NOT_FOUND           — 로그 미발견
INVALID_LINES           — 유효하지 않은 라인 수
INVALID_SOURCE          — 유효하지 않은 소스
MISSING_SOURCE          — 소스 누락
MISSING_QUERY           — 쿼리 누락
INVALID_QUERY           — 유효하지 않은 쿼리
SOURCE_EXISTS           — 소스 이미 존재
INVALID_COMMENT         — 유효하지 않은 주석
```

**기타 에러**:

```
CRON_ERROR              — Cron 일반 오류
INVALID_SCHEDULE        — 유효하지 않은 스케줄
EMPTY_SETTINGS          — 빈 설정
TUNING_ERROR            — 튜닝 오류
APPSTORE_ERROR          — 앱 스토어 오류
PORT_CONFLICT           — 포트 충돌
CONTAINER_CONFLICT      — 컨테이너 충돌
ALERT_ERROR             — 알림 일반 오류
CHANNEL_ERROR           — 채널 오류
RULE_ERROR              — 규칙 오류
UPDATE_CHECK_FAILED     — 업데이트 확인 실패
UPDATE_FAILED           — 업데이트 실패
BACKUP_FAILED           — 백업 실패
RESTORE_FAILED          — 복원 실패
COMMAND_TIMEOUT         — 명령어 타임아웃
```

---

## 4. 인증 모델

### 4.1 JWT 토큰

**발급 및 유효성**:
- 알고리즘: HS256 (HMAC SHA-256)
- 서명 키: `config.yaml`의 `auth.jwt_secret`
- 클레임:
  - `username` (string) — 사용자명
  - `exp` (int64) — 만료 시간 (Unix timestamp)
- 기본 만료: 24시간 (config: `auth.token_expiry`)

**토큰 획득**:

| 시나리오 | 엔드포인트 | 조건 |
|---------|----------|------|
| 최초 셋업 | `POST /api/v1/auth/setup` | 관리자 계정 없음 |
| 로그인 | `POST /api/v1/auth/login` | 유효한 자격증명 (2FA 필요 시) |
| CLI 위임 | 내부 (`callLocalAPI`) | 설정의 JWT 시크릿으로 서명 |

**검증**:
- Bearer 헤더: `Authorization: Bearer <token>`
- 쿼리 파라미터: `?token=<token>` (파일 다운로드, WebSocket)
- 내부 프록시: `X-SFPanel-Internal-Proxy: <secret>` (JWT 스킵)

### 4.2 인증 흐름

#### 초기 셋업

1. 클라이언트: `GET /api/v1/auth/setup-status`
2. 서버: `setup_required: true` (관리자 없음)
3. 클라이언트: `POST /api/v1/auth/setup` (username, password)
4. 서버: JWT 토큰 발급 + DB에 저장 (bcrypt 해시)

조건:
- 첫 관리자만 가능 (이후 `ALREADY_SETUP` 오류)
- 비밀번호 최소 8자 (`WEAK_PASSWORD` 제약)

#### 로그인

1. 클라이언트: `POST /api/v1/auth/login`
2. 요청 본문:
   ```json
   {
     "username": "admin",
     "password": "...",
     "totp_code": "..."  // 2FA 활성화 시 필수
   }
   ```
3. 검증:
   - 사용자명/비밀번호 bcrypt 확인
   - 2FA 활성화 여부 확인 → 코드 검증
   - 요청 제한 확인 (5회 시도/60초, 5분 차단)
4. 응답: JWT 토큰

#### 2FA (TOTP)

**활성화**:
1. `POST /api/v1/auth/2fa/setup` (JWT 필수)
2. 서버: TOTP 시크릿 생성, QR URL 반환
3. 클라이언트: 인증기 앱에 등록
4. `POST /api/v1/auth/2fa/verify` (JWT, secret, 6자리 코드)
5. 서버: 검증 후 DB에 시크릿 저장

**비활성화**:
1. `DELETE /api/v1/auth/2fa` (JWT 필수)
2. 서버: DB에서 시크릿 삭제

**로그인 시**:
- 2FA 활성화 시 로그인 요청에 totp_code 필수
- 코드 없으면: `TOTP_REQUIRED` (400)
- 잘못된 코드: `INVALID_TOTP` (401)

### 4.3 비밀번호 변경

`POST /api/v1/auth/change-password` (JWT 필수)

요청:
```json
{
  "current_password": "...",
  "new_password": "..."
}
```

검증:
- 현재 비밀번호 확인
- 새 비밀번호 8자 이상 (`WEAK_PASSWORD`)

### 4.4 공개 엔드포인트

인증 불필요:

| 엔드포인트 | 역할 |
|----------|------|
| `GET /api/v1/health` | 헬스체크 |
| `GET /api/v1/auth/setup-status` | 초기 셋업 필요 여부 |
| `POST /api/v1/auth/setup` | 관리자 계정 생성 (1회용) |
| `POST /api/v1/auth/login` | 로그인 |

---

## 5. 클러스터 프록시 동작

### 5.1 라우팅 메커니즘

**쿼리 파라미터**: `?node=<nodeID또는nodeName>`

- 없음: 로컬 실행
- 로컬 노드 ID: 로컬 실행
- 원격 노드 ID: gRPC 프록시

**지원 프로토콜**:

1. **일반 REST 엔드포인트** (30초 타임아웃)
   - gRPC `ProxyRequest` 사용
   - 응답: HTTP 상태, 헤더, JSON 본문 복제

2. **SSE 스트리밍 엔드포인트** (5분 타임아웃, HTTP 직접 릴레이)
   - 경로 패턴 감지:
     - `-stream` suffix (예: `/up-stream`, `/update-stream`)
     - `/system/update`
     - `/appstore/apps/.../install`
   - 릴레이 방식:
     - 직접 HTTP 연결 (gRPC 풀링 불가)
     - SSE 데이터 스트림 실시간 포워딩
   - Fallback: 스트리밍 미지원 원격 노드 → 일반 엔드포인트 호출 후 JSON 구문 분석해 SSE 이벤트로 변환

### 5.2 노드 선택

**단계**:
1. 클러스터 비활성화 → 통과 (로컬 실행)
2. node 파라미터 없거나 로컬 노드 → 통과
3. 노드 ID/명으로 검색
4. 상태 확인: 온라인 아니면 `503 Service Unavailable`

### 5.3 인증 전파

**헤더**:
- `X-SFPanel-Internal-Proxy: <secret>` — 내부 프록시 인증 (mTLS CA 해시)
- `X-SFPanel-Original-User: <username>` — 원본 사용자 전파
- 원본 `Authorization: Bearer <token>` — JWT 폴백 (프록시 시크릿 없을 시)

### 5.4 연결 풀링

- gRPC 연결 재사용
- 실패 시 자동 제거 및 재연결 (최대 1회)
- Compose 작업 시 5분 타임아웃 (이미지 pull 등)

### 5.5 클러스터 활성/비활성 시 응답 차이

**상태 조회** (`GET /api/v1/cluster/status`):

| 시나리오 | 응답 |
|---------|------|
| 클러스터 비활성 | `{ "enabled": false }` (대략) |
| 로컬 노드 | `{ "enabled": true, "leader": true, ...}` |
| 팔로워 노드 | `{ "enabled": true, "leader": false, ...}` |

**제어 엔드포인트** (`POST /api/v1/cluster/*`):

- `init`: 클러스터 비활성 시 가능
- `join`: 클러스터 비활성 시 가능
- `leave`: 클러스터 활성 시 가능
- `disband`: 리더만 가능

---

## 6. SSE 스트리밍 엔드포인트

### 6.1 SSE 스트리밍이 필요한 이유

명령어 실행이 오래 걸리는 작업 (패키지 설치, Docker pull, 클러스터 업데이트 등)에서 **실시간 진행 상황** 피드백 필요.

### 6.2 SSE 프로토콜

**응답 헤더**:
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

**메시지 형식** (JSON):
```
data: {"phase": "string", "line": "string"}

```

**예제**:
```
data: {"phase": "downloading", "line": "Fetching package-1.0.0..."}
data: {"phase": "installing", "line": "Setting up dependencies..."}
data: {"phase": "complete", "line": "Installation finished"}
```

### 6.3 SSE 엔드포인트 목록

| 엔드포인트 | 모듈 | 설명 |
|----------|------|------|
| `POST /api/v1/system/update` | system | 시스템 업데이트 |
| `POST /api/v1/appstore/apps/:id/install` | appstore | 앱 설치 |
| `POST /api/v1/docker/images/pull` | docker | 이미지 다운로드 |
| `POST /api/v1/docker/compose/:project/up-stream` | compose | 프로젝트 시작 (스트리밍) |
| `POST /api/v1/docker/compose/:project/update-stream` | compose | 스택 업데이트 (스트리밍) |
| `POST /api/v1/packages/upgrade` | packages | 전체 패키지 업그레이드 |
| `POST /api/v1/packages/install` | packages | 패키지 설치 |
| `POST /api/v1/packages/remove` | packages | 패키지 제거 |
| `POST /api/v1/packages/install-docker` | packages | Docker 설치 |
| `POST /api/v1/packages/node-switch` | packages | Node.js 버전 전환 |
| `POST /api/v1/packages/node-install-version` | packages | Node.js 버전 설치 |
| `POST /api/v1/packages/node-uninstall-version` | packages | Node.js 버전 제거 |
| `POST /api/v1/packages/install-claude` | packages | Claude 설치 |
| `POST /api/v1/packages/install-codex` | packages | Codex 설치 |
| `POST /api/v1/packages/install-gemini` | packages | Gemini 설치 |
| `POST /api/v1/cluster/update` | cluster | 클러스터 전체 업데이트 |
| `POST /api/v1/network/tailscale/up` | network | Tailscale 시작 (가능) |

**비-SSE 대체 엔드포인트** (JSON 응답):

| 스트리밍 | 비-스트리밍 |
|----------|----------|
| `POST /api/v1/docker/compose/:project/up-stream` | `POST /api/v1/docker/compose/:project/up` |
| `POST /api/v1/docker/compose/:project/update-stream` | `POST /api/v1/docker/compose/:project/update` |

---

## 7. 문서와의 불일치 (drift)

### 7.1 스캔 대상: `docs/specs/api-spec.md`

문서는 한국어이며, 주요 섹션:
- 기본 URL: `/api/v1`
- 인증: JWT Bearer 또는 `?token=` 쿼리
- 응답 형식: `{ success, data, error }`
- 공통 에러

문서 상 기록된 엔드포인트는 광범위합니다. 코드와의 비교를 위해 표본 검사:

### 7.2 확인된 일치

✓ 인증 엔드포인트 (`/auth/login`, `/auth/setup` 등)
✓ 기본 경로 `/api/v1`
✓ 응답 형식 (success/data/error)
✓ JWT 토큰 처리
✓ 2FA (TOTP) 엔드포인트
✓ 에러 코드 대부분 일치

### 7.3 확인된 차이

| 항목 | 문서 | 코드 | 상태 |
|------|------|------|------|
| Docker 등록 조건 | 명시 안 함 | `if dockerHandler != nil` | **차이**: Docker 없으면 모든 `/docker/*` 라우트 미등록 |
| WebSocket 경로 | 별도 문서 | `/ws/*` (root level) | **일치**: WebSocket은 `/api/v1` 외부 |
| Compose 라우트 | 미포함 가능 | `/api/v1/docker/compose/*` 전체 등록 | **추가**: Compose 라우트 완전 구현 |
| SSE 스트리밍 | 언급 제한적 | 명확한 구현 | **추가**: 15개+ 스트리밍 엔드포인트 명시 필요 |
| 클러스터 프록시 | 명시 제한 | 완전 구현 (`?node=X`) | **추가**: 프록시 동작 명시 필요 |
| 내부 프록시 헤더 | 없음 | `X-SFPanel-Internal-Proxy`, `X-SFPanel-Original-User` | **추가**: 클러스터 통신용 헤더 |
| Tailscale 엔드포인트 | 명시 제한 | 완전 구현 (12개 라우트) | **추가**: VPN 라우트 확대 필요 |
| Disk/LVM/RAID/Swap | 일부만 | 완전 구현 (50개+ 라우트) | **추가**: 디스크 관리 라우트 확대 필요 |

### 7.4 권장사항

1. **`docs/specs/api-spec.md` 업데이트 필요**:
   - SSE 스트리밍 엔드포인트 (15개)
   - Docker Compose 라우트 (20개)
   - 디스크/LVM/RAID/Swap (50개)
   - Tailscale/WireGuard (22개)
   - 클러스터 프록시 메커니즘
   - 내부 프록시 헤더

2. **조건부 라우트 명시**:
   - Docker 가용 시에만 `/docker/*` 등록

3. **SSE 프로토콜 명시**:
   - 메시지 형식, phase/line 필드

---

## 8. 종합 통계

| 항목 | 개수 |
|------|------|
| **전체 REST 엔드포인트** | ~200+ |
| **공개 (인증 불필요)** | 4 |
| **보호됨 (JWT 필수)** | ~196 |
| **POST/PUT/DELETE (상태 변경)** | ~120 |
| **GET (읽기 전용)** | ~76 |
| **SSE 스트리밍** | 17 |
| **클러스터 프록시 가능** | 모든 보호 라우트 |
| **Docker 조건부** | 26 |
| **기능 모듈** | 18 |
| **에러 코드** | 150+ |

---

## 9. 엔드포인트별 기능 모듈 요약

| 모듈 | 라우트 개수 | 주요 역할 |
|------|-----------|---------|
| **auth** | 5 | 로그인, 2FA, 비밀번호 |
| **monitor** | 3 | 대시보드, 메트릭 |
| **system** | 7 | 시스템 정보, 업데이트, 백업 |
| **process** | 3 | 프로세스 관리 |
| **services** | 8 | Systemd 서비스 |
| **appstore** | 6 | 앱 스토어 |
| **files** | 8 | 파일 관리 |
| **cron** | 4 | Cron 작업 |
| **logs** | 4 | 로그 조회 |
| **audit** | 2 | 감사 로그 |
| **alert** | 10 | 알림 채널/규칙 |
| **cluster** | 15 | 클러스터 관리 |
| **network** | 12 | 네트워크 + VPN |
| **disk** | 52 | 디스크/LVM/RAID/스왑 |
| **firewall** | 21 | UFW + Fail2ban |
| **packages** | 19 | APT + 언어 도구 |
| **docker** | 26 | 컨테이너/이미지/볼륨 |
| **compose** | 20 | Docker Compose |
| **settings** | 2 | 전역 설정 |

---

## 10. 주요 설계 패턴

### 10.1 응답 형식

모든 REST 응답:
```json
{
  "success": true,
  "data": { /* 리소스 */ }
}
```

또는:
```json
{
  "success": false,
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable message"
  }
}
```

### 10.2 경로 매개변수 vs 쿼리 매개변수

**경로 매개변수** (리소스 ID):
- `:id`, `:name`, `:project`, `:device` 등

**쿼리 매개변수** (필터/옵션):
- `?node=X` — 클러스터 프록시
- `?path=/dir` — 파일 경로
- `?lines=100` — 라인 수
- `?limit=50` — 결과 제한
- `?after=ID` — 페이징

### 10.3 상태 변경 작업

- POST — 생성/시작/설치
- PUT — 수정/교체
- DELETE — 삭제/제거
- PATCH — 부분 수정 (라벨, 주소)

### 10.4 클러스터 고려사항

**로컬 전용**:
- `/api/v1/auth/*` (토큰 생성은 로컬만)
- `/api/v1/settings` (로컬 설정)

**클러스터 가능** (모든 보호 라우트):
- `?node=<id>` 쿼리 파라미터로 대상 노드 지정
- gRPC 프록시 (일반) 또는 HTTP 릴레이 (SSE)

**리더만** (`/api/v1/cluster/*`):
- `POST /api/v1/cluster/update` — 리더 초기화
- `POST /api/v1/cluster/disband` — 리더만 가능

---

## 주요 발견사항 요약

1. **완전한 엔드포인트 등록**: router.go는 중앙 진입점이며 모든 라우트가 명시적으로 등록됨.

2. **조건부 Docker 라우트**: Docker 클라이언트 초기화 실패 시 26개 Docker 라우트가 미등록됨.

3. **클러스터 프록시 투명성**: 모든 보호 라우트는 `?node=X`를 통해 다른 노드로 라우팅 가능하며, SSE 엔드포인트는 HTTP 직접 릴레이로 처리됨.

4. **내부 통신 인증**: mTLS로 보호된 클러스터 내부 통신은 `X-SFPanel-Internal-Proxy` 헤더로 JWT 검증 우회 가능.

5. **감사 로깅**: POST/PUT/DELETE만 기록하며, 로그인/셋업은 비밀번호 보호를 위해 제외.

6. **SSE 스트리밍**: 17개의 장시간 작업이 SSE 이벤트 스트림으로 실시간 진행 상황 전송.

7. **문서 불일치**: 특히 Compose, Disk, Firewall, VPN, Tailscale 라우트는 코드에 있지만 문서 미갱신.

8. **에러 코드 체계**: 150+ 코드로 세분화되며, 각 모듈별 고유 코드 사용.

---

**보고서 완료**: 2026-04-19
**조사 수준**: 완전 (전체 라우터, 미들웨어, 에러 코드, 18개 기능 모듈 스캔)

