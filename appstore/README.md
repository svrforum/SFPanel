# SFPanel App Store

SFPanel 앱스토어 레포지토리. 이 저장소의 앱 정의를 기반으로 SFPanel에서 원클릭 설치가 가능합니다.

## 저장소 구조

```
├── index.json                   # 앱 ID 목록 (필수 — 새 앱 추가 시 여기에도 등록)
├── categories.json              # 카테고리 목록
└── apps/
    └── {app-id}/
        ├── metadata.json        # 앱 메타데이터 (필수)
        ├── docker-compose.yml   # Docker Compose 파일 (필수)
        └── icon.svg             # 앱 아이콘 (선택 — metadata.json에 icon URL 지정 가능)
```

## 새 앱 추가 방법

### 1. 앱 디렉토리 생성

`apps/` 아래에 앱 ID로 디렉토리를 생성합니다.

**앱 ID 규칙:** 소문자 + 숫자 + 하이픈(`-`), 최대 50자. 정규식: `^[a-z0-9][a-z0-9_-]{0,49}$`

```bash
mkdir apps/my-app
```

### 2. metadata.json 작성

```jsonc
{
  "id": "my-app",                          // 앱 ID (디렉토리명과 동일)
  "name": "My App",                        // 표시 이름
  "description": {                         // 다국어 설명 (ko, en 필수)
    "ko": "한글 설명",
    "en": "English description"
  },
  "category": "dev",                       // 카테고리 ID (아래 목록 참조)
  "version": "1.0.0",                      // 앱스토어 패키지 버전 (실제 앱 버전은 GitHub Releases에서 자동 가져옴)
  "website": "https://example.com",        // 공식 웹사이트
  "source": "https://github.com/org/repo", // GitHub 소스 저장소 (버전 자동 감지 + README 표시에 사용)
  "icon": "https://...svg or png",         // (선택) 아이콘 URL. 미지정 시 apps/{id}/icon.svg 사용
  "ports": [8080],                         // 사용하는 포트 목록
  "features": [                            // (선택) 주요 기능 카드 (최대 4개 권장)
    {
      "title": { "ko": "기능명", "en": "Feature" },
      "description": { "ko": "설명", "en": "Description" },
      "icon": "🚀"                         // 이모지 아이콘
    }
  ],
  "env": [                                 // .env 파일에 들어갈 환경변수 정의
    {
      "key": "PORT",                       // 환경변수 키
      "label": { "ko": "외부 포트", "en": "External Port" },
      "type": "port",                      // 입력 타입 (아래 참조)
      "default": "8080"                    // 기본값
    },
    {
      "key": "DB_PASSWORD",
      "label": { "ko": "DB 비밀번호", "en": "DB Password" },
      "type": "password",
      "required": true,                    // 필수 여부
      "generate": true                     // true면 설치 시 32자 랜덤 생성
    },
    {
      "key": "STORAGE_TYPE",
      "label": { "ko": "저장소 타입", "en": "Storage Type" },
      "type": "select",
      "default": "local",
      "options": ["local", "s3"]           // select 타입일 때 선택지
    }
  ]
}
```

### 3. docker-compose.yml 작성

**반드시 공식 GitHub 저장소의 `docker-compose.yml`을 기반으로 작성하세요.** 임의로 새로 작성하면 healthcheck 누락, 의존성 순서 오류 등으로 설치가 실패할 수 있습니다.

#### 작성 절차

1. 공식 저장소의 `docker-compose.yml` (또는 `docker-compose.prod.yml`) 원본을 가져옵니다
2. SFPanel 앱스토어에 맞게 **최소한의 수정만** 적용합니다:
   - 사용자가 변경할 포트를 `${PORT:-기본값}` 형태로 환경변수화
   - `generate: true`로 자동 생성할 비밀번호를 `${DB_PASSWORD}` 등으로 환경변수화
   - optional 서비스 (OnlyOffice, SSO 등 profile 기반 서비스)는 제거하여 간소화
   - 호스트 바인드 마운트(`./data:/data`)는 named volume으로 변경
