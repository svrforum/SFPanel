#!/usr/bin/env bash
set -euo pipefail

# SFPanel Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | bash
#        sudo ./install.sh                # install / upgrade
#        sudo ./install.sh uninstall      # remove binary + service
#        FORCE_SYSTEMD=1 sudo ./install.sh   # rewrite systemd unit even if present
#        curl -fsSL …/install.sh | sudo bash -s -- --agent <manager>:3629 <token> --ca <fingerprint> [--advertise <ip>] [--name <name>]
#                                         # install and join a cluster as an agent ('sfpanel cluster token --agent' on a manager prints this)

REPO="svrforum/SFPanel"
INSTALL_SCRIPT_URL="https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh"
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
# Set by parse_agent_args from --agent ADDR TOKEN --ca FP [--advertise IP]
# [--name NAME]: a server given them joins a cluster as an agent before its
# service starts.
AGENT_MANAGER=""
AGENT_TOKEN=""
AGENT_CA=""
AGENT_ADVERTISE=""
AGENT_NAME=""

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
    if [ -n "$(sudo_prefix)" ]; then
      log_error "This script must be run as root (use sudo)"
    else
      log_error "This script must be run as root (this host has no sudo: log in as root)"
    fi
    exit 1
  fi
}

# sudo_prefix goes in front of the commands this script prints: "sudo " where
# the host has sudo, nothing where it has none. Proxmox VE ships without sudo,
# root is its administrator, and so does Debian installed with a root
# password; a printed "sudo …" fails there before it does anything.
sudo_prefix() {
  if command -v sudo >/dev/null 2>&1; then
    echo "sudo "
  fi
}

# is_pve reports a Proxmox VE host. /etc/os-release says Debian there; the
# pve-manager package installed ("ii") is what makes it Proxmox, the same test
# the panel makes (internal/common/platform).
is_pve() {
  local status
  status=$(dpkg-query -W -f='${db:Status-Abbrev}' pve-manager 2>/dev/null) || return 1
  [ "${status%% *}" = "ii" ]
}

# pve_firewall_enabled reports whether the Proxmox VE firewall filters this
# host: `pve-firewall status` prints "Status: enabled/running" then, and
# "Status: disabled/running" while the datacenter firewall is off (the
# default). Captured first so pipefail cannot turn a match into a failure.
pve_firewall_enabled() {
  local out
  out=$(pve-firewall status 2>/dev/null) || true
  case "$out" in
    *"Status: enabled"*) return 0 ;;
    *) return 1 ;;
  esac
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

