# SFPanel 모바일 반응형 UI/UX 설계

**목표:** 스마트폰(375~430px)에서 최적의 서버 관리 경험 제공. 기존 데스크톱 UI를 유지하면서 모바일 전용 뷰를 추가.

**브레이크포인트:** `<768px` = 모바일, `≥768px` = 데스크톱 (Tailwind `md:` 기준)

**현재 상태:** 모바일 대응 ~25%. 사이드바/테이블 완전 데스크톱 전용.

---

## 1. 바텀 네비게이션

모바일에서 사이드바를 숨기고 하단 고정 바텀탭 5개 표시.

| 탭 | 아이콘 | 경로 | 비고 |
|---|--------|------|------|
| 대시보드 | LayoutDashboard | `/dashboard` | 상태 확인 |
| Docker | Container | `/docker` | 핵심 관리 |
| 터미널 | Terminal | `/terminal` | 긴급 대응 |
| **컨텍스트** | — | — | 아래 참조 |
| 더보기 | Menu | 바텀시트 | 나머지 메뉴 |

### 4번 탭 (컨텍스트 탭)
- **싱글 노드**: 프로세스 (Activity 아이콘, `/processes`)
- **클러스터 모드**: 노드 선택 (Server 아이콘, 바텀시트로 노드 목록)
  - 현재 노드명 표시, 탭하면 노드 목록 바텀시트
  - 노드 탭 하나로 즉시 전환

### 바텀탭 스펙
- 높이: `56px` + `env(safe-area-inset-bottom)`
- 활성 탭: primary 색상 (`#3182f6`)
- 비활성: `text-muted-foreground`
- `md:hidden` (데스크톱에서 숨김)

---

## 2. 모바일 레이아웃 구조

```
┌─────────────────────────┐
│ 헤더 (제목 + 액션)       │  44px
├─────────────────────────┤
│                         │
│     콘텐츠 영역          │  flex-1, overflow-y-auto
│     (패딩 p-4)          │
│                         │
├─────────────────────────┤
│ 바텀탭 (5개)             │  56px + safe-area
└─────────────────────────┘
```

### 렌더링 분기
```
모바일(<768px):  MobileHeader + 콘텐츠(카드뷰) + BottomNav
데스크톱(≥768px): Sidebar + 콘텐츠(테이블뷰) + (바텀탭 숨김)
```

### Layout.tsx 변경
- 사이드바: `hidden md:flex`
- 메인 패딩: `p-4 md:p-8`
- 바텀 패딩: `pb-[calc(56px+env(safe-area-inset-bottom))] md:pb-0`
- NodeSelector: 데스크톱만 표시 (`hidden md:block`)

---

## 3. 페이지별 모바일 최적화

### Tier 1 — 핵심 (모바일에서 가장 자주 사용)

| 페이지 | 모바일 변경사항 |
|--------|----------------|
| 대시보드 | 메트릭 카드 1열, 차트 가로 스크롤, 시스템 정보 아코디언 |
| Docker 컨테이너 | 테이블 → 카드 리스트 (이름+상태+액션), 스택 접기 유지 |
| Docker 스택 | 스택 카드 리스트, 에디터 풀스크린 모달 |
| 터미널 | 풀스크린 (바텀탭 숨김), 키보드 대응 높이 조정 |
| 프로세스 | 테이블 → 카드 (이름+CPU+MEM+kill 버튼) |

### Tier 2 — 자주 사용

| 페이지 | 모바일 변경사항 |
|--------|----------------|
| 파일 관리 | 파일 목록 카드, 에디터 풀스크린 모달 |
| 서비스 | 카드 리스트 (이름+상태+시작/중지 버튼) |
| 로그 | 소스 선택 드롭다운으로 변경, 로그 뷰어 풀폭 |
| 방화벽 | 테이블에 overflow-x-auto + 열 숨김 |
| 클러스터 | 노드 카드 리스트, 오버뷰 메트릭 1열 |

### Tier 3 — 가끔 사용 (최소 대응)

| 페이지 | 모바일 변경사항 |
|--------|----------------|
| 네트워크/VPN | overflow-x-auto, 패딩 축소 |
| 디스크 | overflow-x-auto, 패딩 축소 |
| 패키지 | 이미 반응형 (변경 없음) |
| 크론 | overflow-x-auto, 패딩 축소 |
| 앱스토어 | 이미 반응형 (변경 없음) |
| 설정 | 이미 반응형 (변경 없음) |

---

