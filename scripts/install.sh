#!/usr/bin/env bash
set -euo pipefail

# SFPanel Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | bash
#        sudo ./install.sh                # install / upgrade
#        sudo ./install.sh uninstall      # remove binary + service
#        FORCE_SYSTEMD=1 sudo ./install.sh   # rewrite systemd unit even if present

REPO="svrforum/SFPanel"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/sfpanel"
DATA_DIR="/var/lib/sfpanel"
LOG_DIR="/var/log/sfpanel"
SERVICE_NAME="sfpanel"
FORCE_SYSTEMD="${FORCE_SYSTEMD:-0}"
# SFPANEL_VERSION pins a specific version (rollback / reproducible install):
#   SFPANEL_VERSION=0.50.0 ./install.sh
# SFPANEL_REQUIRE_COSIGN=1 hard-fails the install when the cosign signature
# can't be verified (production supply-chain enforcement) instead of warning.
SFPANEL_REQUIRE_COSIGN="${SFPANEL_REQUIRE_COSIGN:-0}"
# Set to 1 by download_binary once it has saved the prior binary to .bak, so
# verify_service_started only auto-reverts on a failed upgrade THIS run (not on
# a same-version reconcile that finds a stale .bak from an earlier upgrade).
BINARY_BACKED_UP=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# --- Pre-flight checks ---

check_root() {
  if [ "$(id -u)" -ne 0 ]; then
    log_error "This script must be run as root (use sudo)"
    exit 1
  fi
}

check_os() {
  if [ "$(uname -s)" != "Linux" ]; then
    log_error "SFPanel only supports Linux"
    exit 1
  fi
  # SFPanel targets Debian/Ubuntu — install.sh hard-codes apt-style paths (logrotate
  # at /etc/logrotate.d, systemd unit at /etc/systemd/system) and the runtime panel
  # shells out to apt for package management. Allow non-Debian distros to proceed
  # with a warning rather than blocking, since the binary itself works anywhere.
  if [ ! -f /etc/debian_version ]; then
    log_warn "Non-Debian/Ubuntu host detected; package management features (apt) will not work."
  fi
}

detect_arch() {
  local arch
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) log_error "Unsupported architecture: $arch"; exit 1 ;;
  esac
}

check_systemd() {
  # Container bases (e.g. plain debian:slim docker images) ship without systemd;
  # calling systemctl mid-install just produces a cryptic "Failed to connect to bus"
  # error. Detect upfront so the operator gets a clear message and the binary still
  # gets installed for manual launch.
  [ -d /run/systemd/system ]
}

check_commands() {
  for cmd in curl tar sha256sum awk; do
    if ! command -v "$cmd" &>/dev/null; then
      log_error "Required command not found: $cmd"
      exit 1
    fi
  done
}

get_current_version() {
  if [ -x "${INSTALL_DIR}/sfpanel" ]; then
    # `sfpanel version` prints e.g. "SFPanel 0.10.0 (commit: X, built: Y)".
    # Match the semver without requiring a 'v' prefix (the binary never
    # prints one); the old \Kv-lookbehind regex always returned empty,
    # which silently broke "already installed"/"upgrade" detection.
    "${INSTALL_DIR}/sfpanel" version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || echo ""
  else
    echo ""
  fi
}

# Read server.port out of config.yaml using POSIX awk only — `grep -oP` (PCRE)
# isn't available on Alpine/busybox, so the previous one-liner crashed there.
read_config_port() {
  awk '
    /^server:/        { in_server=1; next }
    /^[^[:space:]]/   { in_server=0 }
    in_server && /port[[:space:]]*:/ {
      gsub(/[^0-9]/, "", $0); print; exit
    }
  ' "${CONFIG_DIR}/config.yaml" 2>/dev/null
}

