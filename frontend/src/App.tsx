import { useEffect, useRef, useState } from 'react'
import type { CSSProperties, FormEvent } from 'react'
import { BrowserRouter, NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import './App.css'

type Metric = { label: string; value: string; detail: string; change: string; tone: 'blue' | 'green' | 'amber' | 'violet' }
type Task = { id: string; title: string; owner: string; status: string; progress: number; updated: string }
type Source = { id: string; name: string; type: string; host: string; port: string; status: string; lastTest: string; username?: string; database?: string; remark?: string; options?: Record<string, string> }
type MySQLInstanceStatus = { sourceId: string; sourceName: string; host: string; port: string; status: string; version?: string; uptimeSeconds: number; threadsConnected: number; maxConnections: number; slowQueries: number; questions: number; databaseSizeBytes: number; replicaStatus?: string; lastError?: string; lastCollectedAt: string }
type MySQLMetricSnapshot = { id: number; sourceId: string; collectedAt: string; metrics: Record<string, string> }
type MySQLSlowQuerySample = { id: number; sourceId: string; schemaName?: string; digest?: string; queryText: string; count: number; totalLatencyMs: number; averageLatencyMs: number; maxLatencyMs: number; rowsExamined: number; rowsSent: number; firstSeen?: string; lastSeen?: string; collectedAt: string }
type MySQLDashboardData = { status: MySQLInstanceStatus; snapshot?: MySQLMetricSnapshot; slowQueries: MySQLSlowQuerySample[] }
type Rule = { id: string; name: string; source: string; database: string; table: string; field: string; condition: string; status: string }
type SourceType = 'MySQL' | 'Kafka' | 'Redis' | 'PostgreSQL' | 'Elasticsearch'

const api = '/api'
const importedDashboardKey = 'opsguard_imported_mysql_dashboards'
const sourceTypes: SourceType[] = ['MySQL', 'Kafka', 'Redis', 'PostgreSQL', 'Elasticsearch']
const defaultPorts: Record<SourceType, string> = { MySQL: '3306', Kafka: '9092', Redis: '6379', PostgreSQL: '5432', Elasticsearch: '9200' }
const fallbackMetrics: Metric[] = [
  { label: '全网请求数', value: '812.4K', detail: '次 / 分钟', change: '+12.8%', tone: 'blue' },
  { label: '应用可用率', value: '99.97%', detail: '近 30 天 SLA', change: '+0.03%', tone: 'green' },
  { label: '平均响应时间', value: '184ms', detail: 'P95 响应时间', change: '−9.4%', tone: 'violet' },
  { label: '待处理告警', value: '7', detail: '较昨日减少 2 条', change: '需关注', tone: 'amber' },
]
const fallbackTasks: Task[] = [
  { id: 'insp-101', title: '订单系统巡检', owner: '刘旭', status: '运行中', progress: 84, updated: '10 分钟前' },
  { id: 'insp-102', title: '支付链路巡检', owner: '周琳', status: '待执行', progress: 24, updated: '32 分钟前' },
  { id: 'insp-103', title: '日志采集健康检查', owner: '许凯', status: '已完成', progress: 100, updated: '2 小时前' },
]
const fallbackSources: Source[] = []
const fallbackRules: Rule[] = [
  { id: 'rule-001', name: '订单支付慢查询', source: 'MySQL', database: 'order_center', table: 'payment_orders', field: 'paid_at', condition: '今天有数据', status: '启用' },
  { id: 'rule-002', name: '库存预警值为 0', source: 'Redis', database: 'inventory', table: 'stock_info', field: 'available_qty', condition: '数值为 0', status: '启用' },
  { id: 'rule-003', name: '订单状态为空', source: 'MySQL', database: 'order_center', table: 'orders', field: 'status', condition: '为空', status: '待确认' },
]
const icons: Record<string, string> = { overview: '▦', inspection: '◌', data: '◫', alert: '◇', settings: '⚙', plus: '+', arrow: '→', bell: '●' }
function Icon({ name }: { name: string }) { return <span className={`icon icon-${name}`} aria-hidden="true">{icons[name]}</span> }

function App() {
  const [authed, setAuthed] = useState(() => localStorage.getItem('opsguard_token') === 'opsguard-admin')
  const logout = async () => {
    await fetch(`${api}/logout`, { method: 'POST' }).catch(() => {})
    localStorage.removeItem('opsguard_token')
    setAuthed(false)
  }
  if (!authed) return <Login onLogin={() => setAuthed(true)} />
  return <BrowserRouter><div className="app-shell"><Sidebar /><main className="workspace"><TopNav onLogout={logout} /><Routes><Route path="/" element={<Dashboard />} /><Route path="/inspection" element={<Inspection />} /><Route path="/alerts" element={<Alerts />} /><Route path="/datasources" element={<DataSources />} /><Route path="/config" element={<Settings />} /></Routes></main></div></BrowserRouter>
}
function Login({ onLogin }: { onLogin: () => void }) {
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setLoading(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      const response = await fetch(`${api}/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: form.get('username'), password: form.get('password') }) })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '登录失败')
      localStorage.setItem('opsguard_token', result.token)
      onLogin()
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setLoading(false)
    }
  }
  return <main className="login-page"><section className="login-panel surface"><div className="login-brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><span>巡检平台</span></div></div><h1>登录巡检平台</h1><p>使用管理员账号进入监控与巡检工作台。</p><form onSubmit={submit}><label>用户名 <span className="required-mark">*</span><input name="username" required autoComplete="username" /></label><label>密码 <span className="required-mark">*</span><input name="password" type="password" required autoComplete="current-password" /></label>{error && <span className="login-error">{error}</span>}<button className="button" type="submit" disabled={loading}>{loading ? '登录中...' : '登录'}</button></form></section></main>
}
function TopNav({ onLogout }: { onLogout: () => void | Promise<void> }) {
  const location = useLocation()
  const titles: Record<string, string> = { '/': '监控总览', '/inspection': '巡检任务', '/alerts': '平台告警', '/datasources': '数据节点', '/config': '系统配置' }
  const title = titles[location.pathname] || '监控总览'
  return <header className="page-nav"><div className="nav-path"><span>Ops</span><i>/</i><b>{title}</b></div><AccountMenu onLogout={onLogout} /></header>
}
function AccountMenu({ onLogout }: { onLogout: () => void | Promise<void> }) {
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement | null>(null)
  const navigate = useNavigate()
  useEffect(() => {
    if (!open) return
    const closeOnOutside = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', closeOnOutside)
    return () => document.removeEventListener('pointerdown', closeOnOutside)
  }, [open])
  return <div className="account-menu" ref={menuRef}><button className="user-avatar" type="button" aria-label="用户菜单" aria-expanded={open} onClick={() => setOpen(!open)}><span>管</span></button>{open && <section className="surface account-popover"><button type="button" onClick={() => { setOpen(false); navigate('/config') }}>修改密码</button><button className="danger" type="button" onClick={() => { setOpen(false); void onLogout() }}>注销登录</button></section>}</div>
}
function Sidebar() { const items = [['overview', '监控总览', '/'], ['inspection', '巡检任务', '/inspection'], ['alert', '平台告警', '/alerts'], ['data', '数据节点', '/datasources'], ['settings', '系统配置', '/config']]; return <aside className="sidebar"><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><small>巡检平台</small></div></div><nav>{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name={icon} /><span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><span className="online-dot" /><span>23 / 26 节点在线</span><small>采集服务运行正常</small></div></aside> }
function Dashboard() {
  const [metrics, setMetrics] = useState(fallbackMetrics)
  const [mysqlDashboards, setMysqlDashboards] = useState<MySQLDashboardData[]>([])
  const [loading, setLoading] = useState(false)
  const refresh = async () => {
    setLoading(true)
    try {
      const overviewResponse = await fetch(`${api}/overview`)
      const overviewData = await overviewResponse.json()
      if (Array.isArray(overviewData.metrics)) {
        setMetrics(overviewData.metrics.map((m: any, i: number) => ({ ...fallbackMetrics[i % 4], label: m.label ?? m.name ?? fallbackMetrics[i % 4].label, value: m.value ?? fallbackMetrics[i % 4].value })))
      }
      const importedIds = getImportedDashboardIds()
      if (importedIds.length === 0) {
        setMysqlDashboards([])
        return
      }
      const instanceResponse = await fetch(`${api}/mysql-monitor/instances`)
      const instanceData = await instanceResponse.json()
      const instances: MySQLInstanceStatus[] = Array.isArray(instanceData.instances) ? instanceData.instances : []
      const selected = instances.filter(item => importedIds.includes(item.sourceId))
      const dashboards = await Promise.all(selected.map(async status => {
        const [metricResponse, slowResponse] = await Promise.all([
          fetch(`${api}/mysql-monitor/instances/${status.sourceId}/metrics?limit=1`),
          fetch(`${api}/mysql-monitor/instances/${status.sourceId}/slow-queries?limit=8`),
        ])
        const metricData = await metricResponse.json()
        const slowData = await slowResponse.json()
        return {
          status,
          snapshot: Array.isArray(metricData.snapshots) ? metricData.snapshots[0] : undefined,
          slowQueries: Array.isArray(slowData.slowQueries) ? slowData.slowQueries : [],
        }
      }))
      setMysqlDashboards(dashboards)
    } catch {
      setMysqlDashboards([])
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 15000)
    return () => window.clearInterval(timer)
  }, [])
  return <div className="page dashboard"><section className="hero"><div><h2>平台运行平稳，<em>服务健康。</em></h2><p>{mysqlDashboards.length > 0 ? `已导入 ${mysqlDashboards.length} 个 MySQL 监控大屏，数据每 15 秒同步。` : '系统已连续稳定运行 32 天，关键业务链路处于预期区间。'}</p></div><button className="button secondary" onClick={refresh} disabled={loading}>{loading ? '同步中…' : '刷新数据'} <Icon name="arrow" /></button></section><section className="metric-grid">{metrics.map((m) => <article className={`metric-card ${m.tone}`} key={m.label}><div className="metric-top"><span>{m.label}</span><span className="metric-symbol">⌁</span></div><strong>{m.value}</strong><div className="metric-foot"><span>{m.detail}</span><b>{m.change}</b></div></article>)}</section>{mysqlDashboards.length === 0 ? <section className="surface dashboard-empty"><b>暂无已导入大屏</b><span>在数据节点中点击 MySQL 节点的导入大屏按钮后，这里会按固定模板展示该实例的全部采集数据。</span></section> : <section className="mysql-dashboard-stack">{mysqlDashboards.map(item => <MySQLDashboard key={item.status.sourceId} data={item} />)}</section>}</div>
}
function MySQLDashboard({ data }: { data: MySQLDashboardData }) {
  const status = data.status
  const metrics = data.snapshot?.metrics || {}
  const connectionPercent = percent(status.threadsConnected, status.maxConnections)
  const bufferTotal = metricNumber(metrics, 'Innodb_buffer_pool_pages_total')
  const bufferFree = metricNumber(metrics, 'Innodb_buffer_pool_pages_free')
  const bufferDirty = metricNumber(metrics, 'Innodb_buffer_pool_pages_dirty')
  const bufferUsedPercent = bufferTotal > 0 ? Math.round(((bufferTotal - bufferFree) / bufferTotal) * 100) : 0
  const allMetrics = Object.entries(metrics).sort(([a], [b]) => a.localeCompare(b))
  return <article className="mysql-template surface"><header className="mysql-template-head"><div><span className="template-kicker">MySQL 固定大屏模板</span><h2>{status.sourceName}</h2><p>{status.host}:{status.port} · MySQL {status.version || '-'}</p></div><div className="template-status"><span className={`tag ${status.status === '健康' ? 'success' : 'pending'}`}>{status.status}</span><small>最近采集：{formatCollectedAt(status.lastCollectedAt)}</small></div></header><section className="mysql-hero-grid"><div className="mysql-score"><div className="mysql-ring" style={{ '--ring': `${connectionPercent * 3.6}deg` } as CSSProperties & Record<string, string>}><span>{connectionPercent}%</span></div><b>连接使用率</b><small>{status.threadsConnected} / {status.maxConnections}</small></div><div className="mysql-kpi-grid"><DashboardKpi label="存活时间" value={formatDuration(status.uptimeSeconds)} detail="Uptime" /><DashboardKpi label="慢查询" value={String(status.slowQueries)} detail="Slow_queries" /><DashboardKpi label="库大小" value={formatBytes(status.databaseSizeBytes)} detail="information_schema" /><DashboardKpi label="复制状态" value={formatReplicaStatus(status.replicaStatus)} detail="Replica" /></div></section><section className="mysql-panels"><div className="surface mysql-panel"><SectionTitle title="连接与流量" /><MetricRows rows={[['累计连接', metrics.Connections], ['中止客户端', metrics.Aborted_clients], ['中止连接', metrics.Aborted_connects], ['接收流量', formatBytes(metricNumber(metrics, 'Bytes_received'))], ['发送流量', formatBytes(metricNumber(metrics, 'Bytes_sent'))], ['运行线程', metrics.Threads_running]]} /></div><div className="surface mysql-panel"><SectionTitle title="查询吞吐" /><MetricRows rows={[['Questions', metrics.Questions], ['Queries', metrics.Queries], ['SELECT', metrics.Com_select], ['INSERT', metrics.Com_insert], ['UPDATE', metrics.Com_update], ['DELETE', metrics.Com_delete]]} /></div><div className="surface mysql-panel"><SectionTitle title="InnoDB Buffer" /><div className="buffer-meter"><i style={{ width: `${bufferUsedPercent}%` }} /></div><MetricRows rows={[['使用率', `${bufferUsedPercent}%`], ['脏页', String(bufferDirty)], ['空闲页', String(bufferFree)], ['物理读', metrics.Innodb_buffer_pool_reads], ['逻辑读', metrics.Innodb_buffer_pool_read_requests], ['日志等待', metrics.Innodb_log_waits]]} /></div><div className="surface mysql-panel"><SectionTitle title="风险信号" /><MetricRows rows={[['全表扫描', metrics.Select_scan], ['无索引 Join', metrics.Select_full_join], ['磁盘临时表', metrics.Created_tmp_disk_tables], ['临时表', metrics.Created_tmp_tables], ['行锁等待', metrics.Innodb_row_lock_waits], ['表锁等待', metrics.Table_locks_waited]]} /></div></section><section className="surface slow-panel"><SectionTitle title="慢 SQL / 高耗时样本" action={`${data.slowQueries.length} 条`} />{data.slowQueries.length === 0 ? <div className="empty-state"><b>暂无慢 SQL 样本</b><span>当前实例 performance_schema 没有返回可展示的 digest 数据。</span></div> : <div className="slow-table">{data.slowQueries.map(item => <div className="slow-row" key={`${item.id}-${item.digest}`}><code>{item.queryText}</code><span>{item.schemaName || '-'}</span><b>{item.count} 次</b><b>{item.averageLatencyMs.toFixed(1)} ms</b><small>扫描 {item.rowsExamined} 行 · 返回 {item.rowsSent} 行</small></div>)}</div>}</section><section className="surface all-metrics"><SectionTitle title="全部采集指标" action={`${allMetrics.length} 项`} /><div>{allMetrics.map(([key, value]) => <span key={key}><b>{key}</b><em>{value}</em></span>)}</div></section></article>
}
function DashboardKpi({ label, value, detail }: { label: string; value: string; detail: string }) { return <div className="dashboard-kpi"><span>{label}</span><b>{value}</b><small>{detail}</small></div> }
function MetricRows({ rows }: { rows: Array<[string, string | undefined]> }) { return <div className="metric-rows">{rows.map(([label, value]) => <p key={label}><span>{label}</span><b>{value || '-'}</b></p>)}</div> }
function SectionTitle({ title, action }: { title: string; action?: string }) { return <div className="section-title"><div><h2>{title}</h2></div>{action && <button className="text-button">{action} <Icon name="arrow" /></button>}</div> }
function PageHead({ title, description, action, onAction }: { title: string; description: string; action?: string; onAction?: () => void }) { return <header className="page-head"><div><h2>{title}</h2><span>{description}</span></div>{action && <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header> }
function Inspection() { return <div className="page"><PageHead title="巡检任务" description="统一查看任务执行状态与最近的健康检查结果。" action="新建巡检" /><section className="surface table-card"><div className="table-toolbar"><b>全部任务 <small>{fallbackTasks.length}</small></b><div><button className="filter">状态：全部⌄</button><button className="filter">最近更新⌄</button></div></div><div className="task-table">{fallbackTasks.map(t => <div className="task-row" key={t.id}><div><b>{t.title}</b><span>{t.id} · 负责人：{t.owner}</span></div><span className={`tag ${t.status === '已完成' ? 'success' : t.status === '运行中' ? 'running' : 'pending'}`}>{t.status}</span><div className="progress"><i><b style={{ width: `${t.progress}%` }} /></i><span>{t.progress}%</span></div><time>{t.updated}</time><button className="more">•••</button></div>)}</div></section></div> }
function DataSources() {
  const navigate = useNavigate()
  const [sources, setSources] = useState<Source[]>(fallbackSources)
  const [mysqlStatuses, setMysqlStatuses] = useState<MySQLInstanceStatus[]>([])
  const [refreshing, setRefreshing] = useState(false)
  const [message, setMessage] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [editingSource, setEditingSource] = useState<Source | null>(null)
  const [sourceType, setSourceType] = useState<SourceType>('MySQL')
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [optionRows, setOptionRows] = useState([{ key: '', value: '' }, { key: '', value: '' }])

  const loadSources = async () => {
    setRefreshing(true)
    try {
      const [sourceResponse, monitorResponse] = await Promise.all([
        fetch(`${api}/data-sources`),
        fetch(`${api}/mysql-monitor/instances`),
      ])
      const sourceData = await sourceResponse.json()
      const monitorData = await monitorResponse.json()
      setSources(Array.isArray(sourceData.dataSources) ? sourceData.dataSources : [])
      setMysqlStatuses(Array.isArray(monitorData.instances) ? monitorData.instances : [])
    } catch {
      setSources([])
      setMysqlStatuses([])
    } finally {
      setRefreshing(false)
    }
  }
  useEffect(() => {
    void loadSources()
    const timer = window.setInterval(() => void loadSources(), 15000)
    return () => window.clearInterval(timer)
  }, [])

  const openCreateModal = () => {
    setEditingSource(null)
    setSourceType('MySQL')
    setOptionRows([{ key: '', value: '' }, { key: '', value: '' }])
    setModalOpen(true)
  }
  const openEditModal = (source: Source) => {
    setEditingSource(source)
    setSourceType(source.type as SourceType)
    const rows = Object.entries(source.options || {}).map(([key, value]) => ({ key, value }))
    setOptionRows(rows.length > 0 ? rows : [{ key: '', value: '' }, { key: '', value: '' }])
    setModalOpen(true)
  }
  const closeModal = () => {
    setModalOpen(false)
    setEditingSource(null)
    setTesting(false)
    setSaving(false)
  }
  const buildPayload = (form: HTMLFormElement) => {
    const formData = new FormData(form)
    const options = optionRows.reduce<Record<string, string>>((acc, row) => {
      const key = row.key.trim()
      if (key) acc[key] = row.value.trim()
      return acc
    }, {})
    return {
      name: String(formData.get('name') || `${sourceType} 数据源`),
      type: sourceType,
      host: String(formData.get('host') || ''),
      port: String(formData.get('port') || defaultPorts[sourceType]),
      database: String(formData.get('database') || formData.get('topic') || ''),
      username: String(formData.get('username') || ''),
      password: String(formData.get('password') || ''),
      remark: String(formData.get('remark') || ''),
      options,
    }
  }
  const testConnection = async (form: HTMLFormElement) => {
    setTesting(true)
    try {
      const response = await fetch(`${api}/data-sources/test`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(buildPayload(form)) })
      const result = await response.json()
      setMessage(result.message || (result.success ? '连接测试通过' : '连接测试失败'))
    } catch {
      setMessage('连接测试失败，请检查后端服务')
    } finally {
      setTesting(false)
    }
  }
  const saveSource = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    try {
      const payload = buildPayload(event.currentTarget)
      const url = editingSource ? `${api}/data-sources/${editingSource.id}` : `${api}/data-sources`
      const response = await fetch(url, { method: editingSource ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const added = await response.json()
      if (!response.ok) throw new Error(added.error || '保存失败')
      if (editingSource) {
        setSources(current => current.map(source => source.id === editingSource.id ? { ...source, ...added } : source))
      } else {
        setSources(current => [added, ...current])
      }
      setMessage(`${added.name} 已保存`)
      closeModal()
      void loadSources()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '保存失败')
      setSaving(false)
    }
  }
  const deleteSource = async (source: Source) => {
    if (!window.confirm(`确认删除 ${source.name}？`)) return
    try {
      const response = await fetch(`${api}/data-sources/${source.id}`, { method: 'DELETE' })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '删除失败')
      setSources(current => current.filter(item => item.id !== source.id))
      setMysqlStatuses(current => current.filter(item => item.sourceId !== source.id))
      setMessage(`${source.name} 已删除`)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '删除失败')
    }
  }
  const importDashboard = (source: Source) => {
    if (source.type !== 'MySQL') {
      setMessage('当前仅支持导入 MySQL 固定大屏')
      return
    }
    saveImportedDashboardId(source.id)
    setMessage(`${source.name} 已导入监控总览`)
    navigate('/')
  }

  const statusBySourceId = new Map(mysqlStatuses.map(item => [item.sourceId, item]))
  const healthyCount = sources.filter(source => {
    const live = statusBySourceId.get(source.id)
    return (live?.status || source.status) === '健康'
  }).length

  return <div className="page"><PageHead title="数据节点" description={`实时同步节点采集状态，当前 ${healthyCount} / ${sources.length} 个节点健康。`} action="添加数据节点" onAction={openCreateModal} /><section className="node-toolbar"><span>{refreshing ? '正在同步节点状态' : '每 15 秒自动刷新'}</span><button className="button secondary" type="button" onClick={() => void loadSources()} disabled={refreshing}>{refreshing ? '刷新中...' : '刷新状态'}</button></section>{sources.length === 0 ? <section className="surface empty-state"><b>暂无数据节点</b><span>点击右上角添加数据节点，完成连接测试后即可保存。</span></section> : <section className="source-list">{sources.map(s => { const live = statusBySourceId.get(s.id); const status = live?.status || (s.type === 'MySQL' ? '待采集' : s.status); const isHealthy = status === '健康'; return <article className={`surface source-row ${isHealthy ? 'healthy' : 'warning'}`} key={s.id}><div className="node-main"><span className="source-logo">{s.type.slice(0, 1)}</span><div><h3>{s.name}</h3><p>{s.type} · {s.host}:{s.port}{s.database ? ` · ${s.database}` : ''}</p>{s.remark && <small className="source-remark">{s.remark}</small>}</div></div><div className="node-status"><span className={`tag ${isHealthy ? 'success' : 'pending'}`}>{status}</span><small>{live?.lastError || (live ? '采集正常' : '等待采集数据')}</small></div><div className="node-metrics"><span><b>{formatDuration(live?.uptimeSeconds || 0)}</b><small>存活时间</small></span><span><b>{live ? `${live.threadsConnected}/${live.maxConnections}` : '-'}</b><small>连接数</small></span><span><b>{live?.slowQueries ?? '-'}</b><small>慢查询</small></span><span><b>{live ? formatBytes(live.databaseSizeBytes) : '-'}</b><small>库大小</small></span></div><div className="node-meta"><span>最近采集：{formatCollectedAt(live?.lastCollectedAt || s.lastTest)}</span><span>{live?.version ? `MySQL ${live.version}` : '监控数据待生成'}</span></div><div className="source-actions">{s.type === 'MySQL' && <button type="button" onClick={() => importDashboard(s)}>导入大屏</button>}<button type="button" onClick={() => openEditModal(s)}>编辑</button><button className="danger" type="button" onClick={() => void deleteSource(s)}>删除</button></div></article> })}</section>}{modalOpen && <div className="modal-backdrop" role="presentation" onMouseDown={closeModal}><section className="surface source-modal" role="dialog" aria-modal="true" aria-labelledby="source-modal-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="source-modal-title">{editingSource ? '编辑数据节点' : '添加数据节点'}</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={closeModal}>×</button></header><form key={`${editingSource?.id || 'new'}-${sourceType}`} onSubmit={saveSource}><div className="type-picker" role="group" aria-label="数据类型">{sourceTypes.map(type => <button key={type} type="button" className={sourceType === type ? 'active' : ''} onClick={() => { setSourceType(type); if (!editingSource || editingSource.type !== type) setOptionRows([{ key: '', value: '' }, { key: '', value: '' }]) }}>{type}</button>)}</div><div className="modal-form"><Field label="数据节点名称" name="name" value={editingSource?.name || `${sourceType} 生产节点`} required /><label>主机地址 <span className="required-mark">*</span><input name="host" defaultValue={editingSource?.host || ''} placeholder="例如 127.0.0.1 或 broker.internal" required /></label><label>端口 <span className="required-mark">*</span><input name="port" defaultValue={editingSource?.port || defaultPorts[sourceType]} required /></label>{sourceType === 'Kafka' ? <label>Topic / Consumer Group<input name="topic" defaultValue={editingSource?.database || ''} placeholder="例如 ops-events / ops-monitor" /></label> : <label>数据库 / 命名空间<input name="database" defaultValue={editingSource?.database || ''} placeholder={sourceType === 'Redis' ? '例如 0' : '例如 opsguard_lab'} /></label>}<label>用户名{sourceType === 'MySQL' && <span className="required-mark"> *</span>}<input name="username" defaultValue={editingSource?.username || ''} required={sourceType === 'MySQL'} placeholder={sourceType === 'Redis' ? '可选' : '请输入用户名'} /></label><label>密码{sourceType === 'MySQL' && !editingSource && <span className="required-mark"> *</span>}<input name="password" type="password" required={sourceType === 'MySQL' && !editingSource} placeholder={editingSource ? '留空则不修改密码' : '请输入密码'} /></label>{sourceType === 'Elasticsearch' && <label>索引前缀<input name="indexPrefix" placeholder="例如 logs-*" /></label>}<label className="wide">备注<textarea name="remark" defaultValue={editingSource?.remark || ''} placeholder="记录用途、负责人、环境或注意事项" /></label><div className="wide option-editor"><div><b>连接参数</b><span>示例：ssl true、timeout 10s、brokers host1:9092,host2:9092</span></div>{optionRows.map((row, index) => <div className="option-row" key={index}><input aria-label="参数名" placeholder="key" value={row.key} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, key: event.target.value } : item))} /><input aria-label="参数值" placeholder="value" value={row.value} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} /></div>)}<button className="text-button" type="button" onClick={() => setOptionRows(rows => [...rows, { key: '', value: '' }])}>添加参数 <Icon name="plus" /></button></div></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.form; if (form) void testConnection(form) }} disabled={testing}>{testing ? '测试中...' : '测试连接'}</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
}
function formatDuration(seconds: number) {
  if (!seconds) return '-'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}天 ${hours}小时`
  if (hours > 0) return `${hours}小时 ${minutes}分钟`
  return `${minutes}分钟`
}
function getImportedDashboardIds() {
  try {
    const parsed = JSON.parse(localStorage.getItem(importedDashboardKey) || '[]')
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : []
  } catch {
    return []
  }
}
function saveImportedDashboardId(sourceId: string) {
  const ids = getImportedDashboardIds()
  if (!ids.includes(sourceId)) {
    localStorage.setItem(importedDashboardKey, JSON.stringify([...ids, sourceId]))
  }
}
function metricNumber(metrics: Record<string, string>, key: string) {
  const value = Number(metrics[key])
  return Number.isFinite(value) ? value : 0
}
function percent(value: number, total: number) {
  if (!total) return 0
  return Math.max(0, Math.min(100, Math.round((value / total) * 100)))
}
function formatReplicaStatus(value?: string) {
  if (!value) return '-'
  if (value === 'running') return '运行中'
  if (value === 'not_replica_or_no_privilege') return '非从库/无权限'
  return value
}
function formatBytes(bytes: number) {
  if (!bytes) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value.toFixed(value >= 10 || index === 0 ? 0 : 1)} ${units[index]}`
}
function formatCollectedAt(value: string) {
  if (!value) return '待采集'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}
function Alerts() { return <div className="page"><PageHead title="告警规则" description="以业务优先级管理告警规则和处置状态。" action="新建规则" /><section className="surface rules">{fallbackRules.map(r => <div className="rule-row" key={r.id}><i className="rule-icon">⌁</i><div><b>{r.name}</b><span>{r.source} · {r.database}.{r.table}.{r.field} · {r.condition}</span></div><span className={`tag ${r.status === '启用' ? 'success' : 'pending'}`}>{r.status}</span><button className="text-button">编辑 <Icon name="arrow" /></button></div>)}</section></div> }
function Settings() {
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const changePassword = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setMessage('')
    const form = new FormData(event.currentTarget)
    const newPassword = String(form.get('newPassword') || '')
    if (newPassword !== String(form.get('confirmPassword') || '')) {
      setMessage('两次新密码不一致')
      return
    }
    setSaving(true)
    try {
      const response = await fetch(`${api}/change-password`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ oldPassword: form.get('oldPassword'), newPassword }) })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '修改失败')
      setMessage('密码已修改')
      event.currentTarget.reset()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '修改失败')
    } finally {
      setSaving(false)
    }
  }
  return <div className="page"><PageHead title="系统配置" description="维护账号安全与平台基础配置。" /><section className="surface settings"><div className="form-section"><h3>修改密码</h3><form className="settings-form" onSubmit={changePassword}><label>原密码 <span className="required-mark">*</span><input name="oldPassword" type="password" autoComplete="current-password" required /></label><label>新密码 <span className="required-mark">*</span><input name="newPassword" type="password" autoComplete="new-password" required /></label><label>确认新密码 <span className="required-mark">*</span><input name="confirmPassword" type="password" autoComplete="new-password" required /></label><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存密码'}</button>{message && <span>{message}</span>}</div></form></div></section></div>
}
function Field({ label, value, name, required }: { label: string; value: string; name?: string; required?: boolean }) { return <label>{label}{required && <span className="required-mark"> *</span>}<input name={name} defaultValue={value} required={required} /></label> }
export default App
