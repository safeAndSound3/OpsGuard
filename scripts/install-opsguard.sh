#!/usr/bin/env bash
set -Eeuo pipefail

# OpsGuard 服务端一键安装脚本。
# 支持从源码发布根目录或独立 x86/ARM 包执行；不安装 Agent。

SERVICE_NAME="opsguard"
INSTALL_ROOT="${OPSGUARD_INSTALL_ROOT:-/opt/opsguard}"
SERVICE_ROOT="${INSTALL_ROOT}/server"
CONFIG_FILE="${SERVICE_ROOT}/config/opsguard.conf"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
LOGROTATE_FILE="/etc/logrotate.d/${SERVICE_NAME}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -d "${SCRIPT_DIR}/../server" ]]; then
  SCRIPT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
else
  SCRIPT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
fi
BACKUP_ROOT="${INSTALL_ROOT}/backup"

log() { printf '[opsguard] %s\n' "$*"; }
fail() { printf '[opsguard] 错误：%s\n' "$*" >&2; exit 1; }

require_root() { [[ "$(id -u)" -eq 0 ]] || fail '请使用 root 执行安装脚本'; }
require_command() { command -v "$1" >/dev/null 2>&1 || fail "缺少系统命令：$1"; }

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'x86' ;;
    aarch64|arm64) printf 'arm' ;;
    *) fail "不支持的 CPU 架构：$(uname -m)，仅支持 x86_64/amd64/aarch64/arm64" ;;
  esac
}

timestamp() { date '+%Y%m%d-%H%M%S'; }

backup_current() {
  local target="${BACKUP_ROOT}/$(timestamp)"
  mkdir -p "$target"
  [[ ! -e "$SERVICE_ROOT/opsguard-server" ]] || cp -a "$SERVICE_ROOT/opsguard-server" "$target/"
  [[ ! -e "$CONFIG_FILE" ]] || cp -a "$CONFIG_FILE" "$target/"
  [[ ! -e "$SERVICE_FILE" ]] || cp -a "$SERVICE_FILE" "$target/"
  printf '%s\n' "$target"
}

prompt_value() {
  local name="$1" prompt="$2" default="${3:-}" value
  if [[ -n "${!name:-}" ]]; then return; fi
  if [[ -n "$default" ]]; then
    read -r -p "${prompt} [${default}]：" value || true
    printf -v "$name" '%s' "${value:-$default}"
  else
    read -r -p "${prompt}：" value || true
    printf -v "$name" '%s' "$value"
  fi
}

prompt_secret() {
  local name="$1" prompt="$2" value
  if [[ -n "${!name:-}" ]]; then return; fi
  read -r -s -p "${prompt}：" value || true
  printf '\n'
  printf -v "$name" '%s' "$value"
}

