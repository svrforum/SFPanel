# SFPanel Android

SFPanel 서버에 연결하는 Android 앱입니다. 서버 연결·코딩 도구는 Android 기본 UI로 제공하고, 관리 기능은 서버의 반응형 웹 화면을 사용합니다. 서버별 이름과 주소를 저장하며, 대시보드를 기본 시작 화면으로 사용하며, 전체 기능 메뉴에서 관리 화면과 코딩 화면으로 이동합니다. 직접 선택한 시작 화면은 유지합니다.

## 주요 기능

- **전체 관리 기능:** 상단 `전체 기능`에서 대시보드, 파일, Docker·Compose, 앱 스토어, 서비스, 프로세스, 예약 작업, 로그, 네트워크·VPN, 방화벽, 디스크, 패키지, 클러스터, 서버 설정, AI, 터미널을 검색하고 바로 엽니다. 기존 웹 화면의 상세 탭·작업·모바일 하단 탐색도 유지합니다.
- **터미널 / AI 작업 공간:** 상단 탐색 48dp와 하단 입력 키 48dp만 고정합니다. AI 소개·도구 상태는 접어 출력 공간을 확보하고, `⋯ → AI 도구·계정 설정 열기/닫기`로 펼칩니다. 세션 탭과 새 세션 버튼은 계속 표시하며, `⋯ → 세션 관리`로 세션 작업 메뉴를 엽니다.
- **특수 키:** 한 줄에 입력·Shift·Ctrl·Esc·Tab·Enter·키를 표시합니다. `키`에서 방향키·Alt·페이지 이동·Ctrl 조합을 엽니다. Shift·Ctrl·Alt를 조합해 화면 키에 적용합니다. Shift+Tab, Shift+Enter, 방향키, Page Up/Down, Home/End, Ctrl+C를 지원합니다. 조합 상태는 다음 화면 키를 누르면 해제됩니다. 휴대폰 키보드의 문자 대소문자는 키보드 자체 Shift로 선택합니다.
- **출력 탐색:** 손가락으로 위아래로 밀거나 `키` 메뉴의 `기록 위로 / 아래로`를 사용합니다. 일반 화면에서는 xterm 출력 기록을 이동하고, 전체 화면 또는 마우스 추적 프로그램에서는 터미널 휠 입력을 전달합니다. `PgUp / PgDn`은 실행 중인 프로그램으로 전달됩니다. `최신 출력`으로 돌아갈 수 있습니다. 마우스 추적이 없는 전체 화면에서는 xterm의 방향키 변환을 사용하므로 이동 방식은 실행 중인 프로그램에 따라 다릅니다.
- **프롬프트 작성:** Android 여러 줄 편집창으로 한글·음성 입력·선택·붙여넣기를 사용하고, xterm의 `paste()`로 삽입합니다. Enter를 덧붙이지 않습니다. 붙여넣기 보호를 지원하지 않는 셸에서는 포함된 줄바꿈이 실행될 수 있으므로 입력 내용을 확인하세요.
- **출력 읽기 / 검색:** 최근 500줄을 선택 가능한 기본 텍스트 화면에서 읽고 복사합니다. 검색은 활성 xterm 버퍼의 이전/다음 일치 항목으로 이동합니다.
- **기존 관리 기능:** 로그인·TOTP·노드 선택·Docker·Compose·파일·네트워크 등은 서버의 UI와 API를 사용합니다. 별도 Android CORS 설정은 필요하지 않습니다.
- **파일:** 시스템 파일 선택기로 업로드하고 저장 위치 선택기로 다운로드합니다. HTTP 다운로드는 스트리밍하고, 브라우저 Blob 내보내기는 192 KiB씩 나누어 저장합니다. 원본 Blob을 생성하는 서버 웹 화면의 메모리 사용량은 기존과 같습니다.
- **접근성:** 48dp 이상 기본 터치 영역, 텍스트가 있는 조작 버튼, TalkBack 레이블·상태 안내·기본 텍스트 읽기, 시스템 글자 크기 + 100/120/140% 읽기 배율, 키보드·시스템 바 여백 처리를 제공합니다. 한국어와 영어를 지원합니다.

화면 예시: [전체 기능](docs/all-features.png) · [키보드와 터미널](docs/terminal-keyboard.png). [검증 기록](TESTING.md).

## 연결

1. Android 8.0(API 26) 이상 기기에 APK를 설치합니다.
2. 서버 주소를 포트까지 입력합니다. 예: `https://panel.example.com:3628`.
3. 공개 health API로 SFPanel 응답을 확인한 뒤 로그인합니다.
4. 상단 `전체 기능`에서 원하는 관리 기능이나 `AI 코딩`·`터미널`을 엽니다. 시작 화면은 `보기 설정`에서 바꿀 수 있습니다.