# Whether server.tls.enabled is on, so the summary prints a URL that actually
# works. Scoped to the server: block — `enabled:` also appears under
# docker.observability, and printing the wrong scheme sends the operator to a
# URL their browser refuses at the transport with no explanation.
# Same POSIX-awk-only constraint as read_config_port: no `grep -oP` here.
read_config_tls() {
  awk '
    /^server:/        { in_server=1; next }
    /^[^[:space:]]/   { in_server=0 }
    in_server && /enabled[[:space:]]*:/ {
      if ($0 ~ /true/) { print "1" } else { print "0" }
      exit
    }
  ' "${CONFIG_DIR}/config.yaml" 2>/dev/null
}

# --- Core functions ---

get_latest_version() {
  # Pinned version (rollback / reproducible install) takes precedence.
  if [ -n "${SFPANEL_VERSION:-}" ]; then
    echo "${SFPANEL_VERSION#v}"
    return
  fi
  local version
  version=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
  if [ -z "$version" ]; then
    log_error "Failed to fetch latest version. Check https://github.com/${REPO}/releases"
    log_error "(Pin a known version to bypass: SFPANEL_VERSION=0.50.0 $0)"
    exit 1
  fi
  echo "$version"
}

download_binary() {
  local version="$1"
  local arch="$2"
  local asset="sfpanel_${version}_linux_${arch}.tar.gz"
  local base="https://github.com/${REPO}/releases/download/v${version}"
  local tmp_dir

  tmp_dir=$(mktemp -d)

  log_info "Downloading SFPanel v${version} (linux/${arch})..."
  if ! curl -fsSL "${base}/${asset}" -o "${tmp_dir}/sfpanel.tar.gz"; then
    rm -rf "$tmp_dir"
    log_error "Download failed: ${base}/${asset}"
    exit 1
  fi

  # Integrity check against the release's checksums.txt. Without this step
  # a compromised mirror or MITM could ship a tampered binary that install.sh
  # would happily run as root.
  log_info "Verifying SHA-256 checksum..."
  if ! curl -fsSL "${base}/checksums.txt" -o "${tmp_dir}/checksums.txt"; then
    rm -rf "$tmp_dir"
    log_error "Could not fetch checksums.txt from ${base}/"
    exit 1
  fi

  # Verify the Sigstore (cosign keyless) signature of checksums.txt before
  # trusting it. SHA-256 alone is self-referential: an attacker able to serve a
  # tampered release swaps both the tarball AND checksums.txt and the hash still
  # matches. cosign binds checksums.txt to the GitHub Actions release-workflow
  # identity (Rekor-logged), which SHA-256 cannot. Hard-fail on a bad signature;
  # if cosign isn't installed, warn and fall back to SHA-256-over-HTTPS only.
  if command -v cosign >/dev/null 2>&1; then
    if curl -fsSL "${base}/checksums.txt.sig" -o "${tmp_dir}/checksums.txt.sig" \
       && curl -fsSL "${base}/checksums.txt.pem" -o "${tmp_dir}/checksums.txt.pem"; then
      log_info "Verifying release signature (cosign keyless)..."
      # Pin the identity to the release workflow on a TAG ref (mirrors the
      # in-binary policy at internal/release/cosign.go) — the old `/workflows/.*`
      # regexp would accept a signature minted by ANY workflow in the repo.
      # The detached --signature/--certificate flags work on cosign v2 AND v3
      # (deprecated-with-warning on v3.1.3, verified against the v0.56.0
      # assets). If cosign v4 removes them, switch to the release's
      # checksums.txt.sigstore.json bundle (published since v0.56.0).
      if ! cosign verify-blob \
          --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
          --certificate-identity-regexp "^https://github.com/${REPO}/\.github/workflows/release\.yml@refs/tags/v" \
          --signature "${tmp_dir}/checksums.txt.sig" \
          --certificate "${tmp_dir}/checksums.txt.pem" \
          "${tmp_dir}/checksums.txt" >/dev/null 2>&1; then
        rm -rf "$tmp_dir"
        log_error "cosign signature verification FAILED for checksums.txt — refusing to install"
        exit 1
      fi
      log_info "Release signature verified (Sigstore keyless)."
    else
      log_warn "Release has no cosign signature assets; using SHA-256 only."
      if [ "$SFPANEL_REQUIRE_COSIGN" = "1" ]; then
        rm -rf "$tmp_dir"
        log_error "SFPANEL_REQUIRE_COSIGN=1 but the release carries no signature — refusing to install."
        exit 1
      fi
    fi
  else
    if [ "$SFPANEL_REQUIRE_COSIGN" = "1" ]; then
      rm -rf "$tmp_dir"
      log_error "SFPANEL_REQUIRE_COSIGN=1 but cosign is not installed — refusing the unverified install."
      log_error "Install cosign (https://docs.sigstore.dev/cosign/installation) or unset SFPANEL_REQUIRE_COSIGN."
      exit 1
    fi
    log_warn "============================================================"
    log_warn "cosign NOT installed — the release SIGNATURE is NOT verified."
    log_warn "Falling back to SHA-256-over-HTTPS only (integrity, not provenance)."
    log_warn "For full supply-chain verification install cosign, or set"
    log_warn "SFPANEL_REQUIRE_COSIGN=1 to make a missing signature fatal."
    log_warn "============================================================"
  fi

  local expected actual
  expected=$(awk -v a="${asset}" '$2==a || $2=="*"a {print $1; exit}' "${tmp_dir}/checksums.txt")
  if [ -z "$expected" ]; then
    rm -rf "$tmp_dir"
    log_error "Asset ${asset} not listed in checksums.txt"
    exit 1
  fi
  actual=$(sha256sum "${tmp_dir}/sfpanel.tar.gz" | awk '{print $1}')
  if [ "$expected" != "$actual" ]; then
    rm -rf "$tmp_dir"
    log_error "Checksum mismatch: expected ${expected}, got ${actual}"
    exit 1
  fi

  log_info "Extracting..."
  if ! tar -xzf "${tmp_dir}/sfpanel.tar.gz" -C "$tmp_dir"; then
    rm -rf "$tmp_dir"
    log_error "Failed to extract ${tmp_dir}/sfpanel.tar.gz (corrupt or truncated download)"
    exit 1
  fi

  if [ ! -f "${tmp_dir}/sfpanel" ]; then
    rm -rf "$tmp_dir"
    log_error "Binary not found in archive"
    exit 1
  fi

  # Only touch the running service after every verification has passed,
  # so a bad download can't leave the host with the service stopped and
  # no replacement binary in place.
  if check_systemd && systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    log_info "Stopping existing SFPanel service..."
    systemctl stop "$SERVICE_NAME"
  fi

  # With the service stopped (or fresh install — no service yet), snapshot the
  # DB *and its WAL/SHM sidecars* before any new migration touches the schema.
  # A clean stop usually checkpoints the WAL, but a prior unclean exit may not,
  # so we copy the sidecars to keep the snapshot consistent. Roll back via:
  #   systemctl stop sfpanel
  #   rm -f /var/lib/sfpanel/sfpanel.db-wal /var/lib/sfpanel/sfpanel.db-shm
  #   cp <bak> /var/lib/sfpanel/sfpanel.db
  #   [ -f <bak>-wal ] && cp <bak>-wal /var/lib/sfpanel/sfpanel.db-wal
  #   systemctl start sfpanel
  backup_db

  # Back up the current binary so a failed upgrade (new binary crashes on boot)
  # can be auto-reverted by verify_service_started — mirrors the web/CLI update
  # watchdog's binary-only revert. Fresh install (no current binary) skips this.
  if [ -f "${INSTALL_DIR}/sfpanel" ]; then
    cp -p "${INSTALL_DIR}/sfpanel" "${INSTALL_DIR}/sfpanel.bak"
    BINARY_BACKED_UP=1
  fi

  install -m 755 "${tmp_dir}/sfpanel" "${INSTALL_DIR}/sfpanel"
  rm -rf "$tmp_dir"
  log_info "Binary installed to ${INSTALL_DIR}/sfpanel"
}

