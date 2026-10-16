#!/usr/bin/env sh
set -eu
umask 077

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PACKAGE_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
DEFAULT_INSTALL_ROOT=$(CDPATH= cd -- "$PACKAGE_ROOT/.." && pwd)
INSTALL_ROOT=${OPSGUARD_INSTALL_ROOT:-$DEFAULT_INSTALL_ROOT}
AGENT_ROOT=$INSTALL_ROOT/agent
LOG_ROOT=$INSTALL_ROOT/logs/agent
CONFIG_FILE=$AGENT_ROOT/config/agent.conf
SERVICE_FILE=/etc/systemd/system/opsguard-agent.service
BIN=${1:-$SCRIPT_DIR/../opsguard-agent}

[ "$(id -u)" -eq 0 ] || { echo '请使用 root 执行 Agent 安装脚本' >&2; exit 1; }
[ -f "$BIN" ] || { echo "Agent 二进制不存在：$BIN" >&2; exit 1; }
install -d -m 0755 "$AGENT_ROOT"
id -u opsguard >/dev/null 2>&1 || useradd --system --home "$AGENT_ROOT/data" --shell /usr/sbin/nologin opsguard
install -d -m 0750 -o opsguard -g opsguard "$AGENT_ROOT/config" "$AGENT_ROOT/data" "$LOG_ROOT"

if [ ! -f "$CONFIG_FILE" ]; then
  if [ -f /etc/opsguard-agent/agent.conf ]; then
    cp /etc/opsguard-agent/agent.conf "$CONFIG_FILE"
    for name in agent.crt agent.key; do
      if [ -f "/etc/opsguard-agent/$name" ] && [ ! -f "$AGENT_ROOT/config/$name" ]; then
        cp -p "/etc/opsguard-agent/$name" "$AGENT_ROOT/config/$name"
      fi
    done
  else
    printf 'LOG_FILE=%s\n' "$LOG_ROOT/agent.log" > "$CONFIG_FILE"
  fi
  rewritten=$(mktemp "$AGENT_ROOT/config/.agent.conf.XXXXXX")
  awk -v logfile="$LOG_ROOT/agent.log" -v configdir="$AGENT_ROOT/config" '
    /^LOG_FILE=/ { print "LOG_FILE=" logfile; next }
    /^TLS_CERT_FILE=\/etc\/opsguard-agent\/agent.crt$/ { print "TLS_CERT_FILE=" configdir "/agent.crt"; next }
    /^TLS_KEY_FILE=\/etc\/opsguard-agent\/agent.key$/ { print "TLS_KEY_FILE=" configdir "/agent.key"; next }
    { print }
  ' "$CONFIG_FILE" > "$rewritten"
  mv -f "$rewritten" "$CONFIG_FILE"
  chown opsguard:opsguard "$CONFIG_FILE"
  chmod 0600 "$CONFIG_FILE"
fi
for name in agent.crt agent.key; do
  if [ -f "$AGENT_ROOT/config/$name" ]; then
    chown opsguard:opsguard "$AGENT_ROOT/config/$name"
    chmod 0600 "$AGENT_ROOT/config/$name"
  fi
done

ACTIVE_BEFORE=false
if systemctl --quiet is-active opsguard-agent; then ACTIVE_BEFORE=true; fi
PREVIOUS_BIN=
PREVIOUS_SERVICE=
if [ -f "$AGENT_ROOT/opsguard-agent" ]; then
  PREVIOUS_BIN=$(mktemp "$AGENT_ROOT/.opsguard-agent.previous.XXXXXX")
  cp -p "$AGENT_ROOT/opsguard-agent" "$PREVIOUS_BIN"
fi
if [ -f "$SERVICE_FILE" ]; then
  PREVIOUS_SERVICE=$(mktemp /etc/systemd/system/.opsguard-agent.service.XXXXXX)
  cp -p "$SERVICE_FILE" "$PREVIOUS_SERVICE"
fi
STAGED_BIN=$(mktemp "$AGENT_ROOT/.opsguard-agent.XXXXXX")
trap 'rm -f "$STAGED_BIN"; if [ -n "$PREVIOUS_BIN" ]; then rm -f "$PREVIOUS_BIN"; fi; if [ -n "$PREVIOUS_SERVICE" ]; then rm -f "$PREVIOUS_SERVICE"; fi' EXIT
install -m 0755 "$BIN" "$STAGED_BIN"
mv -f "$STAGED_BIN" "$AGENT_ROOT/opsguard-agent"

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=OpsGuard host monitoring agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=opsguard
Group=opsguard
ExecStart=${AGENT_ROOT}/opsguard-agent -config ${CONFIG_FILE}
Restart=always
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryMax=256M
CPUQuota=50%
TasksMax=128
KillMode=control-group
ReadWritePaths=${AGENT_ROOT}/config ${AGENT_ROOT}/data ${LOG_ROOT}

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "$SERVICE_FILE"
systemctl daemon-reload
if [ "$ACTIVE_BEFORE" = true ]; then
  if ! systemctl restart opsguard-agent || ! systemctl --quiet is-active opsguard-agent; then
    if [ -n "$PREVIOUS_BIN" ]; then mv -f "$PREVIOUS_BIN" "$AGENT_ROOT/opsguard-agent"; fi
    if [ -n "$PREVIOUS_SERVICE" ]; then mv -f "$PREVIOUS_SERVICE" "$SERVICE_FILE"; systemctl daemon-reload; fi
    systemctl restart opsguard-agent || true
    echo 'Agent 升级失败，已尝试恢复上一版本；请查看 journalctl -u opsguard-agent' >&2
    exit 1
  fi
  echo "Agent 已升级：$AGENT_ROOT"
else
  echo "Agent 已安装：$AGENT_ROOT；请检查 $CONFIG_FILE 中的 TOKEN 和 ALLOWED_PLATFORM_IPS，再运行 systemctl enable --now opsguard-agent"
fi
