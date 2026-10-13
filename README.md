# OpsGuard

[English](README.en.md) | 中文

OpsGuard 是一个面向运维巡检和告警处置的本地部署平台。它不替代 Prometheus、Hadoop 等生产系统，而是统一接入已有服务，提供数据源管理、告警规则、通知中心和关键指标概览。

## 能力

- 数据源：Prometheus、MySQL、HiveServer2、SSH、SSH - Agent、Hadoop / YARN。
- 告警：Prometheus 规则同步，以及 MySQL 数据监测、端口检测、主机文件与脚本检测等自定义规则。
- 通知：活跃、告警和恢复事件分离展示；支持未读、全部已读、持续时间与隐秘告警。
- Hadoop：运行任务从 ResourceManager 获取；已完成的 MapReduce 任务可由 JobHistory 补充；支持后端筛选、分页、容器日志和任务详情跳转。
- 大屏：MySQL 与 SSH 已采集指标概览，MySQL 慢查询与高耗时 SQL 明细。
- 系统设置：全局刷新频率和密码修改。

## 架构

```text
Browser
  │
  ├─ React + TypeScript + Vite (frontend, :3000)
  │       └─ /api 代理
  │
  └─ Go HTTP API (backend, :8030)
          ├─ MySQL: 平台配置、通知、采样数据
          ├─ Prometheus: 指标、规则、告警
          ├─ Hadoop: ResourceManager / NodeManager / JobHistory
          ├─ SSH: 兼容已有节点指标与自定义检测脚本
          └─ SSH - Agent: 平台拉取主机指标，并下发文件与脚本检测请求
```

平台数据与被监控的数据源相互隔离。每个 Prometheus、Hadoop、SSH、MySQL 数据源都以其数据源 ID 作为查询边界。登录会话以 HttpOnly Cookie 传递，服务重启或多实例部署时会话保存在平台 MySQL 中；安全审计表记录登录成功、失败与限流事件。

## 前置条件

- Node.js 22+
- Go 1.22+
- MySQL 8+，用于保存 OpsGuard 自身数据

外部 Prometheus、Hadoop、SSH 和 MySQL 均为可选接入项。生产数据源凭据不会写入浏览器代码。

## Linux Agent

生产监控可使用 `SSH - Agent` 代替平台保存 SSH 登录密码。Agent 在被监控 Linux 主机监听指定端口（默认 `4001`）；平台使用地址、端口和令牌拉取指标，并在执行规则时发送文件路径、正则、脚本和工作目录。Agent 配置采用易读的 `.conf` 格式，不保存固定检测目录或脚本白名单。部署步骤见 [agent/README.md](agent/README.md)。

服务端以 root 启动时，平台会自动创建一个“服务端本机”SSH 数据源。该节点不发起 SSH 连接，也不需要 SSH 密钥、账号或密码；指标、文件检测和脚本检测直接在服务端主机执行。

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

# 传统 SSH 数据源使用账号密码连接，不需要额外的主机密钥文件
```

先用 MySQL 管理员执行 [create_opsguard_database.sql](backend/sql/create_opsguard_database.sql)，创建平台库和应用账号；随后启动后端，平台会自动初始化该库内所需表结构。已有 `admin` 账号不会被初始密码配置覆盖；旧版明文账号密码会在启动后迁移为 bcrypt 哈希。数据源凭据使用 `OPSGUARD_ENCRYPTION_KEY` 加密保存。`backend/config/opsguard.conf` 已被 Git 忽略，不应提交真实密码。容器或系统服务部署时，也可以通过 `OPSGUARD_CONFIG` 指向任意绝对配置文件路径；环境变量优先级高于配置文件。前后端跨域部署时，通过 `CORS_ALLOWED_ORIGINS` 配置允许的完整来源；同源部署无需配置。

设置 `TLS_ENABLED=true` 时，后端首次启动会自动生成并保存自签名证书及私钥，后续启动不会覆盖它们。`TLS_SERVER_NAMES` 必须包含浏览器访问平台使用的 IP 或域名，否则浏览器会报告名称不匹配。将生成的 `TLS_CERT_FILE` 导入运维终端的受信任证书库后，可消除自签名证书提示；私钥文件不得复制到客户端。

为避免数据源配置和自定义 HTTP 探测被用作内网扫描入口，生产环境应设置 `OUTBOUND_ALLOWED_HOSTS` 和 `OUTBOUND_ALLOWED_CIDRS`。配置后，目标域名与其解析 IP 都必须在白名单内；默认禁止链路本地、组播、未指定和回环地址。仅在本机开发时才将 `OUTBOUND_ALLOW_LOOPBACK=true`。

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

## 构建与验证

```powershell
cd backend
go test ./internal/service ./internal/router

cd ..\frontend
pnpm build
```

本地后端使用 `go run ./cmd/server` 启动；不使用 Windows exe 作为测试后端。Linux x86_64 和 ARM64 的已编译服务端与 Agent 发布包统一位于 [replease](replease)。

## Hadoop 接入说明

新增 Hadoop 数据源时填写 ResourceManager Web 地址，例如 `http://hadoop-master:8088`。NodeManager 和 JobHistory 地址为日志与历史任务的选填配置：

- NodeManager：读取仍在节点上的容器日志。
- JobHistory：读取聚合后的日志，并补充完成的 MapReduce 任务。

Hadoop 页面默认每页 20 条，筛选和分页在后端执行。每次远端读取最多 1000 个任务，本地快照保留最近 30 天且单数据源最多读取 5000 条，避免长期运行导致刷新退化。运行中任务由 ResourceManager 提供；JobHistory 不可用时，页面仍可展示运行中任务，但已完成历史可能不完整。

## 开发约定

- 浏览器只调用相对路径 `/api`，不暴露数据库地址或密码。
- 数据库、访问令牌和真实环境地址仅存放在本地环境配置中。
- 构建产物、日志、缓存和本机工具目录不提交至仓库。
