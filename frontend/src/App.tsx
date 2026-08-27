import { useEffect, useRef, useState } from 'react'
import type { CSSProperties, FormEvent } from 'react'
import { BrowserRouter, NavLink, Route, Routes, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import './App.css'

type Source = { id: string; name: string; type: string; host: string; port: string; enabled: boolean; status: string; lastTest: string; username?: string; database?: string; remark?: string; options?: Record<string, string> }
type SourceDraft = Partial<Source> & { password?: string }
type MySQLInstanceStatus = { sourceId: string; sourceName: string; host: string; port: string; status: string; version?: string; uptimeSeconds: number; threadsConnected: number; maxConnections: number; slowQueries: number; questions: number; databaseSizeBytes: number; replicaStatus?: string; lastError?: string; lastCollectedAt: string }
type MySQLMetricSnapshot = { id: number; sourceId: string; collectedAt: string; metrics: Record<string, string> }
type MySQLSlowQuerySample = { id: number; sourceId: string; schemaName?: string; digest?: string; queryText: string; count: number; totalLatencyMs: number; averageLatencyMs: number; maxLatencyMs: number; rowsExamined: number; rowsSent: number; firstSeen?: string; lastSeen?: string; collectedAt: string }
type MySQLDashboardData = { status: MySQLInstanceStatus; displayName: string; snapshot?: MySQLMetricSnapshot; snapshots?: MySQLMetricSnapshot[]; slowQueries: MySQLSlowQuerySample[] }
type RedisInstanceStatus = { sourceId: string; sourceName: string; host: string; port: string; status: string; version?: string; uptimeSeconds: number; connectedClients: number; blockedClients: number; usedMemory: number; maxMemory: number; memoryFragmentation: number; opsPerSecond: number; totalCommands: number; hitRate: number; evictedKeys: number; expiredKeys: number; rejectedConnections: number; slowlogLength: number; keyCount: number; role?: string; lastError?: string; lastCollectedAt: string }
type RedisMetricSnapshot = { id: number; sourceId: string; collectedAt: string; metrics: Record<string, string> }
type RedisDashboardData = { status: RedisInstanceStatus; displayName: string; snapshot?: RedisMetricSnapshot; snapshots?: RedisMetricSnapshot[] }
type SSHInstanceStatus = { sourceId: string; sourceName: string; host: string; port: string; status: string; hostname?: string; kernel?: string; uptimeSeconds: number; cpuUsagePercent: number; load1: number; load5: number; load15: number; memoryUsed: number; memoryTotal: number; memoryPercent: number; diskUsed: number; diskTotal: number; diskPercent: number; processCount: number; tcpConnections: number; lastError?: string; lastCollectedAt: string }
type SSHMetricSnapshot = { id: number; sourceId: string; collectedAt: string; metrics: Record<string, string> }
type SSHDashboardData = { status: SSHInstanceStatus; displayName: string; snapshot?: SSHMetricSnapshot; snapshots?: SSHMetricSnapshot[] }
type DashboardData = (MySQLDashboardData & { kind: 'MySQL' }) | (RedisDashboardData & { kind: 'Redis' }) | (SSHDashboardData & { kind: 'SSH' })
type ImportedDashboard = { sourceId: string; name: string }
type MetricRow = [string, string | undefined, string?]
type Rule = { id: string; name: string; source: string; database: string; table: string; field: string; condition: string; threshold?: string; timeWindow: string; lastRun: string; status: string }
type NotificationItem = { id: string; ruleId: string; ruleName: string; source: string; database: string; table: string; field: string; severity: string; status: string; message: string; unread: boolean; firstSeenAt: string; lastSeenAt: string; resolvedAt?: string }
type ExternalMonitorConfig = { prometheusUrl?: string; prometheusConfigured: boolean; prometheusTokenConfigured?: boolean; grafanaUrl?: string; grafanaConfigured: boolean; grafanaTokenConfigured?: boolean }
type PrometheusAlert = { name: string; state: string; severity?: string; summary?: string; description?: string; activeAt?: string; value?: string; labels?: Record<string, string>; annotations?: Record<string, string> }
type PrometheusMetric = { name: string }
type GrafanaDashboardItem = { uid: string; title: string; uri?: string; url?: string; folderTitle?: string; tags?: string[] }
type SourceSchema = Record<string, Record<string, string[]>>
type SourceType = 'MySQL' | 'Kafka' | 'Redis' | 'SSH' | 'PostgreSQL' | 'Elasticsearch'

const api = '/api'
const importedDashboardKey = 'opsguard_imported_mysql_dashboards'
const hiddenDetailMetricKeys = new Set(['Threads_connected', 'max_connections', 'Uptime', 'Slow_queries', 'database_size_bytes', 'replica_status'])
const hiddenRedisDetailMetricKeys = new Set(['redis_version', 'uptime_in_seconds', 'connected_clients', 'used_memory', 'instantaneous_ops_per_sec', 'hit_rate', 'slowlog_len', 'key_count', 'role'])
const hiddenSSHDetailMetricKeys = new Set(['hostname', 'kernel', 'uptime_seconds', 'cpu_usage_percent', 'memory_percent', 'disk_percent'])
const sourceTypes: SourceType[] = ['MySQL', 'Kafka', 'Redis', 'SSH', 'PostgreSQL', 'Elasticsearch']
const defaultPorts: Record<SourceType, string> = { MySQL: '3306', Kafka: '9092', Redis: '6379', SSH: '22', PostgreSQL: '5432', Elasticsearch: '9200' }
function splitDatabaseList(value = '') {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}
function parseDatabaseRemarks(options?: Record<string, string>) {
  try {
    const parsed = JSON.parse(options?.monitor_database_remarks || '{}')
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, string> : {}
  } catch {
    return {}
  }
}
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
const redisMetricInfo: Record<string, string> = {
  blocked_clients: '正在等待阻塞命令的客户端数量，偏高会影响请求响应',
  connected_clients: '当前客户端连接数，是连接压力的直接信号',
  connected_slaves: '连接到当前实例的从节点数量',
  evicted_keys: '因内存达到上限被淘汰的 key 数量，增加说明容量有压力',
  expired_keys: '因 TTL 过期删除的 key 数量，用于观察缓存生命周期',
  hit_rate: '缓存命中率，越低代表穿透到后端存储的比例越高',
  instantaneous_ops_per_sec: '当前每秒处理命令数，表示 Redis 实时吞吐',
  key_count: '当前选中库或实例中的 key 数量',
  keyspace_hits: '缓存命中累计次数',
  keyspace_misses: '缓存未命中累计次数',
  latest_fork_usec: '最近一次 fork 耗时，偏高可能影响持久化或复制',
  maxmemory: 'Redis 配置的最大内存，0 表示未限制',
  mem_fragmentation_ratio: '内存碎片率，过高说明内存分配效率变差',
  rejected_connections: '被拒绝的连接数，增加通常说明连接数达到上限',
  role: '实例角色，master 或 slave',
  slowlog_len: '慢日志队列长度，说明存在高耗时命令样本',
  total_commands_processed: '实例启动后累计处理命令数',
  uptime_in_seconds: 'Redis 实例启动后的运行秒数',
  used_cpu_sys: '系统态 CPU 累计消耗',
  used_cpu_user: '用户态 CPU 累计消耗',
  used_memory: '当前使用内存，是容量压力核心指标',
  used_memory_peak: '历史内存峰值',
}
const sshMetricInfo: Record<string, string> = {
  cpu_usage_percent: 'CPU 使用率，持续偏高代表计算资源压力',
  disk_percent: '根分区磁盘使用率，接近上限会影响写入和系统稳定性',
  disk_total: '根分区总容量',
  disk_used: '根分区已用容量',
  hostname: '主机名',
  kernel: '内核版本',
  load1: '1 分钟系统负载，适合观察当前压力',
  load5: '5 分钟系统负载，适合观察短周期压力',
  load15: '15 分钟系统负载，适合观察持续压力',
  memory_percent: '内存使用率，持续偏高可能导致换页或进程异常',
  memory_total: '系统总内存',
  memory_used: '系统已用内存',
  process_count: '当前进程数，异常增长可能代表任务堆积或泄漏',
  tcp_connections: '当前 ESTABLISHED TCP 连接数，反映网络连接压力',
  uptime_seconds: '系统启动后的运行秒数',
}
const fallbackSources: Source[] = []
const fallbackRules: Rule[] = [
  { id: 'rule-001', name: '订单支付慢查询', source: 'MySQL', database: 'order_center', table: 'payment_orders', field: 'paid_at', condition: '大于', threshold: '1000ms', timeWindow: '5分钟', lastRun: '待执行', status: '启用' },
]
const icons: Record<string, string> = { overview: '▦', inspection: '◌', external: '◎', data: '◫', alert: '◇', notify: '◉', settings: '⚙', plus: '+', arrow: '→', bell: '●' }
function Icon({ name }: { name: string }) { return <span className={`icon icon-${name}`} aria-hidden="true">{icons[name]}</span> }

function SelectField({
  label,
  required,
  value,
  onChange,
  disabled,
  options,
  placeholder,
}: {
  label: string
  required?: boolean
  value: string
  onChange: (value: string) => void
  disabled?: boolean
  options: { value: string; label: string }[]
  placeholder: string
}) {
  const [open, setOpen] = useState(false)
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({})
  const selectRef = useRef<HTMLDivElement>(null)
  const selected = options.find(option => option.value === value)
  const updateMenuPosition = () => {
    const trigger = selectRef.current?.querySelector('.custom-select > button')
    if (!(trigger instanceof HTMLElement)) return
    const rect = trigger.getBoundingClientRect()
    const viewportPadding = 16
    const gap = 7
    const availableBelow = window.innerHeight - rect.bottom - viewportPadding
    const availableAbove = rect.top - viewportPadding
    const openAbove = availableBelow < 220 && availableAbove > availableBelow
    const maxHeight = Math.max(140, Math.min(360, openAbove ? availableAbove - gap : availableBelow - gap))
    setMenuStyle({
      left: rect.left,
      top: openAbove ? rect.top - gap : rect.bottom + gap,
      width: rect.width,
      maxHeight,
      transform: openAbove ? 'translateY(-100%)' : undefined,
    })
  }
  useEffect(() => {
    if (!open) return
    updateMenuPosition()
    const close = (event: PointerEvent) => {
      if (!selectRef.current?.contains(event.target as Node)) setOpen(false)
    }
    const reposition = () => updateMenuPosition()
    window.addEventListener('pointerdown', close)
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)
    return () => {
      window.removeEventListener('pointerdown', close)
      window.removeEventListener('resize', reposition)
      window.removeEventListener('scroll', reposition, true)
    }
  }, [open])
  return <div className="field-block custom-select-field" ref={selectRef}><span className="field-label">{label}{required && <span className="required-mark"> *</span>}</span><span className={`custom-select ${open ? 'open' : ''} ${disabled ? 'disabled' : ''}`}><button type="button" className={!selected ? 'placeholder' : ''} disabled={disabled} onClick={() => setOpen(current => !current)}>{selected?.label || placeholder}<i aria-hidden="true">⌄</i></button>{open && <span className="custom-select-menu" style={menuStyle}>{options.length === 0 ? <span className="custom-select-empty">暂无可选项</span> : options.map(option => <button type="button" key={option.value} className={option.value === value ? 'active' : ''} onClick={() => { onChange(option.value); setOpen(false) }}>{option.label}</button>)}</span>}</span></div>
}

function StatusSwitch({ checked, onChange, disabled }: { checked: boolean; onChange: (checked: boolean) => void; disabled?: boolean }) {
  return <button type="button" className={`status-switch ${checked ? 'checked' : ''}`} aria-pressed={checked} disabled={disabled} onClick={() => onChange(!checked)}><span className="status-switch-track"><span className="status-switch-thumb" /></span><span>{checked ? '启用' : '停用'}</span></button>
}

