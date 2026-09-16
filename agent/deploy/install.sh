#!/usr/bin/env sh
set -eu

BIN=${1:-./opsguard-agent}
install -d -m 0755 /opt/opsguard-agent /etc/opsguard-agent /var/lib/opsguard-agent
id -u opsguard >/dev/null 2>&1 || useradd --system --home /var/lib/opsguard-agent --shell /usr/sbin/nologin opsguard
install -m 0755 "$BIN" /opt/opsguard-agent/opsguard-agent
install -m 0640 deploy/opsguard-agent.service /etc/systemd/system/opsguard-agent.service
chown -R opsguard:opsguard /var/lib/opsguard-agent /etc/opsguard-agent
systemctl daemon-reload
echo "Copy config/agent.conf.example to /etc/opsguard-agent/agent.conf, edit TOKEN, then run: systemctl enable --now opsguard-agent"
