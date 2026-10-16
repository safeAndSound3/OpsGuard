#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# OpsGuard 服务端一键安装脚本。
# 发布包位于 opsguard/install，运行目录位于相邻的 opsguard 根目录；不安装 Agent。

SERVICE_NAME="opsguard"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ "$(basename "$SCRIPT_DIR")" == "install" ]]; then
  SCRIPT_ROOT="$SCRIPT_DIR"
else
  SCRIPT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
fi
if [[ "$(basename "$SCRIPT_ROOT")" == "install" ]]; then
  DEFAULT_INSTALL_ROOT="$(cd -- "${SCRIPT_ROOT}/.." && pwd)"
else
  DEFAULT_INSTALL_ROOT="$SCRIPT_ROOT"
fi
INSTALL_ROOT="${OPSGUARD_INSTALL_ROOT:-$DEFAULT_INSTALL_ROOT}"
SERVICE_ROOT="${INSTALL_ROOT}/server"
CONFIG_FILE="${SERVICE_ROOT}/config/opsguard.conf"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
LOGROTATE_FILE="/etc/logrotate.d/${SERVICE_NAME}"
BACKUP_ROOT="${INSTALL_ROOT}/backup"

log() { printf '[opsguard] %s\n' "$*"; }
fail() { printf '[opsguard] 错误：%s\n' "$*" >&2; exit 1; }

require_root() { [[ "$(id -u)" -eq 0 ]] || fail '请使用 root 执行安装脚本'; }
require_command() { command -v "$1" >/dev/null 2>&1 || fail "缺少系统命令：$1"; }

install_binary() {
  local source="$1" destination="$2" staged
  staged="$(mktemp "${destination}.XXXXXX")" || fail '创建二进制临时文件失败'
  if ! install -m 0755 "$source" "$staged"; then
    rm -f "$staged"
    fail "复制二进制失败：${source}"
  fi
  if ! mv -f "$staged" "$destination"; then
    rm -f "$staged"
    fail "替换二进制失败：${destination}"
  fi
}

config_value() {
  local key="$1"
  awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$CONFIG_FILE"
}

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
  mkdir -p "${SERVICE_ROOT}/config" "${INSTALL_ROOT}/logs" "${INSTALL_ROOT}/data" "$BACKUP_ROOT"
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
  prompt_value LOG_FILE '服务端日志文件' "${INSTALL_ROOT}/logs/opsguard-server.log"
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
${INSTALL_ROOT}/logs/*.log {
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
  local arch="$1" binary backup=""
  if [[ -f "${SCRIPT_ROOT}/server/opsguard-server" ]]; then
    binary="${SCRIPT_ROOT}/server/opsguard-server"
    if [[ -f "${SCRIPT_ROOT}/release-info" ]]; then
      local package_arch
      package_arch="$(awk -F= '$1 == "ARCH" { gsub(/\r/, "", $2); print $2; exit }' "${SCRIPT_ROOT}/release-info")"
      [[ "$package_arch" == "$arch" ]] || fail "当前服务器架构为 ${arch}，但上传的是 ${package_arch:-未知} 架构部署包"
    fi
  else
    binary="${SCRIPT_ROOT}/${arch}/server/opsguard-server"
  fi
  [[ -f "$binary" ]] || fail "发布包中不存在服务端二进制：${binary}"
  mkdir -p "$SERVICE_ROOT" "${SERVICE_ROOT}/config" "${INSTALL_ROOT}/logs" "${INSTALL_ROOT}/data" "$BACKUP_ROOT"
  if [[ -f "$SERVICE_ROOT/opsguard-server" || -f "$CONFIG_FILE" ]]; then
    backup="$(backup_current)"
    log "已备份当前版本：${backup}"
  fi
  install_binary "$binary" "$SERVICE_ROOT/opsguard-server"
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
  if ! systemctl --quiet is-active "$SERVICE_NAME"; then
    systemctl --no-pager --full status "$SERVICE_NAME" || true
    if [[ -n "$backup" && -f "$backup/opsguard-server" ]]; then
      log '新版本启动失败，正在恢复上一版本'
      install_binary "$backup/opsguard-server" "$SERVICE_ROOT/opsguard-server"
      [[ ! -f "$backup/opsguard.conf" ]] || install -m 0600 "$backup/opsguard.conf" "$CONFIG_FILE"
      [[ ! -f "$backup/opsguard.service" ]] || install -m 0644 "$backup/opsguard.service" "$SERVICE_FILE"
      systemctl daemon-reload
      systemctl restart "$SERVICE_NAME" || true
      log "上一版本已恢复：${backup}"
    fi
    fail '服务启动失败，请查看 journalctl -u opsguard'
  fi
  log "服务已启动：${SERVICE_NAME}"
  local configured_host configured_port configured_tls configured_log
  configured_host="$(config_value HOST)"
  configured_port="$(config_value PORT)"
  configured_tls="$(config_value TLS_ENABLED)"
  configured_log="$(config_value LOG_FILE)"
  if [[ "$configured_tls" == "true" ]]; then
    log "访问地址：https://${configured_host:-配置文件中的 HOST}:${configured_port:-8030}（首次启动自动生成自签名证书）"
  else
    log "访问地址：http://${configured_host:-配置文件中的 HOST}:${configured_port:-8030}"
  fi
  log "文件日志：${configured_log:-${INSTALL_ROOT}/logs/opsguard-server.log}"
}

rollback() {
  require_root
  local latest
  latest="$(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort | tail -n 1)"
  [[ -n "$latest" ]] || fail '没有可回滚的备份'
  [[ -f "$latest/opsguard-server" ]] && install_binary "$latest/opsguard-server" "$SERVICE_ROOT/opsguard-server"
  [[ -f "$latest/opsguard.conf" ]] && install -m 0600 "$latest/opsguard.conf" "$CONFIG_FILE"
  [[ -f "$latest/opsguard.service" ]] && install -m 0644 "$latest/opsguard.service" "$SERVICE_FILE"
  systemctl daemon-reload
  systemctl restart "$SERVICE_NAME"
  log "已回滚到：${latest}"
}

status() { systemctl --no-pager --full status "$SERVICE_NAME"; }

uninstall() {
  require_root
  read -r -p "将停止并删除 OpsGuard 服务，但保留 ${INSTALL_ROOT} 中的数据和备份。确认？[y/N]：" answer
  [[ "$answer" =~ ^[Yy]$ ]] || { log '已取消卸载'; return; }
  systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
  rm -f "$SERVICE_FILE"
  rm -f "$LOGROTATE_FILE"
  systemctl daemon-reload
  log "服务已卸载，程序、配置、数据和备份仍保留在 ${INSTALL_ROOT}"
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
