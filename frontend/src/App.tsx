import { useEffect, useRef, useState } from 'react'
import type { CSSProperties, FormEvent } from 'react'
import { BrowserRouter, NavLink, Route, Routes, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import './App.css'

type Task = { id: string; title: string; owner: string; status: string; progress: number; updated: string }
type Source = { id: string; name: string; type: string; host: string; port: string; status: string; lastTest: string; username?: string; database?: string; remark?: string; options?: Record<string, string> }
type MySQLInstanceStatus = { sourceId: string; sourceName: string; host: string; port: string; status: string; version?: string; uptimeSeconds: number; threadsConnected: number; maxConnections: number; slowQueries: number; questions: number; databaseSizeBytes: number; replicaStatus?: string; lastError?: string; lastCollectedAt: string }
type MySQLMetricSnapshot = { id: number; sourceId: string; collectedAt: string; metrics: Record<string, string> }
type MySQLSlowQuerySample = { id: number; sourceId: string; schemaName?: string; digest?: string; queryText: string; count: number; totalLatencyMs: number; averageLatencyMs: number; maxLatencyMs: number; rowsExamined: number; rowsSent: number; firstSeen?: string; lastSeen?: string; collectedAt: string }
type MySQLDashboardData = { status: MySQLInstanceStatus; displayName: string; snapshot?: MySQLMetricSnapshot; slowQueries: MySQLSlowQuerySample[] }
type ImportedDashboard = { sourceId: string; name: string }
type MetricRow = [string, string | undefined, string?]
type Rule = { id: string; name: string; source: string; database: string; table: string; field: string; condition: string; threshold?: string; timeWindow: string; lastRun: string; status: string }
type SourceType = 'MySQL' | 'Kafka' | 'Redis' | 'PostgreSQL' | 'Elasticsearch'

const api = '/api'
const importedDashboardKey = 'opsguard_imported_mysql_dashboards'
const hiddenDetailMetricKeys = new Set(['Threads_connected', 'max_connections', 'Uptime', 'Slow_queries', 'database_size_bytes', 'replica_status'])
const sourceTypes: SourceType[] = ['MySQL', 'Kafka', 'Redis', 'PostgreSQL', 'Elasticsearch']
const defaultPorts: Record<SourceType, string> = { MySQL: '3306', Kafka: '9092', Redis: '6379', PostgreSQL: '5432', Elasticsearch: '9200' }
const mysqlMetricInfo: Record<string, string> = {
  Aborted_clients: '客户端异常断开数量，偏高通常说明应用连接释放或网络不稳定',
  Aborted_connects: '失败连接数量，偏高通常说明账号、密码、连接数或网络存在问题',
  Bytes_received: 'MySQL 接收的网络流量',
  Bytes_sent: 'MySQL 发送的网络流量',
  Com_delete: 'DELETE 语句累计执行次数',
  Com_insert: 'INSERT 语句累计执行次数',
  Com_select: 'SELECT 语句累计执行次数',
  Com_update: 'UPDATE 语句累计执行次数',
  Connections: '累计连接尝试次数',
  Created_tmp_disk_tables: '磁盘临时表数量，偏高通常代表排序、分组或大结果集压力',
  Created_tmp_tables: '内存临时表数量，用于判断查询中间结果压力',
  Handler_read_rnd_next: '顺序扫描读取次数，偏高通常说明全表扫描较多',
  Innodb_buffer_pool_pages_dirty: 'Buffer Pool 脏页数量，反映待刷盘压力',
  Innodb_buffer_pool_pages_free: 'Buffer Pool 空闲页数量',
  Innodb_buffer_pool_pages_total: 'Buffer Pool 总页数',
  Innodb_buffer_pool_read_requests: 'Buffer Pool 逻辑读次数',
  Innodb_buffer_pool_reads: 'Buffer Pool 物理读次数，偏高说明内存命中不足',
  Innodb_log_waits: 'Redo Log 等待次数，偏高说明日志写入成为瓶颈',
  Innodb_row_lock_current_waits: '当前正在等待的行锁数量',
  Innodb_row_lock_time: '行锁等待累计耗时',
  Innodb_row_lock_waits: '行锁等待累计次数',
  Max_used_connections: '历史最大同时连接数',
  Questions: '客户端发起的语句数量，可用于观察业务请求压力',
  Queries: '服务端执行的语句数量，包含存储过程内部语句',
  Select_full_join: '未使用索引的 Join 次数，偏高需要检查索引',
  Select_scan: '全表扫描次数，偏高需要检查索引和 SQL 写法',
  Slow_queries: '慢查询累计数量',
  Table_locks_waited: '表锁等待次数，偏高会影响并发写入',
  Threads_connected: '当前已打开连接数',
  Threads_running: '当前正在运行的线程数',
  Uptime: '实例启动后的运行秒数',
  database_size_bytes: '当前实例所有库的数据和索引总大小',
  innodb_buffer_pool_size: 'InnoDB Buffer Pool 配置大小',
  max_connections: '允许的最大连接数',
  process_locked: '当前处于锁等待的会话数量',
  process_running: '当前处于 Query 状态的会话数量',
  replica_status: '复制状态，主库或无权限时会显示非从库/无权限',
}
const fallbackTasks: Task[] = [
  { id: 'insp-101', title: '订单系统巡检', owner: '刘旭', status: '运行中', progress: 84, updated: '10 分钟前' },
  { id: 'insp-102', title: '支付链路巡检', owner: '周琳', status: '待执行', progress: 24, updated: '32 分钟前' },
  { id: 'insp-103', title: '日志采集健康检查', owner: '许凯', status: '已完成', progress: 100, updated: '2 小时前' },
]
const fallbackSources: Source[] = []
const fallbackRules: Rule[] = [
  { id: 'rule-001', name: '订单支付慢查询', source: 'MySQL', database: 'order_center', table: 'payment_orders', field: 'paid_at', condition: '大于', threshold: '1000ms', timeWindow: '5分钟', lastRun: '待执行', status: '启用' },
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
function Sidebar() {
  const currentLocation = useLocation()
  const [dashboards, setDashboards] = useState<MySQLInstanceStatus[]>([])
  const [importedDashboards, setImportedDashboards] = useState<ImportedDashboard[]>([])
  useEffect(() => {
    const load = async () => {
      try {
        const response = await fetch(`${api}/mysql-monitor/instances`)
        const data = await response.json()
        setDashboards(Array.isArray(data.instances) ? data.instances : [])
        setImportedDashboards(getImportedDashboards())
      } catch {
        setDashboards([])
        setImportedDashboards(getImportedDashboards())
      }
    }
    void load()
    const timer = window.setInterval(() => void load(), 15000)
    window.addEventListener('opsguard-dashboards-change', load)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('opsguard-dashboards-change', load)
    }
  }, [])
  const items = [['inspection', '巡检任务', '/inspection'], ['alert', '平台告警', '/alerts'], ['data', '数据节点', '/datasources'], ['settings', '系统配置', '/config']]
  const importedItems = importedDashboards.map(config => ({ config, status: dashboards.find(item => item.sourceId === config.sourceId) }))
  return <aside className="sidebar"><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><small>巡检平台</small></div></div><nav><NavLink end to="/" className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name="overview" /><span>监控总览</span></NavLink>{importedItems.length > 0 && <div className="subnav">{importedItems.map(({ config, status }) => <NavLink key={config.sourceId} to={`/?dashboard=${config.sourceId}`} className={({ isActive }) => `subnav-link ${isActive && currentLocation.search.includes(config.sourceId) ? 'active' : ''}`}><span className={status?.status === '健康' ? 'mini-dot ok' : 'mini-dot warn'} /><em>{config.name}</em></NavLink>)}</div>}{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name={icon} /><span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><span className="online-dot" /><span>{dashboards.filter(item => item.status === '健康').length} / {dashboards.length} 节点在线</span><small>采集服务运行正常</small></div></aside>
}
function Dashboard() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const [mysqlInstances, setMysqlInstances] = useState<MySQLInstanceStatus[]>([])
  const [dataSources, setDataSources] = useState<Source[]>([])
  const [selectedDashboard, setSelectedDashboard] = useState<MySQLDashboardData | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<MySQLDashboardData | null>(null)
  const [historyStart, setHistoryStart] = useState('')
  const [historyEnd, setHistoryEnd] = useState('')
  const [loading, setLoading] = useState(false)
  const selectedDashboardId = searchParams.get('dashboard')
  const refresh = async () => {
    setLoading(true)
    try {
      const [instanceResponse, sourceResponse] = await Promise.all([
        fetch(`${api}/mysql-monitor/instances`),
        fetch(`${api}/data-sources`),
      ])
      const instanceData = await instanceResponse.json()
      const sourceData = await sourceResponse.json()
      const instances: MySQLInstanceStatus[] = Array.isArray(instanceData.instances) ? instanceData.instances : []
      setMysqlInstances(instances)
      setDataSources(Array.isArray(sourceData.dataSources) ? sourceData.dataSources : [])
      if (!selectedDashboardId) {
        setSelectedDashboard(null)
        return
      }
      const imported = getImportedDashboards().find(item => item.sourceId === selectedDashboardId)
      if (!imported) {
        setSelectedDashboard(null)
        return
      }
      const selected = instances.find(item => item.sourceId === selectedDashboardId)
      if (!selected) {
        setSelectedDashboard(null)
        return
      }
      const historyQuery = new URLSearchParams({ limit: historyStart || historyEnd ? '200' : '1' })
      const slowQuery = new URLSearchParams({ limit: '50' })
      if (historyStart) {
        historyQuery.set('start', historyStart)
        slowQuery.set('start', historyStart)
      }
      if (historyEnd) {
        historyQuery.set('end', historyEnd)
        slowQuery.set('end', historyEnd)
      }
      const [metricResponse, slowResponse] = await Promise.all([
        fetch(`${api}/mysql-monitor/instances/${selected.sourceId}/metrics?${historyQuery.toString()}`),
        fetch(`${api}/mysql-monitor/instances/${selected.sourceId}/slow-queries?${slowQuery.toString()}`),
        ])
        const metricData = await metricResponse.json()
        const slowData = await slowResponse.json()
      setSelectedDashboard({
        status: selected,
        displayName: imported.name,
          snapshot: Array.isArray(metricData.snapshots) ? metricData.snapshots[0] : undefined,
          slowQueries: Array.isArray(slowData.slowQueries) ? slowData.slowQueries : [],
      })
    } catch {
      setMysqlInstances([])
      setDataSources([])
      setSelectedDashboard(null)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 15000)
    return () => window.clearInterval(timer)
  }, [selectedDashboardId, historyStart, historyEnd])
  const deleteDashboard = (sourceId: string) => {
    deleteImportedDashboard(sourceId)
    setDeleteTarget(null)
    navigate('/')
  }
  return <div className="page dashboard"><section className="hero"><div><h2>{selectedDashboardId ? 'MySQL 监控大屏' : '监控总览'}</h2><p>{selectedDashboardId ? '当前展示单个 MySQL 实例大屏，可按时间段查询历史采集数据。' : '当前按数据源展示关键运行信息，详细大屏请从左侧二级菜单进入。'}</p></div><div className="hero-actions">{selectedDashboardId && <div className="history-filter"><label>开始<input type="datetime-local" value={historyStart} onChange={(event) => setHistoryStart(event.target.value)} /></label><label>结束<input type="datetime-local" value={historyEnd} onChange={(event) => setHistoryEnd(event.target.value)} /></label><button className="button secondary" type="button" onClick={() => { setHistoryStart(''); setHistoryEnd('') }}>清空</button></div>}<button className="button secondary" onClick={refresh} disabled={loading}>{loading ? '同步中...' : '刷新数据'} <Icon name="arrow" /></button></div></section>{selectedDashboardId ? (selectedDashboard ? <section className="mysql-dashboard-stack"><MySQLDashboard data={selectedDashboard} onDelete={() => setDeleteTarget(selectedDashboard)} /></section> : <section className="surface dashboard-empty"><b>未找到该大屏</b><span>该大屏可能尚未导入、对应节点尚未采集成功，或该时间段内没有采集数据。</span></section>) : <MonitorOverview sources={dataSources} instances={mysqlInstances} />}{deleteTarget && <div className="modal-backdrop" role="presentation" onMouseDown={() => setDeleteTarget(null)}><section className="surface confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-dashboard-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="delete-dashboard-title">确认删除大屏</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={() => setDeleteTarget(null)}>×</button></header><p>确认删除“{deleteTarget.displayName}”吗？删除后不会影响 MySQL 数据节点和采集数据。</p><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => setDeleteTarget(null)}>取消</button><button className="button danger-button" type="button" onClick={() => deleteDashboard(deleteTarget.status.sourceId)}>确认</button></footer></section></div>}</div>
}
function MonitorOverview({ sources, instances }: { sources: Source[]; instances: MySQLInstanceStatus[] }) {
  const statusBySourceId = new Map(instances.map(item => [item.sourceId, item]))
  return <section className="overview-stack">{sources.length === 0 ? <div className="surface dashboard-empty"><b>暂无数据源</b><span>添加数据源并等待采集后，这里会展示关键运行信息。</span></div> : <div className="overview-source-list">{sources.map(source => {
    const mysql = source.type === 'MySQL' ? statusBySourceId.get(source.id) : undefined
    const status = mysql?.status || (source.type === 'MySQL' ? '待采集' : source.status)
    const isHealthy = status === '健康'
    const keyMetrics = mysql
      ? [['慢查询', String(mysql.slowQueries)], ['负载', `${percent(mysql.threadsConnected, mysql.maxConnections)}%`], ['存活', formatDuration(mysql.uptimeSeconds)], ['最近采集', formatCollectedAt(mysql.lastCollectedAt)]]
      : [['状态', source.type === 'MySQL' ? '等待采集' : source.status], ['最近检测', formatCollectedAt(source.lastTest)], ['指标', source.type === 'MySQL' ? '采集中' : '待接入']]
    return <article className={`surface overview-source ${isHealthy ? 'healthy' : 'warning'}`} key={source.id}><div className="overview-source-head"><span className="source-logo">{source.type.slice(0, 1)}</span><div><b>{source.name}</b><small>{source.type}{source.database ? ` · ${source.database}` : ''}</small></div><span className={`tag ${isHealthy ? 'success' : 'pending'}`}>{status}</span></div><div className="overview-source-metrics">{keyMetrics.map(([label, value]) => <p key={label}><span>{label}</span><b>{value}</b></p>)}</div>{mysql?.lastError && <em>{mysql.lastError}</em>}</article>
  })}</div>}</section>
}
function MySQLDashboard({ data, onDelete }: { data: MySQLDashboardData; onDelete: () => void }) {
  const status = data.status
  const metrics = data.snapshot?.metrics || {}
  const connectionPercent = percent(status.threadsConnected, status.maxConnections)
  const bufferTotal = metricNumber(metrics, 'Innodb_buffer_pool_pages_total')
  const bufferFree = metricNumber(metrics, 'Innodb_buffer_pool_pages_free')
  const bufferDirty = metricNumber(metrics, 'Innodb_buffer_pool_pages_dirty')
  const bufferUsedPercent = bufferTotal > 0 ? Math.round(((bufferTotal - bufferFree) / bufferTotal) * 100) : 0
  const allMetrics = Object.entries(metrics).filter(([key]) => mysqlMetricInfo[key] && !hiddenDetailMetricKeys.has(key)).sort(([a], [b]) => a.localeCompare(b))
  const [slowExpanded, setSlowExpanded] = useState(false)
  const visibleSlowQueries = slowExpanded ? data.slowQueries : data.slowQueries.slice(0, 10)
  return <article className="mysql-template surface"><header className="mysql-template-head"><div><span className="template-kicker">MySQL 固定大屏模板</span><h2>{data.displayName}</h2><p>{status.sourceName} · {status.host}:{status.port} · MySQL {status.version || '-'}</p></div><div className="template-status"><div className="template-actions"><span className={`tag ${status.status === '健康' ? 'success' : 'pending'}`}>{status.status}</span><button className="delete-dashboard" type="button" onClick={onDelete}>删除</button></div><small>最近采集：{formatCollectedAt(status.lastCollectedAt)}</small></div></header><section className="mysql-hero-grid"><div className="mysql-score"><div className="mysql-ring" style={{ '--ring': `${connectionPercent * 3.6}deg` } as CSSProperties & Record<string, string>}><span>{connectionPercent}%</span></div><b>连接使用率</b><small>{status.threadsConnected} / {status.maxConnections}</small></div><div className="mysql-kpi-grid"><DashboardKpi label="存活时间" value={formatDuration(status.uptimeSeconds)} detail="MySQL 实例持续运行时间" /><DashboardKpi label="慢查询" value={String(status.slowQueries)} detail="累计慢 SQL 数" /><DashboardKpi label="库大小" value={formatBytes(status.databaseSizeBytes)} detail="数据和索引总量" /><DashboardKpi label="复制状态" value={formatReplicaStatus(status.replicaStatus)} detail="主从复制健康度" /></div></section><section className="mysql-panels"><div className="surface mysql-panel"><SectionTitle title="连接与流量" /><MetricRows rows={[['累计连接', metrics.Connections], ['中止客户端', metrics.Aborted_clients], ['中止连接', metrics.Aborted_connects], ['接收流量', formatBytes(metricNumber(metrics, 'Bytes_received'))], ['发送流量', formatBytes(metricNumber(metrics, 'Bytes_sent'))], ['运行线程', metrics.Threads_running]]} /></div><div className="surface mysql-panel"><SectionTitle title="查询吞吐" /><MetricRows rows={[['客户端语句', metrics.Questions, 'questions'], ['服务端语句', metrics.Queries, 'queries'], ['查询', metrics.Com_select, 'select'], ['新增', metrics.Com_insert, 'insert'], ['更新', metrics.Com_update, 'update'], ['删除', metrics.Com_delete, 'delete']]} /></div><div className="surface mysql-panel"><SectionTitle title="InnoDB Buffer" /><div className="buffer-meter"><i style={{ width: `${bufferUsedPercent}%` }} /></div><MetricRows rows={[['使用率', `${bufferUsedPercent}%`], ['脏页', String(bufferDirty)], ['空闲页', String(bufferFree)], ['物理读', metrics.Innodb_buffer_pool_reads], ['逻辑读', metrics.Innodb_buffer_pool_read_requests], ['日志等待', metrics.Innodb_log_waits]]} /></div><div className="surface mysql-panel"><SectionTitle title="风险信号" /><MetricRows rows={[['全表扫描', metrics.Select_scan], ['无索引 Join', metrics.Select_full_join], ['磁盘临时表', metrics.Created_tmp_disk_tables], ['临时表', metrics.Created_tmp_tables], ['行锁等待', metrics.Innodb_row_lock_waits], ['表锁等待', metrics.Table_locks_waited]]} /></div></section><section className="surface slow-panel"><div className="slow-panel-head"><SectionTitle title="慢 SQL / 高耗时样本" action={`${visibleSlowQueries.length} / ${data.slowQueries.length} 条`} />{data.slowQueries.length > 10 && <button className="slow-fold-button" type="button" onClick={() => setSlowExpanded(current => !current)}>{slowExpanded ? '折叠' : '展开全部'}</button>}</div>{data.slowQueries.length === 0 ? <div className="empty-state"><b>暂无慢 SQL 样本</b><span>当前实例 performance_schema 没有返回可展示的 digest 数据。</span></div> : <div className="slow-table"><div className="slow-row slow-head"><span>分类</span><span>SQL 语句</span><span>库名</span><span>次数</span><span>平均耗时</span><span>扫描 / 返回</span></div>{visibleSlowQueries.map(item => <div className="slow-row" key={`${item.id}-${item.digest}`}><span className="sql-kind">{sqlCategory(item.queryText)}</span><code className="sql-preview"><span className="sql-text">{item.queryText}</span><span className="sql-tooltip">{slowQueryDetail(item)}</span></code><span>{item.schemaName || '-'}</span><b>{item.count} 次</b><b>{item.averageLatencyMs.toFixed(1)} ms</b><small>扫描 {item.rowsExamined} 行 · 返回 {item.rowsSent} 行</small></div>)}</div>}</section><section className="surface all-metrics"><SectionTitle title="全部采集指标" action={`${allMetrics.length} 项`} /><div>{allMetrics.map(([key, value]) => <span key={key}><b>{key}</b><small>{mysqlMetricInfo[key]}</small><em>{value}</em></span>)}</div></section></article>
}
function DashboardKpi({ label, value, detail }: { label: string; value: string; detail: string }) { return <div className="dashboard-kpi"><span>{label}</span><b>{value}</b><small>{detail}</small></div> }
function MetricRows({ rows }: { rows: MetricRow[] }) { return <div className="metric-rows">{rows.map(([label, value, detail]) => <p key={label}><span>{label}{detail && <small>{detail}</small>}</span><b>{value || '-'}</b></p>)}</div> }
function SectionTitle({ title, action }: { title: string; action?: string }) { return <div className="section-title"><div><h2>{title}</h2></div>{action && <span className="section-badge">{action}</span>}</div> }
function PageHead({ title, description, action, onAction }: { title: string; description: string; action?: string; onAction?: () => void }) { return <header className="page-head"><div><h2>{title}</h2><span>{description}</span></div>{action && <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header> }
function Inspection() { return <div className="page"><PageHead title="巡检任务" description="统一查看任务执行状态与最近的健康检查结果。" action="新建巡检" /><section className="surface table-card"><div className="table-toolbar"><b>全部任务 <small>{fallbackTasks.length}</small></b><div><button className="filter">状态：全部⌄</button><button className="filter">最近更新⌄</button></div></div><div className="task-table">{fallbackTasks.map(t => <div className="task-row" key={t.id}><div><b>{t.title}</b><span>{t.id} · 负责人：{t.owner}</span></div><span className={`tag ${t.status === '已完成' ? 'success' : t.status === '运行中' ? 'running' : 'pending'}`}>{t.status}</span><div className="progress"><i><b style={{ width: `${t.progress}%` }} /></i><span>{t.progress}%</span></div><time>{t.updated}</time><button className="more">•••</button></div>)}</div></section></div> }
function slowQueryDetail(item: MySQLSlowQuerySample) {
  return `完整 SQL：${item.queryText}\n最大耗时：${item.maxLatencyMs.toFixed(1)} ms\n总耗时：${item.totalLatencyMs.toFixed(1)} ms\n首次出现：${formatCollectedAt(item.firstSeen || '')}\n最近出现：${formatCollectedAt(item.lastSeen || '')}`
}
function sqlCategory(sql: string) {
  const keyword = sql.trim().match(/^[a-zA-Z]+/)?.[0]?.toUpperCase()
  if (!keyword) return 'OTHER'
  if (['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'REPLACE'].includes(keyword)) return keyword
  if (['CREATE', 'ALTER', 'DROP', 'TRUNCATE'].includes(keyword)) return 'DDL'
  if (['COMMIT', 'ROLLBACK', 'BEGIN', 'START'].includes(keyword)) return 'TXN'
  return keyword
}
function DataSources() {
  const navigate = useNavigate()
  const [sources, setSources] = useState<Source[]>(fallbackSources)
  const [mysqlStatuses, setMysqlStatuses] = useState<MySQLInstanceStatus[]>([])
  const [refreshing, setRefreshing] = useState(false)
  const [message, setMessage] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [dashboardSource, setDashboardSource] = useState<Source | null>(null)
  const [dashboardName, setDashboardName] = useState('')
  const [deleteSourceTarget, setDeleteSourceTarget] = useState<Source | null>(null)
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
    try {
      const response = await fetch(`${api}/data-sources/${source.id}`, { method: 'DELETE' })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '删除失败')
      setSources(current => current.filter(item => item.id !== source.id))
      setMysqlStatuses(current => current.filter(item => item.sourceId !== source.id))
      deleteImportedDashboard(source.id)
      setDeleteSourceTarget(null)
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
    setDashboardSource(source)
    setDashboardName(getImportedDashboards().find(item => item.sourceId === source.id)?.name || `${source.name} 监控大屏`)
  }
  const confirmImportDashboard = () => {
    if (!dashboardSource) return
    const name = dashboardName.trim() || `${dashboardSource.name} 监控大屏`
    saveImportedDashboard(dashboardSource.id, name)
    setDashboardSource(null)
    setDashboardName('')
    setMessage(`${name} 已导入监控总览`)
    navigate(`/?dashboard=${dashboardSource.id}`)
  }

  const statusBySourceId = new Map(mysqlStatuses.map(item => [item.sourceId, item]))
  const healthyCount = sources.filter(source => {
    const live = statusBySourceId.get(source.id)
    return (live?.status || source.status) === '健康'
  }).length

  return <div className="page"><PageHead title="数据节点" description={`实时同步节点采集状态，当前 ${healthyCount} / ${sources.length} 个节点健康。`} action="添加数据节点" onAction={openCreateModal} /><section className="node-toolbar"><span>{refreshing ? '正在同步节点状态' : '每 15 秒自动刷新'}</span><button className="button secondary" type="button" onClick={() => void loadSources()} disabled={refreshing}>{refreshing ? '刷新中...' : '刷新状态'}</button></section>{sources.length === 0 ? <section className="surface empty-state"><b>暂无数据节点</b><span>点击右上角添加数据节点，完成连接测试后即可保存。</span></section> : <section className="source-list">{sources.map(s => { const live = statusBySourceId.get(s.id); const status = live?.status || (s.type === 'MySQL' ? '待采集' : s.status); const isHealthy = status === '健康'; return <article className={`surface source-row ${isHealthy ? 'healthy' : 'warning'}`} key={s.id}><div className="node-main"><span className="source-logo">{s.type.slice(0, 1)}</span><div><h3>{s.name}</h3><p>{s.type} · {s.host}:{s.port}{s.database ? ` · ${s.database}` : ''}</p>{s.remark && <small className="source-remark">{s.remark}</small>}</div></div><div className="node-status"><span className={`tag ${isHealthy ? 'success' : 'pending'}`}>{status}</span><small>{live?.lastError || (live ? '采集正常' : '等待采集数据')}</small></div><div className="node-meta"><span>最近采集：{formatCollectedAt(live?.lastCollectedAt || s.lastTest)}</span><span>{live?.version ? `MySQL ${live.version}` : '监控数据待生成'}</span></div><div className="source-actions">{s.type === 'MySQL' && <button type="button" onClick={() => importDashboard(s)}>导入大屏</button>}<button type="button" onClick={() => openEditModal(s)}>编辑</button><button className="danger" type="button" onClick={() => setDeleteSourceTarget(s)}>删除</button></div></article> })}</section>}{deleteSourceTarget && <div className="modal-backdrop" role="presentation" onMouseDown={() => setDeleteSourceTarget(null)}><section className="surface confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-source-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="delete-source-title">确认删除数据源</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={() => setDeleteSourceTarget(null)}>×</button></header><p>确认删除“{deleteSourceTarget.name}”吗？删除后会同步移除该数据源已导入的大屏入口，历史采集数据不在此处展示。</p><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => setDeleteSourceTarget(null)}>取消</button><button className="button danger-button" type="button" onClick={() => void deleteSource(deleteSourceTarget)}>确认</button></footer></section></div>}{dashboardSource && <div className="modal-backdrop" role="presentation" onMouseDown={() => setDashboardSource(null)}><section className="surface dashboard-import-modal" role="dialog" aria-modal="true" aria-labelledby="dashboard-import-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="dashboard-import-title">导入监控大屏</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={() => setDashboardSource(null)}>×</button></header><label>大屏名称 <span className="required-mark">*</span><input value={dashboardName} onChange={(event) => setDashboardName(event.target.value)} autoFocus /></label><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => setDashboardSource(null)}>取消</button><button className="button" type="button" onClick={confirmImportDashboard}>导入</button></footer></section></div>}{modalOpen && <div className="modal-backdrop" role="presentation" onMouseDown={closeModal}><section className="surface source-modal" role="dialog" aria-modal="true" aria-labelledby="source-modal-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="source-modal-title">{editingSource ? '编辑数据节点' : '添加数据节点'}</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={closeModal}>×</button></header><form key={`${editingSource?.id || 'new'}-${sourceType}`} onSubmit={saveSource}><div className="type-picker" role="group" aria-label="数据类型">{sourceTypes.map(type => <button key={type} type="button" className={sourceType === type ? 'active' : ''} onClick={() => { setSourceType(type); if (!editingSource || editingSource.type !== type) setOptionRows([{ key: '', value: '' }, { key: '', value: '' }]) }}>{type}</button>)}</div><div className="modal-form"><Field label="数据节点名称" name="name" value={editingSource?.name || `${sourceType} 生产节点`} required /><label>主机地址 <span className="required-mark">*</span><input name="host" defaultValue={editingSource?.host || ''} placeholder="例如 127.0.0.1 或 broker.internal" required /></label><label>端口 <span className="required-mark">*</span><input name="port" defaultValue={editingSource?.port || defaultPorts[sourceType]} required /></label>{sourceType === 'Kafka' ? <label>Topic / Consumer Group<input name="topic" defaultValue={editingSource?.database || ''} placeholder="例如 ops-events / ops-monitor" /></label> : <label>数据库 / 命名空间<input name="database" defaultValue={editingSource?.database || ''} placeholder={sourceType === 'Redis' ? '例如 0' : '例如 opsguard_lab'} /></label>}<label>用户名{sourceType === 'MySQL' && <span className="required-mark"> *</span>}<input name="username" defaultValue={editingSource?.username || ''} required={sourceType === 'MySQL'} placeholder={sourceType === 'Redis' ? '可选' : '请输入用户名'} /></label><label>密码{sourceType === 'MySQL' && !editingSource && <span className="required-mark"> *</span>}<input name="password" type="password" required={sourceType === 'MySQL' && !editingSource} placeholder={editingSource ? '留空则不修改密码' : '请输入密码'} /></label>{sourceType === 'Elasticsearch' && <label>索引前缀<input name="indexPrefix" placeholder="例如 logs-*" /></label>}<label className="wide">备注<textarea name="remark" defaultValue={editingSource?.remark || ''} placeholder="记录用途、负责人、环境或注意事项" /></label><div className="wide option-editor"><div><b>连接参数</b><span>示例：ssl true、timeout 10s、brokers host1:9092,host2:9092</span></div>{optionRows.map((row, index) => <div className="option-row" key={index}><input aria-label="参数名" placeholder="key" value={row.key} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, key: event.target.value } : item))} /><input aria-label="参数值" placeholder="value" value={row.value} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} /></div>)}<button className="text-button" type="button" onClick={() => setOptionRows(rows => [...rows, { key: '', value: '' }])}>添加参数 <Icon name="plus" /></button></div></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.form; if (form) void testConnection(form) }} disabled={testing}>{testing ? '测试中...' : '测试连接'}</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
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
function getImportedDashboards(): ImportedDashboard[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(importedDashboardKey) || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed.flatMap((item): ImportedDashboard[] => {
      if (typeof item === 'string') return [{ sourceId: item, name: 'MySQL 监控大屏' }]
      if (item && typeof item.sourceId === 'string') return [{ sourceId: item.sourceId, name: typeof item.name === 'string' && item.name.trim() ? item.name : 'MySQL 监控大屏' }]
      return []
    })
  } catch {
    return []
  }
}
function saveImportedDashboard(sourceId: string, name: string) {
  const dashboards = getImportedDashboards()
  const next = dashboards.some(item => item.sourceId === sourceId)
    ? dashboards.map(item => item.sourceId === sourceId ? { sourceId, name } : item)
    : [...dashboards, { sourceId, name }]
  localStorage.setItem(importedDashboardKey, JSON.stringify(next))
  window.dispatchEvent(new Event('opsguard-dashboards-change'))
}
function deleteImportedDashboard(sourceId: string) {
  localStorage.setItem(importedDashboardKey, JSON.stringify(getImportedDashboards().filter(item => item.sourceId !== sourceId)))
  window.dispatchEvent(new Event('opsguard-dashboards-change'))
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
function Alerts() {
  const [rules, setRules] = useState<Rule[]>([])
  const [editingRule, setEditingRule] = useState<Rule | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const loadRules = async () => {
    try {
      const response = await fetch(`${api}/collection-rules`)
      const data = await response.json()
      setRules(Array.isArray(data.rules) ? data.rules : [])
    } catch {
      setRules(fallbackRules)
    }
  }
  useEffect(() => { void loadRules() }, [])
  const openRuleModal = (rule?: Rule) => {
    setEditingRule(rule || null)
    setModalOpen(true)
  }
  const closeRuleModal = () => {
    setModalOpen(false)
    setEditingRule(null)
    setSaving(false)
  }
  const saveRule = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    const form = new FormData(event.currentTarget)
    const payload: Rule = {
      id: editingRule?.id || '',
      name: String(form.get('name') || ''),
      source: String(form.get('source') || 'MySQL'),
      database: String(form.get('database') || ''),
      table: String(form.get('table') || ''),
      field: String(form.get('field') || ''),
      condition: String(form.get('condition') || '大于'),
      threshold: String(form.get('threshold') || ''),
      timeWindow: String(form.get('timeWindow') || '5分钟'),
      lastRun: editingRule?.lastRun || '待执行',
      status: String(form.get('status') || '启用'),
    }
    try {
      const response = await fetch(editingRule ? `${api}/collection-rules/${editingRule.id}` : `${api}/collection-rules`, { method: editingRule ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const saved = await response.json()
      if (!response.ok) throw new Error(saved.error || '保存失败')
      setMessage(`${saved.name} 已保存`)
      closeRuleModal()
      void loadRules()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '保存失败')
      setSaving(false)
    }
  }
  const deleteRule = async (rule: Rule) => {
    if (!window.confirm(`确认删除 ${rule.name}？`)) return
    try {
      const response = await fetch(`${api}/collection-rules/${rule.id}`, { method: 'DELETE' })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '删除失败')
      setRules(current => current.filter(item => item.id !== rule.id))
      setMessage(`${rule.name} 已删除`)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '删除失败')
    }
  }
  return <div className="page"><PageHead title="告警规则" description="配置数据源指标阈值、时间窗口和启停状态。" action="新建规则" onAction={() => openRuleModal()} /><section className="surface rules">{rules.length === 0 ? <div className="empty-state"><b>暂无告警规则</b><span>点击右上角新建规则，后续采集任务会按规则进行告警判断。</span></div> : rules.map(r => <div className="rule-row alert-rule-row" key={r.id}><i className="rule-icon">⌁</i><div><b>{r.name}</b><span>{r.source} · {r.database || '-'}.{r.table || '-'}.{r.field || '-'} · {r.condition}{r.threshold ? ` ${r.threshold}` : ''} · {r.timeWindow}</span></div><span className={`tag ${r.status === '启用' ? 'success' : 'pending'}`}>{r.status}</span><div className="rule-actions"><button className="text-button" type="button" onClick={() => openRuleModal(r)}>编辑 <Icon name="arrow" /></button><button className="text-button danger" type="button" onClick={() => void deleteRule(r)}>删除</button></div></div>)}</section>{modalOpen && <div className="modal-backdrop" role="presentation" onMouseDown={closeRuleModal}><section className="surface source-modal alert-rule-modal" role="dialog" aria-modal="true" aria-labelledby="rule-modal-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="rule-modal-title">{editingRule ? '编辑告警规则' : '新建告警规则'}</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={closeRuleModal}>×</button></header><form onSubmit={saveRule}><div className="modal-form"><label>规则名称 <span className="required-mark">*</span><input name="name" defaultValue={editingRule?.name || ''} required /></label><label>数据源类型<select name="source" defaultValue={editingRule?.source || 'MySQL'}>{sourceTypes.map(type => <option key={type} value={type}>{type}</option>)}</select></label><label>数据库<input name="database" defaultValue={editingRule?.database || ''} placeholder="例如 opsguard_lab" /></label><label>表名<input name="table" defaultValue={editingRule?.table || ''} placeholder="例如 mysql_metric_snapshots" /></label><label>字段 / 指标<input name="field" defaultValue={editingRule?.field || ''} placeholder="例如 Slow_queries" /></label><label>条件<select name="condition" defaultValue={editingRule?.condition || '大于'}><option>大于</option><option>大于等于</option><option>等于</option><option>小于</option><option>包含</option><option>不为空</option></select></label><label>阈值<input name="threshold" defaultValue={editingRule?.threshold || ''} placeholder="例如 10 或 80%" /></label><label>时间窗口<input name="timeWindow" defaultValue={editingRule?.timeWindow || '5分钟'} /></label><label>状态<select name="status" defaultValue={editingRule?.status || '启用'}><option>启用</option><option>停用</option></select></label></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={closeRuleModal}>取消</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
}
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
