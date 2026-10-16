# OpsGuard

[English](README.en.md) | 中文

OpsGuard 是一个面向运维巡检和告警处置的本地部署平台。它不替代 Prometheus、Hadoop 等生产系统，而是统一接入已有服务，提供数据源管理、多引擎数据工作台、定时报表任务、告警规则、通知中心和关键指标概览。

## 能力

- **数据源管理**：Prometheus、MySQL、HiveServer2、PostgreSQL、SSH、SSH - Agent、Hadoop / YARN。
- **数据工作台**：MySQL、Hive、PostgreSQL 三个引擎统一风格的库表结构树，支持筛选搜索、拖拽调宽、折叠面板、分页浏览数据、只读 SQL 查询、结果导出。
- **报表任务**：按 Cron 定时或手动执行只读 SQL，支持统计时间变量（`${start}` / `${end}`），记录执行历史，结果可在线查看与导出。
- **告警**：Prometheus 规则同步，以及 MySQL 数据监测、端口检测、主机文件与脚本检测等自定义规则。
- **通知中心**：活跃、告警和恢复事件分离展示；支持未读、全部已读、持续时间与静默告警。
- **Hadoop**：运行任务从 ResourceManager 获取；已完成的 MapReduce 任务可由 JobHistory 补充；支持后端筛选、分页、容器日志和任务详情跳转。
- **指标查询**：按指标名前缀分类浏览 Prometheus 指标，支持分页与标签筛选。
- **监控大屏**：MySQL 与 SSH 已采集指标概览，MySQL 慢查询与高耗时 SQL 明细。
- **常用平台**：顶部快捷入口，一键跳转到内部常用系统。
- **系统设置**：全局刷新频率、密码修改、常用平台配置。

## 架构

```text
Browser
  │
  ├─ React + TypeScript + Vite (frontend, :3000)
  │       └─ /api 代理
  │
  └─ Go HTTP API (backend, :8030)
          ├─ MySQL: 平台配置、通知、采样数据、报表任务
          ├─ Prometheus: 指标、规则、告警
          ├─ HiveServer2 / PostgreSQL: 数据工作台查询
          ├─ Hadoop: ResourceManager / NodeManager / JobHistory
          ├─ SSH: 兼容已有节点指标与自定义检测脚本
          └─ SSH - Agent: 平台拉取主机指标，并下发文件与脚本检测请求
```

平台数据与被监控的数据源相互隔离。每个数据源都以其数据源 ID 作为查询边界。登录会话以 HttpOnly Cookie 传递，服务重启或多实例部署时会话保存在平台 MySQL 中；安全审计表记录登录成功、失败与限流事件。前端构建产物内嵌在 Go 二进制中（`internal/webassets`），生产部署只需替换单个可执行文件。

## 前置条件

- Node.js 22+（仅开发前端时需要）
- Go 1.22+
- MySQL 8+，用于保存 OpsGuard 自身数据

外部 Prometheus、Hadoop、Hive、PostgreSQL、SSH 和 MySQL 均为可选接入项。生产数据源凭据不会写入浏览器代码。

## Linux Agent

生产监控可使用 `SSH - Agent` 代替平台保存 SSH 登录密码。Agent 在被监控 Linux 主机监听指定端口（默认 `4001`）；平台使用地址、端口和令牌拉取指标，并在执行规则时发送文件路径、正则、脚本和工作目录。Agent 配置采用易读的 `.conf` 格式，不保存固定检测目录或脚本白名单。部署步骤见 [agent/README.md](agent/README.md)。

### Agent 传输安全

- **传输强制 HTTPS**：平台与 Agent 之间的通信一律使用 TLS，Bearer 令牌不再明文出现在网络上。Agent 配置 `TLS_ENABLED=true`（推荐默认）后，证书文件缺失时会自动生成自签证书，无需手工签发。
- **证书指纹固定（TOFU）**：平台首次连接 Agent 时自动记录其证书 SHA-256 指纹，此后证书变化将拒绝连接并提示“可能遭受中间人攻击”。确认真实更换证书后，可在数据源列表执行「重置证书指纹」重新信任。
- **查看指纹**：在 Agent 主机执行 `opsguard-agent --print-fingerprint`（无需启动服务，会自动生成证书）。
- **令牌轮换**：Agent 的 `TOKEN` 支持逗号分隔多个令牌。平台数据源列表「轮换令牌」会生成新令牌（仅返回一次），将其追加到 Agent 配置并重启后，即可移除旧令牌，全程不中断监控。

### SSH 主机密钥校验

传统 SSH 数据源默认以 **TOFU** 模式校验主机密钥：首次连接自动记录指纹，之后指纹变化拒绝连接（防止中间人攻击）。可通过 `SSH_HOST_KEY_MODE=strict` 切换为 known_hosts 严格校验（需配置 `SSH_KNOWN_HOSTS`），或 `off` 关闭（仅兼容遗留部署）。主机密钥确实更换时，在数据源列表执行「重置主机密钥」重新信任。

服务端以 root 启动时，平台会自动创建一个"服务端本机"SSH 数据源。该节点不发起 SSH 连接，也不需要 SSH 密钥、账号或密码；指标、文件检测和脚本检测直接在服务端主机执行。