function App() {
  const [authed, setAuthed] = useState(() => localStorage.getItem('opsguard_token') === 'opsguard-admin')
  const logout = async () => {
    await fetch(`${api}/logout`, { method: 'POST' }).catch(() => {})
    localStorage.removeItem('opsguard_token')
    setAuthed(false)
  }
  if (!authed) return <Login onLogin={() => setAuthed(true)} />
  return <BrowserRouter><div className="app-shell"><Sidebar /><main className="workspace"><TopNav onLogout={logout} /><Routes><Route path="/" element={<Dashboard />} /><Route path="/inspection" element={<Inspection />} /><Route path="/external-monitor" element={<ExternalMonitor />} /><Route path="/alerts" element={<Alerts />} /><Route path="/notifications" element={<Notifications />} /><Route path="/datasources" element={<DataSources />} /><Route path="/config" element={<Settings />} /></Routes></main></div></BrowserRouter>
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
  const navigate = useNavigate()
  const [unread, setUnread] = useState(0)
  const titles: Record<string, string> = { '/': '监控总览', '/inspection': '巡检任务', '/external-monitor': '外部监控', '/alerts': '告警规则', '/notifications': '通知中心', '/datasources': '数据节点', '/config': '系统配置' }
  const title = titles[location.pathname] || '监控总览'
  const loadUnread = async () => {
    try {
      const response = await fetch(`${api}/notifications?unread=1&limit=1`)
      const data = await response.json()
      setUnread(Number(data.unread) || 0)
    } catch {
      setUnread(0)
    }
  }
  useEffect(() => {
    void loadUnread()
    const timer = window.setInterval(() => void loadUnread(), 15000)
    window.addEventListener('opsguard-notifications-change', loadUnread)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('opsguard-notifications-change', loadUnread)
    }
  }, [])
  return <header className="page-nav"><div className="nav-path"><span>Ops</span><i>/</i><b>{title}</b></div><div className="nav-tools"><button className="bell-button" type="button" aria-label="通知中心" onClick={() => navigate('/notifications')}><Icon name="bell" />{unread > 0 && <i>{unread > 99 ? '99+' : unread}</i>}</button><AccountMenu onLogout={onLogout} /></div></header>
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
  const [mysqlDashboards, setMysqlDashboards] = useState<MySQLInstanceStatus[]>([])
  const [redisDashboards, setRedisDashboards] = useState<RedisInstanceStatus[]>([])
  const [sshDashboards, setSshDashboards] = useState<SSHInstanceStatus[]>([])
  const [importedDashboards, setImportedDashboards] = useState<ImportedDashboard[]>([])
  const [sources, setSources] = useState<Source[]>([])
  useEffect(() => {
    const load = async () => {
      try {
        const [mysqlResponse, redisResponse, sshResponse, sourceResponse] = await Promise.all([
          fetch(`${api}/mysql-monitor/instances`),
          fetch(`${api}/redis-monitor/instances`),
          fetch(`${api}/ssh-monitor/instances`),
          fetch(`${api}/data-sources`),
        ])
        const mysqlData = await mysqlResponse.json()
        const redisData = await redisResponse.json()
        const sshData = await sshResponse.json()
        const sourceData = await sourceResponse.json()
        setMysqlDashboards(Array.isArray(mysqlData.instances) ? mysqlData.instances : [])
        setRedisDashboards(Array.isArray(redisData.instances) ? redisData.instances : [])
        setSshDashboards(Array.isArray(sshData.instances) ? sshData.instances : [])
        setSources(Array.isArray(sourceData.dataSources) ? sourceData.dataSources : [])
        setImportedDashboards(getImportedDashboards())
      } catch {
        setMysqlDashboards([])
        setRedisDashboards([])
        setSshDashboards([])
        setSources([])
        setImportedDashboards(getImportedDashboards())
      }
    }
    void load()
    const timer = window.setInterval(() => void load(), 15000)
    window.addEventListener('opsguard-dashboards-change', load)
    window.addEventListener('opsguard-data-sources-change', load)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('opsguard-dashboards-change', load)
      window.removeEventListener('opsguard-data-sources-change', load)
    }
  }, [])
  const items = [['inspection', '巡检任务', '/inspection'], ['external', '外部监控', '/external-monitor'], ['alert', '告警规则', '/alerts'], ['notify', '通知中心', '/notifications'], ['data', '数据节点', '/datasources'], ['settings', '系统配置', '/config']]
  const sourceById = new Map(sources.map(source => [source.id, source]))
  const mysqlStatusBySourceId = new Map(mysqlDashboards.map(item => [item.sourceId, item.status]))
  const redisStatusBySourceId = new Map(redisDashboards.map(item => [item.sourceId, item.status]))
  const sshStatusBySourceId = new Map(sshDashboards.map(item => [item.sourceId, item.status]))
  const importedItems = importedDashboards.flatMap(config => {
    const source = sourceById.get(config.sourceId)
    if (source && !source.enabled) return []
    const status = source?.type === 'Redis'
      ? redisDashboards.find(item => item.sourceId === config.sourceId)
      : source?.type === 'SSH'
        ? sshDashboards.find(item => item.sourceId === config.sourceId)
      : mysqlDashboards.find(item => item.sourceId === config.sourceId)
    return [{ config, status }]
  })
  const onlineSourceCount = sources.filter(source => source.enabled && (source.type === 'MySQL' ? mysqlStatusBySourceId.get(source.id) === '健康' : source.type === 'Redis' ? redisStatusBySourceId.get(source.id) === '健康' : source.type === 'SSH' ? sshStatusBySourceId.get(source.id) === '健康' : source.status === '健康')).length
  return <aside className="sidebar"><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><small>巡检平台</small></div></div><nav><NavLink end to="/" className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name="overview" /><span>监控总览</span></NavLink>{importedItems.length > 0 && <div className="subnav">{importedItems.map(({ config, status }) => <NavLink key={config.sourceId} to={`/?dashboard=${config.sourceId}`} className={({ isActive }) => `subnav-link ${isActive && currentLocation.search.includes(config.sourceId) ? 'active' : ''}`}><span className={status?.status === '健康' ? 'mini-dot ok' : 'mini-dot warn'} /><em>{config.name}</em></NavLink>)}</div>}{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name={icon} /><span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><span className="online-dot" /><span>{onlineSourceCount} / {sources.length} 节点在线</span><small>采集服务运行正常</small></div></aside>
}
function Dashboard() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const [mysqlInstances, setMysqlInstances] = useState<MySQLInstanceStatus[]>([])
  const [redisInstances, setRedisInstances] = useState<RedisInstanceStatus[]>([])
  const [sshInstances, setSshInstances] = useState<SSHInstanceStatus[]>([])
  const [dataSources, setDataSources] = useState<Source[]>([])
  const [selectedDashboard, setSelectedDashboard] = useState<DashboardData | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<DashboardData | null>(null)
  const [historyStart, setHistoryStart] = useState('')
  const [historyEnd, setHistoryEnd] = useState('')
  const [loading, setLoading] = useState(false)
  const selectedDashboardId = searchParams.get('dashboard')
  const refresh = async () => {
    setLoading(true)
    try {
      const [instanceResponse, redisInstanceResponse, sshInstanceResponse, sourceResponse] = await Promise.all([
        fetch(`${api}/mysql-monitor/instances`),
        fetch(`${api}/redis-monitor/instances`),
        fetch(`${api}/ssh-monitor/instances`),
        fetch(`${api}/data-sources`),
      ])
      const instanceData = await instanceResponse.json()
      const redisInstanceData = await redisInstanceResponse.json()
      const sshInstanceData = await sshInstanceResponse.json()
      const sourceData = await sourceResponse.json()
      const instances: MySQLInstanceStatus[] = Array.isArray(instanceData.instances) ? instanceData.instances : []
      const redisInstances: RedisInstanceStatus[] = Array.isArray(redisInstanceData.instances) ? redisInstanceData.instances : []
      const sshInstances: SSHInstanceStatus[] = Array.isArray(sshInstanceData.instances) ? sshInstanceData.instances : []
      const sources: Source[] = Array.isArray(sourceData.dataSources) ? sourceData.dataSources : []
      setMysqlInstances(instances)
      setRedisInstances(redisInstances)
      setSshInstances(sshInstances)
      setDataSources(sources)
      if (!selectedDashboardId) {
        setSelectedDashboard(null)
        return
      }
      const imported = getImportedDashboards().find(item => item.sourceId === selectedDashboardId)
      const selectedSource = sources.find(item => item.id === selectedDashboardId)
      if (!imported || selectedSource?.enabled === false) {
        if (selectedSource?.enabled === false) deleteImportedDashboard(selectedDashboardId)
        setSelectedDashboard(null)
        return
      }
      const selectedType = selectedSource?.type || 'MySQL'
      const selected = selectedType === 'Redis'
        ? redisInstances.find(item => item.sourceId === selectedDashboardId)
        : selectedType === 'SSH'
          ? sshInstances.find(item => item.sourceId === selectedDashboardId)
          : instances.find(item => item.sourceId === selectedDashboardId)
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
      if (selectedType === 'Redis') {
        const metricResponse = await fetch(`${api}/redis-monitor/instances/${selected.sourceId}/metrics?${historyQuery.toString()}`)
        const metricData = await metricResponse.json()
        setSelectedDashboard({
          kind: 'Redis',
          status: selected as RedisInstanceStatus,
          displayName: imported.name,
          snapshot: Array.isArray(metricData.snapshots) ? metricData.snapshots[0] : undefined,
          snapshots: Array.isArray(metricData.snapshots) ? metricData.snapshots : [],
        })
        return
      }
      if (selectedType === 'SSH') {
        const metricResponse = await fetch(`${api}/ssh-monitor/instances/${selected.sourceId}/metrics?${historyQuery.toString()}`)
        const metricData = await metricResponse.json()
        setSelectedDashboard({
          kind: 'SSH',
          status: selected as SSHInstanceStatus,
          displayName: imported.name,
          snapshot: Array.isArray(metricData.snapshots) ? metricData.snapshots[0] : undefined,
          snapshots: Array.isArray(metricData.snapshots) ? metricData.snapshots : [],
        })
        return
      }
      const [metricResponse, slowResponse] = await Promise.all([
        fetch(`${api}/mysql-monitor/instances/${selected.sourceId}/metrics?${historyQuery.toString()}`),
        fetch(`${api}/mysql-monitor/instances/${selected.sourceId}/slow-queries?${slowQuery.toString()}`),
      ])
      const metricData = await metricResponse.json()
      const slowData = await slowResponse.json()
      setSelectedDashboard({
        kind: 'MySQL',
        status: selected as MySQLInstanceStatus,
        displayName: imported.name,
        snapshot: Array.isArray(metricData.snapshots) ? metricData.snapshots[0] : undefined,
        snapshots: Array.isArray(metricData.snapshots) ? metricData.snapshots : [],
        slowQueries: Array.isArray(slowData.slowQueries) ? slowData.slowQueries : [],
      })
    } catch {
      setMysqlInstances([])
      setRedisInstances([])
      setSshInstances([])
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
  return <div className="page dashboard"><section className="hero"><div><h2>{selectedDashboardId ? `${selectedDashboard?.kind || ''} 监控大屏` : '监控总览'}</h2><p>{selectedDashboardId ? '当前展示单个数据节点固定大屏，可按时间段查询历史采集数据。' : '当前按数据源展示关键运行信息，详细大屏请从左侧二级菜单进入。'}</p></div><div className="hero-actions">{selectedDashboardId && <div className="history-filter"><label>开始<input type="datetime-local" value={historyStart} onChange={(event) => setHistoryStart(event.target.value)} /></label><label>结束<input type="datetime-local" value={historyEnd} onChange={(event) => setHistoryEnd(event.target.value)} /></label><button className="button secondary" type="button" onClick={() => { setHistoryStart(''); setHistoryEnd('') }}>清空</button></div>}<button className="button secondary" onClick={refresh} disabled={loading}>{loading ? '同步中...' : '刷新数据'} <Icon name="arrow" /></button></div></section>{selectedDashboardId ? (selectedDashboard ? <section className="mysql-dashboard-stack">{selectedDashboard.kind === 'Redis' ? <RedisDashboard data={selectedDashboard} onDelete={() => setDeleteTarget(selectedDashboard)} /> : selectedDashboard.kind === 'SSH' ? <SSHDashboard data={selectedDashboard} onDelete={() => setDeleteTarget(selectedDashboard)} /> : <MySQLDashboard data={selectedDashboard} onDelete={() => setDeleteTarget(selectedDashboard)} />}</section> : <section className="surface dashboard-empty"><b>未找到该大屏</b><span>该大屏可能尚未导入、对应节点尚未采集成功，或该时间段内没有采集数据。</span></section>) : <MonitorOverview sources={dataSources} mysqlInstances={mysqlInstances} redisInstances={redisInstances} sshInstances={sshInstances} />}{deleteTarget && <div className="modal-backdrop" role="presentation" onMouseDown={() => setDeleteTarget(null)}><section className="surface confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-dashboard-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="delete-dashboard-title">确认删除大屏</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={() => setDeleteTarget(null)}>×</button></header><p>确认删除“{deleteTarget.displayName}”吗？删除后不会影响数据节点和采集数据。</p><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => setDeleteTarget(null)}>取消</button><button className="button danger-button" type="button" onClick={() => deleteDashboard(deleteTarget.status.sourceId)}>确认</button></footer></section></div>}</div>
}
function MonitorOverview({ sources, mysqlInstances, redisInstances, sshInstances }: { sources: Source[]; mysqlInstances: MySQLInstanceStatus[]; redisInstances: RedisInstanceStatus[]; sshInstances: SSHInstanceStatus[] }) {
  const mysqlStatusBySourceId = new Map(mysqlInstances.map(item => [item.sourceId, item]))
  const redisStatusBySourceId = new Map(redisInstances.map(item => [item.sourceId, item]))
  const sshStatusBySourceId = new Map(sshInstances.map(item => [item.sourceId, item]))
  return <section className="overview-stack">{sources.length === 0 ? <div className="surface dashboard-empty"><b>暂无数据源</b><span>添加数据源并等待采集后，这里会展示关键运行信息。</span></div> : <div className="overview-source-list">{sources.map(source => {
    const mysql = source.type === 'MySQL' ? mysqlStatusBySourceId.get(source.id) : undefined
    const redis = source.type === 'Redis' ? redisStatusBySourceId.get(source.id) : undefined
    const ssh = source.type === 'SSH' ? sshStatusBySourceId.get(source.id) : undefined
    const status = source.enabled ? (mysql?.status || redis?.status || ssh?.status || (source.type === 'MySQL' || source.type === 'Redis' || source.type === 'SSH' ? '待采集' : source.status)) : '停用'
    const isHealthy = status === '健康'
    const keyMetrics = source.enabled && mysql
      ? [['慢查询', String(mysql.slowQueries)], ['负载', `${percent(mysql.threadsConnected, mysql.maxConnections)}%`], ['存活', formatDuration(mysql.uptimeSeconds)], ['最近采集', formatCollectedAt(mysql.lastCollectedAt)]]
      : source.enabled && redis
        ? [['QPS', String(redis.opsPerSecond)], ['内存', formatBytes(redis.usedMemory)], ['命中率', `${Math.round(redis.hitRate * 100)}%`], ['慢日志', String(redis.slowlogLength)]]
        : source.enabled && ssh
          ? [['CPU', `${ssh.cpuUsagePercent.toFixed(1)}%`], ['内存', `${ssh.memoryPercent.toFixed(1)}%`], ['磁盘', `${ssh.diskPercent.toFixed(1)}%`], ['负载', ssh.load1.toFixed(2)]]
        : [['状态', source.enabled ? (source.type === 'MySQL' || source.type === 'Redis' || source.type === 'SSH' ? '等待采集' : source.status) : '已停止'], ['最近检测', formatCollectedAt(source.lastTest)], ['指标', source.enabled ? (source.type === 'MySQL' || source.type === 'Redis' || source.type === 'SSH' ? '采集中' : '待接入') : '已暂停']]
    return <article className={`surface overview-source ${isHealthy ? 'healthy' : 'warning'}`} key={source.id}><div className="overview-source-head"><span className="source-logo">{source.type.slice(0, 1)}</span><div><b>{source.name}</b><small>{source.type}{source.database ? ` · ${source.database}` : ''}</small></div><span className={`tag ${isHealthy ? 'success' : 'pending'}`}>{status}</span></div><div className="overview-source-metrics">{keyMetrics.map(([label, value]) => <p key={label}><span>{label}</span><b>{value}</b></p>)}</div>{(mysql?.lastError || redis?.lastError || ssh?.lastError) && <em>{mysql?.lastError || redis?.lastError || ssh?.lastError}</em>}</article>
  })}</div>}</section>
}
function MySQLDashboard({ data, onDelete }: { data: MySQLDashboardData; onDelete: () => void }) {
  const [activeTab, setActiveTab] = useState('overview')
  const [slowExpanded, setSlowExpanded] = useState(false)
  const status = data.status
  const metrics = data.snapshot?.metrics || {}
  const connectionPercent = percent(status.threadsConnected, status.maxConnections)
  const bufferTotal = metricNumber(metrics, 'Innodb_buffer_pool_pages_total')
  const bufferFree = metricNumber(metrics, 'Innodb_buffer_pool_pages_free')
  const bufferDirty = metricNumber(metrics, 'Innodb_buffer_pool_pages_dirty')
  const bufferUsedPercent = bufferTotal > 0 ? Math.round(((bufferTotal - bufferFree) / bufferTotal) * 100) : 0
  const allMetrics = Object.entries(metrics).filter(([key]) => mysqlMetricInfo[key] && !hiddenDetailMetricKeys.has(key)).sort(([a], [b]) => a.localeCompare(b))
  const visibleSlowQueries = slowExpanded ? data.slowQueries : data.slowQueries.slice(0, 10)
  const trendSnapshots = (data.snapshots && data.snapshots.length > 0 ? data.snapshots : data.snapshot ? [data.snapshot] : []).slice().reverse()
  const connectionTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: percent(metricNumber(item.metrics, 'Threads_connected'), metricNumber(item.metrics, 'max_connections')) }))
  const questionTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'Questions') }))
  const slowTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'Slow_queries') }))
  const sizeTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'database_size_bytes') }))
  const bufferTrend = trendSnapshots.map(item => {
    const total = metricNumber(item.metrics, 'Innodb_buffer_pool_pages_total')
    const free = metricNumber(item.metrics, 'Innodb_buffer_pool_pages_free')
    return { label: formatChartTime(item.collectedAt), value: total > 0 ? Math.round(((total - free) / total) * 100) : 0 }
  })
  const commandTotal = ['Com_select', 'Com_insert', 'Com_update', 'Com_delete'].reduce((sum, key) => sum + metricNumber(metrics, key), 0)
  const tabs = [
    ['overview', '总览'], ['query', '查询'], ['slow', '慢 SQL'], ['innodb', 'InnoDB'], ['capacity', '容量'], ['connection', '连接'],
  ]
  return <article className="mysql-template surface"><header className="mysql-template-head"><div><span className="template-kicker">MySQL 固定大屏模板</span><h2>{data.displayName}</h2><p>{status.sourceName} · {status.host}:{status.port} · MySQL {status.version || '-'}</p></div><div className="template-status"><div className="template-actions"><span className={`tag ${status.status === '健康' ? 'success' : 'pending'}`}>{status.status}</span><button className="delete-dashboard" type="button" onClick={onDelete}>删除</button></div><small>最近采集：{formatCollectedAt(status.lastCollectedAt)} · 历史点 {trendSnapshots.length}</small></div></header><nav className="dashboard-tabs" aria-label="MySQL 大屏模板">{tabs.map(([key, label]) => <button key={key} type="button" className={activeTab === key ? 'active' : ''} onClick={() => setActiveTab(key)}>{label}</button>)}</nav>{activeTab === 'overview' && <><section className="mysql-hero-grid"><div className="mysql-score"><div className="mysql-ring" style={{ '--ring': `${connectionPercent * 3.6}deg` } as CSSProperties & Record<string, string>}><span>{connectionPercent}%</span></div><b>连接使用率</b><small>{status.threadsConnected} / {status.maxConnections}</small></div><div className="mysql-kpi-grid"><DashboardKpi label="存活时间" value={formatDuration(status.uptimeSeconds)} detail="MySQL 实例持续运行时间" /><DashboardKpi label="慢查询" value={String(status.slowQueries)} detail="累计慢 SQL 数" /><DashboardKpi label="库大小" value={formatBytes(status.databaseSizeBytes)} detail="数据和索引总量" /><DashboardKpi label="复制状态" value={formatReplicaStatus(status.replicaStatus)} detail="主从复制健康度" /></div></section><section className="mysql-chart-grid"><LineChart title="连接负载趋势" unit="%" points={connectionTrend} /><LineChart title="查询吞吐趋势" points={questionTrend} /><LineChart title="慢查询趋势" points={slowTrend} /><DonutChart title="SQL 类型占比" total={commandTotal} slices={[{ label: 'select', value: metricNumber(metrics, 'Com_select'), color: 'blue' }, { label: 'insert', value: metricNumber(metrics, 'Com_insert'), color: 'green' }, { label: 'update', value: metricNumber(metrics, 'Com_update'), color: 'amber' }, { label: 'delete', value: metricNumber(metrics, 'Com_delete'), color: 'red' }]} /></section></>}{activeTab === 'query' && <><section className="mysql-chart-grid two"><LineChart title="查询吞吐趋势" points={questionTrend} /><DonutChart title="SQL 类型占比" total={commandTotal} slices={[{ label: 'select', value: metricNumber(metrics, 'Com_select'), color: 'blue' }, { label: 'insert', value: metricNumber(metrics, 'Com_insert'), color: 'green' }, { label: 'update', value: metricNumber(metrics, 'Com_update'), color: 'amber' }, { label: 'delete', value: metricNumber(metrics, 'Com_delete'), color: 'red' }]} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="查询吞吐" /><MetricRows rows={[["客户端语句", metrics.Questions, "questions"], ["服务端语句", metrics.Queries, "queries"], ["查询", metrics.Com_select, "select"], ["新增", metrics.Com_insert, "insert"], ["更新", metrics.Com_update, "update"], ["删除", metrics.Com_delete, "delete"]]} /></div><div className="surface mysql-panel"><SectionTitle title="风险信号" /><MetricRows rows={[["全表扫描", metrics.Select_scan], ["无索引 Join", metrics.Select_full_join], ["磁盘临时表", metrics.Created_tmp_disk_tables], ["临时表", metrics.Created_tmp_tables], ["顺序扫描", metrics.Handler_read_rnd_next]]} /></div></section></>}{activeTab === 'slow' && <section className="surface slow-panel"><div className="slow-panel-head"><SectionTitle title="慢 SQL / 高耗时样本" action={`${visibleSlowQueries.length} / ${data.slowQueries.length} 条`} />{data.slowQueries.length > 10 && <button className="slow-fold-button" type="button" onClick={() => setSlowExpanded(current => !current)}>{slowExpanded ? '折叠' : '展开全部'}</button>}</div>{data.slowQueries.length === 0 ? <div className="empty-state"><b>暂无慢 SQL 样本</b><span>当前实例 performance_schema 没有返回可展示的 digest 数据。</span></div> : <div className="slow-table"><div className="slow-row slow-head"><span>分类</span><span>SQL 语句</span><span>库名</span><span>次数</span><span>平均耗时</span><span>扫描 / 返回</span></div>{visibleSlowQueries.map(item => <div className="slow-row" key={`${item.id}-${item.digest}`}><span className="sql-kind">{sqlCategory(item.queryText)}</span><code className="sql-preview"><span className="sql-text">{item.queryText}</span><span className="sql-tooltip">{slowQueryDetail(item)}</span></code><span>{item.schemaName || '-'}</span><b>{item.count} 次</b><b>{item.averageLatencyMs.toFixed(1)} ms</b><small>扫描 {item.rowsExamined} 行 · 返回 {item.rowsSent} 行</small></div>)}</div>}</section>}{activeTab === 'innodb' && <><section className="mysql-chart-grid two"><LineChart title="Buffer Pool 使用率" unit="%" points={bufferTrend} /><LineChart title="慢查询趋势" points={slowTrend} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="InnoDB Buffer" /><div className="buffer-meter"><i style={{ width: `${bufferUsedPercent}%` }} /></div><MetricRows rows={[["使用率", `${bufferUsedPercent}%`], ["脏页", String(bufferDirty)], ["空闲页", String(bufferFree)], ["物理读", metrics.Innodb_buffer_pool_reads], ["逻辑读", metrics.Innodb_buffer_pool_read_requests], ["日志等待", metrics.Innodb_log_waits]]} /></div><div className="surface mysql-panel"><SectionTitle title="锁等待" /><MetricRows rows={[["当前行锁等待", metrics.Innodb_row_lock_current_waits], ["行锁等待次数", metrics.Innodb_row_lock_waits], ["行锁等待耗时", metrics.Innodb_row_lock_time], ["表锁等待", metrics.Table_locks_waited], ["锁等待会话", metrics.process_locked]]} /></div></section></>}{activeTab === 'capacity' && <><section className="mysql-chart-grid two"><LineChart title="库大小趋势" points={sizeTrend} /><LineChart title="接收流量趋势" points={trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'Bytes_received') }))} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="容量" /><MetricRows rows={[["库大小", formatBytes(status.databaseSizeBytes)], ["Buffer Pool", formatBytes(metricNumber(metrics, 'innodb_buffer_pool_size'))], ["接收流量", formatBytes(metricNumber(metrics, 'Bytes_received'))], ["发送流量", formatBytes(metricNumber(metrics, 'Bytes_sent'))]]} /></div><div className="surface all-metrics"><SectionTitle title="全部采集指标" action={`${allMetrics.length} 项`} /><div>{allMetrics.map(([key, value]) => <span key={key}><b>{key}</b><small>{mysqlMetricInfo[key]}</small><em>{value}</em></span>)}</div></div></section></>}{activeTab === 'connection' && <><section className="mysql-chart-grid two"><LineChart title="连接负载趋势" unit="%" points={connectionTrend} /><LineChart title="运行线程趋势" points={trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'Threads_running') }))} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="连接与流量" /><MetricRows rows={[["累计连接", metrics.Connections], ["当前连接", metrics.Threads_connected], ["最大连接", metrics.max_connections], ["历史最大连接", metrics.Max_used_connections], ["中止客户端", metrics.Aborted_clients], ["中止连接", metrics.Aborted_connects], ["运行线程", metrics.Threads_running]]} /></div><div className="surface mysql-panel"><SectionTitle title="会话状态" /><MetricRows rows={[["Query 会话", metrics.process_running], ["锁等待会话", metrics.process_locked], ["复制状态", formatReplicaStatus(metrics.replica_status)]]} /></div></section></>}</article>
}
function RedisDashboard({ data, onDelete }: { data: RedisDashboardData; onDelete: () => void }) {
  const [activeTab, setActiveTab] = useState('overview')
  const status = data.status
  const metrics = data.snapshot?.metrics || {}
  const trendSnapshots = (data.snapshots && data.snapshots.length > 0 ? data.snapshots : data.snapshot ? [data.snapshot] : []).slice().reverse()
  const memoryPercent = status.maxMemory > 0 ? percent(status.usedMemory, status.maxMemory) : 0
  const hitRatePercent = Math.round(status.hitRate * 100)
  const qpsTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'instantaneous_ops_per_sec') }))
  const memoryTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'used_memory') }))
  const hitTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: Math.round(metricNumber(item.metrics, 'hit_rate') * 100) }))
  const clientTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'connected_clients') }))
  const allMetrics = Object.entries(metrics).filter(([key]) => redisMetricInfo[key] && !hiddenRedisDetailMetricKeys.has(key)).sort(([a], [b]) => a.localeCompare(b))
  const tabs = [['overview', '总览'], ['throughput', '吞吐'], ['memory', '内存'], ['risk', '风险'], ['metrics', '指标']]
  return <article className="mysql-template redis-template surface"><header className="mysql-template-head"><div><span className="template-kicker">Redis 固定大屏模板</span><h2>{data.displayName}</h2><p>{status.sourceName} · {status.host}:{status.port} · Redis {status.version || '-'}</p></div><div className="template-status"><div className="template-actions"><span className={`tag ${status.status === '健康' ? 'success' : 'pending'}`}>{status.status}</span><button className="delete-dashboard" type="button" onClick={onDelete}>删除</button></div><small>最近采集：{formatCollectedAt(status.lastCollectedAt)} · 历史点 {trendSnapshots.length}</small></div></header><nav className="dashboard-tabs" aria-label="Redis 大屏模板">{tabs.map(([key, label]) => <button key={key} type="button" className={activeTab === key ? 'active' : ''} onClick={() => setActiveTab(key)}>{label}</button>)}</nav>{activeTab === 'overview' && <><section className="mysql-hero-grid"><div className="mysql-score"><div className="mysql-ring redis-ring" style={{ '--ring': `${memoryPercent * 3.6}deg` } as CSSProperties & Record<string, string>}><span>{status.maxMemory > 0 ? `${memoryPercent}%` : '-'}</span></div><b>内存使用率</b><small>{formatBytes(status.usedMemory)} / {status.maxMemory > 0 ? formatBytes(status.maxMemory) : '未限制'}</small></div><div className="mysql-kpi-grid"><DashboardKpi label="存活时间" value={formatDuration(status.uptimeSeconds)} detail="Redis 实例持续运行时间" /><DashboardKpi label="当前 QPS" value={String(status.opsPerSecond)} detail="每秒处理命令数" /><DashboardKpi label="命中率" value={`${hitRatePercent}%`} detail="keyspace hits / 总访问" /><DashboardKpi label="慢日志" value={String(status.slowlogLength)} detail="慢命令样本数量" /></div></section><section className="mysql-chart-grid"><LineChart title="QPS 趋势" points={qpsTrend} /><LineChart title="内存趋势" points={memoryTrend} /><LineChart title="命中率趋势" unit="%" points={hitTrend} /><DonutChart title="Key 生命周期" total={status.evictedKeys + status.expiredKeys} slices={[{ label: 'evicted', value: status.evictedKeys, color: 'red' }, { label: 'expired', value: status.expiredKeys, color: 'amber' }]} /></section></>}{activeTab === 'throughput' && <><section className="mysql-chart-grid two"><LineChart title="QPS 趋势" points={qpsTrend} /><LineChart title="连接趋势" points={clientTrend} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="吞吐与连接" /><MetricRows rows={[["当前 QPS", String(status.opsPerSecond), "instantaneous_ops_per_sec"], ["累计命令", String(status.totalCommands), "total_commands_processed"], ["当前连接", String(status.connectedClients), "connected_clients"], ["阻塞连接", String(status.blockedClients), "blocked_clients"], ["拒绝连接", String(status.rejectedConnections), "rejected_connections"]]} /></div><div className="surface mysql-panel"><SectionTitle title="缓存命中" /><MetricRows rows={[["命中率", `${hitRatePercent}%`, "hit_rate"], ["命中次数", metrics.keyspace_hits, "keyspace_hits"], ["未命中次数", metrics.keyspace_misses, "keyspace_misses"], ["Key 数量", String(status.keyCount), "key_count"]]} /></div></section></>}{activeTab === 'memory' && <><section className="mysql-chart-grid two"><LineChart title="内存趋势" points={memoryTrend} /><LineChart title="命中率趋势" unit="%" points={hitTrend} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="内存压力" /><div className="buffer-meter"><i style={{ width: `${memoryPercent}%` }} /></div><MetricRows rows={[["已用内存", formatBytes(status.usedMemory), "used_memory"], ["最大内存", status.maxMemory > 0 ? formatBytes(status.maxMemory) : '未限制', "maxmemory"], ["内存峰值", formatBytes(metricNumber(metrics, 'used_memory_peak')), "used_memory_peak"], ["碎片率", status.memoryFragmentation.toFixed(2), "mem_fragmentation_ratio"]]} /></div><div className="surface mysql-panel"><SectionTitle title="淘汰与过期" /><MetricRows rows={[["淘汰 Key", String(status.evictedKeys), "evicted_keys"], ["过期 Key", String(status.expiredKeys), "expired_keys"], ["最近 fork", `${metricNumber(metrics, 'latest_fork_usec')} us`, "latest_fork_usec"]]} /></div></section></>}{activeTab === 'risk' && <section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="性能风险" /><MetricRows rows={[["慢日志长度", String(status.slowlogLength), "slowlog_len"], ["阻塞连接", String(status.blockedClients), "blocked_clients"], ["拒绝连接", String(status.rejectedConnections), "rejected_connections"], ["内存碎片率", status.memoryFragmentation.toFixed(2), "mem_fragmentation_ratio"], ["角色", status.role || '-', "role"]]} /></div><div className="surface mysql-panel"><SectionTitle title="运行状态" /><MetricRows rows={[["版本", status.version || '-'], ["运行时长", formatDuration(status.uptimeSeconds)], ["Key 数量", String(status.keyCount)], ["从节点", metrics.connected_slaves || '0'], ["CPU sys", metrics.used_cpu_sys], ["CPU user", metrics.used_cpu_user]]} /></div></section>}{activeTab === 'metrics' && <section className="surface all-metrics"><SectionTitle title="全部采集指标" action={`${allMetrics.length} 项`} /><div>{allMetrics.map(([key, value]) => <span key={key}><b>{key}</b><small>{redisMetricInfo[key]}</small><em>{value}</em></span>)}</div></section>}</article>
}
function SSHDashboard({ data, onDelete }: { data: SSHDashboardData; onDelete: () => void }) {
  const [activeTab, setActiveTab] = useState('overview')
  const status = data.status
  const metrics = data.snapshot?.metrics || {}
  const trendSnapshots = (data.snapshots && data.snapshots.length > 0 ? data.snapshots : data.snapshot ? [data.snapshot] : []).slice().reverse()
  const cpuTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'cpu_usage_percent') }))
  const memoryTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'memory_percent') }))
  const diskTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'disk_percent') }))
  const loadTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'load1') }))
  const processTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'process_count') }))
  const tcpTrend = trendSnapshots.map(item => ({ label: formatChartTime(item.collectedAt), value: metricNumber(item.metrics, 'tcp_connections') }))
  const allMetrics = Object.entries(metrics).filter(([key]) => sshMetricInfo[key] && !hiddenSSHDetailMetricKeys.has(key)).sort(([a], [b]) => a.localeCompare(b))
  const tabs = [['overview', '总览'], ['resource', '资源'], ['load', '负载'], ['metrics', '指标']]
  return <article className="mysql-template ssh-template surface"><header className="mysql-template-head"><div><span className="template-kicker">SSH 固定大屏模板</span><h2>{data.displayName}</h2><p>{status.sourceName} · {status.host}:{status.port} · {status.hostname || 'unknown'} · {status.kernel || '-'}</p></div><div className="template-status"><div className="template-actions"><span className={`tag ${status.status === '健康' ? 'success' : 'pending'}`}>{status.status}</span><button className="delete-dashboard" type="button" onClick={onDelete}>删除</button></div><small>最近采集：{formatCollectedAt(status.lastCollectedAt)} · 历史点 {trendSnapshots.length}</small></div></header><nav className="dashboard-tabs" aria-label="SSH 大屏模板">{tabs.map(([key, label]) => <button key={key} type="button" className={activeTab === key ? 'active' : ''} onClick={() => setActiveTab(key)}>{label}</button>)}</nav>{activeTab === 'overview' && <><section className="mysql-hero-grid"><div className="mysql-score"><div className="mysql-ring ssh-ring" style={{ '--ring': `${status.cpuUsagePercent * 3.6}deg` } as CSSProperties & Record<string, string>}><span>{status.cpuUsagePercent.toFixed(0)}%</span></div><b>CPU 使用率</b><small>系统实时计算压力</small></div><div className="mysql-kpi-grid"><DashboardKpi label="存活时间" value={formatDuration(status.uptimeSeconds)} detail="系统启动后的运行时间" /><DashboardKpi label="内存使用率" value={`${status.memoryPercent.toFixed(1)}%`} detail={`${formatBytes(status.memoryUsed)} / ${formatBytes(status.memoryTotal)}`} /><DashboardKpi label="磁盘使用率" value={`${status.diskPercent.toFixed(1)}%`} detail={`${formatBytes(status.diskUsed)} / ${formatBytes(status.diskTotal)}`} /><DashboardKpi label="1 分钟负载" value={status.load1.toFixed(2)} detail="当前系统负载压力" /></div></section><section className="mysql-chart-grid"><LineChart title="CPU 使用率趋势" unit="%" points={cpuTrend} /><LineChart title="内存使用率趋势" unit="%" points={memoryTrend} /><LineChart title="磁盘使用率趋势" unit="%" points={diskTrend} /><LineChart title="Load1 趋势" points={loadTrend} /></section></>}{activeTab === 'resource' && <><section className="mysql-chart-grid two"><LineChart title="CPU 使用率趋势" unit="%" points={cpuTrend} /><LineChart title="内存使用率趋势" unit="%" points={memoryTrend} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="资源水位" /><div className="buffer-meter"><i style={{ width: `${Math.min(100, status.memoryPercent)}%` }} /></div><MetricRows rows={[["CPU 使用率", `${status.cpuUsagePercent.toFixed(2)}%`, "cpu_usage_percent"], ["内存使用率", `${status.memoryPercent.toFixed(2)}%`, "memory_percent"], ["已用内存", formatBytes(status.memoryUsed), "memory_used"], ["总内存", formatBytes(status.memoryTotal), "memory_total"]]} /></div><div className="surface mysql-panel"><SectionTitle title="磁盘容量" /><div className="buffer-meter"><i style={{ width: `${Math.min(100, status.diskPercent)}%` }} /></div><MetricRows rows={[["磁盘使用率", `${status.diskPercent.toFixed(2)}%`, "disk_percent"], ["已用磁盘", formatBytes(status.diskUsed), "disk_used"], ["总磁盘", formatBytes(status.diskTotal), "disk_total"]]} /></div></section></>}{activeTab === 'load' && <><section className="mysql-chart-grid two"><LineChart title="Load1 趋势" points={loadTrend} /><LineChart title="TCP 连接趋势" points={tcpTrend} /></section><section className="mysql-panels two"><div className="surface mysql-panel"><SectionTitle title="系统负载" /><MetricRows rows={[["Load 1", status.load1.toFixed(2), "load1"], ["Load 5", status.load5.toFixed(2), "load5"], ["Load 15", status.load15.toFixed(2), "load15"], ["进程数", String(status.processCount), "process_count"], ["TCP 连接", String(status.tcpConnections), "tcp_connections"]]} /></div><div className="surface mysql-panel"><SectionTitle title="进程与连接趋势" /><MetricRows rows={[["当前进程数", String(status.processCount)], ["当前 TCP 连接", String(status.tcpConnections)], ["主机名", status.hostname || '-'], ["内核", status.kernel || '-']]} /></div></section><section className="mysql-chart-grid two"><LineChart title="进程数趋势" points={processTrend} /><LineChart title="磁盘使用率趋势" unit="%" points={diskTrend} /></section></>}{activeTab === 'metrics' && <section className="surface all-metrics"><SectionTitle title="全部采集指标" action={`${allMetrics.length} 项`} /><div>{allMetrics.map(([key, value]) => <span key={key}><b>{key}</b><small>{sshMetricInfo[key]}</small><em>{key.endsWith('_used') || key.endsWith('_total') ? formatBytes(Number(value)) : value}</em></span>)}</div></section>}</article>
}
function DashboardKpi({ label, value, detail }: { label: string; value: string; detail: string }) { return <div className="dashboard-kpi"><span>{label}</span><b>{value}</b><small>{detail}</small></div> }
function LineChart({ title, points, unit = '' }: { title: string; points: Array<{ label: string; value: number }>; unit?: string }) {
  const values = points.map(point => Number.isFinite(point.value) ? point.value : 0)
  const max = Math.max(...values, 1)
  const min = Math.min(...values, 0)
  const range = Math.max(max - min, 1)
  const chartPoints = (points.length > 1 ? points : [...points, ...points]).map((point, index, list) => {
    const x = list.length <= 1 ? 8 : 8 + (index / (list.length - 1)) * 284
    const y = 108 - ((point.value - min) / range) * 84
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
  const latest = values.length ? values[values.length - 1] : 0
  return <div className="surface chart-panel"><SectionTitle title={title} action={points.length > 1 ? `${points.length} 点` : '实时'} /><svg viewBox="0 0 300 120" role="img" aria-label={title}><polyline className="chart-grid-line" points="8,108 292,108" /><polyline className="chart-grid-line" points="8,66 292,66" /><polyline className="chart-grid-line" points="8,24 292,24" /><polyline className="chart-line" points={chartPoints} /></svg><div className="chart-summary"><span>{points[0]?.label || '-'}</span><b>{formatCompactNumber(latest)}{unit}</b><span>{points[points.length - 1]?.label || '-'}</span></div></div>
}
function DonutChart({ title, total, slices }: { title: string; total: number; slices: Array<{ label: string; value: number; color: string }> }) {
  const colors: Record<string, string> = { blue: '#3d67f5', green: '#17a673', amber: '#dd8b12', red: '#e44f62' }
  let cursor = 0
  const gradient = total > 0 ? slices.map(slice => {
    const start = cursor
    const end = cursor + (slice.value / total) * 360
    cursor = end
    return `${colors[slice.color] || slice.color} ${start.toFixed(1)}deg ${end.toFixed(1)}deg`
  }).join(', ') : '#e8edf5 0deg 360deg'
  return <div className="surface chart-panel donut-panel"><SectionTitle title={title} action={total > 0 ? formatCompactNumber(total) : '无数据'} /><div className="donut-body"><div className="donut-chart" style={{ '--donut': gradient } as CSSProperties & Record<string, string>}><span>{total > 0 ? 'SQL' : '-'}</span></div><div className="donut-legend">{slices.map(slice => <p key={slice.label}><i style={{ background: colors[slice.color] || slice.color }} /><span>{slice.label}</span><b>{formatCompactNumber(slice.value)}</b></p>)}</div></div></div>
}
function MetricRows({ rows }: { rows: MetricRow[] }) { return <div className="metric-rows">{rows.map(([label, value, detail]) => <p key={label}><span>{label}{detail && <small>{detail}</small>}</span><b>{value || '-'}</b></p>)}</div> }
function SectionTitle({ title, action }: { title: string; action?: string }) { return <div className="section-title"><div><h2>{title}</h2></div>{action && <span className="section-badge">{action}</span>}</div> }
function PageHead({ title, description, action, onAction }: { title: string; description: string; action?: string; onAction?: () => void }) { return <header className="page-head"><div><h2>{title}</h2><span>{description}</span></div>{action && <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header> }
function Inspection() { return <div className="page"><PageHead title="巡检任务" description="统一查看任务执行状态与最近的健康检查结果。" action="新建巡检" /><section className="surface table-card"><div className="table-toolbar"><b>全部任务 <small>0</small></b><div><button className="filter">状态：全部⌄</button><button className="filter">最近更新⌄</button></div></div><div className="empty-state"><b>暂无巡检任务</b><span>当前没有已创建的巡检任务。</span></div></section></div> }
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
  const [redisStatuses, setRedisStatuses] = useState<RedisInstanceStatus[]>([])
  const [sshStatuses, setSshStatuses] = useState<SSHInstanceStatus[]>([])
  const [refreshing, setRefreshing] = useState(false)
  const [message, setMessage] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [dashboardSource, setDashboardSource] = useState<Source | null>(null)
  const [dashboardName, setDashboardName] = useState('')
  const [deleteSourceTarget, setDeleteSourceTarget] = useState<Source | null>(null)
  const [disableSourceTarget, setDisableSourceTarget] = useState<Source | null>(null)
  const [editingSource, setEditingSource] = useState<Source | null>(null)
  const [sourceType, setSourceType] = useState<SourceType>('MySQL')
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [optionRows, setOptionRows] = useState([{ key: '', value: '' }, { key: '', value: '' }])
  const [sourceStep, setSourceStep] = useState<1 | 2>(1)
  const [testPassed, setTestPassed] = useState(false)
  const [availableDatabases, setAvailableDatabases] = useState<string[]>([])
  const [selectedMonitorDatabases, setSelectedMonitorDatabases] = useState<string[]>([])
  const [databaseRemarks, setDatabaseRemarks] = useState<Record<string, string>>({})
  const [sourceDraft, setSourceDraft] = useState<SourceDraft | null>(null)

  const loadSources = async () => {
    setRefreshing(true)
    try {
      const [sourceResponse, monitorResponse, redisResponse, sshResponse] = await Promise.all([
        fetch(`${api}/data-sources`),
        fetch(`${api}/mysql-monitor/instances`),
        fetch(`${api}/redis-monitor/instances`),
        fetch(`${api}/ssh-monitor/instances`),
      ])
      const sourceData = await sourceResponse.json()
      const monitorData = await monitorResponse.json()
      const redisData = await redisResponse.json()
      const sshData = await sshResponse.json()
      setSources(Array.isArray(sourceData.dataSources) ? sourceData.dataSources : [])
      setMysqlStatuses(Array.isArray(monitorData.instances) ? monitorData.instances : [])
      setRedisStatuses(Array.isArray(redisData.instances) ? redisData.instances : [])
      setSshStatuses(Array.isArray(sshData.instances) ? sshData.instances : [])
    } catch {
      setSources([])
      setMysqlStatuses([])
      setRedisStatuses([])
      setSshStatuses([])
    } finally {
      setRefreshing(false)
    }
  }
  useEffect(() => {
    void loadSources()
    const timer = window.setInterval(() => void loadSources(), 15000)
    return () => window.clearInterval(timer)
  }, [])
  useEffect(() => {
    if (!message) return
    const timer = window.setTimeout(() => setMessage(''), 2400)
    return () => window.clearTimeout(timer)
  }, [message])

  const openCreateModal = () => {
    setEditingSource(null)
    setSourceType('MySQL')
    setOptionRows([{ key: '', value: '' }, { key: '', value: '' }])
    setSourceStep(1)
    setTestPassed(false)
    setAvailableDatabases([])
    setSelectedMonitorDatabases([])
    setDatabaseRemarks({})
    setSourceDraft(null)
    setModalOpen(true)
  }
  const openEditModal = (source: Source) => {
    setEditingSource(source)
    setSourceType(source.type as SourceType)
    const rows = Object.entries(source.options || {}).map(([key, value]) => ({ key, value }))
    setOptionRows(rows.length > 0 ? rows : [{ key: '', value: '' }, { key: '', value: '' }])
    const selected = splitDatabaseList(source.database)
    setSourceStep(source.type === 'MySQL' || source.type === 'Redis' ? 2 : 1)
    setTestPassed((source.type === 'MySQL' || source.type === 'Redis') && selected.length > 0)
    setAvailableDatabases(selected)
    setSelectedMonitorDatabases(selected)
    setDatabaseRemarks(parseDatabaseRemarks(source.options))
    setSourceDraft(source)
    setModalOpen(true)
  }
  const closeModal = () => {
    setModalOpen(false)
    setEditingSource(null)
    setTesting(false)
    setSaving(false)
    setSourceStep(1)
    setTestPassed(false)
    setAvailableDatabases([])
    setSelectedMonitorDatabases([])
    setDatabaseRemarks({})
    setSourceDraft(null)
  }
  const buildPayload = (form: HTMLFormElement) => {
    const formData = new FormData(form)
    const options = optionRows.reduce<Record<string, string>>((acc, row) => {
      const key = row.key.trim()
      if (key) acc[key] = row.value.trim()
      return acc
    }, {})
    options.monitor_database_remarks = JSON.stringify(databaseRemarks)
    const draft = sourceDraft
    return {
      name: String(formData.get('name') || draft?.name || `${sourceType} 数据源`),
      type: sourceType || draft?.type || 'MySQL',
      host: String(formData.get('host') || draft?.host || ''),
      port: String(formData.get('port') || draft?.port || defaultPorts[sourceType]),
      database: sourceType === 'MySQL' || sourceType === 'Redis' ? selectedMonitorDatabases.join(',') : String(formData.get('topic') || ''),
      username: String(formData.get('username') || draft?.username || ''),
      password: String(formData.get('password') || draft?.password || ''),
      remark: String(formData.get('remark') || draft?.remark || ''),
      enabled: editingSource?.enabled ?? true,
      options,
    }
  }
  const testConnection = async (form: HTMLFormElement) => {
    setTesting(true)
    setTestPassed(false)
    try {
      const formData = new FormData(form)
      const payload = {
        id: editingSource?.id || '',
        name: String(formData.get('name') || `${sourceType} 数据源`),
        type: sourceType,
        host: String(formData.get('host') || ''),
        port: String(formData.get('port') || defaultPorts[sourceType]),
        database: '',
        username: String(formData.get('username') || ''),
        password: String(formData.get('password') || sourceDraft?.password || ''),
        remark: String(formData.get('remark') || ''),
        options: {},
      }
      const response = await fetch(`${api}/data-sources/test`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const result = await response.json()
      if (result.success) {
        const databases = Array.isArray(result.databases) ? result.databases : []
        setSourceDraft(payload)
        setTestPassed(true)
        setAvailableDatabases(databases)
        setSelectedMonitorDatabases(current => current.length > 0 ? current.filter(item => databases.includes(item)) : sourceType === 'Redis' && databases.includes('db0') ? ['db0'] : [])
      }
      setMessage(result.message || (result.success ? '连接测试通过' : '连接测试失败'))
    } catch {
      setMessage('连接测试失败，请检查后端服务')
    } finally {
      setTesting(false)
    }
  }
  const saveSource = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if ((sourceType === 'MySQL' || sourceType === 'Redis') && selectedMonitorDatabases.length === 0) {
      setMessage('请至少选择一个监控库')
      return
    }
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
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
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
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '删除失败')
    }
  }
  const toggleSourceEnabled = async (source: Source, enabled: boolean, confirmed = false) => {
    if (!enabled && !confirmed) {
      setDisableSourceTarget(source)
      return
    }
    setSources(current => current.map(item => item.id === source.id ? { ...item, enabled, status: enabled ? item.status : '停用' } : item))
    if (!enabled) {
      deleteImportedDashboard(source.id)
    }
    try {
      const response = await fetch(`${api}/data-sources/${source.id}/enabled`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled }),
      })
      const updated = await response.json()
      if (!response.ok) throw new Error(updated.error || '状态更新失败')
      setSources(current => current.map(item => item.id === source.id ? { ...item, ...updated } : item))
      if (!enabled) {
        setMysqlStatuses(current => current.filter(item => item.sourceId !== source.id))
        setRedisStatuses(current => current.filter(item => item.sourceId !== source.id))
        setDisableSourceTarget(null)
        setMessage(`${source.name} 已停止`)
      } else {
        setMessage(`${source.name} 已启用`)
      }
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (error) {
      setSources(current => current.map(item => item.id === source.id ? source : item))
      setMessage(error instanceof Error ? error.message : '状态更新失败')
    }
  }
  const importDashboard = (source: Source) => {
    if (source.type !== 'MySQL' && source.type !== 'Redis' && source.type !== 'SSH') {
      setMessage('当前仅支持导入 MySQL / Redis / SSH 固定大屏')
      return
    }
    if (!source.enabled) {
      setMessage('该数据节点已停止，请先启用后再导入大屏')
      return
    }
    setDashboardSource(source)
    setDashboardName(getImportedDashboards().find(item => item.sourceId === source.id)?.name || `${source.name} ${source.type} 监控大屏`)
  }
  const confirmImportDashboard = () => {
    if (!dashboardSource) return
    const name = dashboardName.trim() || `${dashboardSource.name} ${dashboardSource.type} 监控大屏`
    saveImportedDashboard(dashboardSource.id, name)
    setDashboardSource(null)
    setDashboardName('')
    setMessage(`${name} 已导入监控总览`)
    navigate(`/?dashboard=${dashboardSource.id}&imported=${Date.now()}`)
  }
  const toggleMonitorDatabase = (database: string) => {
    setSelectedMonitorDatabases(current => current.includes(database) ? current.filter(item => item !== database) : [...current, database])
  }
  const renderMonitorDatabases = (source: Source) => {
    const databases = splitDatabaseList(source.database)
    const remarks = parseDatabaseRemarks(source.options)
    if ((source.type !== 'MySQL' && source.type !== 'Redis') || databases.length === 0) return null
    return <div className="node-databases">{databases.map(database => <span key={database}><b>{database}</b>{remarks[database] && <em>{remarks[database]}</em>}</span>)}</div>
  }

  const mysqlStatusBySourceId = new Map(mysqlStatuses.map(item => [item.sourceId, item]))
  const redisStatusBySourceId = new Map(redisStatuses.map(item => [item.sourceId, item]))
  const sshStatusBySourceId = new Map(sshStatuses.map(item => [item.sourceId, item]))
  const healthyCount = sources.filter(source => {
    if (!source.enabled) return false
    const live = source.type === 'Redis' ? redisStatusBySourceId.get(source.id) : source.type === 'SSH' ? sshStatusBySourceId.get(source.id) : mysqlStatusBySourceId.get(source.id)
    return (live?.status || source.status) === '健康'
  }).length

  return (
    <div className="page">
      <PageHead title="数据节点" description={`实时同步节点采集状态，当前 ${healthyCount} / ${sources.length} 个节点健康。`} action="添加数据节点" onAction={openCreateModal} />
      <section className="node-toolbar">
        <span>{refreshing ? '正在同步节点状态' : '每 15 秒自动刷新'}</span>
        <button className="button secondary" type="button" onClick={() => void loadSources()} disabled={refreshing}>{refreshing ? '刷新中...' : '刷新状态'}</button>
      </section>
      {sources.length === 0 ? (
        <section className="surface empty-state"><b>暂无数据节点</b><span>点击右上角添加数据节点，完成连接测试后即可选择监控库。</span></section>
      ) : (
        <section className="source-list">
          {sources.map(s => {
            const live = s.type === 'Redis' ? redisStatusBySourceId.get(s.id) : s.type === 'SSH' ? sshStatusBySourceId.get(s.id) : mysqlStatusBySourceId.get(s.id)
            const status = s.enabled ? (live?.status || (s.type === 'MySQL' || s.type === 'Redis' || s.type === 'SSH' ? '待采集' : s.status)) : '停用'
            const isHealthy = status === '健康'
            const liveLabel = s.type === 'SSH' && live && 'kernel' in live ? live.kernel : live && 'version' in live ? `${s.type} ${live.version}` : '监控数据待生成'
            return (
              <article className={`surface source-row ${isHealthy ? 'healthy' : 'warning'} ${!s.enabled ? 'disabled' : ''}`} key={s.id}>
                <div className="node-main">
                  <span className="source-logo">{s.type.slice(0, 1)}</span>
                  <div><h3>{s.name}</h3><p>{s.type} · {s.host}:{s.port}</p>{renderMonitorDatabases(s)}{s.remark && <small className="source-remark">{s.remark}</small>}</div>
                </div>
                <div className="node-status">{s.enabled && live?.lastError && <small>{live.lastError}</small>}</div>
                <div className="node-enabled"><StatusSwitch checked={s.enabled} onChange={(checked) => void toggleSourceEnabled(s, checked)} /></div>
                <div className="node-meta"><span>最近采集：{formatCollectedAt(live?.lastCollectedAt || s.lastTest)}</span>{s.enabled && <span>{liveLabel}</span>}</div>
                <div className="source-actions">
                  {(s.type === 'MySQL' || s.type === 'Redis' || s.type === 'SSH') && <button type="button" disabled={!s.enabled} onClick={() => importDashboard(s)}>导入大屏</button>}
                  <button type="button" onClick={() => openEditModal(s)}>编辑</button>
                  <button className="danger" type="button" onClick={() => setDeleteSourceTarget(s)}>删除</button>
                </div>
              </article>
            )
          })}
        </section>
      )}
      {disableSourceTarget && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setDisableSourceTarget(null) }}><section className="surface confirm-modal" role="dialog" aria-modal="true" aria-labelledby="disable-source-title" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="disable-source-title">确认停止数据源</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={() => setDisableSourceTarget(null)}>×</button></header><p>确认停止“{disableSourceTarget.name}”吗？停止后会删除该数据源已导入的大屏，并暂停关联告警规则。</p><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => setDisableSourceTarget(null)}>取消</button><button className="button danger-button" type="button" onClick={() => void toggleSourceEnabled(disableSourceTarget, false, true)}>确认</button></footer></section></div>}
      {deleteSourceTarget && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setDeleteSourceTarget(null) }}><section className="surface confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-source-title" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="delete-source-title">确认删除数据源</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={() => setDeleteSourceTarget(null)}>×</button></header><p>确认删除“{deleteSourceTarget.name}”吗？删除后会同步移除该数据源已导入的大屏入口，历史采集数据不在此处展示。</p><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => setDeleteSourceTarget(null)}>取消</button><button className="button danger-button" type="button" onClick={() => void deleteSource(deleteSourceTarget)}>确认</button></footer></section></div>}
      {dashboardSource && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setDashboardSource(null) }}><section className="surface dashboard-import-modal" role="dialog" aria-modal="true" aria-labelledby="dashboard-import-title" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="dashboard-import-title">导入监控大屏</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={() => setDashboardSource(null)}>×</button></header><label><span className="field-label">大屏名称 <span className="required-mark">*</span></span><input value={dashboardName} onChange={(event) => setDashboardName(event.target.value)} autoFocus /></label><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => setDashboardSource(null)}>取消</button><button className="button" type="button" onClick={confirmImportDashboard}>导入</button></footer></section></div>}
      {modalOpen && (
        <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) closeModal() }}>
          <section className="surface source-modal source-wizard-modal" role="dialog" aria-modal="true" aria-labelledby="source-modal-title" onClick={(event) => event.stopPropagation()}>
            <form id="source-form" key={`${editingSource?.id || 'new'}-${sourceType}`} onSubmit={saveSource}>
              <header className="modal-head"><div><h2 id="source-modal-title">{editingSource ? '编辑数据节点' : '添加数据节点'}</h2><p>{sourceType === 'MySQL' || sourceType === 'Redis' ? `步骤 ${sourceStep} / 2` : '连接配置'}</p></div><div className="modal-head-actions">{sourceStep === 2 && <button className="button secondary" type="button" onClick={() => setSourceStep(1)}>上一步</button>}<button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.form; if (form) void testConnection(form) }} disabled={testing || sourceStep === 2}>{testing ? '测试中...' : '测试连接'}</button>{sourceStep === 1 && (sourceType === 'MySQL' || sourceType === 'Redis') ? <button className="button" type="button" disabled={!testPassed} onMouseDown={(event) => event.stopPropagation()} onClick={(event) => { event.stopPropagation(); setSourceStep(2) }}>{testPassed ? '下一步' : '待测试'}</button> : <button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button>}<button className="close-button" type="button" aria-label="关闭" onClick={() => closeModal()}>×</button></div></header>
              {sourceStep === 1 && <><div className="type-picker" role="group" aria-label="数据类型">{sourceTypes.map(type => <button key={type} type="button" className={sourceType === type ? 'active' : ''} onClick={() => { setSourceType(type); setTestPassed(false); setAvailableDatabases([]); setSelectedMonitorDatabases([]); setDatabaseRemarks({}); if (!editingSource || editingSource.type !== type) setOptionRows([{ key: '', value: '' }, { key: '', value: '' }]) }}>{type}</button>)}</div><div className="modal-form"><Field label="数据节点名称" name="name" value={editingSource?.name || `${sourceType} 生产节点`} required /><label>主机地址 <span className="required-mark">*</span><input name="host" defaultValue={editingSource?.host || ''} placeholder="例如 127.0.0.1 或 broker.internal" required /></label><label>端口 <span className="required-mark">*</span><input name="port" defaultValue={editingSource?.port || defaultPorts[sourceType]} required /></label>{sourceType === 'Kafka' && <label>Topic / Consumer Group<input name="topic" defaultValue={editingSource?.database || ''} placeholder="例如 ops-events / ops-monitor" /></label>}<label>用户名{(sourceType === 'MySQL' || sourceType === 'SSH') && <span className="required-mark"> *</span>}<input name="username" defaultValue={editingSource?.username || ''} required={sourceType === 'MySQL' || sourceType === 'SSH'} placeholder={sourceType === 'Redis' ? '可选' : sourceType === 'SSH' ? '请输入 SSH 用户名' : '请输入用户名'} /></label><label>密码{(sourceType === 'MySQL' || sourceType === 'SSH') && !editingSource && <span className="required-mark"> *</span>}<input name="password" type="password" required={(sourceType === 'MySQL' || sourceType === 'SSH') && !editingSource} placeholder={editingSource ? '留空则复用已保存密码测试/保存' : sourceType === 'Redis' ? '无密码可留空' : sourceType === 'SSH' ? '请输入 SSH 密码' : '请输入密码'} /></label>{sourceType === 'Elasticsearch' && <label>索引前缀<input name="indexPrefix" placeholder="例如 logs-*" /></label>}<label className="wide">备注<textarea name="remark" defaultValue={editingSource?.remark || ''} placeholder="记录用途、负责人、环境或注意事项" /></label><div className="wide option-editor"><div><b>连接参数</b><span>示例：ssl true、timeout 10s、brokers host1:9092,host2:9092</span></div>{optionRows.map((row, index) => <div className="option-row" key={index}><input aria-label="参数名" placeholder="key" value={row.key} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, key: event.target.value } : item))} /><input aria-label="参数值" placeholder="value" value={row.value} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} /></div>)}<button className="text-button" type="button" onClick={() => setOptionRows(rows => [...rows, { key: '', value: '' }])}>添加参数 <Icon name="plus" /></button></div></div></>}
              {sourceStep === 2 && <div className="database-picker"><div><b>选择监控库 <span className="required-mark">*</span></b><span>{editingSource ? '当前展示已监控库。如需重新拉取完整库列表，请返回上一步测试连接。' : '可多选，每个库可填写备注，保存后会展示在数据节点列表。'}</span></div><div className="database-list">{availableDatabases.length === 0 ? <p>未获取到可选数据库，请返回上一步重新测试连接。</p> : availableDatabases.map(database => { const checked = selectedMonitorDatabases.includes(database); return <label className={`database-choice ${checked ? 'checked' : ''}`} key={database}><input type="checkbox" checked={checked} onChange={() => toggleMonitorDatabase(database)} /><span><b>{database}</b><input placeholder="备注，可选" value={databaseRemarks[database] || ''} onChange={(event) => setDatabaseRemarks(current => ({ ...current, [database]: event.target.value }))} /></span></label> })}</div></div>}
            </form>
          </section>
        </div>
      )}
      {message && <div className="toast">{message}</div>}
    </div>
  )

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
function formatCompactNumber(value: number) {
  if (!Number.isFinite(value)) return '-'
  if (Math.abs(value) >= 1000000000) return `${(value / 1000000000).toFixed(1)}B`
  if (Math.abs(value) >= 1000000) return `${(value / 1000000).toFixed(1)}M`
  if (Math.abs(value) >= 1000) return `${(value / 1000).toFixed(1)}K`
  return String(Math.round(value))
}
function formatChartTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
}
function formatCollectedAt(value: string) {
  if (!value) return '待采集'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}