AI 기능은 SFPanel v0.73.0 이상의 `/ai` 화면과 tmux가 필요합니다. 앱은 `data-terminal-session`에 노출된 기존 xterm 참조를 사용합니다. 향후 서버가 이 계약을 바꾸면 앱 코딩 도구도 함께 갱신해야 합니다. 웹 터미널의 새 Shift/스크롤 키 바와 StrictMode 수정은 이 저장소의 웹 빌드를 서버에 반영해야 브라우저에서도 사용할 수 있습니다. Android 앱은 자체 화면 키를 제공하므로 v0.73.0 서버에서도 사용 가능합니다.

### HTTPS와 기기 데이터

사설 CA 또는 자체 서명 인증서는 첫 연결 시 서버 주소·발급자·만료일·SHA-256 지문을 표시합니다. 서버 관리자가 제공한 지문과 비교한 뒤 **신뢰하고 연결**을 누르면 해당 주소(스킴·호스트·포트)의 해당 인증서만 저장합니다. 사설 IP와 인증서 이름이 다른 경우도 이 확인에 포함됩니다. 인증서가 바뀌면 다시 확인하며 만료되었거나 아직 유효하지 않은 인증서는 허용하지 않습니다. 서버 카드의 **저장한 인증서 신뢰 해제** 또는 서버 삭제로 신뢰를 지울 수 있습니다. Android에 CA를 설치하는 기존 방식도 계속 지원합니다. HTTP 연결은 연결할 때마다 안내 후 선택하며, HTTPS 페이지의 혼합 콘텐츠는 차단합니다. 외부 링크는 확인 후 브라우저에서 엽니다.

앱은 서버 페이지에 `JavascriptInterface`를 노출하지 않습니다. 파일 다운로드 인증 정보는 같은 origin(스킴·호스트·포트)에만 사용하며 리다이렉트에 전달하지 않습니다. 서버 이름·주소는 앱 내부 저장소, 로그인 토큰은 서버 origin별로 Android Keystore 키를 사용하는 AES-GCM 암호화 저장소에 보관하고, 페이지 실행 전에 sessionStorage로 복원합니다. 정확히 일치하는 서버의 최상위 페이지에서만 로그인 상태 메시지를 받으며 토큰 갱신과 로그아웃을 반영합니다. 서버 삭제 또는 기기의 로그인 정보 지우기로 저장 토큰도 삭제합니다. 최신 Android System WebView가 필요하며 지원 기능이 없으면 업데이트 안내를 표시합니다. 클라우드 백업과 기기 이전을 비활성화했습니다. `기기의 로그인 정보 지우기`는 기기 데이터만 삭제하며 서버의 모든 세션을 일괄 폐기하는 기능은 아닙니다.

## 빌드와 검증

필요 도구: JDK 21, Android SDK Platform 36, Build Tools 35.0.0. Gradle 8.13은 체크섬이 지정된 wrapper로 내려받습니다. Android Studio에서 이 `android` 디렉터리를 열거나 다음을 실행하세요.