prepare_config() {
  mkdir -p "${SERVICE_ROOT}/config" "${SERVICE_ROOT}/logs" "${INSTALL_ROOT}/data" "$BACKUP_ROOT"
  if [[ -e "$CONFIG_FILE" ]]; then
    chmod 600 "$CONFIG_FILE"
    log "保留现有配置：${CONFIG_FILE}"
    return
  fi
  if [[ "${OPSGUARD_NON_INTERACTIVE:-false}" == "true" ]]; then
    : "${MYSQL_HOST:?非交互安装必须设置 MYSQL_HOST}"
    : "${MYSQL_USER:?非交互安装必须设置 MYSQL_USER}"
    : "${MYSQL_PASSWORD:?非交互安装必须设置 MYSQL_PASSWORD}"
    : "${OPSGUARD_ADMIN_PASSWORD:?非交互安装必须设置 OPSGUARD_ADMIN_PASSWORD}"
  else
    prompt_value MYSQL_HOST '平台数据库地址' '127.0.0.1'
    prompt_value MYSQL_PORT '平台数据库端口' '3306'
    prompt_value MYSQL_DATABASE '平台数据库名称' 'opsguard'
    prompt_value MYSQL_USER '平台数据库账号' 'opsguard'
    prompt_secret MYSQL_PASSWORD '平台数据库密码'
    prompt_secret OPSGUARD_ADMIN_PASSWORD '初始 admin 密码（至少 8 位）'
  fi
  [[ "${#OPSGUARD_ADMIN_PASSWORD}" -ge 8 ]] || fail 'OPSGUARD_ADMIN_PASSWORD 至少需要 8 位'
  if [[ -z "${OPSGUARD_ENCRYPTION_KEY:-}" ]]; then
    require_command openssl
    OPSGUARD_ENCRYPTION_KEY="$(openssl rand -base64 32)"
  fi
  prompt_value HOST '服务端监听地址' '0.0.0.0'
  prompt_value PORT '服务端端口' '8030'
  prompt_value LOG_FILE '服务端日志文件' "${SERVICE_ROOT}/logs/opsguard-server.log"
  prompt_value TLS_ENABLED '是否启用 HTTPS（true/false）' 'true'
  TLS_ENABLED="${TLS_ENABLED,,}"
  [[ "$TLS_ENABLED" == "true" || "$TLS_ENABLED" == "false" ]] || fail 'TLS_ENABLED 只能填写 true 或 false'
  local tls_config="TLS_ENABLED=false"
  if [[ "$TLS_ENABLED" == "true" ]]; then
    local default_tls_names="127.0.0.1,localhost" detected_ip
    detected_ip="$(hostname -I 2>/dev/null | tr ' ' '\n' | awk '/^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ { print; exit }' || true)"
    [[ -z "$detected_ip" ]] || default_tls_names="${detected_ip},${default_tls_names}"
    prompt_value TLS_SERVER_NAMES 'HTTPS 证书访问 IP 或域名（逗号分隔）' "$default_tls_names"
    tls_config="TLS_ENABLED=true
TLS_CERT_FILE=certs/opsguard.crt
TLS_KEY_FILE=certs/opsguard.key
TLS_SERVER_NAMES=${TLS_SERVER_NAMES}"
  fi
  cat > "$CONFIG_FILE" <<EOF
# OpsGuard 服务端正式配置，由一键部署脚本生成。
HOST=${HOST}
PORT=${PORT}
ENV=production
LOG_FILE=${LOG_FILE}
${tls_config}
OPSGUARD_ADMIN_PASSWORD=${OPSGUARD_ADMIN_PASSWORD}
SESSION_TTL=12h
MYSQL_HOST=${MYSQL_HOST}
MYSQL_PORT=${MYSQL_PORT:-3306}
MYSQL_DATABASE=${MYSQL_DATABASE:-opsguard}
MYSQL_USER=${MYSQL_USER}
MYSQL_PASSWORD=${MYSQL_PASSWORD}
OPSGUARD_ENCRYPTION_KEY=${OPSGUARD_ENCRYPTION_KEY}
EOF
  chmod 600 "$CONFIG_FILE"
  log "已生成配置：${CONFIG_FILE}"
}

install_service_file() {
  cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=OpsGuard 服务端
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=${SERVICE_ROOT}
Environment=OPSGUARD_CONFIG=${CONFIG_FILE}
ExecStartPre=/usr/bin/chmod 0755 ${SERVICE_ROOT}/opsguard-server
ExecStart=${SERVICE_ROOT}/opsguard-server
Restart=always
RestartSec=5
TimeoutStopSec=30
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$SERVICE_FILE"
}

