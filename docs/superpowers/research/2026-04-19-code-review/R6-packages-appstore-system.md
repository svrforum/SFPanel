# R6 — packages + appstore + system

검토 일시: 2026-04-19
범위: `internal/feature/packages/handler.go`, `internal/feature/appstore/handler.go`, `internal/feature/system/handler.go`, `internal/feature/system/tuning.go`

## P0

### C-01 설치 스크립트 무결성 검증 없음
**위치**: `packages/handler.go:432-453` (Docker), `1013-1031` (Claude)
`curl -fsSL https://get.docker.com` → `/tmp/get-docker.sh` → `sh`. SHA-256/GPG 검증 전무. MITM 또는 CDN 오염 시 root 코드 실행.
`claude.ai/install.sh`도 동일. NVM은 버전 고정만 돼 있고 해시 검증 없음.
**수정**: 다운로드 후 하드코딩 SHA-256 검증 혹은 Docker 공식 apt 리포지토리 GPG 방식 교체.

### C-02 AppStore Advanced 모드 (P0-C 재확인)
**위치**: `appstore/handler.go:571-593`
`req.Advanced=true`에서 임의 `docker-compose.yml` + `.env` → `docker compose up -d`. 호스트 루트 바인드 가능. R0 F-09 완화책 없음.
**수정**: super-admin 권한 분리 또는 볼륨 바인드 화이트리스트.

## P1

### I-01 checksums.txt 서명 없음
**위치**: `system/handler.go:123-175`, `internal/release/release.go`
바이너리 SHA-256 검증은 OK, 그러나 `checksums.txt` 자체에 GPG 서명 없음. MITM이 둘 다 바꿔치기 가능.
**수정**: GoReleaser `--sign`으로 `checksums.txt.sig` + 공개키 검증.

### I-02 Restore tar — 심볼릭 링크 엔트리 미필터
**위치**: `system/handler.go:430-439`
`hdr.Typeflag`가 `TypeDir`만 건너뛰고 `TypeSymlink`/`TypeLink`는 경로 검사 후 `os.WriteFile`에 도달. 악성 타르에 `compose/app/docker-compose.yml → /etc/cron.d/evil` 심볼릭 링크 넣으면 호스트 쓰기 가능.
**수정**: `hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink` 명시적 `continue`.

### I-03 apt / streamCommand output 원문 노출
**위치**: `packages/handler.go:193/226/258`, `appstore/handler.go:685`
`UpgradePackages`/`InstallPackage`/`RemovePackage`가 apt 출력 그대로 JSON 응답 삽입. `appstore.streamCommand`는 docker 출력 무필터 SSE. 내부 경로/버전/스택 노출.
**수정**: `response.SanitizeOutput` 또는 SSE 필터링.

### I-04 tuning 롤백 전역 변수 동시 요청 충돌
**위치**: `system/tuning.go:34-42, 262-267`
`rollbackValues/rollbackTimer/rollbackDeadline`가 패키지 전역. 두 번째 `ApplyTuning`이 첫 번째 스냅샷 덮어쓰기 + 타이머 교체 → 첫 번째에 대한 롤백 유실.
**수정**: 진입 시 `rollbackValues != nil`이면 "pending confirmation" 오류.

### I-05 nvmDir bash -c 삽입 (F-03 재확인)
**위치**: `packages/handler.go:797/849/906`
`findNVMDir()` 반환 경로를 `fmt.Sprintf`로 bash 스크립트에 삽입. `safePathRe`가 일차 방어지만 레이어 단일.
**수정**: `cmd.Env`로 `NVM_DIR` 주입 + 스크립트에서 `"$NVM_DIR"` 참조.

## P2

### L-01 apt lock 경합 UX
**위치**: `packages/handler.go:52-55`
`/var/lib/dpkg/lock-frontend` 보유 중이면 일반 `ErrAPTUpdateError`만 반환 → actionable 정보 부족.
**수정**: stderr에 "lock" 키워드 시 `ErrAPTLocked` 또는 재시도.

## 요약
| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| C-01 | P0 | packages:432, 1013 | get.docker.com / claude 스크립트 무결성 미검증 |
| C-02 | P0 | appstore:571 | advanced 모드 호스트 루트 (F-09 재확인) |
| I-01 | P1 | system:123, release.go | checksums.txt 서명 없음 |
| I-02 | P1 | system:430 | restore tar 심볼릭 링크 미필터 |
| I-03 | P1 | packages:193, appstore:685 | apt/docker output 무필터 |
| I-04 | P1 | tuning:34, 262 | 롤백 전역 변수 동시 요청 충돌 |
| I-05 | P1 | packages:797 | nvmDir bash -c (F-03 재확인) |
| L-01 | P2 | packages:52 | apt lock UX |
