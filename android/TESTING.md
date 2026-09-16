# 검증 기록 — 2026-09-16

## 자동 검증

| 검사 | 결과 |
| --- | --- |
| Android JVM: URL 정규화·origin 경계·특수 키 인코딩 | 5개 통과 |
| Android Lint | 오류 0, 경고 4 |
| Android debug APK | 빌드 및 APK v2 서명 검증 통과 |
| 프론트엔드 Vitest | 14개 파일, 244개 통과 |
| 프론트엔드 ESLint / TypeScript / Vite | 통과 |
| Playwright 모바일 터미널·AI | 2개 시나리오 통과 |

Playwright는 모든 REST/WS 요청을 fixture로 처리합니다. 두 화면에서 Shift+Tab, Shift+Enter, Ctrl+C 실제 전송 바이트, 조합 해제, 300줄 스크롤, 화면 높이 860→560 변경, 최신 출력 복귀, 48px 터치 영역을 확인합니다. 개발 서버의 React StrictMode에서도 실행했습니다.

Lint 경고는 동적 LAN 서버를 위한 HTTP 허용, 사용자가 설치한 CA 신뢰, API 33 이전 기기에서 무시되는 앱별 언어 설정, 최신 Gradle 버전 알림입니다. HTTP는 사용자 확인 후 연결하며 SSL 오류는 우회하지 않습니다.

## Android 15(API 35) 에뮬레이터

- APK 설치·앱 실행, 한국어 앱 리소스와 서버 목록 표시.
- 테스트 SFPanel health 응답 검증 후 실제 빌드된 React AI 화면 로드.
- 기본 화면 키에서 Shift+Tab(`1b 5b 5a`) 전송 확인.
- 키보드 표시 시 세션 프레임 자동 스크롤로 발생하던 빈 공간을 재현하고 수정 확인.
- 기본 여러 줄 입력창, 키보드 표시, 텍스트 삽입 확인. 전송 데이터에 추가 Enter가 붙지 않음.
- 원래 파일명을 유지하는 시스템 저장창 및 여러 조각을 사용하는 1 MiB Blob 다운로드 확인.
- 저장한 파일과 원본의 SHA-256 모두 `4e29ad18ab9f42d7c233500771a39d7c852b200baf328fd00fbbe3fecea1eb56`.

검증 서버는 임시 fixture이며 운영 서버 설정·컨테이너·AI 계정에 작업을 수행하지 않았습니다.

## 남은 기기 검증

실제 삼성 키보드/Gboard의 한글 조합과 음성 입력, TalkBack 탐색, Android 8~14 및 16 기기, VPN/셀룰러 전환, 오래 백그라운드에 둔 세션, 실제 서버의 TOTP와 CLI별 키 해석은 추가 확인 대상입니다. Shift+Enter 해석은 실행 중인 CLI와 tmux의 확장 키 지원에도 좌우됩니다. [tmux 키 지원 설명](https://github.com/tmux/tmux/wiki/Modifier-Keys)과 [Claude Code 터미널 설정](https://code.claude.com/docs/en/terminal-config)을 참고하세요.
