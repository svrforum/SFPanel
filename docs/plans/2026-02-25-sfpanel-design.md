# SFPanel Design Document

**Date**: 2026-02-25
**Status**: Approved

## Overview

SFPanel은 개인 서버 관리자 및 DevOps 팀을 위한 경량 서버 관리 웹 패널이다.
Docker 관리를 핵심 기능으로 두고, 웹사이트 호스팅과 실시간 시스템 모니터링을 제공한다.

## Design Decisions

| 결정 사항 | 선택 | 근거 |
|-----------|------|------|
| 타겟 사용자 | 개인 서버 관리자 + DevOps | Docker 중심 관리가 핵심 |
| 백엔드 언어 | Go | 단일 바이너리, 낮은 리소스, 시스템 제어 최적 |
| 프론트엔드 | React + TypeScript + Tailwind CSS | 생태계 풍부, shadcn/ui, 기여자 풀 넓음 |
| 웹 서버 | Nginx | 업계 표준, 레퍼런스 풍부 |
| 아키텍처 | 올인원 바이너리 | go:embed로 SPA 포함. 설치 한 줄 |
| 내부 DB | SQLite (CGO-free) | 가벼움, 외부 의존성 없음 |
| 인증 | 단일 관리자 + JWT + 2FA (TOTP) | MVP에 적합한 단순 모델 |

## Architecture

```
사용자 브라우저
    │
    ▼ (HTTPS :8443)
┌──────────────────────────────────────┐
│           Go Binary (sfpanel)        │
│  ┌─────────────────────────────────┐ │
│  │  HTTP Router (Echo v4)          │ │
│  │  ├── /api/v1/*    REST API      │ │
│  │  ├── /ws/*        WebSocket     │ │
│  │  └── /*           React SPA     │ │
│  └─────────────────────────────────┘ │
│  ┌──────────┐ ┌──────────────────┐   │
│  │ SQLite   │ │ Service Layer    │   │
│  │ (설정,   │ │ ├── Docker SDK   │   │
│  │  세션,   │ │ ├── Nginx 관리   │   │
│  │  로그)   │ │ ├── SSL (certbot)│   │
│  └──────────┘ │ └── System 메트릭│   │
│               └──────────────────┘   │
└──────────────────────────────────────┘
    │                │
    ▼                ▼
 Docker Socket    Nginx + 시스템
```

## MVP Scope (v0.1)

### 1. 시스템 대시보드
- 실시간 메트릭: CPU, RAM, 디스크 I/O, 네트워크 트래픽
- 수집: gopsutil, 전달: WebSocket (1~3초 간격)
- UI: Recharts 라인 그래프 + 요약 카드
- 호스트 정보: OS, 커널, 업타임, 호스트명

### 2. Docker 관리
- 컨테이너: 목록, 시작/중지/재시작/삭제, 로그(WebSocket), 셸(xterm.js)
- 이미지: 목록, pull, 삭제
- 볼륨: 목록, 생성, 삭제
- 네트워크: 목록, 생성, 삭제
- Compose: YAML 에디터(Monaco) + 배포/중지/삭제, 프로젝트 단위 관리

### 3. 웹사이트 관리
- 사이트 생성: 도메인 → Nginx 가상호스트 자동 생성 → document root 생성
- SSL: certbot CLI로 Let's Encrypt 인증서 발급/갱신, 크론 자동 갱신
- PHP: PHP-FPM 소켓 연결 (다중 버전은 v0.2)
- Nginx 설정: Go text/template 기반 생성, 직접 편집 가능

## API Design

