# SFPanel 프런트엔드 인벤토리

## 1. 디렉토리 레이아웃 (web/src/)

```
main.tsx               # React 19 StrictMode 엔트리
App.tsx                # React Router v7, 41개 라우트
index.css              # Tailwind CSS 4.2.1 기본
pages/                 # 22개 페이지 (모두 lazy)
  Login/Setup/Connect  # 인증 플로우 + Tauri 전용 Connect
  Dashboard            # 실시간 메트릭 + 차트 + 컨테이너/프로세스 요약
  docker/              # Stacks/Containers/Images/Volumes/Networks (5개 탭)
  network/             # Interfaces/WireGuard/Tailscale (3개 탭)
  disk/                # Overview/Partitions/Filesystems/LVM/RAID/Swap (6개 탭)
  firewall/            # Rules/Ports/Fail2ban/Docker/Logs (5개 탭)
  cluster/             # Overview/Nodes/Tokens (3개 탭)
  settings/            # AlertSettings (채널/규칙)
  AppStore / AppStoreDetail
  Files / CronJobs / Logs / Processes / Services / Packages
  Terminal             # xterm.js 멀티 탭
  SettingsTuning       # 커널 튜닝 4개 카테고리
components/            # 공용 22개 + cluster/3개 + ui/14개 shadcn
  Layout / MetricsCard / MetricsChart / NodeSelector
  ContainerShell / ContainerLogs / ComposeEditor / ComposeLogs
  DockerHubSearch / DockerPrune / ErrorBoundary
  MobileHeader / BottomNav / MoreMenu
hooks/
  useWebSocket.ts      # WS 관리 + 자동 재연결 + 지수 백오프
  useVisibleInterval.ts # 탭 가시성 감지
  useIsMobile.ts
lib/
  api.ts               # 싱글턴 ApiClient, 1,543줄, 80+ 메서드
  utils.ts             # formatBytes/formatUptime/cn()
  logParsers.ts        # auth/ufw/sfpanel 구조화 파서
  monaco.ts            # Monaco 언어 로드
types/api.ts           # TypeScript 응답 타입
i18n/
  index.ts             # i18next + languagedetector 초기화
  locales/ko.json      # 1,902줄
  locales/en.json      # 1,902줄
```

## 2. 라우팅 (React Router v7, 41개)

### 비인증 (SetupGuard / TauriGuard 전용)
- `/connect` — Tauri 서버 URL 입력
- `/login` — TOTP 2FA 지원
- `/setup` — 최초 관리자 생성

### 인증 필수 (ProtectedRoute + Layout)
- `/dashboard`
- `/docker` + 5 child (stacks/containers/images/volumes/networks)
- `/docker/stacks/:name` — 스택 상세
- `/cluster` + 3 child (overview/nodes/tokens)
- `/appstore`, `/appstore/:id`
- `/files`, `/cron`, `/logs`, `/processes`, `/services`, `/packages`, `/terminal`
- `/network` + 3 child
- `/disk` + 6 child
- `/firewall` + 5 child
- `/settings` (내부에 Tuning + AlertSettings)

**모든 페이지 `React.lazy()` + `<Suspense fallback={<PageLoader/>}>`**

## 3. 상태 관리

- **Zustand 미사용, Context 미사용** — 순수 React hooks + localStorage
- localStorage 키 7개:
  - `token` — JWT
  - `sfpanel_server_url` — Tauri 서버 주소
  - `sfpanel_current_node` — 클러스터 현재 노드 ID
  - `sfpanel_language` — i18next
  - `sfpanel-sidebar-collapsed`
  - `sfpanel_terminal_tabs` — 터미널 탭 영속화
  - `sfpanel_file_path` — 파일 관리자 마지막 경로

## 4. API 클라이언트 (`lib/api.ts`)

- 싱글턴 `ApiClient` 인스턴스 export `api`
- 기본 URL: `/api/v1` (프로덕션) 또는 Tauri 서버 URL
- `Authorization: Bearer <token>` 자동 주입
- 클러스터 모드: `sfpanel_current_node` 있으면 `?node=<id>` 자동 추가
- 타임아웃 30초 (AbortController)
- 실패 응답 `{success:false,error}` → Error throw
- 80+ 메서드 그룹: auth/system/docker/files/network/disk/firewall/logs/packages/cluster
- 특수 메서드: `runUpdateStream()`, `pullImageStream()`, `downloadBackup()`, `restoreBackup()`, `buildWsUrl()`, `readSSEStream()`
- **React Query / TanStack Query / SWR 미사용** — 직접 fetch + useEffect

## 5. WebSocket 클라이언트 (`useWebSocket` 훅)

