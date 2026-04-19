# R5 — firewall + network + disk

검토 일시: 2026-04-19
범위: `internal/feature/firewall/*`, `internal/feature/network/*`, `internal/feature/disk/*` (R0 F-04/F-11/S-06 제외)

## P0

### C-01 Tailscale authkey CLI 노출
**위치**: `network/tailscale.go:362-368`
`tailscale up --authkey=<key>` → `/proc/<pid>/cmdline` + `ps aux`에 30일 유효 크리덴셜 노출. 타 로컬 사용자가 읽기 가능.
**수정**: `cmd.Env`의 `TS_AUTHKEY=<key>` 또는 `--authkey=- ` + stdin 파이프(`RunWithInput`).

### C-02 Fail2ban jail 파일 권한 0644
**위치**: `firewall/firewall_fail2ban.go:728-730`
`/etc/fail2ban/jail.d/*.local`에 `ignoreip`/`logpath` 포함, world-readable.
**수정**: `0600`.

## P1

### I-01 UFW 코멘트에 `#` 허용 → 파싱 오염
**위치**: `firewall/firewall.go:118`
`validComment`에 `#` 포함. `parseUFWRules`의 `strings.LastIndex(rest, "#")`가 코멘트를 잘못 분리 → To/From/Action 필드 오염.
**수정**: `#` 제거.

### I-02 Netplan apply — 검증/롤백 없음
**위치**: `network/network.go:354-361`
`netplan generate`(구문 검증)나 `netplan try --timeout=N`(자동 롤백) 없이 직접 `netplan apply`. 잘못된 설정 → 원격 네트워크 즉시 단절, 복구 불가.
**수정**: `netplan try --timeout=30`으로 교체.

### I-03 Swap 파일 — tmpfs/ramfs 경로 차단 없음
**위치**: `disk/disk_swap.go:147-213`
`validateDiskPath`가 `/tmp`, `/run`, `/dev/shm` 같은 tmpfs 마운트를 막지 않음. mkswap/swapon 성공하지만 재부팅 시 사라지거나 실질적으로 무의미.
**수정**: `syscall.Statfs`의 `f_type`으로 tmpfs(`0x01021994`), ramfs(`0x858458f6`) 거부.

### I-04 WireGuard 설정 파일 — 원자적 쓰기 없음
**위치**: `network/wireguard.go:381, 445`
`os.WriteFile` 직접 호출. 쓰기 도중 프로세스 종료/디스크 풀 시 파일 손상 → `wg-quick up` 실패 = VPN 단절.
**수정**: 임시 파일 + `os.Rename` (동일 파티션이므로 원자적).

### I-05 `getJailInfo` jail 이름 미검증
**위치**: `firewall/firewall_fail2ban.go:135-149`
`GetJailDetail`은 `validJailName` 검증하지만 `ListJails`가 호출하는 `getJailInfo`는 검증 없음. fail2ban 데몬이 악성 이름 반환하는 이론적 경로.
**수정**: 진입부 `validJailName.MatchString(name)` 검사.

## 요약
| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| C-01 | P0 | tailscale.go:362 | authkey가 ps/proc에 노출 |
| C-02 | P0 | firewall_fail2ban.go:728 | jail 파일 0644 |
| I-01 | P1 | firewall.go:118 | UFW 코멘트 # 허용 |
| I-02 | P1 | network.go:354 | netplan apply 검증/롤백 없음 |
| I-03 | P1 | disk_swap.go:147 | swap tmpfs 경로 차단 없음 |
| I-04 | P1 | wireguard.go:381 | WG 설정 파일 원자적 쓰기 없음 |
| I-05 | P1 | firewall_fail2ban.go:135 | getJailInfo 검증 우회 |
