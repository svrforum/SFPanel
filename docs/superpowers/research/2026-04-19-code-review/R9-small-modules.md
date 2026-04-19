# R9 — alert + audit + cron + process + services

검토 일시: 2026-04-19
범위: `alert/*`, `audit/handler.go`, `cron/handler.go`, `process/handler.go`, `services/handler.go`

Critical 없음. P1 5건.

## P1

### I-1 Telegram Markdown 이스케이프 없음
**위치**: `alert/channels/telegram.go:28` — `ParseMode: "Markdown"`
메트릭 값(`%.1f%%`)이나 서비스 이름의 `_`/`*`/`` ` ``/`[` 미이스케이프 → Telegram API 400 또는 메시지 깨짐.
**수정**: `HTML` parse mode 또는 `MarkdownV2` + 이스케이프.

### I-2 Discord webhook URL 미검증 → SSRF
**위치**: `alert/manager.go:158`, `handler.go:193`
`cfg.WebhookURL`을 prefix 검증 없이 HTTP POST. `http://169.254.169.254` 같은 메타데이터 엔드포인트로 유도 가능.
**수정**: `strings.HasPrefix(url, "https://discord.com/api/webhooks/")` 검사.

### I-3 Services systemctl stderr 원문 노출
**위치**: `services/handler.go:69/86/102/119/136` (5개 핸들러)
`fmt.Sprintf("Failed to X %s: %s", name, strings.TrimSpace(out))` — `SanitizeOutput` 미사용. CLAUDE.md 규칙 위반.
**수정**: `response.SanitizeOutput(out)` 래핑.

### I-4 Audit cleanup TOCTOU
**위치**: `api/middleware/audit.go:53-58`
`COUNT(*) > 50000` 확인 후 별도 `DELETE` → 비원자적. `lastCleanup` CAS 없이 `Store`로 다수 고루틴 동시 진입 가능.
**수정**: `atomic.CompareAndSwap` + 단일 SQL `DELETE WHERE id NOT IN (SELECT id FROM audit_logs ORDER BY id DESC LIMIT 40000)`.

### I-5 Cron read-modify-write 직렬화 없음
**위치**: `cron/handler.go:145-172, 185-206`
`UpdateJob`/`DeleteJob`: `crontab -l` → 편집 → `crontab -`. API 요청 2개 동시 시 last-write-wins. 패키지 mutex 없음.
**수정**: 패키지 수준 `var crontabMu sync.Mutex` 직렬화.

## 양호
- Alert NaN/Inf는 `json.Unmarshal`이 거부하므로 실제 위험 없음
- Alert cooldown 재시작 초기화는 known limitation
- PID 재사용 race는 Linux 전역 한계 (gopsutil 불가)
- TestChannel은 실제 알림과 동일 코드 경로 사용 — 정상

## 요약
| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| I-1 | P1 | telegram.go:28 | Markdown 이스케이프 없음 |
| I-2 | P1 | alert/manager.go:158 | Discord SSRF |
| I-3 | P1 | services/handler.go:69+ | systemctl stderr 원문 |
| I-4 | P1 | middleware/audit.go:53 | cleanup TOCTOU |
| I-5 | P1 | cron/handler.go:145 | crontab 직렬화 없음 |
