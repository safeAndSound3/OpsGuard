# OpsGuard

[English](README.en.md) | 中文

OpsGuard 是一个面向运维巡检和告警处置的本地部署平台。它不替代 Prometheus、Hadoop、Grafana 等生产系统，而是统一接入已有服务，提供数据源管理、告警规则、通知中心和关键指标概览。

## 能力

- 数据源：Prometheus、MySQL、SSH、Hadoop / YARN。
- 告警：Prometheus 规则同步，以及 MySQL 数据监测、端口检测、文件检测、SSH 脚本检测等自定义规则。
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
          └─ SSH: 节点指标与自定义检测脚本
```

平台数据与被监控的数据源相互隔离。每个 Prometheus、Hadoop、SSH、MySQL 数据源都以其数据源 ID 作为查询边界。登录会话以 HttpOnly Cookie 传递，服务重启或多实例部署时会话保存在平台 MySQL 中；安全审计表记录登录成功、失败与限流事件。

## 前置条件

- Node.js 22+
- Go 1.22+
- MySQL 8+，用于保存 OpsGuard 自身数据

外部 Prometheus、Hadoop、SSH 和 MySQL 均为可选接入项。生产数据源凭据不会写入浏览器代码。

## 配置

复制 `backend/config/opsguard.conf.example` 为 `backend/config/opsguard.conf`，填写平台 MySQL 连接：

```ini
HOST=127.0.0.1
PORT=8030
ENV=development

MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_DATABASE=opsguard
MYSQL_USER=opsguard_app
MYSQL_PASSWORD=change-me

# 仅首次创建 admin 账号时使用，至少 12 位；初始化后删除
OPSGUARD_ADMIN_PASSWORD=change-this-initial-password
# 通过 HTTPS 发布时设为 true
SESSION_COOKIE_SECURE=false
SESSION_TTL=12h

# 可选：限制数据源和 HTTP 探测可访问的目标。生产环境建议配置。
OUTBOUND_ALLOWED_HOSTS=prometheus.example.com,hadoop.example.com
OUTBOUND_ALLOWED_CIDRS=10.0.0.0/8,192.168.0.0/16
OUTBOUND_ALLOW_LOOPBACK=false

# 生产环境校验 SSH 主机密钥
SSH_HOST_KEY_POLICY=strict
SSH_KNOWN_HOSTS_FILE=C:/opsguard/ssh_known_hosts
```

启动时后端会自动初始化平台所需表结构。已有 `admin` 账号不会被初始密码配置覆盖；旧版明文账号密码会在启动后迁移为 bcrypt 哈希。数据源凭据使用 `OPSGUARD_ENCRYPTION_KEY` 加密保存。`backend/config/opsguard.conf` 已被 Git 忽略，不应提交真实密码。容器或系统服务部署时，也可以通过 `OPSGUARD_CONFIG` 指向任意绝对配置文件路径；环境变量优先级高于配置文件。前后端跨域部署时，通过 `CORS_ALLOWED_ORIGINS` 配置允许的完整来源；同源部署无需配置。

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

访问 [http://127.0.0.1:3000](http://127.0.0.1:3000)。Vite 会将 `/api` 和 `/health` 转发至 `http://127.0.0.1:8030`。

## 构建与验证

```powershell
cd backend
go test ./internal/service ./internal/router
go build -o .\bin\opsguard.exe .\cmd\server

cd ..\frontend
pnpm build
```

## Hadoop 接入说明

新增 Hadoop 数据源时填写 ResourceManager Web 地址，例如 `http://hadoop-master:8088`。NodeManager 和 JobHistory 地址为日志与历史任务的选填配置：

- NodeManager：读取仍在节点上的容器日志。
- JobHistory：读取聚合后的日志，并补充完成的 MapReduce 任务。

Hadoop 页面默认每页 20 条，筛选和分页在后端执行。每次远端读取最多 1000 个任务，本地快照保留最近 30 天且单数据源最多读取 5000 条，避免长期运行导致刷新退化。运行中任务由 ResourceManager 提供；JobHistory 不可用时，页面仍可展示运行中任务，但已完成历史可能不完整。容器日志按 ApplicationAttempt 分组，显示 AM 与工作容器、节点和状态；单次读取上限为 1 MB，页面会明确提示截断，批量下载最多三路并发读取。

## 开发约定

- 浏览器只调用相对路径 `/api`，不暴露数据库地址或密码。
- 数据库、访问令牌和真实环境地址仅存放在本地环境配置中。
- 构建产物、日志、缓存和本机工具目录不提交至仓库。
