# SFPanel App Store Design

## Overview

Docker Compose 기반 원클릭 앱 설치 시스템. 별도 GitHub 레포(`svrforum/SFPanel-appstore`)에서 앱 카탈로그를 관리하고, SFPanel UI에서 폼 입력 후 원클릭 설치.

## Architecture

```
svrforum/SFPanel-appstore (GitHub)
    │
    │  raw.githubusercontent.com
    ▼
SFPanel Backend (AppStoreHandler)
    │  메모리 캐시 (1h TTL)
    │
    ├─ GET  /appstore/categories     → categories.json fetch
    ├─ GET  /appstore/apps           → apps/*/metadata.json fetch
    ├─ GET  /appstore/apps/:id       → 개별 metadata + compose fetch
    ├─ POST /appstore/apps/:id/install → .env 생성 + compose up
    └─ GET  /appstore/installed      → settings DB 조회
           │
           ▼
    /opt/stacks/{app-id}/
    ├── docker-compose.yml
    └── .env
```

## App Store Repo Structure

```
svrforum/SFPanel-appstore/
├── categories.json
├── apps/
│   ├── immich/
│   │   ├── metadata.json
│   │   ├── docker-compose.yml
│   │   └── icon.svg
│   ├── nextcloud/
│   │   ├── metadata.json
│   │   ├── docker-compose.yml
│   │   └── icon.svg
│   └── .../
└── README.md
```

### categories.json

```json
[
  { "id": "media", "name": { "ko": "미디어", "en": "Media" }, "icon": "Film" },
  { "id": "cloud", "name": { "ko": "클라우드", "en": "Cloud" }, "icon": "Cloud" },
  { "id": "security", "name": { "ko": "보안", "en": "Security" }, "icon": "Shield" },
  { "id": "monitoring", "name": { "ko": "모니터링", "en": "Monitoring" }, "icon": "Activity" },
  { "id": "dev", "name": { "ko": "개발", "en": "Development" }, "icon": "Code" }
]
```

### metadata.json

```json
{
  "id": "immich",
  "name": "Immich",
  "description": {
    "ko": "셀프 호스팅 사진/동영상 백업 솔루션",
    "en": "Self-hosted photo & video backup solution"
  },
  "category": "media",
  "version": "1.0.0",
  "website": "https://immich.app",
  "source": "https://github.com/immich-app/immich",
  "ports": [2283],
  "env": [
    {
      "key": "DB_PASSWORD",
      "label": { "ko": "데이터베이스 비밀번호", "en": "Database Password" },
      "type": "password",
      "required": true,
      "generate": true
    },
    {
      "key": "UPLOAD_LOCATION",
      "label": { "ko": "업로드 경로", "en": "Upload Location" },
      "type": "path",
      "default": "/opt/stacks/immich/upload"
    },
    {
      "key": "EXTERNAL_PORT",
      "label": { "ko": "외부 포트", "en": "External Port" },
      "type": "port",
      "default": "2283"
    }
  ]
}
```

환경변수 type: `string`, `password`, `port`, `path`, `select`. `generate: true`면 설치 시 랜덤 비밀번호 자동 생성.

docker-compose.yml은 표준 `${VAR}` 문법 사용, .env 파일로 주입.

## Backend

### 새 파일: `internal/api/handlers/appstore.go`

**AppStoreHandler** struct:
- `repoOwner`: "svrforum"
- `repoName`: "SFPanel-appstore"
- `branch`: "main"
- `cache`: 메모리 캐시 (categories, app list, TTL 1h)
- `composePath`: "/opt/stacks"
- `db`: settings DB 접근

### API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/appstore/categories` | 카테고리 목록 |
| GET | `/api/v1/appstore/apps` | 앱 목록 (query: `?category=media`) |
| GET | `/api/v1/appstore/apps/:id` | 앱 상세 (metadata + compose YAML) |
| POST | `/api/v1/appstore/apps/:id/install` | 앱 설치 (body: env values) |
| GET | `/api/v1/appstore/installed` | 설치된 앱 목록 |

### 데이터 흐름 — GitHub Fetch

