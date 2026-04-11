#!/usr/bin/env bash
set -euo pipefail

# SFPanel Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | bash

REPO="svrforum/SFPanel"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/sfpanel"
DATA_DIR="/var/lib/sfpanel"
LOG_DIR="/var/log/sfpanel"
SERVICE_NAME="sfpanel"

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

check_commands() {
  for cmd in curl tar; do
    if ! command -v "$cmd" &>/dev/null; then
      log_error "Required command not found: $cmd"
      exit 1
    fi
  done
}

get_current_version() {
  if [ -x "${INSTALL_DIR}/sfpanel" ]; then
    "${INSTALL_DIR}/sfpanel" version 2>/dev/null | grep -oP 'v\K[0-9]+\.[0-9]+\.[0-9]+' || echo ""
  else
    echo ""
  fi
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
  local url="https://github.com/${REPO}/releases/download/v${version}/sfpanel_${version}_linux_${arch}.tar.gz"
  local tmp_dir

  tmp_dir=$(mktemp -d)

  log_info "Downloading SFPanel v${version} (linux/${arch})..."
  if ! curl -fsSL "$url" -o "${tmp_dir}/sfpanel.tar.gz"; then
    rm -rf "$tmp_dir"
    log_error "Download failed: $url"
    exit 1
  fi

  log_info "Extracting..."
  tar -xzf "${tmp_dir}/sfpanel.tar.gz" -C "$tmp_dir"

  if [ ! -f "${tmp_dir}/sfpanel" ]; then
    rm -rf "$tmp_dir"
    log_error "Binary not found in archive"
    exit 1
  fi

  # Stop service if running (upgrade case)
  if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    log_info "Stopping existing SFPanel service..."
    systemctl stop "$SERVICE_NAME"
  fi

  install -m 755 "${tmp_dir}/sfpanel" "${INSTALL_DIR}/sfpanel"
  rm -rf "$tmp_dir"
  log_info "Binary installed to ${INSTALL_DIR}/sfpanel"
}

setup_dirs() {
  mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
}

generate_config() {
  if [ -f "${CONFIG_DIR}/config.yaml" ]; then
    log_warn "Config already exists at ${CONFIG_DIR}/config.yaml (skipping)"
    return
  fi

  local jwt_secret
  jwt_secret=$(head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 32)

  cat > "${CONFIG_DIR}/config.yaml" <<EOF
# SFPanel Configuration
server:
  host: "0.0.0.0"
  port: 8443

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
  cat > "/etc/logrotate.d/sfpanel" <<'EOF'
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
  cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
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
  log_info "Systemd service enabled and started"
}

print_success() {
  local version="$1"
  local mode="$2"
  local port
  port=$(grep -oP 'port:\s*\K[0-9]+' "${CONFIG_DIR}/config.yaml" 2>/dev/null || echo "8443")

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
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
  systemctl daemon-reload
  rm -f "${INSTALL_DIR}/sfpanel"
  log_info "Binary and service removed"
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