# backup_db snapshots sfpanel.db before a binary upgrade so a bad migration
# doesn't strand the operator. Keeps the 3 most recent snapshots; older ones
# are pruned automatically.
backup_db() {
  local db="${DATA_DIR}/sfpanel.db"
  if [ ! -f "$db" ]; then
    return 0
  fi
  local ts bak
  ts=$(date +%Y%m%d-%H%M%S)
  bak="${DATA_DIR}/sfpanel.db.bak-${ts}"
  if cp -p "$db" "$bak"; then
    chmod 600 "$bak"
    # Copy the WAL/SHM sidecars too so the snapshot is a consistent set even if
    # the prior shutdown left uncheckpointed pages in the WAL.
    [ -f "${db}-wal" ] && cp -p "${db}-wal" "${bak}-wal" && chmod 600 "${bak}-wal"
    [ -f "${db}-shm" ] && cp -p "${db}-shm" "${bak}-shm" && chmod 600 "${bak}-shm"
    log_info "DB snapshot saved: ${bak}"
  else
    log_warn "DB snapshot failed (continuing — upgrade is not blocked)"
    return 0
  fi
  # Retain the 3 newest snapshots; prune older ones with their sidecars. Match
  # only the timestamped main files (ts shape YYYYmmdd-HHMMSS) so -wal/-shm
  # sidecars aren't counted as separate logical backups (which would halve the
  # effective retention) and an operator's manually-named .bak isn't pruned.
  local old
  while IFS= read -r old; do
    [ -n "$old" ] && rm -f "$old" "${old}-wal" "${old}-shm"
  done < <(ls -1t "${DATA_DIR}"/sfpanel.db.bak-* 2>/dev/null | grep -E '/sfpanel\.db\.bak-[0-9]{8}-[0-9]{6}$' | tail -n +4)
}

