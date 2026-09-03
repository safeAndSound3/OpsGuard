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

平台数据与被监控的数据源相互隔离。每个 Prometheus、Hadoop、SSH、MySQL 数据源都以其数据源 ID 作为查询边界。

## 前置条件

- Node.js 22+
- Go 1.22+
- MySQL 8+，用于保存 OpsGuard 自身数据

外部 Prometheus、Hadoop、SSH 和 MySQL 均为可选接入项。生产数据源凭据不会写入浏览器代码。

## 配置

复制 `backend/.env.example` 为 `backend/.env`，填写平台 MySQL 连接：

```ini
HOST=127.0.0.1
PORT=8030
ENV=development

MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_DATABASE=opsguard
MYSQL_USER=opsguard_app
MYSQL_PASSWORD=change-me
```

启动时后端会自动初始化平台所需表结构。`backend/.env` 已被 Git 忽略，不应提交真实密码。

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

Hadoop 页面默认每页 20 条，筛选和分页在后端执行。运行中任务由 ResourceManager 提供；JobHistory 不可用时，页面仍可展示运行中任务，但已完成历史可能不完整。

## 开发约定

- 浏览器只调用相对路径 `/api`，不暴露数据库地址或密码。
- 数据库、访问令牌和真实环境地址仅存放在本地环境配置中。
- 构建产物、日志、缓存和本机工具目录不提交至仓库。