function probeField(condition: string) {
  if (condition === '页面包含') return 'body'
  if (condition === '状态码等于') return 'status_code'
  if (condition === 'TCP端口存活') return 'tcp_connect'
  return 'http_status'
}
function Notifications() {
  const [items, setItems] = useState<NotificationItem[]>([])
  const [filter, setFilter] = useState('all')
  const [unreadOnly, setUnreadOnly] = useState(false)
  const [loading, setLoading] = useState(false)
  const [message, setMessage] = useState('')
  const loadNotifications = async () => {
    setLoading(true)
    try {
      const params = new URLSearchParams({ limit: '200' })
      if (filter !== 'all') params.set('status', filter)
      if (unreadOnly) params.set('unread', '1')
      const response = await fetch(`${api}/notifications?${params.toString()}`)
      const data = await response.json()
      setItems(Array.isArray(data.notifications) ? data.notifications : [])
    } catch {
      setItems([])
      setMessage('获取通知失败')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void loadNotifications() }, [filter, unreadOnly])
  useEffect(() => {
    if (!message) return
    const timer = window.setTimeout(() => setMessage(''), 2400)
    return () => window.clearTimeout(timer)
  }, [message])
  const markRead = async (id = 'all') => {
    try {
      const response = await fetch(`${api}/notifications/read`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id }) })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '标记失败')
      setItems(current => id === 'all' ? current.map(item => ({ ...item, unread: false })) : current.map(item => item.id === id ? { ...item, unread: false } : item))
      window.dispatchEvent(new Event('opsguard-notifications-change'))
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '标记失败')
    }
  }
  const activeCount = items.filter(item => item.status === 'active').length
  const unreadCount = items.filter(item => item.unread).length
  return <div className="page"><PageHead title="通知中心" description="集中查看告警规则产生的通知，支持按状态和未读筛选。" /><section className="notification-toolbar surface"><div><button className={filter === 'all' ? 'active' : ''} type="button" onClick={() => setFilter('all')}>全部</button><button className={filter === 'active' ? 'active' : ''} type="button" onClick={() => setFilter('active')}>告警中</button><button className={filter === 'resolved' ? 'active' : ''} type="button" onClick={() => setFilter('resolved')}>已恢复</button><button className={unreadOnly ? 'active' : ''} type="button" onClick={() => setUnreadOnly(current => !current)}>未读</button></div><div><span>{activeCount} 条告警中 · {unreadCount} 条未读</span><button className="button secondary" type="button" onClick={() => void loadNotifications()} disabled={loading}>{loading ? '刷新中...' : '刷新'}</button><button className="button" type="button" onClick={() => void markRead('all')} disabled={unreadCount === 0}>全部已读</button></div></section><section className="surface notification-list">{items.length === 0 ? <div className="empty-state alert-empty-state"><b>暂无通知</b><span>告警规则产生告警后会同步到这里。</span></div> : items.map(item => <article className={`notification-row ${item.unread ? 'unread' : ''}`} key={item.id}><i className={`notification-dot ${item.status === 'active' ? 'danger' : 'success'}`} /><div><header><b>{item.ruleName}</b><span className={`alert-result ${item.status === 'active' ? 'danger' : 'success'}`}>{item.status === 'active' ? '告警中' : '已恢复'}</span></header><p>{item.message}</p><small>{item.database || '-'}.{item.table || '-'}.{item.field || '-'} · 首次：{formatCollectedAt(item.firstSeenAt)} · 最近：{formatCollectedAt(item.lastSeenAt)}</small></div><button className="text-button" type="button" onClick={() => void markRead(item.id)} disabled={!item.unread}>{item.unread ? '标为已读' : '已读'}</button></article>)}</section>{message && <div className="toast">{message}</div>}</div>
}
function ExternalMonitor() {
  const [config, setConfig] = useState<ExternalMonitorConfig | null>(null)
  const [alerts, setAlerts] = useState<PrometheusAlert[]>([])
  const [metrics, setMetrics] = useState<PrometheusMetric[]>([])
  const [dashboards, setDashboards] = useState<GrafanaDashboardItem[]>([])
  const [query, setQuery] = useState('up')
  const [queryResult, setQueryResult] = useState('')
  const [loading, setLoading] = useState(false)
  const [message, setMessage] = useState('')
  const loadExternalMonitor = async () => {
    setLoading(true)
    setMessage('')
    try {
      const configResponse = await fetch(`${api}/external-monitor/config`)
      const nextConfig = await configResponse.json()
      setConfig(nextConfig)
      const requests: Promise<void>[] = []
      if (nextConfig.prometheusConfigured) {
        requests.push(fetch(`${api}/external-monitor/prometheus/alerts`).then(async response => {
          const data = await response.json()
          if (!response.ok) throw new Error(data.error || 'Prometheus 告警获取失败')
          setAlerts(Array.isArray(data.alerts) ? data.alerts : [])
        }))
        requests.push(fetch(`${api}/external-monitor/prometheus/metrics?limit=120`).then(async response => {
          const data = await response.json()
          if (!response.ok) throw new Error(data.error || 'Prometheus 指标获取失败')
          setMetrics(Array.isArray(data.metrics) ? data.metrics : [])
        }))
      } else {
        setAlerts([])
        setMetrics([])
      }
      if (nextConfig.grafanaConfigured) {
        requests.push(fetch(`${api}/external-monitor/grafana/dashboards?limit=120`).then(async response => {
          const data = await response.json()
          if (!response.ok) throw new Error(data.error || 'Grafana 面板获取失败')
          setDashboards(Array.isArray(data.dashboards) ? data.dashboards : [])
        }))
      } else {
        setDashboards([])
      }
      await Promise.all(requests)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '外部监控数据获取失败')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void loadExternalMonitor() }, [])
  const runQuery = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setQueryResult('')
    try {
      const response = await fetch(`${api}/external-monitor/prometheus/query?query=${encodeURIComponent(query)}`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || 'PromQL 查询失败')
      setQueryResult(JSON.stringify(data.data, null, 2))
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'PromQL 查询失败')
    }
  }
  return <div className="page"><PageHead title="外部监控" description="读取 Prometheus 告警与指标，并同步 Grafana 面板目录。" /><section className="external-toolbar surface"><div><b>接入状态</b><span>Prometheus：{config?.prometheusConfigured ? config.prometheusUrl : '未配置'} · Grafana：{config?.grafanaConfigured ? config.grafanaUrl : '未配置'}</span></div><button className="button secondary" type="button" onClick={() => void loadExternalMonitor()} disabled={loading}>{loading ? '同步中...' : '刷新'}</button></section>{message && <div className="toast">{message}</div>}<section className="external-grid"><div className="surface external-panel"><SectionTitle title="Prometheus 告警" action={`${alerts.length} 条`} />{!config?.prometheusConfigured ? <div className="empty-state"><b>Prometheus 未配置</b><span>进入系统配置的外部监控页签填写 Prometheus 地址和 Token。</span></div> : alerts.length === 0 ? <div className="empty-state"><b>暂无 Prometheus 告警</b><span>当前没有从 Prometheus API 读取到告警。</span></div> : <div className="external-list">{alerts.map((alert, index) => <article key={`${alert.name}-${index}`}><header><b>{alert.name}</b><span className={`alert-result ${alert.state === 'firing' ? 'danger' : 'success'}`}>{alert.state || '-'}</span></header><p>{alert.summary || alert.description || '-'}</p><small>{alert.severity || 'unknown'} · {alert.activeAt ? formatCollectedAt(alert.activeAt) : '-'}</small></article>)}</div>}</div><div className="surface external-panel"><SectionTitle title="Prometheus 指标" action={`${metrics.length} 项`} />{!config?.prometheusConfigured ? <div className="empty-state"><b>指标未接入</b><span>配置 Prometheus 后会显示 TSDB 当前存储的指标名。</span></div> : <div className="metric-name-grid">{metrics.map(metric => <span key={metric.name}>{metric.name}</span>)}</div>}</div></section><section className="external-grid"><div className="surface external-panel"><SectionTitle title="PromQL 查询" /><form className="promql-form" onSubmit={runQuery}><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="例如 up 或 rate(http_requests_total[5m])" /><button className="button" type="submit" disabled={!config?.prometheusConfigured}>查询</button></form>{queryResult && <pre className="query-result">{queryResult}</pre>}</div><div className="surface external-panel"><SectionTitle title="Grafana 面板" action={`${dashboards.length} 个`} />{!config?.grafanaConfigured ? <div className="empty-state"><b>Grafana 未配置</b><span>进入系统配置的外部监控页签填写 Grafana 地址和 Token。</span></div> : dashboards.length === 0 ? <div className="empty-state"><b>暂无 Grafana 面板</b><span>当前没有读取到 dashboard。</span></div> : <div className="external-list dashboard-list">{dashboards.map(dashboard => <article key={dashboard.uid || dashboard.uri}><header><b>{dashboard.title}</b>{dashboard.url && <a href={dashboard.url} target="_blank" rel="noreferrer">打开</a>}</header><p>{dashboard.folderTitle || 'General'}</p><small>{(dashboard.tags || []).join(' / ') || dashboard.uri || '-'}</small></article>)}</div>}</div></section></div>
}
function Alerts() {
  const [rules, setRules] = useState<Rule[]>([])
  const [sources, setSources] = useState<Source[]>([])
  const [editingRule, setEditingRule] = useState<Rule | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [deleteRuleTarget, setDeleteRuleTarget] = useState<Rule | null>(null)
  const [enableSourceRuleTarget, setEnableSourceRuleTarget] = useState<{ rule: Rule; source: Source } | null>(null)
  const [schemaLoading, setSchemaLoading] = useState(false)
  const [schema, setSchema] = useState<SourceSchema>({})
  const [selectedSourceId, setSelectedSourceId] = useState('')
  const [selectedDatabase, setSelectedDatabase] = useState('')
  const [selectedTable, setSelectedTable] = useState('')
  const [selectedField, setSelectedField] = useState('')
  const [ruleCondition, setRuleCondition] = useState('大于')
  const [ruleStatus, setRuleStatus] = useState<'启用' | '停用'>('启用')
  const [ruleMode, setRuleMode] = useState<'source' | 'custom'>('source')
  const [probeType, setProbeType] = useState('http')
  const [statusSavingId, setStatusSavingId] = useState('')
  useEffect(() => {
    if (!message) return
    const timer = window.setTimeout(() => setMessage(''), 2400)
    return () => window.clearTimeout(timer)
  }, [message])
  const loadRules = async () => {
    try {
      const response = await fetch(`${api}/collection-rules`)
      const data = await response.json()
      setRules(Array.isArray(data.rules) ? data.rules : [])
    } catch {
      setRules(fallbackRules)
    }
  }
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources : [])
    } catch {
      setSources([])
    }
  }
  useEffect(() => { void loadRules(); void loadSources() }, [])
  const mergeSchema = (base: SourceSchema, next: SourceSchema): SourceSchema => {
    const merged: SourceSchema = { ...base }
    for (const [database, tables] of Object.entries(next)) {
      merged[database] = { ...(merged[database] || {}), ...tables }
    }
    return merged
  }
  const fetchSchema = async (sourceId: string, database = '', table = '') => {
    const params = new URLSearchParams()
    if (database) params.set('database', database)
    if (table) params.set('table', table)
    const response = await fetch(`${api}/data-sources/${sourceId}/schema${params.toString() ? `?${params.toString()}` : ''}`)
    const data = await response.json()
    return data.schema && typeof data.schema === 'object' ? data.schema as SourceSchema : {}
  }
  const loadSchema = async (sourceId: string, preferredDatabase = '', preferredTable = '', preferredField = '') => {
    if (!sourceId) {
      setSchema({})
      setSelectedDatabase('')
      setSelectedTable('')
      setSelectedField('')
      return
    }
    setSchemaLoading(true)
    try {
      const source = sources.find(item => item.id === sourceId)
      let nextSchema = await fetchSchema(sourceId)
      const monitoredDatabases = splitDatabaseList(source?.database || '')
      const database = preferredDatabase && (nextSchema[preferredDatabase] || monitoredDatabases.includes(preferredDatabase))
        ? preferredDatabase
        : ''
      let table = ''
      let field = ''
      if (database) {
        const tableSchema = await fetchSchema(sourceId, database)
        nextSchema = mergeSchema(nextSchema, tableSchema)
        const tables = Object.keys(nextSchema[database] || {})
        table = preferredTable && tables.includes(preferredTable) ? preferredTable : ''
      }
      if (database && table) {
        const fieldSchema = await fetchSchema(sourceId, database, table)
        nextSchema = mergeSchema(nextSchema, fieldSchema)
        const fields = nextSchema[database]?.[table] || []
        field = preferredField && fields.includes(preferredField) ? preferredField : ''
      }
      setSchema(nextSchema)
      setSelectedDatabase(database)
      setSelectedTable(table)
      setSelectedField(field)
    } catch {
      setSchema({})
      setSelectedDatabase('')
      setSelectedTable('')
      setSelectedField('')
      setMessage('获取数据源结构失败')
    } finally {
      setSchemaLoading(false)
    }
  }
  const loadTablesForDatabase = async (database: string) => {
    setSelectedDatabase(database)
    setSelectedTable('')
    setSelectedField('')
    if (!selectedSourceId || !database) return
    setSchemaLoading(true)
    try {
      const tableSchema = await fetchSchema(selectedSourceId, database)
      setSchema(current => mergeSchema(current, tableSchema))
    } catch {
      setMessage('获取表列表失败')
    } finally {
      setSchemaLoading(false)
    }
  }
  const loadFieldsForTable = async (table: string) => {
    setSelectedTable(table)
    setSelectedField('')
    if (!selectedSourceId || !selectedDatabase || !table) return
    setSchemaLoading(true)
    try {
      const fieldSchema = await fetchSchema(selectedSourceId, selectedDatabase, table)
      setSchema(current => mergeSchema(current, fieldSchema))
    } catch {
      setMessage('获取字段列表失败')
    } finally {
      setSchemaLoading(false)
    }
  }
  const openRuleModal = (rule?: Rule) => {
    const custom = rule?.source === 'custom-probe'
    const source = rule ? sources.find(item => item.name === rule.source || item.id === rule.source) : sources[0]
    setEditingRule(rule || null)
    setRuleMode(custom ? 'custom' : 'source')
    setProbeType(custom ? (rule?.database || 'http') : 'http')
    setSelectedSourceId(custom ? '' : source?.id || '')
    setSelectedDatabase(custom ? '' : rule?.database || '')
    setSelectedTable(rule?.table || '')
    setSelectedField(rule?.field || '')
    setRuleCondition(rule?.condition || (custom ? 'HTTP状态正常' : '大于'))
    setRuleStatus(rule?.status === '停用' ? '停用' : '启用')
    setModalOpen(true)
    if (!custom && source?.id) void loadSchema(source.id, rule?.database || '', rule?.table || '', rule?.field || '')
  }
  const closeRuleModal = () => {
    setModalOpen(false)
    setEditingRule(null)
    setSaving(false)
    setSchema({})
    setSelectedSourceId('')
    setSelectedDatabase('')
    setSelectedTable('')
    setSelectedField('')
    setRuleCondition('大于')
    setRuleStatus('启用')
    setRuleMode('source')
    setProbeType('http')
  }
  const sourceDatabases = splitDatabaseList(sources.find(source => source.id === selectedSourceId)?.database || '')
  const databases = sourceDatabases.length > 0 ? Array.from(new Set([...sourceDatabases, ...Object.keys(schema)])) : Object.keys(schema)
  const tables = selectedDatabase ? Object.keys(schema[selectedDatabase] || {}) : []
  const fields = selectedDatabase && selectedTable ? schema[selectedDatabase]?.[selectedTable] || [] : []
  const sourceOptions = sources.map(source => ({ value: source.id, label: `${source.name}（${source.type}${source.enabled ? '' : '，已停止'}）` }))
  const databaseOptions = databases.map(database => ({ value: database, label: database }))
  const tableOptions = tables.map(table => ({ value: table, label: table }))
  const fieldOptions = fields.map(field => ({ value: field, label: field }))
  const conditionOptions = (ruleMode === 'custom' ? ['HTTP状态正常', '状态码等于', '页面包含', 'TCP端口存活'] : ['当天有数据', '大于', '大于等于', '等于', '小于', '包含', '不为空']).map(condition => ({ value: condition, label: condition }))
  const probeTypeOptions = [{ value: 'http', label: 'HTTP 页面' }, { value: 'tcp', label: 'TCP 端口' }]
  const alertingCount = rules.filter(rule => rule.lastRun.startsWith('告警') || rule.lastRun.startsWith('执行失败')).length
  const waitingCount = rules.filter(rule => rule.lastRun.startsWith('等待')).length
  const normalCount = rules.filter(rule => rule.lastRun.startsWith('正常')).length
  const enabledCount = rules.filter(rule => rule.status === '启用').length
  const saveRule = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const selectedSource = sources.find(source => source.id === selectedSourceId || source.name === selectedSourceId)
    if (ruleStatus === '启用' && selectedSource && !selectedSource.enabled) {
      setMessage('该数据源已停止，请先启用数据源后再启用告警规则')
      return
    }
    setSaving(true)
    const form = new FormData(event.currentTarget)
    const payload: Rule = {
      id: editingRule?.id || '',
      name: String(form.get('name') || ''),
      source: ruleMode === 'custom' ? 'custom-probe' : selectedSourceId,
      database: ruleMode === 'custom' ? probeType : selectedDatabase,
      table: ruleMode === 'custom' ? String(form.get('probeTarget') || '') : selectedTable,
      field: ruleMode === 'custom' ? probeField(ruleCondition) : selectedField,
      condition: ruleCondition,
      threshold: String(form.get('threshold') || ''),
      timeWindow: ruleMode === 'custom' ? String(form.get('timeWindow') || '5s') : String(form.get('timeWindow') || '5分钟'),
      lastRun: editingRule?.lastRun || '待执行',
      status: ruleStatus,
    }
    try {
      const response = await fetch(editingRule ? `${api}/collection-rules/${editingRule.id}` : `${api}/collection-rules`, { method: editingRule ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const saved = await response.json()
      if (!response.ok) throw new Error(saved.error || '保存失败')
      setMessage(`${saved.name} ${editingRule ? '已保存' : '已添加'}`)
      closeRuleModal()
      void loadRules()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '保存失败')
      setSaving(false)
    }
  }
  const deleteRule = async (rule: Rule) => {
    try {
      const response = await fetch(`${api}/collection-rules/${rule.id}`, { method: 'DELETE' })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '删除失败')
      setRules(current => current.filter(item => item.id !== rule.id))
      setDeleteRuleTarget(null)
      setMessage(`${rule.name} 已删除`)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '删除失败')
    }
  }
  const toggleRuleStatus = async (rule: Rule, checked: boolean) => {
    const nextStatus = checked ? '启用' : '停用'
    const source = sources.find(item => item.id === rule.source || item.name === rule.source)
    if (nextStatus === '启用' && source && !source.enabled) {
      setEnableSourceRuleTarget({ rule, source })
      return
    }
    setStatusSavingId(rule.id)
    setRules(current => current.map(item => item.id === rule.id ? { ...item, status: nextStatus } : item))
    try {
      const response = await fetch(`${api}/collection-rules/${rule.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...rule, status: nextStatus }),
      })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '状态更新失败')
      setRules(current => current.map(item => item.id === rule.id ? result : item))
      setMessage(`${rule.name} 已${nextStatus}`)
    } catch (error) {
      setRules(current => current.map(item => item.id === rule.id ? rule : item))
      setMessage(error instanceof Error ? error.message : '状态更新失败')
    } finally {
      setStatusSavingId('')
    }
  }
  const enableSourceAndRule = async () => {
    if (!enableSourceRuleTarget) return
    const { rule, source } = enableSourceRuleTarget
    setStatusSavingId(rule.id)
    try {
      const sourceResponse = await fetch(`${api}/data-sources/${source.id}/enabled`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: true }),
      })
      const updatedSource = await sourceResponse.json()
      if (!sourceResponse.ok) throw new Error(updatedSource.error || '数据源启用失败')
      setSources(current => current.map(item => item.id === source.id ? { ...item, ...updatedSource } : item))
      const ruleResponse = await fetch(`${api}/collection-rules/${rule.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...rule, status: '启用' }),
      })
      const updatedRule = await ruleResponse.json()
      if (!ruleResponse.ok) throw new Error(updatedRule.error || '告警规则启用失败')
      setRules(current => current.map(item => item.id === rule.id ? updatedRule : item))
      setEnableSourceRuleTarget(null)
      setMessage(`${source.name} 已启用，${rule.name} 已启用`)
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '启用失败')
    } finally {
      setStatusSavingId('')
    }
  }
  return (
    <div className="page">
      <PageHead title="告警规则" description="支持数据源指标规则和自定义 HTTP / TCP 探测规则。" action="新建规则" onAction={() => openRuleModal()} />
      <section className="alert-summary-grid">
        <div className="surface alert-summary-card danger"><span>当前告警</span><b>{alertingCount}</b><small>告警或执行失败</small></div>
        <div className="surface alert-summary-card pending"><span>等待数据</span><b>{waitingCount}</b><small>未到截止时间</small></div>
        <div className="surface alert-summary-card success"><span>正常规则</span><b>{normalCount}</b><small>最近一次通过</small></div>
        <div className="surface alert-summary-card"><span>启用规则</span><b>{enabledCount}</b><small>共 {rules.length} 条规则</small></div>
      </section>
      <section className="surface rules">
        {rules.length === 0 ? (
          <div className="empty-state alert-empty-state"><b>暂无告警规则</b><span>点击右上角新建规则，选择数据源后会自动加载库表字段。</span></div>
        ) : rules.map(r => {
          const customRule = r.source === 'custom-probe'
          const sourceName = customRule ? '自定义探测' : sources.find(source => source.id === r.source || source.name === r.source)?.name || r.source
          const ruleTarget = customRule ? `${r.database.toUpperCase()} · ${r.table} · ${r.condition}${r.threshold ? ` ${r.threshold}` : ''}` : `${r.database || '-'}.${r.table || '-'}.${r.field || '-'} · ${r.condition}${r.threshold ? ` ${r.threshold}` : ''}`
          const resultKind = r.lastRun.startsWith('告警') || r.lastRun.startsWith('执行失败') ? 'danger' : r.lastRun.startsWith('正常') ? 'success' : r.lastRun.startsWith('等待') ? 'pending' : 'idle'
          return (
            <div className="rule-row alert-rule-row" key={r.id}>
              <i className="rule-icon">⌁</i>
              <div>
                <b>{r.name}</b>
                <span>{sourceName} · {ruleTarget} · {r.timeWindow}</span>
              </div>
              <span className={`alert-result ${resultKind}`}>{r.lastRun || '待执行'}</span>
              <div className="rule-status-cell">
                <StatusSwitch checked={r.status === '启用'} disabled={statusSavingId === r.id} onChange={(checked) => void toggleRuleStatus(r, checked)} />
              </div>
              <div className="rule-actions">
                <button className="text-button" type="button" onClick={() => openRuleModal(r)}>编辑 <Icon name="arrow" /></button>
                <button className="text-button danger" type="button" onClick={() => setDeleteRuleTarget(r)}>删除</button>
              </div>
            </div>
          )
        })}
      </section>
      {modalOpen && (
        <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) closeRuleModal() }}>
          <section className="surface source-modal alert-rule-modal" role="dialog" aria-modal="true" aria-labelledby="rule-modal-title" onClick={(event) => event.stopPropagation()}>
            <header className="modal-head">
              <div><h2 id="rule-modal-title">{editingRule ? '编辑告警规则' : '新建告警规则'}</h2></div>
              <button className="close-button" type="button" aria-label="关闭" onClick={(event) => { if (event.target === event.currentTarget) closeRuleModal() }}>×</button>
            </header>
            <form onSubmit={saveRule}>
              <div className="modal-form">
                <label>规则名称 <span className="required-mark">*</span><input name="name" defaultValue={editingRule?.name || ''} required /></label>
                <SelectField label="规则类型" value={ruleMode} options={[{ value: 'source', label: '数据源指标' }, { value: 'custom', label: '自定义探测' }]} placeholder="请选择规则类型" onChange={(mode) => { const next = mode === 'custom' ? 'custom' : 'source'; setRuleMode(next); setRuleCondition(next === 'custom' ? 'HTTP状态正常' : '大于') }} />
                {ruleMode === 'source' && <><SelectField label="数据源" required value={selectedSourceId} options={sourceOptions} placeholder="请选择数据源" onChange={(sourceId) => { setSelectedSourceId(sourceId); void loadSchema(sourceId) }} /><SelectField label="数据库" required value={selectedDatabase} options={databaseOptions} placeholder={schemaLoading ? '加载中...' : '请选择数据库'} disabled={!selectedSourceId || schemaLoading || databases.length === 0} onChange={(database) => void loadTablesForDatabase(database)} /><SelectField label="表名" required value={selectedTable} options={tableOptions} placeholder={schemaLoading ? '加载中...' : '请选择表'} disabled={!selectedDatabase || schemaLoading || tables.length === 0} onChange={(table) => void loadFieldsForTable(table)} /><SelectField label="字段 / 指标" required value={selectedField} options={fieldOptions} placeholder={schemaLoading ? '加载中...' : '请选择字段'} disabled={!selectedTable || schemaLoading || fields.length === 0} onChange={(field) => setSelectedField(field)} /></>}
                {ruleMode === 'custom' && <><SelectField label="探测类型" value={probeType} options={probeTypeOptions} placeholder="请选择探测类型" onChange={(type) => { setProbeType(type); setRuleCondition(type === 'tcp' ? 'TCP端口存活' : 'HTTP状态正常') }} /><label>目标地址 <span className="required-mark">*</span><input name="probeTarget" defaultValue={editingRule?.source === 'custom-probe' ? editingRule.table : ''} placeholder={probeType === 'tcp' ? '例如 127.0.0.1:80' : '例如 https://example.com/health'} required={ruleMode === 'custom'} /></label></>}
                <SelectField label="条件" value={ruleCondition} options={conditionOptions} placeholder="请选择条件" onChange={(condition) => setRuleCondition(condition)} />
                <label>{ruleMode === 'custom' && ruleCondition === '页面包含' ? '期望内容' : ruleCondition === '状态码等于' ? '期望状态码' : '阈值'}<input name="threshold" defaultValue={editingRule?.threshold || ''} placeholder={ruleMode === 'custom' ? (ruleCondition === '页面包含' ? '例如 OK' : ruleCondition === '状态码等于' ? '例如 200' : '无需填写') : '例如 10 或 80%'} disabled={ruleCondition === '当天有数据' || ruleCondition === 'HTTP状态正常' || ruleCondition === 'TCP端口存活'} /></label>
                <label>{ruleCondition === '当天有数据' ? '截止时间' : ruleMode === 'custom' ? '超时时间' : '时间窗口'}<input key={`${ruleMode}-${ruleCondition}`} name="timeWindow" defaultValue={editingRule?.timeWindow || (ruleCondition === '当天有数据' ? '03:00' : ruleMode === 'custom' ? '5s' : '5分钟')} placeholder={ruleCondition === '当天有数据' ? '例如 03:00' : ruleMode === 'custom' ? '例如 5s，最长 30s' : '例如 5分钟'} /></label>
                {editingRule && <div className="field-block status-field"><span className="field-label">状态</span><StatusSwitch checked={ruleStatus === '启用'} onChange={(checked) => setRuleStatus(checked ? '启用' : '停用')} /></div>}
              </div>
              {ruleMode === 'source' && selectedSourceId && !schemaLoading && databases.length === 0 && <div className="form-hint">当前数据源没有可用库表字段，或账号没有读取 information_schema 权限。</div>}
              {ruleMode === 'custom' && <div className="form-hint">自定义探测会每分钟执行一次，异常写入通知中心，恢复后自动标记恢复。</div>}
              <footer className="modal-actions">
                <button className="button secondary" type="button" onClick={(event) => { if (event.target === event.currentTarget) closeRuleModal() }}>取消</button>
                <button className="button" type="submit" disabled={saving || (ruleMode === 'source' && (!selectedSourceId || !selectedDatabase || !selectedTable || !selectedField))}>{saving ? '保存中...' : '保存'}</button>
              </footer>
            </form>
          </section>
        </div>
      )}
      {enableSourceRuleTarget && (
        <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setEnableSourceRuleTarget(null) }}>
          <section className="surface confirm-modal" role="dialog" aria-modal="true" aria-labelledby="enable-source-rule-title" onClick={(event) => event.stopPropagation()}>
            <header className="modal-head">
              <div><h2 id="enable-source-rule-title">需要启用数据源</h2></div>
              <button className="close-button" type="button" aria-label="关闭" onClick={() => setEnableSourceRuleTarget(null)}>×</button>
            </header>
            <p>告警规则“{enableSourceRuleTarget.rule.name}”关联的数据源“{enableSourceRuleTarget.source.name}”已停止。要开启数据源才可以启用该告警规则，是否现在开启？</p>
            <footer className="modal-actions">
              <button className="button secondary" type="button" onClick={() => setEnableSourceRuleTarget(null)}>取消</button>
              <button className="button" type="button" onClick={() => void enableSourceAndRule()}>确认开启</button>
            </footer>
          </section>
        </div>
      )}
      {deleteRuleTarget && (
        <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setDeleteRuleTarget(null) }}>
          <section className="surface confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-rule-title" onClick={(event) => event.stopPropagation()}>
            <header className="modal-head">
              <div><h2 id="delete-rule-title">确认删除告警规则</h2></div>
              <button className="close-button" type="button" aria-label="关闭" onClick={(event) => { if (event.target === event.currentTarget) setDeleteRuleTarget(null) }}>×</button>
            </header>
            <p>确认删除“{deleteRuleTarget.name}”吗？删除后该规则不会继续参与告警判断。</p>
            <footer className="modal-actions">
              <button className="button secondary" type="button" onClick={(event) => { if (event.target === event.currentTarget) setDeleteRuleTarget(null) }}>取消</button>
              <button className="button danger-button" type="button" onClick={() => void deleteRule(deleteRuleTarget)}>确认</button>
            </footer>
          </section>
        </div>
      )}
      {message && <div className="toast">{message}</div>}
    </div>
  )
}
function Settings() {
  const [saving, setSaving] = useState(false)
  const [monitorSaving, setMonitorSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [activeTab, setActiveTab] = useState<'external' | 'profile'>('external')
  const [monitorConfig, setMonitorConfig] = useState<ExternalMonitorConfig | null>(null)
  const [prometheusUrl, setPrometheusUrl] = useState('')
  const [grafanaUrl, setGrafanaUrl] = useState('')
  const loadMonitorConfig = async () => {
    try {
      const response = await fetch(`${api}/external-monitor/config`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '外部监控配置获取失败')
      setMonitorConfig(data)
      setPrometheusUrl(data.prometheusUrl || '')
      setGrafanaUrl(data.grafanaUrl || '')
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '外部监控配置获取失败')
    }
  }
  useEffect(() => { void loadMonitorConfig() }, [])
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
  const saveExternalMonitor = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setMonitorSaving(true)
    setMessage('')
    const form = new FormData(event.currentTarget)
    try {
      const response = await fetch(`${api}/external-monitor/config`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          prometheusUrl,
          prometheusToken: form.get('prometheusToken'),
          grafanaUrl,
          grafanaToken: form.get('grafanaToken'),
        }),
      })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存失败')
      setMonitorConfig(data)
      setPrometheusUrl(data.prometheusUrl || '')
      setGrafanaUrl(data.grafanaUrl || '')
      event.currentTarget.reset()
      setMessage('外部监控配置已保存')
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '保存失败')
    } finally {
      setMonitorSaving(false)
    }
  }
  return <div className="page"><PageHead title="系统配置" description="维护平台接入配置与个人账号安全。" /><section className="settings-layout"><nav className="settings-tabs" aria-label="系统设置分类"><button type="button" className={activeTab === 'external' ? 'active' : ''} onClick={() => setActiveTab('external')}>外部监控</button><button type="button" className={activeTab === 'profile' ? 'active' : ''} onClick={() => setActiveTab('profile')}>个人信息</button></nav><section className="surface settings">{activeTab === 'external' ? <div className="form-section"><h3>外部监控</h3><form className="settings-form" onSubmit={saveExternalMonitor}><label>Prometheus 地址<input value={prometheusUrl} onChange={(event) => setPrometheusUrl(event.target.value)} placeholder="例如 http://prometheus:9090" /></label><label>Prometheus Token<input name="prometheusToken" type="password" placeholder={monitorConfig?.prometheusTokenConfigured ? '已配置，留空则不修改' : '可选'} autoComplete="off" /></label><label>Grafana 地址<input value={grafanaUrl} onChange={(event) => setGrafanaUrl(event.target.value)} placeholder="例如 http://grafana:3000" /></label><label>Grafana Token<input name="grafanaToken" type="password" placeholder={monitorConfig?.grafanaTokenConfigured ? '已配置，留空则不修改' : '可选'} autoComplete="off" /></label><div className="settings-actions"><button className="button" type="submit" disabled={monitorSaving}>{monitorSaving ? '保存中...' : '保存配置'}</button><button className="button secondary" type="button" onClick={() => void loadMonitorConfig()}>重新读取</button>{message && <span>{message}</span>}</div></form><div className="settings-hint">保存后，“外部监控”菜单会通过后端读取 Prometheus 告警、指标和 Grafana 面板。Token 不会回显到前端。</div></div> : <div className="form-section"><h3>个人信息</h3><form className="settings-form" onSubmit={changePassword}><label>原密码 <span className="required-mark">*</span><input name="oldPassword" type="password" autoComplete="current-password" required /></label><label>新密码 <span className="required-mark">*</span><input name="newPassword" type="password" autoComplete="new-password" required /></label><label>确认新密码 <span className="required-mark">*</span><input name="confirmPassword" type="password" autoComplete="new-password" required /></label><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存密码'}</button>{message && <span>{message}</span>}</div></form></div>}</section></section></div>
}
function Field({ label, value, name, required }: { label: string; value: string; name?: string; required?: boolean }) { return <label>{label}{required && <span className="required-mark"> *</span>}<input name={name} defaultValue={value} required={required} /></label> }
export default App