setup_dirs() {
  mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
  # DB + cluster material under /var/lib/sfpanel contain bcrypt hashes, TOTP
  # secrets, and mTLS private keys. Keep root-only.
  chmod 700 "$DATA_DIR"
  # /etc/sfpanel holds config.yaml (JWT secret).
  chmod 700 "$CONFIG_DIR"
  # /etc/sfpanel/cluster/ holds the CA private key + node cert/key after
  # `cluster init`. The cluster code creates this dir itself, but if it
  # already exists from a prior install make sure the perms are tight.
  if [ -d "${CONFIG_DIR}/cluster" ]; then
    chmod 700 "${CONFIG_DIR}/cluster"
    find "${CONFIG_DIR}/cluster" -type f -name "*.key" -exec chmod 600 {} \; 2>/dev/null || true
  fi
  # The DB + WAL/SHM sidecars hold bcrypt hashes / TOTP secrets. The 0700 parent
  # already blocks other users, but keep the files themselves root-only too
  # (defense in depth, matching backup_db's 0600 snapshots) since the binary
  # creates them at the process umask (commonly 0644).
  local f
  for f in "${DATA_DIR}/sfpanel.db" "${DATA_DIR}/sfpanel.db-wal" "${DATA_DIR}/sfpanel.db-shm"; do
    [ -f "$f" ] && chmod 600 "$f" 2>/dev/null || true
  done
}

# generate_jwt_secret returns 64 hex characters (32 bytes of /dev/urandom).
# Prefer openssl when available; fall back to xxd to keep the script working
# on minimal images that lack openssl. The previous head/base64/tr pipeline
# could under-shoot to fewer than 32 chars in rare runs because tr -d '/+='
# deletes characters before the truncate step.
generate_jwt_secret() {
  if command -v openssl &>/dev/null; then
    openssl rand -hex 32
  elif command -v xxd &>/dev/null; then
    xxd -l 32 -p /dev/urandom | tr -d '\n'
  else
    # Last resort: hex-encode 32 bytes via od. Produces 64 hex chars.
    od -vN 32 -An -tx1 /dev/urandom | tr -d ' \n'
  fi
}

