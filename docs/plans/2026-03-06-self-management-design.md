# SFPanel 셀프 관리 기능 설계

## 개요

SFPanel의 웹 UI에서 패널 업데이트, 설정 백업/복원, 업데이트 알림을 제공.

## 1. 웹 업데이트

### 엔드포인트

- `GET /api/v1/system/update-check` — GitHub Release API로 최신 버전 조회
  - 응답: `{ version, current_version, update_available, release_notes, published_at }`
- `POST /api/v1/system/update` — SSE 스트리밍으로 업데이트 실행
  - SSE 이벤트: `downloading`, `extracting`, `replacing`, `restarting`, `complete`

### 업데이트 흐름

1. GitHub API (`https://api.github.com/repos/sfpanel/sfpanel/releases/latest`) 조회
2. tar.gz 다운로드 (아키텍처별: `sfpanel_{version}_linux_{amd64|arm64}.tar.gz`)
3. 바이너리 추출 → 임시 파일에 쓰기
4. 현재 바이너리를 `.bak`으로 백업
5. 임시 파일 → 현재 경로로 rename
6. `systemctl restart sfpanel` 실행
7. 프론트엔드: 연결 끊김 감지 → 폴링으로 재연결 대기 → 새로고침

### 안전장치

- 업데이트 전 자동 백업 (DB + config)
- 바이너리 .bak 보관 (롤백 가능)
- http.Client에 30초 타임아웃

## 2. 설정 백업/복원

### 엔드포인트

- `POST /api/v1/system/backup` — tar.gz 파일 생성 후 다운로드
  - Content-Type: application/gzip
  - 파일명: `sfpanel-backup-{YYYYMMDD-HHMMSS}.tar.gz`
- `POST /api/v1/system/restore` — multipart/form-data로 tar.gz 업로드 + 복원

### 백업 대상

- `sfpanel.db` (SQLite DB)
- `config.yaml` (설정 파일)

### 복원 흐름

1. 업로드된 tar.gz 검증 (필수 파일 존재 여부)
2. 현재 DB/config를 `.bak`으로 백업
3. tar.gz에서 추출하여 교체
4. `systemctl restart sfpanel`

## 3. 업데이트 알림

### 구현

- `monitor/` 패키지에 업데이트 체커 추가
- 1시간 간격으로 GitHub API 폴링, 결과 캐시
- `/api/v1/system/overview` 응답에 `update_available` 필드 추가

### UI

- 대시보드: 업데이트 배너 (새 버전 있을 때만)
- 사이드바: 설정 링크에 파란 점 배지
- Settings 페이지: 업데이트 섹션 (버전 확인 + 업데이트 버튼 + 백업/복원)

## UI 배치

Settings 페이지에 3개 카드 추가:
1. **패널 업데이트** — 현재 버전, 최신 버전, 업데이트 버튼, 릴리즈 노트
2. **설정 백업** — 백업 다운로드 버튼, 복원 업로드 영역
3. 기존 시스템 정보 카드는 유지