# read_cluster_value prints one value of the top-level cluster: block, quotes
# and a trailing comment stripped, or nothing. Only keys directly under
# cluster: count, at whatever indent the file uses (the panel writes four
# spaces, an operator may write two), so a nested key of the same name cannot
# answer. Same POSIX-awk-only constraint as read_config_port.
read_cluster_value() {
  awk -v key="$1" -v sq="'" '
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    /^cluster:/                         { in_cluster=1; depth=-1; next }
    /^[^[:space:]]/                     { in_cluster=0 }
    in_cluster {
      match($0, /^[[:space:]]*/)
      if (depth < 0) depth = RLENGTH
      if (RLENGTH == depth && $1 == key ":") {
        v = $0
        sub(/^[^:]*:[[:space:]]*/, "", v)
        sub(/[[:space:]]+#.*$/, "", v)
        sub(/[[:space:]]+$/, "", v)
        gsub("^[\"" sq "]|[\"" sq "]$", "", v)
        print v
        exit
      }
    }
  ' "${CONFIG_DIR}/config.yaml" 2>/dev/null || true
}

# config_is_agent reports a config whose cluster.role is agent. Such a server
# boots as an agent whatever cluster.enabled says, as the panel decides.
config_is_agent() {
  [ "$(read_cluster_value role)" = "agent" ]
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
# admin tasks — so the sandboxing flags can't be enabled without
# breaking features. Notably NOT enabled:
# - RestrictSUIDSGID: apt and dpkg, run by the panel, set the setuid and
#   setgid bits of what they install (sudo, mount.nfs, postfix's
#   postdrop), and refusing that fails the install or the upgrade
# - PrivateTmp: gives the service a mount namespace of its own, so the
#   disks and network shares it mounts never reach the host or Docker
# - ProtectKernelTunables: would break the system tuning feature
#   (sysctl -p)
# - ProtectHome: would break the files browser for $HOME of other users
NoNewPrivileges=false

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
      wait_for_panel_port
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

# wait_for_panel_port waits up to 30s for the panel to accept connections.
# is-active turns true the moment the process starts, before it has opened the
# database or its listener. Waiting here keeps the "Access" URL printed next
# honest, and lets a command chained after the installer — the one-step
# `install.sh | sudo bash && sudo sfpanel cluster join …` — reach the running
# panel instead of finding the port closed and joining offline.
wait_for_panel_port() {
  local port attempt
  port=$(read_config_port)
  port=${port:-3628}
  for attempt in $(seq 1 30); do
    if (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  log_warn "The service is running but port ${port} is not answering yet — see: journalctl -u ${SERVICE_NAME}"
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
  # Nobody signs in to an agent: its managers manage it, and its panel answers
  # only them, so there is no URL to open and no admin to set up.
  local agent=0
  if [ -n "$AGENT_MANAGER" ] || config_is_agent; then
    agent=1
  fi

  echo ""
  echo -e "${CYAN}============================================${NC}"
  if [ "$running" -ne 1 ]; then
    echo -e "${CYAN}   SFPanel v${version} binary installed${NC}"
  elif [ -n "$AGENT_MANAGER" ]; then
    echo -e "${CYAN}   SFPanel v${version} joined a cluster as an agent!${NC}"
  elif [ "$mode" = "upgrade" ]; then
    echo -e "${CYAN}   SFPanel upgraded to v${version}!${NC}"
  else
    echo -e "${CYAN}   SFPanel installed successfully!${NC}"
  fi
  echo -e "${CYAN}============================================${NC}"
  echo ""
  echo -e "  Version:   ${GREEN}v${version}${NC}"
  if [ -n "$AGENT_MANAGER" ]; then
    echo -e "  Cluster:   ${GREEN}joined the cluster at ${AGENT_MANAGER} as an agent; manage it from a manager's panel${NC}"
  elif [ "$agent" -eq 1 ]; then
    echo -e "  Cluster:   ${GREEN}an agent of a cluster; manage it from a manager's panel${NC}"
  fi
  if [ "$running" -eq 1 ]; then
    if [ "$agent" -ne 1 ]; then
      echo -e "  Access:    ${GREEN}${scheme}://<server-ip>:${port}${NC}"
    fi
  else
    echo -e "  ${YELLOW}Not running (no systemd detected). Launch it manually:${NC}"
    echo -e "    ${INSTALL_DIR}/sfpanel ${CONFIG_DIR}/config.yaml"
    if [ "$agent" -ne 1 ]; then
      echo -e "  Then open: ${scheme}://<server-ip>:${port}"
    fi
  fi
  echo -e "  Config:    ${CONFIG_DIR}/config.yaml"
  echo -e "  Data:      ${DATA_DIR}/"
  if check_systemd; then
    echo -e "  Logs:      journalctl -u ${SERVICE_NAME} -f"
    echo ""
    echo -e "  Commands:"
    echo -e "    systemctl status ${SERVICE_NAME}"
    echo -e "    systemctl restart ${SERVICE_NAME}"
    echo -e "    systemctl stop ${SERVICE_NAME}"
  else
    echo -e "  Logs:      ${LOG_DIR}/sfpanel.log"
  fi
  echo ""
  if [ "$agent" -eq 1 ]; then
    # Its firewall was a matter for the join; a plain upgrade leaves it be.
    if [ -n "$AGENT_MANAGER" ] || [ "$mode" = "install" ]; then
      print_agent_firewall "$port"
    fi
  elif [ "$mode" = "install" ]; then
    local sudo pve=0
    sudo=$(sudo_prefix)
    if is_pve; then
      pve=1
    fi
    echo -e "  ${YELLOW}First visit: Set up the admin account in the browser.${NC}"
    echo -e "    ${YELLOW}Setup is restricted to the LAN / loopback. On a public host, tunnel it:${NC}"
    echo -e "    ${YELLOW}  ssh -L ${port}:127.0.0.1:${port} <this-server>  →  open ${scheme}://127.0.0.1:${port}${NC}"
    echo ""
    # On Proxmox VE the host's packet filter is the Proxmox firewall's. ufw
    # beside it would cut the web UI on 8006, the cluster and the guest
    # consoles, so it is never suggested there. With the Proxmox firewall on,
    # wait_for_panel_port's loopback check passes while every other machine
    # is dropped, so say which rule lets the panel through.
    if [ "$pve" -eq 1 ]; then
      if pve_firewall_enabled; then
        echo -e "  ${YELLOW}⚠  The Proxmox VE firewall is on: other machines reach ${port} only once a rule allows it.${NC}"
        echo -e "  ${YELLOW}   Datacenter → Firewall → Add, or under [RULES] in /etc/pve/firewall/cluster.fw:${NC}"
        echo -e "        IN ACCEPT -source <trusted-cidr> -p tcp -dport ${port}"
        echo -e "  ${YELLOW}   Between SFPanel cluster nodes, also:${NC}"
        echo -e "        IN ACCEPT -source <sfpanel-node-ips> -p tcp -dport 3628:3630"
      else
        echo -e "  ${YELLOW}⚠  The Proxmox VE firewall is off — ${port} may be reachable from anywhere.${NC}"
        echo -e "  ${YELLOW}   Restrict it with the Proxmox firewall (Datacenter → Firewall), not ufw.${NC}"
      fi
      echo ""
    # Loud warning when no active firewall is obviously confining the port.
    elif ! { command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi "Status: active"; }; then
      echo -e "  ${YELLOW}⚠  No active ufw firewall detected — ${port} may be reachable from anywhere.${NC}"
      echo -e "  ${YELLOW}   Restrict it before exposing this host (see step 3 below).${NC}"
      echo ""
    fi
    echo -e "  ${CYAN}Recommended next steps (do these before exposing the panel):${NC}"
    echo -e "    1. Enable 2FA in Settings → Account after first login."
    if [ "$scheme" = "https" ]; then
      echo -e "    2. The panel serves HTTPS with its own local CA. Download the CA from"
      echo -e "       Settings → System and install it on your devices once to clear the browser warning."
    else
      echo -e "    2. Front the panel with TLS (Caddy / nginx / Cloudflare Tunnel)."
      echo -e "       This panel serves plain HTTP — never expose ${port} to the public Internet."
    fi
    if [ "$pve" -eq 1 ]; then
      echo -e "    3. Restrict ${port} to LAN/VPN only (Proxmox firewall: Datacenter → Firewall)."
    else
      echo -e "    3. Restrict ${port} to LAN/VPN only (ufw default deny + allow from trusted CIDR)."
    fi
    echo ""
    echo -e "  ${CYAN}Tips:${NC}"
    echo -e "    Change port:  Edit ${CONFIG_DIR}/config.yaml → server.port, then restart the panel."
    echo ""
    echo -e "    Join a cluster: on an existing node run  ${sudo}sfpanel cluster token"
    echo -e "                    and paste the command it prints here, e.g."
    echo -e "                    ${sudo}sfpanel cluster join <leader-ip>:3629 <token> --ca <fingerprint>"
    if [ "$pve" -eq 1 ]; then
      echo -e "                    Nodes talk on TCP 3628-3630; allow them between nodes in the Proxmox firewall."
    else
      echo -e "                    Nodes talk on TCP 3628-3630; allow them between nodes in ufw."
    fi
    echo ""
  fi
}

# print_agent_firewall says what an agent's firewall has to let its managers
# through to: the cluster gRPC port, where they send requests, and the panel
# port, where they relay streams, files and terminals. The Raft port is the
# managers' alone. The firewall it names is chosen as print_success chooses:
# never ufw on Proxmox VE.
print_agent_firewall() {
  local port="$1" grpc_port sudo
  sudo=$(sudo_prefix)
  grpc_port=$(read_cluster_value grpc_port)
  # A leading zero reads as octal below, where 0[89] would end the installer.
  case "$grpc_port" in
    ''|*[!0-9]*|0*) grpc_port=3629 ;;
  esac
  echo -e "  Its managers reach this server on TCP ${port} (panel) and ${grpc_port} (cluster gRPC);"
  echo -e "  an agent needs no Raft port ($((grpc_port + 1)))."
  if is_pve; then
    if pve_firewall_enabled; then
      echo -e "  ${YELLOW}⚠  The Proxmox VE firewall is on: the managers reach ${port} and ${grpc_port} only once a rule allows them.${NC}"
      echo -e "  ${YELLOW}   Datacenter → Firewall → Add, or under [RULES] in /etc/pve/firewall/cluster.fw:${NC}"
      echo -e "        IN ACCEPT -source <manager-ips> -p tcp -dport ${port},${grpc_port}"
    else
      echo -e "  ${YELLOW}⚠  The Proxmox VE firewall is off — ${port} and ${grpc_port} may be reachable from anywhere.${NC}"
      echo -e "  ${YELLOW}   Restrict them to the managers with the Proxmox firewall (Datacenter → Firewall), not ufw.${NC}"
    fi
  elif command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi "Status: active"; then
    echo -e "  ufw is active: allow each manager, e.g."
    echo -e "        ${sudo}ufw allow from <manager-ip> to any port ${port},${grpc_port} proto tcp"
  else
    echo -e "  ${YELLOW}⚠  No active ufw firewall detected — ${port} and ${grpc_port} may be reachable from anywhere.${NC}"
    echo -e "  ${YELLOW}   The agent answers its managers only; restrict both ports to them all the same.${NC}"
  fi
  echo ""
  echo -e "  ${CYAN}Tips:${NC}"
  echo -e "    Check the link:  ${sudo}sfpanel agent status"
  echo -e "    Leave:           remove the node on a manager, then here  ${sudo}sfpanel agent reset --yes"
  echo ""
}

# --- Joining as an agent ---

# parse_agent_args reads the installer's arguments, which past uninstall can
# only be an agent join's: --agent ADDR TOKEN, --ca FP, --advertise IP and
# --name NAME, in any order. What it can tell is wrong it refuses before
# anything is downloaded; 'sfpanel agent join' checks the rest — the
# fingerprint's form, the advertise address, the name — before the token is
# sent. An argument it does not know is refused too: skipped, a misspelt
# --advertise would have the agent registered at an address detected instead.
parse_agent_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --agent)
        # A join token is hex, so a value that starts with - is the next
        # option: the address or the token was left out.
        if [ "$#" -lt 3 ] || [ -z "$2" ] || [ -z "$3" ] || [ "${2#-}" != "$2" ] || [ "${3#-}" != "$3" ]; then
          agent_args_error "--agent needs the manager's address and the join token: --agent <manager>:3629 <token>"
        fi
        AGENT_MANAGER="$2"
        AGENT_TOKEN="$3"
        shift 3
        ;;
      --ca|--advertise|--name)
        if [ "$#" -lt 2 ] || [ -z "$2" ] || [ "${2#-}" != "$2" ]; then
          agent_args_error "$1 needs a value"
        fi
        if [ "$1" = "--ca" ]; then
          AGENT_CA="$2"
        elif [ "$1" = "--advertise" ]; then
          AGENT_ADVERTISE="$2"
        else
          AGENT_NAME="$2"
        fi
        shift 2
        ;;
      *)
        agent_args_error "Unknown argument: $1"
        ;;
    esac
  done
  if [ -z "$AGENT_MANAGER" ]; then
    agent_args_error "--agent <manager>:3629 <token> is missing"
  fi
  if [ -z "$AGENT_CA" ]; then
    agent_args_error "--ca is missing: copy the whole command 'sfpanel cluster token --agent' prints on a manager, fingerprint included"
  fi
  local host="${AGENT_MANAGER%:*}" port="${AGENT_MANAGER##*:}"
  case "$port" in
    ''|*[!0-9]*) port=0 ;;
  esac
  if [ "$host" = "$AGENT_MANAGER" ] || [ -z "$host" ] || [ "${#port}" -gt 5 ] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    agent_args_error "The manager address ${AGENT_MANAGER} needs a port: the manager's cluster gRPC address, as 'sfpanel cluster token --agent' prints it"
  fi
}