install_logrotate() {
  cat > "$LOGROTATE_FILE" <<EOF
${SERVICE_ROOT}/logs/*.log {
    daily
    rotate 180
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    su root root
}
EOF
  chmod 644 "$LOGROTATE_FILE"
}

install_server() {
  local arch="$1" binary
  if [[ -f "${SCRIPT_ROOT}/server/opsguard-server" ]]; then
    binary="${SCRIPT_ROOT}/server/opsguard-server"
    if [[ -f "${SCRIPT_ROOT}/release-info" ]] && ! grep -q "^ARCH=${arch}$" "${SCRIPT_ROOT}/release-info"; then
      fail "当前服务器架构为 ${arch}，但上传的是其他架构部署包"
    fi
  else
    binary="${SCRIPT_ROOT}/${arch}/server/opsguard-server"
  fi
  [[ -f "$binary" ]] || fail "发布包中不存在服务端二进制：${binary}"
  mkdir -p "$SERVICE_ROOT" "${SERVICE_ROOT}/config" "${SERVICE_ROOT}/logs" "${INSTALL_ROOT}/data" "$BACKUP_ROOT"
  if [[ -f "$SERVICE_ROOT/opsguard-server" || -f "$CONFIG_FILE" ]]; then
    local backup
    backup="$(backup_current)"
    log "已备份当前版本：${backup}"
  fi
  install -m 0755 "$binary" "$SERVICE_ROOT/opsguard-server"
  local example="${SCRIPT_ROOT}/server/config/opsguard.conf.example"
  [[ -f "$example" ]] || example="${SCRIPT_ROOT}/${arch}/server/config/opsguard.conf.example"
  if [[ ! -e "${SERVICE_ROOT}/config/opsguard.conf.example" && -f "$example" ]]; then
    install -m 0644 "$example" "${SERVICE_ROOT}/config/opsguard.conf.example"
  fi
  prepare_config
  install_service_file
  install_logrotate
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" >/dev/null
  systemctl restart "$SERVICE_NAME"
  sleep 2
  systemctl --quiet is-active "$SERVICE_NAME" || { systemctl --no-pager --full status "$SERVICE_NAME" || true; fail '服务启动失败，请查看 journalctl -u opsguard'; }
  log "服务已启动：${SERVICE_NAME}"
  if [[ "${TLS_ENABLED:-true}" == "true" ]]; then
    log "访问地址：https://${HOST:-配置文件中的 HOST}:${PORT:-8030}（首次启动自动生成自签名证书）"
  else
    log "访问地址：http://${HOST:-配置文件中的 HOST}:${PORT:-8030}"
  fi
  log "文件日志：${LOG_FILE:-${SERVICE_ROOT}/logs/opsguard-server.log}"
}

rollback() {
  require_root
  local latest
  latest="$(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort | tail -n 1)"
  [[ -n "$latest" ]] || fail '没有可回滚的备份'
  [[ -f "$latest/opsguard-server" ]] && install -m 0755 "$latest/opsguard-server" "$SERVICE_ROOT/opsguard-server"
  [[ -f "$latest/opsguard.conf" ]] && install -m 0600 "$latest/opsguard.conf" "$CONFIG_FILE"
  [[ -f "$latest/opsguard.service" ]] && install -m 0644 "$latest/opsguard.service" "$SERVICE_FILE"
  systemctl daemon-reload
  systemctl restart "$SERVICE_NAME"
  log "已回滚到：${latest}"
}

status() { systemctl --no-pager --full status "$SERVICE_NAME"; }

uninstall() {
  require_root
  read -r -p '将停止并删除 OpsGuard 服务，但保留 /opt/opsguard 数据和备份。确认？[y/N]：' answer
  [[ "$answer" =~ ^[Yy]$ ]] || { log '已取消卸载'; return; }
  systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
  rm -f "$SERVICE_FILE"
  rm -f "$LOGROTATE_FILE"
  systemctl daemon-reload
  log '服务已卸载，程序、配置、数据和备份仍保留在 /opt/opsguard'
}

main() {
  require_root
  require_command uname
  require_command systemctl
  local command="${1:-install}" arch
  case "$command" in
    install|upgrade)
      arch="$(detect_arch)"
      install_server "$arch"
      ;;
    rollback) rollback ;;
    status) status ;;
    uninstall) uninstall ;;
    *) fail "用法：$0 [install|upgrade|rollback|status|uninstall]" ;;
  esac
}

main "$@"