## 配置

复制 `backend/config/opsguard.conf.example` 为 `backend/config/opsguard.conf`，填写平台 MySQL 连接：

```ini
HOST=127.0.0.1
PORT=8030
ENV=development

# 启用后端自动生成的自签名 HTTPS 证书。TLS_SERVER_NAMES 填写实际访问平台的 IP 或域名。
TLS_ENABLED=false
TLS_CERT_FILE=certs/opsguard.crt
TLS_KEY_FILE=certs/opsguard.key
TLS_SERVER_NAMES=127.0.0.1,localhost

MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_DATABASE=opsguard
MYSQL_USER=opsguard_app
MYSQL_PASSWORD=change-me

# 仅首次创建 admin 账号时使用，至少 8 位；初始化后删除
OPSGUARD_ADMIN_PASSWORD=example8
SESSION_TTL=12h

# 可选：限制数据源和 HTTP 探测可访问的目标。生产环境建议配置。
OUTBOUND_ALLOWED_HOSTS=prometheus.example.com,hadoop.example.com
OUTBOUND_ALLOWED_CIDRS=10.0.0.0/8,192.168.0.0/16
OUTBOUND_ALLOW_LOOPBACK=false
```

先用 MySQL 管理员执行 [create_opsguard_database.sql](backend/sql/create_opsguard_database.sql)，创建平台库和应用账号；随后启动后端，平台会自动初始化该库内所需表结构。已有 `admin` 账号不会被初始密码配置覆盖；旧版明文账号密码会在启动后迁移为 bcrypt 哈希。数据源凭据使用 `OPSGUARD_ENCRYPTION_KEY` 加密保存。`backend/config/opsguard.conf` 已被 Git 忽略，不应提交真实密码。容器或系统服务部署时，也可以通过 `OPSGUARD_CONFIG` 指向任意绝对配置文件路径；环境变量优先级高于配置文件。前后端跨域部署时，通过 `CORS_ALLOWED_ORIGINS` 配置允许的完整来源；同源部署无需配置。

设置 `TLS_ENABLED=true` 时，后端首次启动会自动生成并保存自签名证书及私钥，后续启动不会覆盖它们。`TLS_SERVER_NAMES` 必须包含浏览器访问平台使用的 IP 或域名，否则浏览器会报告名称不匹配。

为避免数据源配置和自定义 HTTP 探测被用作内网扫描入口，生产环境应设置 `OUTBOUND_ALLOWED_HOSTS` 和 `OUTBOUND_ALLOWED_CIDRS`。

## 本地运行

启动 API：

```powershell
cd backend
go run ./cmd/server
```

启动前端：

```powershell
cd frontend
pnpm install
pnpm dev
```

访问 [http://127.0.0.1:3000](http://127.0.0.1:3000)。Vite 会将 `/api` 和 `/health` 转发至 `https://127.0.0.1:8030`。

## 构建与发布

```powershell
# 前端构建并同步到后端内嵌目录
cd frontend
pnpm build
# 然后将 dist/assets 拷到 backend/internal/webassets/assets，并更新 index.html 的 hash

# 后端测试
cd ..\backend
go test ./internal/service ./internal/router

# 交叉编译 Linux x86_64（前端已内嵌，单文件部署）
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go build -o opsguard-linux-amd64 ./cmd/server
```

Linux x86_64 二进制直接替换生产旧版即可，平台会自动迁移新增表结构。

## 数据工作台说明

- 三个工作台（MySQL / Hive / PostgreSQL）共享统一的库表树与数据表格组件。
- 库表树支持筛选搜索（无需展开库即可搜到表）、拖拽右缘调宽、折叠为窄列。
- 数据区分页浏览（50/100/200/500 条每页），支持 CSV 导出。
- 「执行 SQL」标签页允许只读查询（SELECT / SHOW / DESCRIBE / EXPLAIN / WITH），不支持写入。
- 未选择表时显示提示：建议大数据量通过 SQL 查询方式查看，以免影响服务。

## 报表任务说明

- 三步走配置：基本信息（任务名 + 数据源）→ 执行方式（手动 / Cron 定时）→ 只读 SQL。
- 第一步任务名和数据源必填，否则无法进入下一步。
- 执行历史记录每次运行的状态、耗时和错误信息，结果可在线查看或导出。
- 支持 `${start}` / `${end}` 时间变量，按"昨天 / 今天 / 本周 / 本月"等统计时间自动替换。

## Hadoop 接入说明

新增 Hadoop 数据源时填写 ResourceManager Web 地址，例如 `http://hadoop-master:8088`。NodeManager 和 JobHistory 地址为日志与历史任务的选填配置：

- NodeManager：读取仍在节点上的容器日志。
- JobHistory：读取聚合后的日志，并补充完成的 MapReduce 任务。

## 开发约定

- 浏览器只调用相对路径 `/api`，不暴露数据库地址或密码。
- 数据库、访问令牌和真实环境地址仅存放在本地环境配置中。
- 构建产物、日志、缓存和本机工具目录不提交至仓库。
