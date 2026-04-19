# R10 — Frontend SPA

검토 일시: 2026-04-19
범위: `web/src/lib/api.ts`, `App.tsx`, `hooks/useWebSocket.ts`, `pages/{Login,Setup,Terminal,Files,AppStore,AppStoreDetail}.tsx`, `components/NodeSelector.tsx`, `i18n/index.ts`

## P0

### C-1 XSS: marked README sanitize 없이 innerHTML 주입
위치: `pages/AppStoreDetail.tsx:106-135` (`RenderedReadme` 컴포넌트)
외부 GitHub raw URL에서 README 마크다운을 가져와 `marked`로 HTML로 파싱한 뒤 sanitize 없이 React의 HTML 주입 prop (`__html`)으로 렌더링. JWT가 `localStorage`에 저장되므로 XSS 성공 시 즉시 탈취 가능.
수정: `dompurify` 의존성 추가, `DOMPurify.sanitize(md.parse(raw), { ADD_ATTR: ['target'] })` 래핑.

## P1

### I-1 SetupGuard 캐시: `setupChecked` 모듈 변수 재설정 없음
위치: `App.tsx:66-105`, `Setup.tsx:33-36`
모듈 `let setupChecked`가 `true`로 고정. 서버 리셋 후 재-setup 필요해도 현재 탭은 `/setup`로 리다이렉트되지 않음.
수정: `Setup.tsx` 성공 시 콜백으로 재설정 또는 export.

### I-2 Files.tsx 파일 크기 제한 없음
위치: `pages/Files.tsx:196-218`
`handleEditFile`이 `entry.size` 미확인. 수십 MB 바이너리/로그 파일 클릭 → 브라우저 응답 불능.
수정: 1MB 이상 경고 다이얼로그 또는 차단.

### I-3 buildWsUrl JWT 쿼리 파라미터 노출
위치: `lib/api.ts:1520-1540` (R0 F-05 재확인)
브라우저 WS API 제약상 불가피. WS 전용 단기 티켓 엔드포인트 도입 트래킹 이슈 등록 권장.

### I-4 AppStoreDetail Advanced 모드 UI 경고 없음
위치: `pages/AppStoreDetail.tsx:638-683`
compose YAML 자유 편집 가능하나 경고 배너 없음 (R0 P0-C 관련).
수정: 경고 배너 — "잘못된 설정은 호스트 전체에 영향".

### I-5 i18n `escapeValue: false`
위치: `i18n/index.ts:25-27`
React JSX 자동 이스케이프 덕분에 현재 안전하지만, `t()` 결과를 HTML 주입 경로에 넣는 코드가 추가되면 즉시 XSS.
수정: `escapeValue: true` 복원 + HTML 번역은 `Trans` 컴포넌트.

## 양호
- JWT localStorage 단독, sessionStorage/쿠키 혼용 없음
- 401 자동 리다이렉트 (토큰 삭제 + `/login`)
- WS 재연결 지수 백오프 + 30초 상한
- `isCleanedUpRef` cleanup 플래그
- 노드 ID 주입: `local: true` 옵션으로 setup/login 제외
- TOTP 프런트 자동 재시도 없음 (백엔드 rate limit 일관)
- 외부 오리진 요청 없음

## 요약
| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| C-1 | P0 | AppStoreDetail.tsx:106 | marked README XSS (HTML sanitize 없음) |
| I-1 | P1 | App.tsx:66, Setup.tsx:33 | setupChecked 재설정 없음 |
| I-2 | P1 | Files.tsx:196 | 파일 크기 제한 없음 |
| I-3 | P1 | lib/api.ts:1520 | JWT 쿼리 파라미터 (F-05) |
| I-4 | P1 | AppStoreDetail.tsx:638 | advanced 경고 없음 |
| I-5 | P1 | i18n/index.ts:25 | escapeValue false |