# agent_args_error stops the installer on arguments parse_agent_args cannot use.
agent_args_error() {
  log_error "$1"
  log_error "Usage: curl -fsSL ${INSTALL_SCRIPT_URL} | $(sudo_prefix)bash -s -- --agent <manager>:3629 <token> --ca <fingerprint> [--advertise <ip>] [--name <name>]"
  log_error "       ('sfpanel cluster token --agent' on a manager prints this command, token and fingerprint filled in)"
  exit 1
}

# check_can_join_as_agent refuses --agent on a server in a cluster already.
# 'sfpanel agent join' refuses it as well, but only once download_binary has
# stopped the service, which would leave a manager — a voter — or an agent
# down.
check_can_join_as_agent() {
  local sudo
  sudo=$(sudo_prefix)
  if config_is_agent; then
    log_error "This server is an agent already: '${sudo}sfpanel agent status' shows its link."
    log_error "To upgrade it, run the installer without --agent. To join again, remove it on a manager, then run '${sudo}sfpanel agent reset --yes' here first."
    exit 1
  fi
  if [ "$(read_cluster_value enabled)" = "true" ]; then
    log_error "This server is a cluster manager: '${sudo}sfpanel cluster leave' takes it out of the cluster before it can join as an agent."
    exit 1
  fi
}