```bash
export ANDROID_HOME=/path/to/android-sdk
./gradlew :app:testDebugUnitTest :app:lintDebug :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

디버그 APK는 개발용 키로 서명됩니다. 정식 배포는 아래 GitHub 릴리즈 자동화를 사용하며, 직접 빌드할 때는 소유한 서명 키로 Android Studio의 **Generate Signed App Bundle / APK**를 이용하세요. 서명 키는 저장소에 넣지 않습니다. GitHub Actions의 Android workflow도 테스트·린트 후 디버그 APK를 artifact로 생성합니다.

브라우저 회귀 테스트는 실제 서버 명령을 실행하지 않는 REST/WS fixture를 사용합니다.

```bash
# 별도 터미널에서 web/: npm run dev -- --port 5179
cd ../e2e
PLAYWRIGHT_BASE_URL=http://127.0.0.1:5179 npx playwright test tests/mobile-terminal.spec.ts
```

### 검증 범위와 한계

주소/인증 경계와 키 인코딩의 JVM 단위 테스트, Android Lint, APK 빌드, 브라우저 터미널/AI 키 입력·스크롤·화면 크기 변경 테스트를 제공합니다. 실제 기기의 삼성 키보드·Gboard·TalkBack, 대형 파일, 장시간 백그라운드 후 네트워크 변경은 추가 기기 검증 대상입니다. Android가 앱 프로세스를 종료하면 서버 목록에서 다시 연결하며, AI tmux 프로세스의 유지 여부는 서버의 기존 세션 정책을 따릅니다. 터미널 확대는 서버의 글자 크기 설정을 사용하고, 앱의 읽기 배율은 일반 웹 텍스트에 적용됩니다.

설계 참고: [Android WebView SSL 처리](https://developer.android.com/reference/android/webkit/WebViewClient), [xterm 터미널 API](https://xtermjs.org/docs/api/terminal/classes/terminal/), [AGP 8.13 호환성](https://developer.android.com/build/releases/agp-8-13-0-release-notes).

## GitHub 릴리즈 자동화

`android-vMAJOR.MINOR.PATCH` 태그를 push하면 `Release Android`가 JVM 테스트·Lint·release 빌드·서명 검증 후 APK와 SHA-256 체크섬을 공개합니다. Android 릴리즈는 서버용 Latest 표시를 변경하지 않습니다. 수동 재실행도 해당 태그를 선택해야 합니다.

```bash
git tag android-v0.1.3
git push origin android-v0.1.3
```

버전 코드는 `major × 1,000,000 + minor × 1,000 + patch + 1`이며 minor/patch는 999 이하입니다. 항상 더 높은 버전을 사용하세요. 서명은 저장소 Secrets의 `ANDROID_KEYSTORE_BASE64`(PKCS12, alias `sfpanel`)와 `ANDROID_KEYSTORE_PASSWORD`를 사용합니다. 같은 키를 보관해야 설치된 앱을 업데이트할 수 있습니다. 키 원본과 암호는 저장소에 커밋하지 않습니다.

기존 debug APK에서 첫 release APK로 이동할 때는 서명이 달라 기존 앱을 삭제해야 합니다. 이때 기기에 저장한 서버 목록·로그인 정보는 지워집니다. 이후 release 버전 간에는 덮어 설치할 수 있습니다. 서명 구성은 [Android 공식 문서](https://developer.android.com/studio/publish/app-signing), 태그 실행은 [GitHub Actions 문서](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows)를 따릅니다.

## 앱 내 업데이트 (0.1.1 이상)

앱 실행 시 하루 한 번 GitHub의 `android-v*` 정식 릴리즈를 확인합니다. 서버용 `v*` 릴리즈와 사전 릴리즈는 제외합니다. **보기 설정 → 앱 업데이트 확인**으로 수동 확인하거나 **자동 업데이트 확인**을 끌 수 있습니다. GitHub 연결 실패는 서버 이용을 차단하지 않습니다.

새 버전 안내에서 **다운로드 및 설치**를 누르면 현재 네트워크로 APK를 내려받습니다. SHA-256, 패키지 ID, 버전 증가, 현재 설치 앱과 동일한 서명을 검증한 뒤 Android 설치 화면을 엽니다. 최초에는 SFPanel의 **이 출처 허용** 설정이 필요하며, 최종 설치는 사용자가 승인합니다. 설치 중 앱은 종료되지만 연결 정보와 인증서 신뢰는 유지됩니다. 다운로드 도중 앱이 종료되면 다시 시도하세요. 업데이트 통신은 시스템 HTTPS 검증만 사용하며 서버별 사설 인증서 신뢰와 로그인 정보를 사용하지 않습니다.

0.1.0에는 업데이트 기능이 없으므로 0.1.1은 GitHub에서 직접 받아 한 번 덮어 설치해야 합니다. 이후부터 앱 내 업데이트를 사용할 수 있습니다.

## 0.1.2 UI 구조 변경

저장한 서버를 연결 화면 위쪽에 배치하고 새 서버 입력은 필요할 때 펼칩니다. 서버에 연결한 뒤에는 어떤 페이지에서도 `전체 기능`을 열 수 있습니다. 코딩과 무관한 페이지의 `⋯`는 보기 설정·새로고침만 제공하며 코딩 메뉴를 섞지 않습니다. 관리 기능은 서버의 실제 웹 UI를 사용하므로 서버의 권한과 기능 설치 여부가 그대로 적용됩니다.

## 0.1.3 스크롤·로그인 유지

전체 화면 터미널에서 손가락 스크롤이 무시되던 문제를 수정했습니다. 앱 자체 보정이므로 기존 v0.73.0 서버에서도 적용됩니다. 내 서버로 돌아갔다 재접속하거나 앱을 종료해도 로그인 정보를 복원합니다. 업데이트 후 처음 한 번 로그인하면 이후부터 유지되며 서버에서 세션을 만료·폐기하면 다시 로그인해야 합니다. 로그아웃은 그대로 적용됩니다.
