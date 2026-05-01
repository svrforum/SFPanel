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

# --- Core functions ---

get_latest_version() {
  local version
  version=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
  if [ -z "$version" ]; then
    log_error "Failed to fetch latest version. Check https://github.com/${REPO}/releases"
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
  tar -xzf "${tmp_dir}/sfpanel.tar.gz" -C "$tmp_dir"

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

  install -m 755 "${tmp_dir}/sfpanel" "${INSTALL_DIR}/sfpanel"
  rm -rf "$tmp_dir"
  log_info "Binary installed to ${INSTALL_DIR}/sfpanel"
}

setup_dirs() {
  mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
  # DB + cluster material under /var/lib/sfpanel contain bcrypt hashes, TOTP
  # secrets, and mTLS private keys. Keep root-only.
  chmod 700 "$DATA_DIR"
  # /etc/sfpanel holds config.yaml (JWT secret).
  chmod 700 "$CONFIG_DIR"
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

  cat > "${CONFIG_DIR}/config.yaml" <<EOF
# SFPanel Configuration
server:
  host: "0.0.0.0"
  port: 19443

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
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/sfpanel ${CONFIG_DIR}/config.yaml
# Restart=always (not on-failure) because several HTTP handlers
# intentionally exit the process so a supervisor can pick up new cluster
# config — on-failure would treat those clean exits as "done" and leave
# the panel down. See internal/feature/cluster/handler.go.
Restart=always
RestartSec=5
LimitNOFILE=65536

# SFPanel needs full system access for firewall (ufw), packages (apt),
# disk management, and other system administration tasks.
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
      return 0
    fi
    sleep 1
  done
  log_error "Service ${SERVICE_NAME} failed to start within 10s. Recent journal:"
  journalctl -u "$SERVICE_NAME" -n 30 --no-pager 2>&1 | sed 's/^/  /' >&2 || true
  exit 1
}

print_success() {
  local version="$1"
  local mode="$2"
  local port
  port=$(read_config_port)
  : "${port:=19443}"

  echo ""
  echo -e "${CYAN}============================================${NC}"
  if [ "$mode" = "upgrade" ]; then
    echo -e "${CYAN}   SFPanel upgraded to v${version}!${NC}"
  else
    echo -e "${CYAN}   SFPanel installed successfully!${NC}"
  fi
  echo -e "${CYAN}============================================${NC}"
  echo ""
  echo -e "  Version:   ${GREEN}v${version}${NC}"
  echo -e "  Access:    ${GREEN}http://<server-ip>:${port}${NC}"
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
    echo -e "  ${YELLOW}First visit: Set up admin account in the browser${NC}"
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
  log_info "Uninstalling SFPanel..."
  if check_systemd; then
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  fi
  rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
  rm -f "/etc/logrotate.d/sfpanel"
  if check_systemd; then
    systemctl daemon-reload
  fi
  rm -f "${INSTALL_DIR}/sfpanel"
  log_info "Binary, service unit, and logrotate config removed"
  log_warn "Config (${CONFIG_DIR}) and data (${DATA_DIR}) preserved. Remove manually if needed."
}

# --- Main ---

main() {
  if [ "${1:-}" = "uninstall" ]; then
    check_root
    uninstall
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
      exit 0
    fi
    log_info "Upgrading SFPanel: v${current_version} → v${version}"
    mode="upgrade"
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