```typescript
interface UseWebSocketOptions<T = unknown> {
  url: string            // /ws/... 경로
  onMessage?: (data: T) => void
  autoReconnect?: boolean   // 기본 true
  reconnectInterval?: number // 기본 3000ms, 지수 백오프
}
// 반환: { connected, send, ws }
```

소비 엔드포인트: `/ws/metrics`, `/ws/logs`, `/ws/terminal`, `/ws/docker/containers/:id/logs`, `/ws/docker/containers/:id/exec`, `/ws/docker/compose/:project/logs`

## 6. i18n

- i18next + react-i18next + i18next-browser-languagedetector
- ko / en 2개 언어, `fallbackLng: 'en'`
- 감지 순서: localStorage → 브라우저
- 리소스: `ko.json` / `en.json` 각 1,902줄, 계층적 키

## 7. 빌드 (Vite 7)

- 플러그인: `@vitejs/plugin-react`, `@tailwindcss/vite`, `vite-plugin-pwa`
- 수동 청크 6개: `react-vendor`, `ui-vendor`, `xterm`, `i18n`, `uplot`, `monaco`
- 출력: `web/dist/` (~16MB)
- Go 번들링: `web.go`에서 `//go:embed all:web/dist` → 단일 바이너리
- dev server: `:5173`, API/WS: `:8443` (config default)
- PWA 서비스워커 (monaco/xterm 워커 파일 캐시 제외)

## 8. Desktop (Tauri 2, `desktop/`)

- 프로젝트: SFPanel v0.6.2, 식별자 `com.sfpanel.desktop`
- 프런트엔드 주입: `../../web/dist`
- dev URL: `http://localhost:5173`
- `beforeDevCommand`, `beforeBuildCommand`으로 `cd ../web && npm run dev/build`
- 윈도우: 1280x800 기본, 최소 900x600, 중앙 정렬
- CSP: `connect-src 'self' http: https: ws: wss:`, `script-src` jsdelivr 허용
- 플랫폼: Windows exe / macOS dmg+app / Linux deb+AppImage
- Tauri 감지: 프런트엔드가 `window.__TAURI_INTERNALS__` 체크, `api.isTauri` 플래그

## 9. 무거운 의존성 / 번들 최적화

| 패키지 | 크기(대략) | 비고 |
|--------|-----------|------|
| `monaco-editor` | ~4MB | lazy, 청크 분리 |
| `@xterm/xterm` + addons | ~500KB | 청크 분리 |
| `uplot` | ~100KB | 시계열 차트 |
| `react+react-dom` | ~400KB | |
| `react-router-dom` | ~80KB | |
| `lucide-react` | ~200KB | 아이콘 |
| `i18next` + react-i18next | ~150KB | |
| `sonner` | ~50KB | 토스트 |

## 10. 린팅/테스트

- ESLint 9 flat config + typescript-eslint + react-hooks + react-refresh
- `tsc -b`로 타입 체크 (빌드 전)
- **Vitest 미설정, Playwright 미설정** — 자동화 테스트 없음

## 11. 외부 서비스 호출

- **없음** — 프런트엔드는 전적으로 SFPanel 백엔드(`/api/v1`, `/ws/`)만 호출
- QR 코드: `qrcode.react` 클라이언트 렌더링
- 마크다운: `marked` 클라이언트 렌더링
- Sentry/DataDog/GA 등 외부 텔레메트리 없음

## 12. 스펙 문서와의 편차 (`docs/specs/frontend-spec.md` 1,033줄)

**일치:** 라우팅, 페이지 목록, API 메서드, shadcn/ui, i18n, Tauri, WS 엔드포인트

**누락/부족:**
- Vite 수동 청크 6개 구성 미기재
- PWA(`vite-plugin-pwa`) 상세 설정 미기재
- 모바일 컴포넌트 (BottomNav, MobileHeader) 간략 언급만
- 클러스터 UI 컴포넌트(ClusterSidebar, TreePanel 등) 상세 부족
- 실제 번들 크기(~16MB) 미기재
- Tauri `beforeDevCommand`/`beforeBuildCommand` 미명시
- `logParsers.ts` 인터페이스 상세 없음
- 실제로는 **Zustand 없음** — 문서에 상태관리 라이브러리 언급이 있다면 수정 필요

**신규 기록 필요:**
- `ApiClient`가 싱글턴 패턴 (클래스 인스턴스 export)
- 노드 ID 쿼리 파라미터 자동 주입
- `readSSEStream()` SSE 헬퍼
- localStorage 7개 키 (설정 영속화)
- `__TAURI_INTERNALS__` 감지 패턴