generate_config() {
  if [ -f "${CONFIG_DIR}/config.yaml" ]; then
    log_warn "Config already exists at ${CONFIG_DIR}/config.yaml (skipping)"
    return
  fi

  local jwt_secret
  jwt_secret=$(generate_jwt_secret)

  # umask 077 so the file is created 0600 from the start — the JWT secret is in
  # cleartext, and writing it world-default (0644) then chmod'ing leaves a brief
  # window where any local user could read it. chmod below is belt-and-suspenders.
  ( umask 077; cat > "${CONFIG_DIR}/config.yaml" <<EOF
# SFPanel Configuration
server:
  host: "0.0.0.0"
  port: 3628
  # HTTPS is on for fresh installs. On first boot the panel generates a local
  # certificate authority under tls.dir and issues itself a certificate from it;
  # no openssl, no manual steps. Download the CA from Settings and install it on
  # your devices once, and the browser warning goes away for good.
  #
  # Turn this off if you terminate TLS at a reverse proxy — that arrangement is
  # still fully supported, and a self-signed backend just makes the proxy config
  # harder (proxy_ssl_verify off and friends).
  #
  # Upgrades never reach this block: the installer leaves an existing
  # config.yaml alone, so panels installed before this existed keep serving
  # plain HTTP until their operator opts in.
  tls:
    enabled: true
    dir: "${CONFIG_DIR}/tls"

database:
  path: "${DATA_DIR}/sfpanel.db"

auth:
  jwt_secret: "${jwt_secret}"
  token_expiry: "24h"

docker:
  socket: "unix:///var/run/docker.sock"

log:
  level: "info"
  file: "${LOG_DIR}/sfpanel.log"
EOF
  )

  chmod 600 "${CONFIG_DIR}/config.yaml"
  log_info "Config created at ${CONFIG_DIR}/config.yaml"
}

setup_logrotate() {
  local target="/etc/logrotate.d/sfpanel"
  # Don't clobber an operator-tweaked logrotate config on every re-run. The
  # bundled defaults are fine for the common case, but a host that already
  # has custom rotation (e.g. forwarding to journald or a longer retention)
  # would silently lose those edits otherwise. FORCE_SYSTEMD=1 also forces
  # logrotate rewrite — same big hammer covers both.
  if [ -f "$target" ] && [ "$FORCE_SYSTEMD" != "1" ]; then
    log_info "Logrotate config already present (use FORCE_SYSTEMD=1 to rewrite)"
    return
  fi
  cat > "$target" <<'EOF'
/var/log/sfpanel/sfpanel.log {
    daily
    rotate 7
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    maxsize 10M
}
EOF
  log_info "Logrotate config installed"
}

setup_systemd() {
  if ! check_systemd; then
    log_warn "systemd not detected (no /run/systemd/system); skipping unit install."
    log_warn "Run the binary directly: ${INSTALL_DIR}/sfpanel ${CONFIG_DIR}/config.yaml"
    return
  fi

  local unit="/etc/systemd/system/${SERVICE_NAME}.service"
  # Same idempotency reasoning as setup_logrotate: don't blow away ExecStartPre,
  # Environment=, or LimitMEMLOCK= edits operators add for tuning. The
  # `update`/CLI path uses lifecycle.MigrateRestartPolicy() to inject the one
  # change that's mandatory (Restart=always), so most upgrades don't need a
  # full unit rewrite anyway.
  if [ -f "$unit" ] && [ "$FORCE_SYSTEMD" != "1" ]; then
    log_info "Systemd unit already present at $unit (use FORCE_SYSTEMD=1 to rewrite)"
    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
    systemctl start "$SERVICE_NAME"
    verify_service_started
    return
  fi

  cat > "$unit" <<EOF
[Unit]
Description=SFPanel - Server Management Panel
# network-online.target (not just network.target) so an interface actually has
# an address/route before boot-time outbound calls (update check, cluster peer
# dial). The panel tolerates no-network via retries, but this avoids avoidable
# errors on every reboot.
After=network-online.target docker.service
Wants=network-online.target docker.service

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/sfpanel ${CONFIG_DIR}/config.yaml
# Restart=always (not on-failure) because several HTTP handlers
# intentionally exit the process so a supervisor can pick up new cluster
# config — on-failure would treat those clean exits as "done" and leave
# the panel down. See internal/feature/cluster/handler.go.
Restart=always
RestartSec=5

# Resource limits: panel + monitor goroutines + cluster fanout typically
# stay under 200 MB; MemoryHigh keeps a runaway leak from squeezing the
# host without OOM-killing legitimate spikes. TasksMax bounds the worst
# case for the process tree (terminal PTYs, compose subprocs).
LimitNOFILE=65536
MemoryHigh=1G
TasksMax=4096

# Hardening: SFPanel needs full system access for firewall (ufw),
# packages (apt), disk management, terminal, sysctl tuning, and other
# admin tasks — so most sandboxing flags can't be enabled without
# breaking features. The few that *are* safe:
# - PrivateTmp: panel doesn't share /tmp with other services
# - RestrictSUIDSGID: panel never creates suid/sgid files
# Notably NOT enabled:
# - ProtectKernelTunables: would break the system tuning feature
#   (sysctl -p)
# - ProtectHome: would break the files browser for $HOME of other users
NoNewPrivileges=false
PrivateTmp=true
RestrictSUIDSGID=true

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"
  systemctl start "$SERVICE_NAME"
  verify_service_started
  log_info "Systemd service enabled and started"
}

