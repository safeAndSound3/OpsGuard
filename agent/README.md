# OpsGuard Agent

用于 `SSH - Agent` 数据源的 Linux 主机 Agent。平台以拉取方式读取主机指标，
并按告警规则向 Agent 发送文件检测和脚本检测请求。

## 接口与安全

- `GET /health` 用于连通性检查。
- `GET /v1/metrics`、`POST /v1/checks/file`、`POST /v1/checks/script`
  均需要 `Authorization: Bearer <TOKEN>` 认证。
- 默认监听端口为 `4001`，使用 HTTP。生产环境需同时配置 `ALLOWED_PLATFORM_IPS` 和主机防火墙。
- `ALLOWED_PLATFORM_IPS` 支持逗号分隔的单个 IP 或 CIDR 网段，例如
  `10.0.0.10/32,10.0.1.0/24`。为空时仅为兼容升级保留，实际生产环境必须填写。
- 白名单填写的是 Agent 实际看到的平台来源地址，不是浏览器地址。经 NAT、堡垒机或
  端口转发部署前，应先从 Agent 主机确认该来源地址；填写错误会拒绝平台采集请求。
- 文件和脚本检测会记录来源、检测类型、脱敏后的命令或路径、退出码和结果，可使用
  `journalctl -u opsguard-agent` 查看。
- 文件路径、文件名正则、脚本和工作目录均由平台规则下发，不在 Agent 配置中写死。

## 构建与安装

```bash
cd agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o opsguard-agent ./cmd/opsguard-agent
sudo sh deploy/install.sh ./opsguard-agent
sudo cp config/agent.conf.example /etc/opsguard-agent/agent.conf
sudo editor /etc/opsguard-agent/agent.conf
sudo systemctl enable --now opsguard-agent
curl http://127.0.0.1:4001/health
```

在 OpsGuard 中新增 `SSH - Agent` 数据源，填写 Agent 主机、端口，以及
`/etc/opsguard-agent/agent.conf` 中相同的 `TOKEN`。