3. **절대 변경하지 말 것:**
   - `healthcheck` 설정 — 원본 그대로 유지 (누락 시 `depends_on: condition: service_healthy` 실패)
   - `depends_on` 의존성 구조 — 원본의 순서와 조건을 그대로 유지
   - `environment` 기본값 — 앱이 필요로 하는 환경변수를 임의로 제거하지 않음
   - `command` 설정 — 원본에 있는 커스텀 명령은 그대로 유지

#### 예시

```yaml
services:
  my-app:
    image: org/my-app:latest
    container_name: my-app
    restart: unless-stopped
    ports:
      - "${PORT:-8080}:80"
    environment:
      - DB_PASSWORD=${DB_PASSWORD}
    volumes:
      - my-app-data:/data
    healthcheck:                    # 공식 compose에서 그대로 복사!
      test: ["CMD", "curl", "-f", "http://localhost:80/health"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  my-app-data:
```

**규칙:**
- **공식 docker-compose.yml 기반 필수** — 직접 작성 금지
- `healthcheck`는 원본에서 반드시 복사 — 누락 시 의존 서비스 시작 실패
- `container_name`은 앱별 고유 접두사 사용 (예: `fh-api`, `npg-db`)
- 볼륨은 named volume 사용 (호스트 바인드 마운트 지양)
- `restart: unless-stopped` 권장
- 포트는 `${PORT:-기본값}:컨테이너포트` 형태로 사용자가 변경 가능하게

### 4. (선택) 아이콘 추가

- `apps/{id}/icon.svg` 파일로 추가하거나
- `metadata.json`의 `icon` 필드에 외부 URL 지정

SVG 권장. 48x48 ~ 128x128 크기에 최적화.

## 카테고리 목록

| ID | 한글 | English |
|----|------|---------|
| `media` | 미디어 | Media |
| `cloud` | 클라우드 | Cloud |
| `security` | 보안 | Security |
| `monitoring` | 모니터링 | Monitoring |
| `dev` | 개발 | Development |

새 카테고리가 필요하면 `categories.json`에 추가합니다:

```json
{
  "id": "new-category",
  "name": { "ko": "새 카테고리", "en": "New Category" },
  "icon": "Folder"
}
```

## env 타입 설명

| type | 설명 | UI 동작 |
|------|------|---------|
| `text` | 일반 텍스트 | 텍스트 입력 |
| `port` | 포트 번호 | 숫자 입력 |
| `password` | 비밀번호 | 마스킹 입력 + 눈 토글 |
| `select` | 선택형 | 드롭다운 (`options` 필드 필요) |

## 새 앱 등록 (index.json)

`index.json`에 새 앱 ID를 추가해야 SFPanel이 인식합니다. **이 파일을 수정하지 않으면 앱이 표시되지 않습니다.**

```json
["filehatch","gitea","immich","jellyfin","my-new-app","nextcloud","..."]
```

알파벳 순 정렬을 권장합니다.

## 동작 방식

SFPanel이 이 저장소를 사용하는 흐름:

1. **캐시 로드** — `index.json` + 각 앱의 `metadata.json`을 raw URL로 가져옴 (1시간 캐시, GitHub API 미사용)
2. **버전 감지** — `source` 필드의 GitHub 저장소에서 `/releases/latest` 리다이렉트로 최신 태그 자동 감지
3. **README 표시** — `source` 필드의 GitHub 저장소 README.md를 가져와 상세 페이지에 표시 (main → master → develop 순으로 시도)
4. **설치 전 검증** — 포트 충돌, 컨테이너 이름 충돌, 디렉토리 존재 여부 사전 체크
5. **설치** — `docker-compose.yml` 다운로드 → `/opt/stacks/{app-id}/`에 저장 → `.env` 생성 → SSE 스트리밍으로 `docker compose pull` + `up -d` 실행

## 예시: 기존 앱 참조

새 앱 추가 시 기존 앱을 참조하세요:

- **단순 (환경변수 적음)**: `apps/uptime-kuma/`
- **비밀번호 자동 생성**: `apps/vaultwarden/` (`generate: true`)
- **다중 서비스 + DB**: `apps/nextcloud/`, `apps/immich/`
- **포트 여러 개**: `apps/nginx-proxy-manager/`