# verify_service_started polls systemctl is-active for ~10 seconds. Without
# this check the script exits 0 even when the service never came up (port
# already bound, missing config, broken migration), so the operator sees
# "installed successfully" and a 502 in the browser.
verify_service_started() {
  local attempt
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if systemctl is-active --quiet "$SERVICE_NAME"; then
      return 0
    fi
    sleep 1
  done
  log_error "Service ${SERVICE_NAME} failed to start within 10s. Recent journal:"
  journalctl -u "$SERVICE_NAME" -n 30 --no-pager 2>&1 | sed 's/^/  /' >&2 || true
  # Auto-revert the binary if THIS run upgraded it (BINARY_BACKED_UP) — a
  # crash-on-boot new binary shouldn't leave the host on the broken version.
  if [ "$BINARY_BACKED_UP" = "1" ] && [ -f "${INSTALL_DIR}/sfpanel.bak" ]; then
    log_warn "Reverting to the previous binary and restarting..."
    install -m 755 "${INSTALL_DIR}/sfpanel.bak" "${INSTALL_DIR}/sfpanel"
    systemctl start "$SERVICE_NAME" 2>/dev/null || true
    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
      log_warn "Previous binary restored — the panel is back up on the prior version (upgrade reverted)."
    else
      log_error "Auto-revert restart also failed — manual recovery needed."
    fi
  fi
  # If a migration ran before the crash, the schema may also need rolling back —
  # point the operator at the DB snapshot.
  local latest_bak
  latest_bak=$(ls -1t "${DATA_DIR}"/sfpanel.db.bak-*[0-9] 2>/dev/null | head -1)
  if [ -n "$latest_bak" ]; then
    log_warn "If a migration broke the DB, roll back with:"
    log_warn "  systemctl stop ${SERVICE_NAME}"
    log_warn "  rm -f ${DATA_DIR}/sfpanel.db-wal ${DATA_DIR}/sfpanel.db-shm"
    log_warn "  cp ${latest_bak} ${DATA_DIR}/sfpanel.db"
    log_warn "  systemctl start ${SERVICE_NAME}"
  fi
  exit 1
}

