# Known Issues

Unresolved / deferred problems. Each has a reproduction and current state so the next session doesn't re-investigate from zero. _Updated 2026-06-04._

## Mobile terminal: blank gap on keyboard show/hide/show (Samsung Internet)
**State: deferred — not reproducible in tooling.**
On Samsung Internet, repeatedly toggling the soft keyboard (or loading the page with the keyboard already up) leaves the terminal mostly blank / mis-sized. Touch scrollback itself is fixed; this is purely a sizing issue.

- **Cause (best understanding):** the browser reports a stale `visualViewport.height` at load-with-keyboard-up and doesn't fire a correcting `resize`, so `--app-h` and the xterm fit stay wrong.
- **Mitigations already shipped** (`Terminal.tsx` / `Layout.tsx`): debounced + dimension-guarded fit, `scrollToBottom` after fit, refit on focus, and `Layout` re-reads `visualViewport.height` at 150/350/700/1200 ms after mount.
- **Why it's stuck:** cannot reproduce in Playwright (the MCP context has no soft keyboard; even a Galaxy S9+ device context + viewport-resize simulation shows *no* blank). A clean viewport resize ≠ Samsung Internet's actual keyboard timing.
- **Next options if revisited:** (1) a one-tap "재조정 / redraw" button in the terminal toolbar as a guaranteed escape hatch; (2) on-device debugging by surfacing live `visualViewport.height` values; (3) evaluate downgrading xterm v6 → v5 (note: v6 worked correctly in emulation, so a downgrade is a gamble).

## Peer node `192.168.1.x` has a LAN IP conflict
**State: workaround in place (use Tailscale).**
The peer `peer-node` (NIC `<redacted-mac>`, static `192.168.1.x` via netplan) shares that IP with a **Tuya Wi-Fi IoT device** (`<redacted-mac>`). The ASUS router (`192.168.1.1`) DHCP-leases `.118` to the Tuya because `.118` isn't excluded from the pool. From other hosts, the ARP cache flaps between the two MACs, so LAN SSH/HTTP to `.118` works intermittently.

- **Reliable access:** Tailscale `ssh root@100.x.x.x` (tailnet name `<tailnet>`).
- **Permanent fix (operator action on the router):** reserve/exclude `.118` for the peer MAC, or move the Tuya to its own reservation.
- Don't conclude "the peer is offline" from a failed `.118` probe — check Tailscale first.

## `image_signatures` dead schema
**State: intentional, leave it.**
Migrations 21–23 created the `image_signatures` table + indexes for the Cosign image-verification feature removed in v0.13.0. The migration list is append-only, so the DDL stays but is never read/written. Don't repurpose this table; a future verifier feature should get a new one.

## golangci-lint not installed on the dev host
**State: environment-only; CI covers it.**
`make lint` fails locally with `golangci-lint: command not found` on this machine. Substitute `go vet ./...` + `eslint` locally; the real `golangci-lint` runs in CI.