# run_agent_join joins this server as an agent with what parse_agent_args
# read: no-restart while the service is stopped or not set up yet, so that its
# first start is an agent's; restart for a panel that may be running, which
# the join restarts as an agent. Piped from curl, the installer's stdin is the
# script itself, so the join gets /dev/null.
run_agent_join() {
  local mode="$1" sudo
  set -- agent join "$AGENT_MANAGER" "$AGENT_TOKEN" --ca "$AGENT_CA"
  if [ -n "$AGENT_ADVERTISE" ]; then
    set -- "$@" --advertise "$AGENT_ADVERTISE"
  fi
  # Only when given: this script comes from main, and a release from before
  # --name refuses an option it does not know.
  if [ -n "$AGENT_NAME" ]; then
    set -- "$@" --name "$AGENT_NAME"
  fi
  set -- "$@" --config "${CONFIG_DIR}/config.yaml"
  if [ "$mode" = "no-restart" ]; then
    set -- "$@" --no-restart
  fi
  if ! "${INSTALL_DIR}/sfpanel" "$@" </dev/null; then
    sudo=$(sudo_prefix)
    log_error "Joining the cluster at ${AGENT_MANAGER} as an agent failed: the reason is above."
    if [ "$mode" = "no-restart" ]; then
      log_error "sfpanel was not started. Once the cause is fixed, run the same command again:"
    else
      log_error "Once the cause is fixed, run the same command again:"
    fi
    log_error "  curl -fsSL ${INSTALL_SCRIPT_URL} | ${sudo}bash -s -- --agent ${AGENT_MANAGER} <token> --ca ${AGENT_CA}${AGENT_ADVERTISE:+ --advertise ${AGENT_ADVERTISE}}${AGENT_NAME:+ --name ${AGENT_NAME}}"
    log_error "A token the reason calls used, expired or not found needs a new one: '${sudo}sfpanel cluster token --agent' on a manager."
    exit 1
  fi
  # The binary saved before an upgrade may predate agents: swapped back in
  # under an agent's config it would serve a login page (and, with no admin
  # yet, the setup screen) instead of an agent's routes, so a new binary that
  # fails to start is left down.
  BINARY_BACKED_UP=0
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
  if [ -x "${INSTALL_DIR}/sfpanel" ] && ! config_is_agent \
     && awk '/^cluster:/{f=1;next} /^[^[:space:]]/{f=0} f' "${CONFIG_DIR}/config.yaml" 2>/dev/null \
          | grep -qsE '^[[:space:]]*enabled:[[:space:]]*true'; then
    log_info "Cluster member detected — leaving the cluster first..."
    if "${INSTALL_DIR}/sfpanel" cluster leave --config "${CONFIG_DIR}/config.yaml" >/dev/null 2>&1; then
      log_info "Left the cluster cleanly."
    else
      log_warn "Could not leave the cluster automatically (peer unreachable / quorum lost)."
      log_warn "This node is STILL a Raft voter. From a surviving node run:"
      log_warn "    $(sudo_prefix)sfpanel cluster remove <node-id>"
      log_warn "or follow docs/specs/cluster-partition-runbook.md (peers.json recovery)."
    fi
  elif [ -x "${INSTALL_DIR}/sfpanel" ] && config_is_agent; then
    # An agent is outside Raft and cannot take itself out of the cluster: its
    # record stays until a manager removes it. The reset makes the config that
    # stays without --purge a standalone panel's and removes the agent's key
    # and certificates. The service is stopped first, because the reset
    # restarts a running one — as a standalone panel, moments before it goes.
    local node_id
    node_id=$(read_cluster_value node_id)
    log_info "Cluster agent detected — resetting it first..."
    if check_systemd; then
      systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    fi
    if "${INSTALL_DIR}/sfpanel" agent reset --yes --config "${CONFIG_DIR}/config.yaml" >/dev/null; then
      log_info "Reset the agent: its cluster key and certificates are removed."
    else
      log_warn "Could not reset the agent; ${CONFIG_DIR} keeps its cluster key and certificates."
    fi
    log_warn "The cluster keeps this agent's record until a manager removes it. Unless it was removed already, on a manager run:"
    log_warn "    $(sudo_prefix)sfpanel cluster remove ${node_id:-<node-id>}"
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
  if [ "$#" -gt 0 ]; then
    parse_agent_args "$@"
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
  if [ -n "$AGENT_MANAGER" ]; then
    check_can_join_as_agent
  fi

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
      # The way back after a join that failed: the binary is in place, only
      # the join is left. It goes before setup_systemd, which starts a service
      # that is not running — as an agent then, never as an unowned standalone
      # panel; one that is running the join restarts as an agent.
      if [ -n "$AGENT_MANAGER" ]; then
        run_agent_join restart
      fi
      setup_systemd
      if [ -n "$AGENT_MANAGER" ]; then
        print_success "$version" install
      fi
      exit 0
    fi
    log_info "Upgrading SFPanel: v${current_version} → v${version}"
    mode="upgrade"
    # A script upgrade stops THIS node's service for the whole download+restart
    # window. Fanning install.sh across all voters at once breaks heartbeat/quorum
    # simultaneously — steer clustered operators to the safe paths. An agent
    # does not vote, so stopping it takes no voter down.
    if awk '/^cluster:/{f=1;next} /^[^[:space:]]/{f=0} f' "${CONFIG_DIR}/config.yaml" 2>/dev/null \
         | grep -qsE '^[[:space:]]*enabled:[[:space:]]*true' && ! config_is_agent; then
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
  # Joined before the service first starts, the first boot is an agent's: a
  # standalone panel's setup screen, open to the LAN until someone claims it,
  # never comes up. On an upgrade download_binary has stopped the service.
  if [ -n "$AGENT_MANAGER" ]; then
    run_agent_join no-restart
  fi
  setup_logrotate
  setup_systemd
  print_success "$version" "$mode"
}

main "$@"