print_success() {
  local version="$1"
  local mode="$2"
  local port
  port=$(read_config_port)
  : "${port:=3628}"
  local scheme="http"
  if [ "$(read_config_tls)" = "1" ]; then
    scheme="https"
  fi

  # Whether the service is actually running: systemd present AND active. On a
  # no-systemd host the binary is installed but nothing was launched, so we must
  # not present a live Access URL.
  local running=0
  if check_systemd && systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    running=1
  fi

  echo ""
  echo -e "${CYAN}============================================${NC}"
  if [ "$running" -ne 1 ]; then
    echo -e "${CYAN}   SFPanel v${version} binary installed${NC}"
  elif [ "$mode" = "upgrade" ]; then
    echo -e "${CYAN}   SFPanel upgraded to v${version}!${NC}"
  else
    echo -e "${CYAN}   SFPanel installed successfully!${NC}"
  fi
  echo -e "${CYAN}============================================${NC}"
  echo ""
  echo -e "  Version:   ${GREEN}v${version}${NC}"
  if [ "$running" -eq 1 ]; then
    echo -e "  Access:    ${GREEN}${scheme}://<server-ip>:${port}${NC}"
  else
    echo -e "  ${YELLOW}Not running (no systemd detected). Launch it manually:${NC}"
    echo -e "    ${INSTALL_DIR}/sfpanel ${CONFIG_DIR}/config.yaml"
    echo -e "  Then open: ${scheme}://<server-ip>:${port}"
  fi
  echo -e "  Config:    ${CONFIG_DIR}/config.yaml"
  echo -e "  Data:      ${DATA_DIR}/"
  echo -e "  Logs:      journalctl -u ${SERVICE_NAME} -f"
  echo ""
  echo -e "  Commands:"
  echo -e "    systemctl status ${SERVICE_NAME}"
  echo -e "    systemctl restart ${SERVICE_NAME}"
  echo -e "    systemctl stop ${SERVICE_NAME}"
  echo ""
  if [ "$mode" = "install" ]; then
    echo -e "  ${YELLOW}First visit: Set up the admin account in the browser.${NC}"
    echo -e "    ${YELLOW}Setup is restricted to the LAN / loopback. On a public host, tunnel it:${NC}"
    echo -e "    ${YELLOW}  ssh -L ${port}:127.0.0.1:${port} <this-server>  →  open ${scheme}://127.0.0.1:${port}${NC}"
    echo ""
    # Loud warning when no active firewall is obviously confining the port.
    if ! { command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi "Status: active"; }; then
      echo -e "  ${YELLOW}⚠  No active ufw firewall detected — ${port} may be reachable from anywhere.${NC}"
      echo -e "  ${YELLOW}   Restrict it before exposing this host (see step 3 below).${NC}"
      echo ""
    fi
    echo -e "  ${CYAN}Recommended next steps (do these before exposing the panel):${NC}"
    echo -e "    1. Enable 2FA in Settings → Security after first login."
    echo -e "    2. Front the panel with TLS (Caddy / nginx / Cloudflare Tunnel)."
    echo -e "       The bundled HTTP listener is plain — never expose ${port} to the public Internet."
    echo -e "    3. Restrict ${port} to LAN/VPN only (ufw default deny + allow from trusted CIDR)."
    echo ""
    echo -e "  ${CYAN}Tips:${NC}"
    echo -e "    Change port:  Edit ${CONFIG_DIR}/config.yaml → server.port"
    echo -e "                  Then: systemctl restart ${SERVICE_NAME}"
    echo ""
    echo -e "    Join cluster: sfpanel cluster join <token>"
    echo ""
  fi
}

# --- Uninstall ---