```
raw.githubusercontent.com/svrforum/SFPanel-appstore/main/categories.json
raw.githubusercontent.com/svrforum/SFPanel-appstore/main/apps/{id}/metadata.json
raw.githubusercontent.com/svrforum/SFPanel-appstore/main/apps/{id}/docker-compose.yml
raw.githubusercontent.com/svrforum/SFPanel-appstore/main/apps/{id}/icon.svg
```

1시간 메모리 캐시. 수동 새로고침 가능.

앱 목록 조회: categories.json 1회 + GitHub Contents API로 `apps/` 디렉토리 목록 → 각 앱 metadata.json fetch. 캐시되므로 반복 요청 없음.

### 설치 흐름

1. metadata.json에서 env 정의 확인
2. 사용자 입력값 + 자동생성 비밀번호로 .env 구성
3. `/opt/stacks/{app-id}/` 디렉토리 생성
4. docker-compose.yml 저장
5. .env 파일 저장
6. `docker compose -f {path} up -d` 실행
7. settings DB에 설치 기록: `appstore_installed_{id}` = `{"version":"1.0.0","installed_at":"..."}`

### 설치 추적

별도 테이블 없이 기존 `settings` 테이블 활용:
- key: `appstore_installed_{app-id}`
- value: JSON `{"version":"1.0.0","installed_at":"2026-03-10T..."}`

삭제 시: 기존 Docker Stacks 삭제 기능 활용 + settings에서 키 제거.

## Frontend

### 새 파일: `web/src/pages/AppStore.tsx`

사이드바에 "앱스토어" 메뉴 추가 (Docker 아래).

### 레이아웃

```
┌─────────────────────────────────────────────────────┐
│ 앱스토어                              🔄 새로고침    │
│ 원클릭으로 셀프호스팅 앱을 설치하세요                   │
├─────────────────────────────────────────────────────┤
│ [전체] [미디어] [클라우드] [보안] [모니터링] [개발]     │
│                                                     │
│ 🔍 앱 검색...                                       │
│                                                     │
│ ┌──────────┐ ┌──────────┐ ┌──────────┐             │
│ │  icon    │ │  icon    │ │  icon    │             │
│ │ Immich   │ │Nextcloud │ │Jellyfin  │             │
│ │ 사진백업  │ │ 클라우드  │ │미디어서버│             │
│ │ [설치됨] │ │ [설치]   │ │ [설치]   │             │
│ └──────────┘ └──────────┘ └──────────┘             │
└─────────────────────────────────────────────────────┘
```

### 설치 다이얼로그

앱 클릭 또는 "설치" 버튼 → Dialog 오픈:
- 앱 아이콘 + 이름 + 설명 + 웹사이트 링크
- 환경변수 입력 폼 (metadata.json의 env 정의에 따라 동적 생성)
- password + generate:true → 랜덤값 자동채움 + 표시/숨김 토글
- port → 숫자 입력
- path → 텍스트 입력
- [취소] [설치하기] 버튼

설치 후: 토스트 성공 알림 + 카드에 "설치됨" 필 표시.

### 디자인 시스템

기존 Toss + Apple 스타일 준수:
- 카드: `bg-card rounded-2xl card-shadow`
- 상태 필: `rounded-full text-[11px]` (설치됨 = 녹색, 미설치 = primary)
- 카테고리 필터: pill 버튼
- 검색: `bg-secondary/50 border-0 rounded-xl`

## Initial Apps (8개)

| App | Category | Ports | Key Env Vars |
|-----|----------|-------|-------------|
| Immich | media | 2283 | DB_PASSWORD, UPLOAD_LOCATION, PORT |
| Jellyfin | media | 8096 | MEDIA_PATH, PORT |
| Nextcloud | cloud | 8080 | DB_PASSWORD, ADMIN_PASSWORD, PORT |
| Vaultwarden | security | 8080 | ADMIN_TOKEN, PORT |
| Uptime Kuma | monitoring | 3001 | PORT |
| Gitea | dev | 3000 | DB_PASSWORD, PORT |
| Portainer | monitoring | 9443 | PORT |
| Nginx Proxy Manager | cloud | 80, 443, 81 | DB_PASSWORD, HTTP_PORT, HTTPS_PORT, ADMIN_PORT |

## Scope

- v1: 설치 + 삭제만
- 추후: 앱 업데이트 감지, 버전 관리
