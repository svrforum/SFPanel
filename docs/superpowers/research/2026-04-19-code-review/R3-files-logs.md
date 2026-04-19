# R3 — files + logs

검토 일시: 2026-04-19
범위: `internal/feature/files/handler.go`, `internal/feature/logs/handler.go`

## R0 항목 검증

| R0 항목 | 결과 | 근거 |
|---------|-----|-----|
| F-06 custom log source `/home/`, `/tmp/` 허용 | CONFIRMED | `logs/handler.go:434` |
| F-07 ListDir readProtectedPaths 미적용 | CONFIRMED | `files/handler.go:152-201`, 176-191에 호출 없음 |
| P0-1 WS write 경합 | CONFIRMED — scanner 고루틴이 safeWSWriter 없이 `ws.WriteMessage` 직접. R0의 P1 등급 판단 동의 |

## P0 신규

### N-01 `isCriticalPath` exact-match — `/etc` 하위 경로 전부 무방비
**위치**: `files/handler.go:32-105, WriteFile:270, UploadFile:511, MkDir:333`
**신뢰도 100**

`criticalPaths`는 exact-match 맵. `validatePathForWrite("/etc/cron.d/backdoor")` 실행 시:
- `parentDir = /etc/cron.d`
- `isCriticalPath("/etc/cron.d")` = **false** (맵에 없음)
- 검증 통과

결과적으로 인증된 관리자가 `/etc/cron.d/*`(root cron), `/etc/sudoers.d/*`(sudo 탈취), `/usr/local/bin/*`(바이너리 교체)에 파일 쓰기/업로드 가능. `DeletePath`, `RenamePath`도 동일 경로. **호스트 루트 권한 상승 직결.**

**수정**: `isCriticalPath`를 prefix 체크로:
```go
func isCriticalPath(p string) bool {
    clean := filepath.Clean(p)
    for critical := range criticalPaths {
        if clean == critical || strings.HasPrefix(clean, critical+"/") {
            return true
        }
    }
    return false
}
```

## P1 신규

### N-02 bufio.Scanner 기본 64KB 버퍼 → WS 스트림 조용히 중단
**위치**: `logs/handler.go:394`
기본 `bufio.NewScanner(stdout)` = 64KB. 단일 로그 라인 64KB 초과 시 `scanner.Scan()`이 `bufio.ErrTooLong` 반환 → 고루틴 종료. WS 연결은 살아있지만 로그 미전송, 클라이언트 무응답.
**수정**: `scanner.Buffer(make([]byte, 256*1024), 256*1024)`.

### N-03 스캐너 고루틴 종료 미대기 + concurrent write
**위치**: `logs/handler.go:392-400, 376-379`
`<-done` 후 `defer ws.Close()`와 스캐너의 `ws.WriteMessage` 동시 실행 가능. gorilla/websocket concurrent write = undefined behavior. 이미 `websocket/handler.go`에 `safeWSWriter`가 있는데 logs 모듈은 미사용.
**수정**: `safeWSWriter` 사용 + `scannerDone chan struct{}`로 동기화.

## P2 신규

### N-04 custom log source INSERT가 cleanPath 아닌 req.Path 원본 저장
**위치**: `logs/handler.go:433→460`
검증은 `cleanPath`로, 저장은 `req.Path` 원본. trailing slash/이중 슬래시 등 비정규화 경로 저장 → `allSources()`가 raw DB 값을 `tail -F`에 전달.
**수정**: INSERT 시 `cleanPath` 저장.

## 요약
| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| N-01 | P0 | files/handler.go:32, 270, 511, 333 | isCriticalPath exact-match → /etc/cron.d 쓰기 가능 |
| N-02 | P1 | logs/handler.go:394 | Scanner 64KB 버퍼 조용히 중단 |
| N-03 | P1 | logs/handler.go:392 | 스캐너 종료 미대기 + safeWSWriter 미사용 |
| N-04 | P2 | logs/handler.go:433 | 비정규 경로 DB 저장 |
