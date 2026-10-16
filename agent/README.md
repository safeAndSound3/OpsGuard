# OpsGuard Agent

用于 `SSH - Agent` 数据源的 Linux 主机 Agent。平台以拉取方式读取主机指标，
并按告警规则向 Agent 发送文件检测和脚本检测请求。

## 接口与安全

- `GET /health` 用于连通性检查。
- `GET /v1/metrics`、`POST /v1/checks/file`、`POST /v1/checks/script`
  均需要 `Authorization: Bearer <TOKEN>` 认证。
- 默认监听端口为 `4001`。平台强制使用 HTTPS 访问 Agent：配置
  `TLS_ENABLED=true`（推荐默认值）后，证书缺失时 Agent 会自动生成自签证书，
  无需手工签发。生产环境需同时配置 `ALLOWED_PLATFORM_IPS` 和主机防火墙。
- `ALLOWED_PLATFORM_IPS` 支持逗号分隔的单个 IP 或 CIDR 网段，例如
  `10.0.0.10/32,10.0.1.0/24`。为空时仅为兼容升级保留，实际生产环境必须填写。
- 白名单填写的是 Agent 实际看到的平台来源地址，不是浏览器地址。经 NAT、堡垒机或
  端口转发部署前，应先从 Agent 主机确认该来源地址；填写错误会拒绝平台采集请求。
- `TOKEN` 支持逗号分隔多个令牌（如 `TOKEN=旧令牌,新令牌`），用于滚动轮换。
  平台侧「轮换令牌」生成的新令牌需追加到此处并重启 Agent，之后可移除旧令牌。
- 查看 TLS 证书指纹：`opsguard-agent --print-fingerprint`（会自动生成证书并打印
  SHA-256 指纹，随后退出）。平台首次连接会自动记录该指纹（TOFU），之后证书变化
  将拒绝连接。
- 文件和脚本检测会记录来源、检测类型、脱敏后的命令或路径、退出码和结果，可使用
  `journalctl -u opsguard-agent` 查看。
- 文件路径、文件名正则、脚本和工作目录均由平台规则下发，不在 Agent 配置中写死。

## 构建与安装

在对应架构的服务端 ZIP 中找到 `opsguard/install/opsguard-agent-*.zip`，
将这个 Agent ZIP 单独复制到需要监控的 Linux 节点。解压后目录为
`opsguard/install`。进入解压后的 `opsguard` 目录运行：

```bash
sudo sh install/install-agent.sh
sudo nano agent/config/agent.conf  # 保留 LOG_FILE，追加平台弹窗配置并修改来源 IP
sudo systemctl enable --now opsguard-agent
```

安装脚本按自身位置确定安装根目录，生成同级的 `agent` 和
`logs/agent`。服务端与 Agent 可分开部署；服务端脚本不会自动安装 Agent。
首次部署时，安装脚本只在 `agent.conf` 写入实际日志路径 `LOG_FILE`。
平台「SSH - Agent」数据源弹窗给出其余配置；修改 `ALLOWED_PLATFORM_IPS` 后，
将配置追加到 `agent/config/agent.conf` 末尾。
升级时再次运行同一命令，配置和证书会保留；已有节点只更新必要配置项。

从源码构建时也可直接安装：

```bash
cd agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o opsguard-agent ./cmd/opsguard-agent
sudo sh deploy/install.sh ./opsguard-agent
sudo editor config/agent.conf   # 从源码目录执行时的安装根目录
sudo systemctl enable --now opsguard-agent
# 配置了平台 IP 白名单后，请在平台上执行数据源“测试连接”验证
```

安装脚本会原子替换二进制，并在服务已运行时自动重启；现有配置和证书不会被覆盖。
旧版 `/etc/opsguard-agent` 中的配置和证书在首次迁移时会复制到新目录。证书与私钥必须成对
保存，缺少其中一个时 Agent 会报错，避免重新生成证书导致平台指纹校验失败。

在 OpsGuard 中新增 `SSH - Agent` 数据源，填写 Agent 主机、端口，以及
`opsguard/agent/config/agent.conf` 中相同的 `TOKEN`。平台保存后会提示一次 Agent
配置；新建/编辑数据源后测试连接即完成证书指纹记录。