uninstall() {
  local purge="${1:-}"   # "purge" → also remove config/data/logs
  log_info "Uninstalling SFPanel..."

  # If this node is a Raft cluster member, leave the cluster FIRST — while the
  # service + binary are still present — so the surviving voters drop it from
  # the Raft configuration. Otherwise the node vanishes while still counted as a
  # voter; on a 2-voter cluster the survivor loses quorum and needs peers.json
  # recovery. Best-effort (set -e safe).
  if [ -x "${INSTALL_DIR}/sfpanel" ] \
     && awk '/^cluster:/{f=1;next} /^[^[:space:]]/{f=0} f' "${CONFIG_DIR}/config.yaml" 2>/dev/null \
          | grep -qsE '^[[:space:]]*enabled:[[:space:]]*true'; then
    log_info "Cluster member detected — leaving the cluster first..."
    if "${INSTALL_DIR}/sfpanel" cluster leave --config "${CONFIG_DIR}/config.yaml" >/dev/null 2>&1; then
      log_info "Left the cluster cleanly."
    else
      log_warn "Could not leave the cluster automatically (peer unreachable / quorum lost)."
      log_warn "This node is STILL a Raft voter. From a surviving node run:"
      log_warn "    sudo sfpanel cluster remove <node-id>"
      log_warn "or follow docs/specs/cluster-partition-runbook.md (peers.json recovery)."
    fi
  fi

  if check_systemd; then
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  fi
  rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
  rm -f "/etc/logrotate.d/sfpanel"
  if check_systemd; then
    systemctl daemon-reload
  fi
  rm -f "${INSTALL_DIR}/sfpanel" "${INSTALL_DIR}/sfpanel.bak"
  log_info "Binary, service unit, and logrotate config removed."

  if [ "$purge" = "purge" ]; then
    rm -rf "${CONFIG_DIR}" "${DATA_DIR}" "${LOG_DIR}"
    log_warn "PURGED: ${CONFIG_DIR} (config + JWT secret + cluster certs), ${DATA_DIR} (DB + snapshots), ${LOG_DIR}."
  else
    log_warn "Preserved (remove with 'uninstall --purge'):"
    log_warn "  ${CONFIG_DIR}  — config.yaml + JWT secret + cluster mTLS certs"
    log_warn "  ${DATA_DIR}  — SQLite DB + sfpanel.db.bak-* snapshots"
    log_warn "  ${LOG_DIR}  — logs"
  fi
  log_warn "Appstore-deployed Docker stacks under /opt/stacks keep running (managed by Docker, not the panel) — remove them separately if decommissioning the host."
}

# --- Main ---

main() {
  if [ "${1:-}" = "uninstall" ]; then
    check_root
    if [ "${2:-}" = "--purge" ]; then uninstall purge; else uninstall; fi
    exit 0
  fi

  echo -e "${CYAN}"
  echo "  ____  _____ ____                  _ "
  echo " / ___||  ___|  _ \ __ _ _ __   ___| |"
  echo " \___ \| |_  | |_) / _\` | '_ \ / _ \ |"
  echo "  ___) |  _| |  __/ (_| | | | |  __/ |"
  echo " |____/|_|   |_|   \__,_|_| |_|\___|_|"
  echo -e "${NC}"
  echo ""

  check_root
  check_os
  check_commands

  local arch version current_version mode
  arch=$(detect_arch)
  current_version=$(get_current_version)
  version=$(get_latest_version)

  if [ -n "$current_version" ]; then
    if [ "$current_version" = "$version" ]; then
      log_info "SFPanel v${version} is already installed and up to date"
      # Still reconcile dirs / logrotate / systemd so a deleted or corrupted
      # unit can be repaired by re-running the installer without a version bump.
      setup_dirs
      setup_logrotate
      setup_systemd
      exit 0
    fi
    log_info "Upgrading SFPanel: v${current_version} → v${version}"
    mode="upgrade"
    # A script upgrade stops THIS node's service for the whole download+restart
    # window. Fanning install.sh across all voters at once breaks heartbeat/quorum
    # simultaneously — steer clustered operators to the safe paths.
    if awk '/^cluster:/{f=1;next} /^[^[:space:]]/{f=0} f' "${CONFIG_DIR}/config.yaml" 2>/dev/null \
         | grep -qsE '^[[:space:]]*enabled:[[:space:]]*true'; then
      log_warn "This node is part of a cluster. For zero-downtime upgrades use the panel's"
      log_warn "Cluster → Update (rolling), or run install.sh one node at a time (>=10s apart)."
    fi
  else
    log_info "Installing SFPanel v${version}..."
    mode="install"
  fi

  download_binary "$version" "$arch"
  setup_dirs
  generate_config
  setup_logrotate
  setup_systemd
  print_success "$version" "$mode"
}

main "$@"