```
# 인증
POST   /api/v1/auth/login
POST   /api/v1/auth/2fa/setup
POST   /api/v1/auth/2fa/verify

# 시스템
GET    /api/v1/system/info
WS     /ws/metrics

# Docker 컨테이너
GET    /api/v1/docker/containers
POST   /api/v1/docker/containers/{id}/start
POST   /api/v1/docker/containers/{id}/stop
POST   /api/v1/docker/containers/{id}/restart
DELETE /api/v1/docker/containers/{id}
WS     /ws/docker/containers/{id}/logs
WS     /ws/docker/containers/{id}/exec

# Docker 이미지/볼륨/네트워크
GET    /api/v1/docker/images
POST   /api/v1/docker/images/pull
DELETE /api/v1/docker/images/{id}
GET    /api/v1/docker/volumes
POST   /api/v1/docker/volumes
DELETE /api/v1/docker/volumes/{name}
GET    /api/v1/docker/networks
POST   /api/v1/docker/networks
DELETE /api/v1/docker/networks/{id}

# Docker Compose
GET    /api/v1/docker/compose
POST   /api/v1/docker/compose
DELETE /api/v1/docker/compose/{project}
POST   /api/v1/docker/compose/{project}/up
POST   /api/v1/docker/compose/{project}/down

# 웹사이트
GET    /api/v1/sites
POST   /api/v1/sites
DELETE /api/v1/sites/{id}
POST   /api/v1/sites/{id}/ssl
GET    /api/v1/sites/{id}/config
PUT    /api/v1/sites/{id}/config
```

## Data Model

```sql
CREATE TABLE admin (
    id         INTEGER PRIMARY KEY,
    username   TEXT NOT NULL UNIQUE,
    password   TEXT NOT NULL,
    totp_secret TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sites (
    id          INTEGER PRIMARY KEY,
    domain      TEXT NOT NULL UNIQUE,
    doc_root    TEXT NOT NULL,
    php_enabled BOOLEAN DEFAULT 0,
    ssl_enabled BOOLEAN DEFAULT 0,
    ssl_expiry  DATETIME,
    status      TEXT DEFAULT 'active',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE compose_projects (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    yaml_path  TEXT NOT NULL,
    status     TEXT DEFAULT 'stopped',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sessions (
    id         INTEGER PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL
);
```

## Tech Stack

| 영역 | 선택 | 버전/비고 |
|------|------|-----------|
| 백엔드 | Go | 1.22+ |
| HTTP 프레임워크 | Echo v4 | WebSocket 내장 |
| DB | SQLite | modernc.org/sqlite (CGO-free) |
| Docker | Docker Go SDK | docker/docker/client |
| 프론트엔드 | React 18 + TypeScript | Vite 빌드 |
| UI | shadcn/ui + Tailwind CSS | |
| 차트 | Recharts | 실시간 메트릭 |
| 터미널 | xterm.js | 컨테이너 셸 |
| 에디터 | Monaco Editor | Compose YAML |
| 인증 | JWT + TOTP | bcrypt 해시 |
| 설정 | YAML | /etc/sfpanel/config.yaml |

## Project Structure

```
SFPanel/
├── cmd/sfpanel/main.go
├── internal/
│   ├── api/
│   │   ├── router.go
│   │   ├── middleware/
│   │   └── handlers/
│   ├── config/config.go
│   ├── db/
│   │   ├── sqlite.go
│   │   └── migrations/
│   ├── docker/client.go
│   ├── nginx/
│   │   ├── manager.go
│   │   └── templates/
│   ├── ssl/certbot.go
│   ├── monitor/collector.go
│   └── auth/
│       ├── jwt.go
│       └── totp.go
├── web/                    # React frontend
│   ├── src/
│   │   ├── pages/
│   │   ├── components/
│   │   ├── hooks/
│   │   ├── lib/
│   │   └── types/
│   └── dist/               # → go:embed
├── configs/config.example.yaml
├── docs/plans/
├── Makefile
├── go.mod
└── CLAUDE.md
```

## Future Roadmap

- v0.2: 파일 관리자, DB 관리 (MariaDB + phpMyAdmin), Multi-PHP, 기본 멀티유저
- v0.3: 방화벽 (UFW) GUI, 백업/복구, 크론잡 관리
- v0.4: 원클릭 앱 스토어, 원격 백업 (S3, Google Drive)
