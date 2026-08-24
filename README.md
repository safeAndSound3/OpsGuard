# 监控与采集管理平台

这是一个使用 React + Vite + TypeScript 前端和 Go 后端的监控平台演示项目，结构分层清晰，适合后续继续扩展并推送到 GitHub。

## 目录结构

- `frontend/`: React 前端应用
- `backend/`: Go API 与 mock 数据服务

## 功能概览

- 一级菜单：监控大屏、巡检、数据源、系统配置
- 监控大屏：模拟数据、指标卡片、趋势面板、告警中心
- 数据源：MySQL / Kafka / Redis / 中间件结构地址配置
- 系统配置：必填项、选填项、配置保存和测试告警通道
- 自定义采集：支持选择数据源 -> 数据库 -> 表 -> 字段，并配置条件（今天有数据 / 数值为 0 / 为空等）
- Go 后端：提供 /api/overview、/api/data-sources、/api/system-config、/api/collection-rules 等接口

## 启动方式

### 1. 启动前端

```bash
export NVM_DIR="$HOME/.nvm"
. "$NVM_DIR/nvm.sh"
nvm use 22
cd frontend
npm install
npm run dev -- --host 0.0.0.0 --port 5173
```

访问：http://localhost:5173

### 2. 启动后端

```bash
cd backend
go run ./cmd/server
```

访问：http://localhost:8080/health

## 生产构建

```bash
cd frontend
npm run build
```

```bash
cd backend
go build ./...
```