## 4. 공통 모바일 컴포넌트

### 4-1. `BottomNav.tsx`
- 5개 탭 고정 바텀바, `md:hidden`
- 클러스터 모드 감지 → 4번 탭 자동 전환
- 현재 경로 기반 활성 탭 하이라이트
- safe-area-inset-bottom 대응

### 4-2. `MobileHeader.tsx`
- 페이지 제목 + 우측 액션 버튼
- `md:hidden` (데스크톱에서는 기존 페이지 내 헤더)
- 높이 44px 고정, 상단 고정 (sticky)

### 4-3. `BottomSheet.tsx`
- "더보기" 메뉴 + 노드 선택에 재사용
- 배경 오버레이 + 드래그로 닫기
- shadcn의 Drawer 컴포넌트 활용 (vaul 라이브러리)

### 4-4. `MobileCard.tsx`
- 테이블 행 대체 카드 (컨테이너/프로세스/서비스용)
- 좌: 이름+상태필, 우: 액션 버튼
- `md:hidden` (데스크톱에서는 기존 테이블)

---

## 5. 터치 및 인터랙션

### 터치 영역
- 모든 인터랙티브 요소 최소 높이 44px (Apple HIG)
- 바텀탭 아이콘 터치 영역 48×48px
- 카드 액션 버튼 40×40px 이상

### 스와이프 제스처
- 컨테이너/서비스 카드: 좌 스와이프 → 중지/삭제 액션 노출
- 바텀 시트: 아래로 드래그 닫기

### 키보드 대응
- 터미널: 소프트 키보드 올라올 때 바텀탭 숨김 + xterm 높이 자동 조정
- 검색 인풋 포커스 시 자동 스크롤

### Pull-to-Refresh
- 대시보드, Docker 컨테이너, 프로세스 페이지
- `overscroll-behavior-y: contain` + 터치 이벤트 감지

---

## 6. PWA 지원

### 매니페스트 (`web/public/manifest.json`)
```json
{
  "name": "SFPanel",
  "short_name": "SFPanel",
  "description": "Server Management Panel",
  "start_url": "/dashboard",
  "display": "standalone",
  "background_color": "#ffffff",
  "theme_color": "#3182f6",
  "icons": [
    { "src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png" }
  ]
}
```

### Service Worker (vite-plugin-pwa)
- **캐시 전략**: 앱 셸(HTML/CSS/JS) 캐시 + API는 네트워크 우선
- **오프라인**: 앱 셸 표시 + "오프라인" 배너 (API 호출 불가 표시)
- **업데이트**: 새 버전 감지 시 "업데이트 가능" 토스트 → 클릭 시 새로고침

### 홈 화면 추가
- iOS Safari: "홈 화면에 추가" 지원 (`apple-touch-icon`, `apple-mobile-web-app-capable`)
- Android Chrome: 자동 설치 프롬프트 (매니페스트 기반)
- standalone 모드에서 상단 status bar 색상 = theme_color

### 앱 아이콘
- SFPanel 로고 기반 192x192, 512x512 PNG
- maskable 아이콘 포함 (Android adaptive icon 대응)

---

## 7. 구현 순서

### Phase 1 — 기반 인프라
1. Layout.tsx 수정 (사이드바 `hidden md:flex`, 패딩 반응형)
2. BottomNav.tsx 생성
3. BottomSheet.tsx 생성
4. safe-area 대응
5. PWA 매니페스트 + Service Worker 설정

### Phase 2 — Tier 1 핵심 페이지 (5개)
1. 대시보드 모바일 뷰
2. Docker 컨테이너 카드 리스트
3. Docker 스택 모바일 뷰
4. 터미널 풀스크린 모드
5. 프로세스 카드 리스트

### Phase 3 — Tier 2 페이지 (5개)
1. 파일 관리 모바일 뷰
2. 서비스 카드 리스트
3. 로그 뷰어 모바일 뷰
4. 방화벽 테이블 스크롤
5. 클러스터 모바일 뷰

### Phase 4 — Tier 3 + 터치 UX
1. 나머지 페이지 overflow-x-auto + 패딩 축소
2. 스와이프 액션
3. Pull-to-Refresh
4. 키보드/터미널 대응

---

## 8. 하지 않는 것 (YAGNI)

- 네이티브 앱 래퍼 (Capacitor/React Native)
- 가로 모드 전용 레이아웃
- 페이지 간 스와이프 네비게이션
- 오프라인 데이터 동기화
- 푸시 알림 (별도 설계 필요)
