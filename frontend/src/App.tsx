import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent, KeyboardEvent, ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import './App.css'

type Source = { id: string; name: string; type: string; host: string; port: string; enabled: boolean; status: string; lastTest: string; username?: string; password?: string; database?: string; remark?: string; options?: Record<string, string> }
type PrometheusRule = { name: string; type: string; query: string; duration: number; frequency?: number; health: string; state?: string; severity?: string; summary?: string; description?: string; group: string; file?: string; labels?: Record<string, string>; annotations?: Record<string, string>; sourceId?: string; sourceName?: string }
type NotificationItem = { id: string; ruleName: string; source?: string; database?: string; status: string; message: string; unread: boolean; muted?: boolean; firstSeenAt: string; lastSeenAt: string }
type CollectionRule = { id: string; name: string; source: string; database: string; table: string; field: string; condition: string; threshold?: string; timeWindow: string; frequency?: string; remark?: string; lastRun: string; resultDetails?: string; status: string }
type DashboardItem = { id: string; name: string; sourceId: string; sourceName: string; sourceType: string; createdAt: string }
type HadoopMenuItem = { sourceId: string }
type MySQLSQLSample = { schemaName?: string; digest?: string; queryText: string; count: number; totalLatencyMs: number; averageLatencyMs: number; maxLatencyMs: number; rowsExamined: number; rowsSent: number; firstSeen?: string; lastSeen?: string }
type PrometheusMetric = { name: string }
type MetricCategory = { id: string; label: string; matches: (name: string) => boolean }
type SelectOption = { value: string; label: string; disabled?: boolean }

function DataSourceDeleteDialog({ sourceName, onClose, onConfirm }: { sourceName: string; onClose: () => void; onConfirm: () => void }) {
  return <div className="modal-backdrop source-delete-backdrop" role="presentation" onClick={onClose}><section className="surface source-delete-modal" role="dialog" aria-modal="true" aria-labelledby="source-delete-title" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="source-delete-title">删除数据源</h2><p>{sourceName}</p></div><button className="close-button" type="button" aria-label="关闭" onClick={onClose}>×</button></header><div className="source-delete-content"><b>删除后无法恢复</b><span>该数据源配置、关联告警规则和导入的大屏将一并删除。</span></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={onClose}>取消</button><button className="button danger-button" type="button" onClick={onConfirm}>确认删除</button></footer></section></div>
}

const api = '/api'
const refreshIntervalStorageKey = 'opsguard_refresh_interval'
const refreshIntervalEvent = 'opsguard-refresh-interval-change'

function readRefreshInterval() {
  try {
    const saved = JSON.parse(localStorage.getItem(refreshIntervalStorageKey) || '{}')
    const value = Math.max(1, Number(saved.value) || 15)
    const unit = ['s', 'm', 'h'].includes(saved.unit) ? saved.unit : 's'
    return { value, unit, milliseconds: value * (unit === 'h' ? 3600000 : unit === 'm' ? 60000 : 1000) }
  } catch { return { value: 15, unit: 's', milliseconds: 15000 } }
}

function useRefreshInterval() {
  const [interval, setIntervalValue] = useState(() => readRefreshInterval().milliseconds)
  useEffect(() => {
    const update = () => setIntervalValue(readRefreshInterval().milliseconds)
    window.addEventListener(refreshIntervalEvent, update)
    return () => window.removeEventListener(refreshIntervalEvent, update)
  }, [])
  return interval
}
const icons: Record<string, string> = { query: '◎', dashboard: '▦', data: '◫', alert: '◇', notify: '◉', settings: '⚙', plus: '+', arrow: '→', bell: '' }
const metricCategories: MetricCategory[] = [
  { id: 'mysql', label: 'MySQL', matches: name => name.includes('mysql') },
  { id: 'node', label: '主机', matches: name => name.startsWith('node_') },
  { id: 'prometheus', label: 'Prometheus', matches: name => name.startsWith('prometheus_') },
  { id: 'scrape', label: '采集', matches: name => name.startsWith('scrape_') || name === 'up' },
]
function Icon({ name }: { name: string }) { return <span className={`icon icon-${name}`} aria-hidden="true">{icons[name]}</span> }

function isVisibleMetric(name: string) {
  return !name.startsWith('go_') && !name.startsWith('process_')
}

function metricCategory(name: string) {
  return metricCategories.find(category => category.matches(name))?.id || 'other'
}

function metricCategoryLabel(name: string) {
  return metricCategories.find(category => category.id === metricCategory(name))?.label || '其他'
}

function frequencyParts(value?: string) {
  const matched = String(value || '1分钟').trim().match(/^(\d+(?:\.\d+)?)\s*(s|秒|m|分钟|分|h|小时|时)$/i)
  if (!matched) return { value: '1', unit: 'm' }
  return { value: matched[1], unit: /^(s|秒)$/i.test(matched[2]) ? 's' : /^(h|小时|时)$/i.test(matched[2]) ? 'h' : 'm' }
}

function notificationDuration(start?: string, end?: string) {
  const startAt = new Date(start || '').getTime()
  const endAt = end ? new Date(end).getTime() : Date.now()
  if (!Number.isFinite(startAt) || !Number.isFinite(endAt) || endAt < startAt) return '-'
  const seconds = Math.floor((endAt - startAt) / 1000)
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainSeconds = seconds % 60
  if (hours > 0) return `${hours}小时${minutes}分钟`
  if (minutes > 0) return `${minutes}分钟${remainSeconds}秒`
  return `${remainSeconds}秒`
}

function timeoutSeconds(value?: string) {
  const matched = String(value || '').match(/\d+(?:\.\d+)?/)
  return matched?.[0] || '5'
}

function App() {
  const [authed, setAuthed] = useState(() => localStorage.getItem('opsguard_token') === 'opsguard-admin')
  const logout = async () => {
    await fetch(`${api}/logout`, { method: 'POST' }).catch(() => {})
    localStorage.removeItem('opsguard_token')
    setAuthed(false)
  }
  if (!authed) return <Login onLogin={() => setAuthed(true)} />
  return <BrowserRouter><div className="app-shell"><Sidebar /><main className="workspace"><TopNav onLogout={logout} /><Routes><Route path="/" element={<Dashboards />} /><Route path="/metrics" element={<MetricQuery />} /><Route path="/hadoop" element={<HadoopYarn />} /><Route path="/datasources" element={<DataSources />} /><Route path="/alerts" element={<Alerts />} /><Route path="/notifications" element={<Notifications />} /><Route path="/config" element={<Settings />} /></Routes></main></div></BrowserRouter>
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
  return <div className="login-page"><section className="surface login-panel"><div className="login-brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><span>Prometheus 指标查询平台</span></div></div><h1>登录平台</h1><p>请输入管理员账号继续。</p><form onSubmit={submit}><label>用户名<input name="username" defaultValue="admin" autoComplete="username" required /></label><label>密码<input name="password" type="password" autoComplete="current-password" required /></label>{error && <span className="login-error">{error}</span>}<button className="button" type="submit" disabled={loading}>{loading ? '登录中...' : '登录'}</button></form></section></div>
}

function TopNav({ onLogout }: { onLogout: () => void }) {
  const location = useLocation()
  const navigate = useNavigate()
  const titles: Record<string, string> = { '/': '监控大屏', '/metrics': '指标查询', '/hadoop': 'Hadoop / YARN', '/datasources': '数据节点', '/alerts': '告警规则', '/notifications': '通知中心', '/config': '系统设置' }
  const [unread, setUnread] = useState(0)
  const [notificationOpen, setNotificationOpen] = useState(false)
  const [notifications, setNotifications] = useState<NotificationItem[]>([])
  const [notificationsLoading, setNotificationsLoading] = useState(false)
  const refreshInterval = useRefreshInterval()
  const notificationRoot = useRef<HTMLDivElement>(null)
  const loadUnread = async () => {
    try {
      const response = await fetch(`${api}/notifications?unread=1&limit=1`)
      const data = await response.json()
      setUnread(Number(data.unread || 0))
    } catch { setUnread(0) }
  }
  const loadNotifications = async () => {
    setNotificationsLoading(true)
    try {
      const response = await fetch(`${api}/notifications?unread=1&limit=8`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '通知加载失败')
      setNotifications(Array.isArray(data.notifications) ? data.notifications : [])
      setUnread(Number(data.unread || 0))
    } catch { setNotifications([]) } finally { setNotificationsLoading(false) }
  }
  const toggleNotifications = () => {
    setNotificationOpen(current => {
      if (!current) void loadNotifications()
      return !current
    })
  }
  const markAllRead = async () => {
    try {
      const response = await fetch(`${api}/notifications/read`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({}) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '操作失败')
      setUnread(Number(data.unread || 0))
      setNotifications([])
      window.dispatchEvent(new Event('opsguard-notifications-change'))
    } catch { /* Keep the unread list visible when the request fails. */ }
  }
  useEffect(() => { void loadUnread(); const timer = window.setInterval(loadUnread, refreshInterval); return () => window.clearInterval(timer) }, [refreshInterval])
  useEffect(() => {
    if (!notificationOpen) return
    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!notificationRoot.current?.contains(event.target as Node)) setNotificationOpen(false)
    }
    document.addEventListener('pointerdown', closeOnOutsideClick)
    return () => document.removeEventListener('pointerdown', closeOnOutsideClick)
  }, [notificationOpen])
  return <header className="page-nav"><div className="nav-path"><b>{titles[location.pathname] || '指标查询'}</b></div><div className="nav-tools">{location.pathname !== '/config' && <RefreshButton onClick={() => window.location.reload()} />}<div ref={notificationRoot} className="notification-menu"><button className={`bell-button ${notificationOpen ? 'active' : ''}`} type="button" aria-label="通知" aria-expanded={notificationOpen} onClick={toggleNotifications}><Icon name="bell" />{unread > 0 && <i>{unread > 99 ? '99+' : unread}</i>}</button>{notificationOpen && <section className="surface notification-popover"><header><b>未读消息</b><span>{unread > 0 ? `${unread} 条未读` : '已全部阅读'}</span></header><div className="notification-popover-list">{notificationsLoading ? <span className="notification-popover-empty">正在加载...</span> : notifications.length === 0 ? <span className="notification-popover-empty">暂无未读消息</span> : notifications.map(item => <button type="button" key={item.id} onClick={() => { setNotificationOpen(false); navigate('/notifications') }}><i className={`notification-dot ${item.status === 'resolved' ? 'success' : 'danger'}`} /><span><b>{item.ruleName}</b><small>{item.message}</small><time>{formatCollectedAt(item.lastSeenAt)}</time></span></button>)}</div><footer><button type="button" disabled={unread === 0 || notificationsLoading} onClick={() => void markAllRead()}>全部已读</button></footer></section>}</div><AccountMenu onLogout={onLogout} /></div></header>
}

function AccountMenu({ onLogout }: { onLogout: () => void }) {
  const [open, setOpen] = useState(false)
  return <div className="account-menu"><button className="user-avatar" type="button" onClick={() => setOpen(current => !current)}><span>管</span></button>{open && <div className="surface account-popover"><button type="button" onClick={() => setOpen(false)}>平台管理员</button><button className="danger" type="button" onClick={onLogout}>退出登录</button></div>}</div>
}

function Sidebar() {
  const location = useLocation()
  const [sources, setSources] = useState<Source[]>([])
  const [hadoopMenus, setHadoopMenus] = useState<HadoopMenuItem[]>(hadoopMenusFromStorage)
  const refreshInterval = useRefreshInterval()
  const load = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources : [])
    } catch { setSources([]) }
  }
  useEffect(() => {
    const reloadHadoopMenus = () => setHadoopMenus(hadoopMenusFromStorage())
    void load()
    const timer = window.setInterval(load, refreshInterval)
    window.addEventListener('opsguard-data-sources-change', load)
    window.addEventListener('opsguard-hadoop-menus-change', reloadHadoopMenus)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('opsguard-data-sources-change', load)
      window.removeEventListener('opsguard-hadoop-menus-change', reloadHadoopMenus)
    }
  }, [refreshInterval])
  const online = sources.filter(item => item.enabled && item.status === '健康').length
  const importedHadoopSources = hadoopMenus.map(item => sources.find(source => source.id === item.sourceId && source.type === 'Hadoop' && source.enabled)).filter((source): source is Source => Boolean(source))
  const items = [['dashboard', '监控大屏', '/'], ['query', '指标查询', '/metrics'], ...importedHadoopSources.map(source => ['hadoop', source.name, `/hadoop?source=${encodeURIComponent(source.id)}`]), ['alert', '告警规则', '/alerts'], ['notify', '通知中心', '/notifications'], ['data', '数据节点', '/datasources'], ['settings', '系统设置', '/config']]
  const selectedHadoopSourceID = new URLSearchParams(location.search).get('source') || ''
  return <aside className="sidebar"><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><small>巡检平台</small></div></div><nav>{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} className={({ isActive }) => {
    const hadoopSourceID = path.startsWith('/hadoop?source=') ? new URLSearchParams(path.slice(path.indexOf('?') + 1)).get('source') : ''
    const active = hadoopSourceID ? location.pathname === '/hadoop' && selectedHadoopSourceID === hadoopSourceID : isActive
    return `nav-link ${active ? 'active' : ''}`
  }}>{icon === 'hadoop' ? <img className="hadoop-nav-icon" src="/hadoop.svg" alt="" aria-hidden="true" /> : <Icon name={icon} />}<span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><span className="online-dot" /><span>{online} / {sources.length} 数据源在线</span></div></aside>
}

function PageHead({ action, onAction, actionNode }: { title: string; description: string; action?: string; onAction?: () => void; actionNode?: any }) {
  if (!action && !actionNode) return null
  return <header className="page-head compact"><span />{actionNode || <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header>
}

function AppSelect({ value, options, onChange, name, placeholder = '请选择', disabled = false, className = '' }: { value: string; options: SelectOption[]; onChange: (value: string) => void; name?: string; placeholder?: string; disabled?: boolean; className?: string }) {
  const [open, setOpen] = useState(false)
  const [openUpward, setOpenUpward] = useState(false)
  const root = useRef<HTMLSpanElement>(null)
  const selected = options.find(option => option.value === value)
  useEffect(() => {
    if (!open) return
    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', closeOnOutsideClick)
    document.addEventListener('keydown', closeOnEscape)
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsideClick)
      document.removeEventListener('keydown', closeOnEscape)
    }
  }, [open])
  const toggle = () => {
    if (!open && root.current) setOpenUpward(window.innerHeight - root.current.getBoundingClientRect().bottom < 270)
    setOpen(current => !current)
  }
  return <span ref={root} className={`app-select ${open ? 'open' : ''} ${openUpward ? 'open-upward' : ''} ${disabled ? 'disabled' : ''} ${className}`}>
    {name && <input type="hidden" name={name} value={value} />}
    <button className="app-select-trigger" type="button" disabled={disabled} aria-haspopup="listbox" aria-expanded={open} onClick={toggle}><span className={selected ? '' : 'placeholder'}>{selected?.label || placeholder}</span></button>
    {open && <span className="app-select-menu" role="listbox">{options.map(option => <button className={option.value === value ? 'active' : ''} type="button" role="option" aria-selected={option.value === value} disabled={option.disabled} key={option.value} onClick={() => { onChange(option.value); setOpen(false) }}>{option.label}</button>)}</span>}
  </span>
}

function RefreshButton({ loading, disabled, onClick }: { loading?: boolean; disabled?: boolean; onClick: () => void }) {
  return <button className="refresh-button" type="button" onClick={onClick} disabled={disabled}>{loading ? '刷新中...' : '刷新'}</button>
}

function ListPagination({ total, page, onPageChange }: { total: number; page: number; onPageChange: (page: number) => void }) {
  const pageSize = 20
  const pages = Math.max(1, Math.ceil(total / pageSize))
  const current = Math.min(page, pages)
  if (total === 0) return null
  return <footer className="list-pagination"><span>共 {total} 条，第 {current} / {pages} 页</span><div><button type="button" disabled={current === 1} onClick={() => onPageChange(1)}>首页</button><button type="button" disabled={current === 1} onClick={() => onPageChange(Math.max(1, current - 1))}>上一页</button><input type="number" min="1" max={pages} value={current} aria-label="页码" onChange={(event) => { const next = Number(event.target.value); if (Number.isInteger(next) && next >= 1 && next <= pages) onPageChange(next) }} /><span>/ {pages}</span><button type="button" disabled={current === pages} onClick={() => onPageChange(Math.min(pages, current + 1))}>下一页</button><button type="button" disabled={current === pages} onClick={() => onPageChange(pages)}>末页</button></div></footer>
}

function dashboardsFromStorage(): DashboardItem[] {
  try { return JSON.parse(localStorage.getItem('opsguard_dashboards') || '[]') } catch { return [] }
}

function saveDashboards(items: DashboardItem[]) {
  localStorage.setItem('opsguard_dashboards', JSON.stringify(items))
  window.dispatchEvent(new Event('opsguard-dashboards-change'))
}

function hadoopMenusFromStorage(): HadoopMenuItem[] {
  try {
    const stored = JSON.parse(localStorage.getItem('opsguard_hadoop_menus') || '[]')
    return Array.isArray(stored) ? stored.filter((item): item is HadoopMenuItem => typeof item?.sourceId === 'string' && item.sourceId.length > 0) : []
  } catch { return [] }
}

function saveHadoopMenus(items: HadoopMenuItem[]) {
  localStorage.setItem('opsguard_hadoop_menus', JSON.stringify(items))
  window.dispatchEvent(new Event('opsguard-hadoop-menus-change'))
}

function Dashboards() {
  const [items, setItems] = useState<DashboardItem[]>(dashboardsFromStorage)
  const [values, setValues] = useState<Record<string, Record<string, string>>>({})
  const [sources, setSources] = useState<Source[]>([])
  const [customRuleCount, setCustomRuleCount] = useState(0)
  const [prometheusRuleCount, setPrometheusRuleCount] = useState(0)
  const [notifications, setNotifications] = useState<NotificationItem[]>([])
  const [sqlSource, setSQLSource] = useState<DashboardItem | null>(null)
  const [sqlItems, setSQLItems] = useState<MySQLSQLSample[]>([])
  const [sqlMode, setSQLMode] = useState('')
  const [sqlPage, setSQLPage] = useState(1)
  const [sqlLoading, setSQLLoading] = useState(false)
  const [sqlError, setSQLError] = useState('')
  const refreshInterval = useRefreshInterval()
  const dashboards = items.filter(item => item.sourceType === 'MySQL' || item.sourceType === 'SSH')
  const refreshOverview = async () => {
    try {
      const sourceResponse = await fetch(`${api}/data-sources`)
      const sourceData = await sourceResponse.json()
      const nextSources: Source[] = Array.isArray(sourceData.dataSources) ? sourceData.dataSources : []
      setSources(nextSources)
      const [customResult, notificationsResult, ...prometheusResults] = await Promise.allSettled([
        fetch(`${api}/collection-rules`).then(response => response.json()),
        fetch(`${api}/notifications?limit=200`).then(response => response.json()),
        ...nextSources.filter(source => source.enabled && source.type === 'Prometheus').map(source => fetch(`${api}/prometheus/${source.id}/rules`).then(response => response.json())),
      ])
      setCustomRuleCount(customResult.status === 'fulfilled' && Array.isArray(customResult.value.rules) ? customResult.value.rules.length : 0)
      setNotifications(notificationsResult.status === 'fulfilled' && Array.isArray(notificationsResult.value.notifications) ? notificationsResult.value.notifications : [])
      setPrometheusRuleCount(prometheusResults.reduce((count, result) => count + (result.status === 'fulfilled' && Array.isArray(result.value.rules) ? result.value.rules.length : 0), 0))
    } catch { setSources([]); setCustomRuleCount(0); setPrometheusRuleCount(0); setNotifications([]) }
  }
  const refresh = async () => {
    if (!dashboards.length) return
    const next: Record<string, Record<string, string>> = {}
    for (const item of dashboards) {
      try {
        const response = await fetch(`${api}/data-sources/${item.sourceId}/dashboard-metrics`)
        const data = await response.json()
        next[item.sourceId] = Object.fromEntries(Object.entries(data.metrics || {}).map(([key, value]) => [key, String(value)]))
      } catch { next[item.sourceId] = {} }
    }
    setValues(next)
  }
  useEffect(() => { const onChange = () => setItems(dashboardsFromStorage()); window.addEventListener('opsguard-dashboards-change', onChange); return () => window.removeEventListener('opsguard-dashboards-change', onChange) }, [])
  useEffect(() => { void refresh(); const timer = window.setInterval(refresh, refreshInterval); return () => window.clearInterval(timer) }, [items, refreshInterval])
  useEffect(() => { void refreshOverview(); const timer = window.setInterval(refreshOverview, refreshInterval); return () => window.clearInterval(timer) }, [refreshInterval])
  const remove = (id: string) => saveDashboards(items.filter(item => item.id !== id))
  const openSQL = async (item: DashboardItem) => {
    setSQLSource(item); setSQLItems([]); setSQLMode(''); setSQLPage(1); setSQLError(''); setSQLLoading(true)
    try {
      const response = await fetch(`${api}/data-sources/${item.sourceId}/dashboard-sql`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || 'SQL 明细加载失败')
      setSQLItems(Array.isArray(data.items) ? data.items : [])
      setSQLMode(String(data.mode || 'top'))
    } catch (err) { setSQLError(err instanceof Error ? err.message : 'SQL 明细加载失败') } finally { setSQLLoading(false) }
  }
  const pageItems = sqlItems.slice((sqlPage - 1) * 10, sqlPage * 10)
  const pages = Math.max(1, Math.ceil(sqlItems.length / 10))
  const enabledSources = sources.filter(source => source.enabled)
  const healthySources = enabledSources.filter(source => source.status === '健康')
  const abnormalSources = enabledSources.length - healthySources.length
  const activeNotifications = notifications.filter(item => item.status === 'active')
  const totalRules = customRuleCount + prometheusRuleCount
  const sourceHealthPercent = enabledSources.length ? Math.round(healthySources.length / enabledSources.length * 100) : 0
  const sourceHealthStyle = { background: `conic-gradient(#21a879 0 ${sourceHealthPercent}%, #edf1f6 ${sourceHealthPercent}% 100%)` }
  const trend = Array.from({ length: 7 }, (_, offset) => {
    const date = new Date(); date.setHours(0, 0, 0, 0); date.setDate(date.getDate() - (6 - offset))
    const next = new Date(date); next.setDate(date.getDate() + 1)
    const count = notifications.filter(item => { const value = new Date(item.firstSeenAt || item.lastSeenAt).getTime(); return value >= date.getTime() && value < next.getTime() && item.status === 'active' }).length
    return { label: `${date.getMonth() + 1}/${date.getDate()}`, count }
  })
  const trendMax = Math.max(1, ...trend.map(item => item.count))
  return <div className="page"><section className="overview-stat-grid"><article className="overview-stat alert"><span>当前告警</span><b>{activeNotifications.length}</b><small>未恢复的告警事件</small></article><article className="overview-stat rule"><span>告警规则</span><b>{totalRules}</b><small>自定义 {customRuleCount} · Prometheus {prometheusRuleCount}</small></article><article className="overview-stat source"><span>接入数据源</span><b>{sources.length}</b><small>启用 {enabledSources.length} 个</small></article><article className={`overview-stat health ${abnormalSources > 0 ? 'warning' : ''}`}><span>数据源状态</span><b>{abnormalSources > 0 ? `${abnormalSources} 异常` : '正常'}</b><small>健康 {healthySources.length} / 启用 {enabledSources.length}</small></article></section><section className="overview-grid"><article className="surface overview-trend"><header><div><h2>近 7 天告警趋势</h2><p>按首次告警时间统计</p></div><span>{activeNotifications.length} 条当前告警</span></header><div className="overview-bars">{trend.map(item => <div key={item.label}><b>{item.count || ''}</b><i style={{ height: `${Math.max(item.count ? 14 : 4, item.count / trendMax * 100)}%` }} /><span>{item.label}</span></div>)}</div></article><article className="surface overview-health"><header><div><h2>数据源健康度</h2><p>仅统计已启用数据源</p></div></header><div className="overview-ring" style={sourceHealthStyle}><div><b>{sourceHealthPercent}%</b><span>健康率</span></div></div><footer><span><i className="healthy-dot" />健康 {healthySources.length}</span><span><i className="abnormal-dot" />异常 {abnormalSources}</span></footer></article></section><section className="overview-section-title"><div><h2>已导入大屏</h2><p>MySQL 与 SSH 采集指标</p></div><span>{dashboards.length} 个数据源</span></section>{dashboards.length === 0 ? <section className="surface empty-state"><b>暂无已导入大屏</b><span>可在数据节点中为 MySQL 或 SSH 数据源点击“导入大屏”。</span></section> : <section className="dashboard-list">{dashboards.map(item => { const v = values[item.sourceId] || {}; const cards: Array<[string, string]> = item.sourceType === 'SSH' ? [['状态', v.up === '1' ? '正常' : '-'], ['CPU 使用率', formatPercent100(v.cpu)], ['1 分钟负载', v.load1 || '-'], ['内存使用率', formatPercent100(v.memory)], ['磁盘使用率', formatPercent100(v.disk)]] : [['状态', v.up === '1' ? '正常' : '-'], ['连接数', v.threads || '-'], ['运行线程', v.running || '-'], ['慢查询', v.slow || '-'], ['查询总数', v.questions || '-'], ['Buffer 命中率', formatPercent(v.hit)]]; return <section className="surface mysql-dashboard-card" key={item.id}><header><div><h3>{item.name}</h3><span>{item.sourceName} · {item.sourceType}</span></div><div className="dashboard-card-actions">{item.sourceType === 'MySQL' && <button className="text-button" type="button" onClick={() => void openSQL(item)}>更多</button>}<button className="text-button danger" type="button" onClick={() => remove(item.id)}>删除</button></div></header><div className="dashboard-metrics">{cards.map(([label, value]) => <div key={label}><span>{label}</span><b>{value}</b></div>)}</div></section> })}</section>}{sqlSource && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setSQLSource(null) }}><section className="surface sql-insight-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>{sqlMode === 'slow' ? '慢查询 SQL' : '耗时最高 SQL'}</h2><p>{sqlSource.sourceName} · 最多 50 条</p></div><button className="close-button" type="button" onClick={() => setSQLSource(null)}>×</button></header>{sqlLoading ? <div className="empty-state"><b>正在加载 SQL 明细</b></div> : sqlError ? <div className="empty-state"><b>加载失败</b><span>{sqlError}</span></div> : sqlItems.length === 0 ? <div className="empty-state"><b>暂无 SQL 明细</b><span>慢日志与 Performance Schema 均未返回可展示记录。</span></div> : <><div className="sql-insight-list">{pageItems.map((item, index) => <article key={`${item.digest || item.queryText}-${index}`}><header><span>{(sqlPage - 1) * 10 + index + 1}</span><b>{item.schemaName || '未指定库'}</b><small>{sqlMode === 'slow' ? `耗时 ${formatMilliseconds(item.maxLatencyMs)}` : `最长 ${formatMilliseconds(item.maxLatencyMs)}`}</small></header><code>{item.queryText}</code><footer><span>执行 {item.count} 次</span><span>平均 {formatMilliseconds(item.averageLatencyMs)}</span><span>扫描 {item.rowsExamined} 行</span></footer></article>)}</div><footer className="sql-pagination"><button type="button" disabled={sqlPage === 1} onClick={() => setSQLPage(current => current - 1)}>上一页</button><span>{sqlPage} / {pages}</span><button type="button" disabled={sqlPage === pages} onClick={() => setSQLPage(current => current + 1)}>下一页</button></footer></>}</section></div>}</div>
}

function formatPercent(value?: string) {
  const n = Number(value)
  if (!Number.isFinite(n)) return '-'
  return `${(n * 100).toFixed(2)}%`
}

function MetricQuery() {
  const [sources, setSources] = useState<Source[]>([])
  const [sourceId, setSourceId] = useState('')
  const [metrics, setMetrics] = useState<PrometheusMetric[]>([])
  const [metricsLoading, setMetricsLoading] = useState(false)
  const [selectedCategory, setSelectedCategory] = useState('all')
  const [query, setQuery] = useState('')
  const [cursorPosition, setCursorPosition] = useState(0)
  const [queryFocused, setQueryFocused] = useState(false)
  const [activeSuggestion, setActiveSuggestion] = useState(0)
  const [result, setResult] = useState<any>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const queryInput = useRef<HTMLInputElement>(null)
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const enabledSources = sources.filter(item => item.enabled && item.type === 'Prometheus')
  const defaultSource = enabledSources.find(item => item.status === '健康') || enabledSources[0]
  const selectedSourceId = enabledSources.some(item => item.id === sourceId) ? sourceId : defaultSource?.id || ''
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '数据源加载失败')
      const next = Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => item.type === 'Prometheus') : []
      setSources(next)
      if (!sourceId) setSourceId(next.find((item: Source) => item.enabled && item.status === '健康')?.id || next.find((item: Source) => item.enabled)?.id || '')
    } catch (err) {
      setSources([])
      showMessage(err instanceof Error ? err.message : '数据源加载失败')
    }
  }
  useEffect(() => { void loadSources() }, [])
  useEffect(() => {
    if (!selectedSourceId) { setMetrics([]); return }
    const loadMetrics = async () => {
      setMetricsLoading(true)
      try {
        const response = await fetch(`${api}/prometheus/${selectedSourceId}/metrics?limit=5000`)
        const data = await response.json()
        if (!response.ok) throw new Error(data.error || '指标加载失败')
        setMetrics(Array.isArray(data.metrics) ? data.metrics : [])
      } catch (err) {
        setMetrics([])
        showMessage(err instanceof Error ? err.message : '指标加载失败')
      } finally { setMetricsLoading(false) }
    }
    setResult(null)
    setSelectedCategory('all')
    void loadMetrics()
  }, [selectedSourceId])
  const availableMetrics = useMemo(() => metrics.filter(metric => isVisibleMetric(metric.name)), [metrics])
  const categoryOptions = useMemo(() => {
    const counts = new Map<string, number>()
    for (const metric of availableMetrics) {
      const category = metricCategory(metric.name)
      counts.set(category, (counts.get(category) || 0) + 1)
    }
    const options = metricCategories.filter(category => counts.has(category.id)).map(category => ({ id: category.id, label: category.label, count: counts.get(category.id) || 0 }))
    if (counts.has('other')) options.push({ id: 'other', label: '其他', count: counts.get('other') || 0 })
    return [{ id: 'all', label: '全部', count: availableMetrics.length }, ...options]
  }, [availableMetrics])
  const queryToken = useMemo(() => {
    const beforeCursor = query.slice(0, cursorPosition)
    return beforeCursor.match(/[a-zA-Z_:][a-zA-Z0-9_:]*$/)?.[0] || ''
  }, [cursorPosition, query])
  const suggestions = useMemo(() => {
    if (queryToken.length < 2) return []
    const keyword = queryToken.toLowerCase()
    return availableMetrics
      .filter(metric => (selectedCategory === 'all' || metricCategory(metric.name) === selectedCategory) && metric.name.toLowerCase().includes(keyword))
      .sort((a, b) => Number(!a.name.toLowerCase().startsWith(keyword)) - Number(!b.name.toLowerCase().startsWith(keyword)) || a.name.localeCompare(b.name))
      .slice(0, 8)
  }, [availableMetrics, queryToken, selectedCategory])
  const suggestionsOpen = queryFocused && suggestions.length > 0
  const completeMetric = (name: string) => {
    const start = cursorPosition - queryToken.length
    const nextQuery = `${query.slice(0, start)}${name}${query.slice(cursorPosition)}`
    const nextCursor = start + name.length
    setQuery(nextQuery)
    setCursorPosition(nextCursor)
    setActiveSuggestion(0)
    window.requestAnimationFrame(() => {
      queryInput.current?.focus()
      queryInput.current?.setSelectionRange(nextCursor, nextCursor)
    })
  }
  const handleQueryKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!suggestionsOpen) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActiveSuggestion(current => (current + 1) % suggestions.length)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActiveSuggestion(current => (current - 1 + suggestions.length) % suggestions.length)
    } else if (event.key === 'Tab' || event.key === 'Enter') {
      event.preventDefault()
      completeMetric(suggestions[activeSuggestion]?.name || suggestions[0].name)
    } else if (event.key === 'Escape') {
      setQueryFocused(false)
    }
  }
  const runQuery = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selectedSourceId || !query.trim()) return
    setLoading(true)
    setMessage('')
    try {
      const response = await fetch(`${api}/prometheus/${selectedSourceId}/query?query=${encodeURIComponent(query)}`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || 'PromQL 查询失败')
      setResult(data.data)
    } catch (err) {
      showMessage(err instanceof Error ? err.message : 'PromQL 查询失败')
    } finally { setLoading(false) }
  }
  const resultRows = Array.isArray(result?.result) ? result.result : []
  const resultType = typeof result?.resultType === 'string' ? result.resultType : ''
  const sourceSelect = <AppSelect className="metric-source-select" value={selectedSourceId} onChange={setSourceId} options={enabledSources.map(source => ({ value: source.id, label: source.name }))} />
  return <div className="page metric-query-page">
    <PageHead title="指标查询" description="查询 Prometheus 数据源。" actionNode={sourceSelect} />
    {message && <div className="toast">{message}</div>}
    {enabledSources.length === 0 ? <section className="surface empty-state"><b>暂无 Prometheus 数据源</b><span>请先到数据节点新增并启用 Prometheus 数据源。</span></section> : <section className="surface metric-query-workspace">
      <div className="metric-categories" aria-label="指标分类">{categoryOptions.map(category => <button className={selectedCategory === category.id ? 'active' : ''} type="button" key={category.id} onClick={() => setSelectedCategory(category.id)}><span>{category.label}</span><b>{category.count}</b></button>)}{metricsLoading && <i className="catalog-loading" aria-label="指标加载中" />}</div>
      <section className="promql-workspace">
        <form className="promql-form" onSubmit={runQuery}>
          <div className="promql-input-wrap">
            <input ref={queryInput} value={query} onChange={(event) => { setQuery(event.target.value); setCursorPosition(event.target.selectionStart ?? event.target.value.length); setActiveSuggestion(0) }} onSelect={(event) => setCursorPosition(event.currentTarget.selectionStart ?? query.length)} onKeyDown={handleQueryKeyDown} onFocus={() => setQueryFocused(true)} onBlur={() => window.setTimeout(() => setQueryFocused(false), 120)} placeholder="输入 PromQL" role="combobox" aria-autocomplete="list" aria-expanded={suggestionsOpen} aria-controls="metric-suggestions" autoComplete="off" />
            {suggestionsOpen && <div className="metric-suggestions" id="metric-suggestions" role="listbox">{suggestions.map((metric, index) => <button className={activeSuggestion === index ? 'active' : ''} type="button" role="option" aria-selected={activeSuggestion === index} key={metric.name} onMouseDown={(event) => event.preventDefault()} onClick={() => completeMetric(metric.name)}><code>{metric.name}</code><span>{metricCategoryLabel(metric.name)}</span></button>)}</div>}
          </div>
          <button className="button" type="submit" disabled={loading || !query.trim()}>{loading ? '查询中...' : '查询'}</button>
        </form>
        <div className="query-output">
          {!result ? <div className="query-empty"><b>查询结果</b><span>—</span></div> : resultRows.length > 0 && (resultType === 'vector' || resultRows.every((row: any) => row && typeof row === 'object' && ('value' in row || 'values' in row))) ? <div className="query-table"><div className="query-row query-head"><span>指标标签</span><span>值</span></div>{resultRows.map((row: any, index: number) => { const sample = Array.isArray(row.value) ? row.value : Array.isArray(row.values) ? row.values[row.values.length - 1] : null; return <div className="query-row" key={index}><code>{Object.keys(row.metric || {}).length ? JSON.stringify(row.metric) : query}</code><div className="query-value"><b>{Array.isArray(sample) ? sample[1] : '-'}</b>{Array.isArray(row.values) && <small>{row.values.length} 个样本</small>}</div></div> })}</div> : <pre className="query-result">{JSON.stringify(result, null, 2)}</pre>}
        </div>
      </section>
    </section>}
  </div>
}
function formatPercent100(value?: string) { const n = Number(value); return Number.isFinite(n) ? `${n.toFixed(2)}%` : '-' }
function formatMilliseconds(value?: number) { const n = Number(value); return Number.isFinite(n) ? n >= 1000 ? `${(n / 1000).toFixed(2)}s` : `${n.toFixed(2)}ms` : '-' }
function formatHadoopTime(value?: number) {
  const date = new Date(Number(value))
  if (!Number.isFinite(date.getTime()) || Number(value) <= 0) return ''
  return `${date.getFullYear()}年${date.getMonth() + 1}月${date.getDate()}日 ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}:${String(date.getSeconds()).padStart(2, '0')}`
}
function formatHadoopTimeRange(app: any) {
  const started = formatHadoopTime(app.startedTime)
  if (!started) return '时间未知'
  return `${started} - ${formatHadoopTime(app.finishedTime) || '进行中'}`
}

function hadoopStatusTone(value?: string) {
  const status = String(value || '').toUpperCase()
  if (status === 'FAILED' || status === 'KILLED') return 'danger'
  if (status === 'FINISHED' || status === 'RUNNING') return 'success'
  return 'neutral'
}

function formatHadoopApplicationName(name?: string, fallback?: string) {
  const cleaned = String(name || '')
    .replace(/[\u0000-\u001f\u007f-\u009f\u200b-\u200d\ufeff]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/[：:]+$/, '')
    .trim()
  return cleaned || fallback || '-'
}

function hadoopApplicationDetailsURL(source: Source | undefined, appID: string) {
  const host = String(source?.host || '').trim()
  if (!host || !appID) return ''
  const withProtocol = /^https?:\/\//i.test(host) ? host : `http://${host}${source?.port ? `:${source.port}` : ''}`
  return `${withProtocol.replace(/\/+$/, '')}/cluster/app/${encodeURIComponent(appID)}`
}

function HadoopLogPager({ content }: { content: string }) {
  const lines = content.split(/\r?\n/)
  const pageSize = 120
  const totalPages = Math.max(1, Math.ceil(lines.length / pageSize))
  const [page, setPage] = useState(1)
  const currentPage = Math.min(page, totalPages)
  const visibleLines = lines.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  useEffect(() => setPage(1), [content])
  return <span className="hadoop-log-pager"><span className="hadoop-log-pagination"><span>共 {lines.length} 行，每页 {pageSize} 行</span><span><button type="button" disabled={currentPage === 1} onClick={() => setPage(1)}>首页</button><button type="button" disabled={currentPage === 1} onClick={() => setPage(value => Math.max(1, value - 1))}>上一页</button><input type="number" min="1" max={totalPages} value={currentPage} aria-label="日志页码" onChange={(event) => { const next = Number(event.target.value); if (Number.isInteger(next) && next >= 1 && next <= totalPages) setPage(next) }} /><span>/ {totalPages}</span><button type="button" disabled={currentPage === totalPages} onClick={() => setPage(value => Math.min(totalPages, value + 1))}>下一页</button><button type="button" disabled={currentPage === totalPages} onClick={() => setPage(totalPages)}>末页</button></span></span><span className="hadoop-log-content">{visibleLines.join('\n')}</span></span>
}

function HadoopYarn() {
  const location = useLocation()
  const requestedSourceID = new URLSearchParams(location.search).get('source') || ''
  const [sources, setSources] = useState<Source[]>([]); const [sourceID, setSourceID] = useState(''); const [apps, setApps] = useState<any[]>([]); const [appTotal, setAppTotal] = useState(0); const [appFacets, setAppFacets] = useState<Record<string, string[]>>({}); const [containers, setContainers] = useState<any[]>([]); const [containerPage, setContainerPage] = useState(1); const [selectedContainerID, setSelectedContainerID] = useState(''); const [log, setLog] = useState<ReactNode>(''); const [logModalOpen, setLogModalOpen] = useState(false); const [selectedAppID, setSelectedAppID] = useState(''); const [message, setMessage] = useState(''); const [downloadingLog, setDownloadingLog] = useState(false); const [, setLoading] = useState(false)
  const [keyword, setKeyword] = useState(''); const [userFilter, setUserFilter] = useState(''); const [typeFilter, setTypeFilter] = useState(''); const [stateFilter, setStateFilter] = useState(''); const [finalStatusFilter, setFinalStatusFilter] = useState(''); const [appPage, setAppPage] = useState(1)
  useEffect(() => { void (async () => { try { const data = await (await fetch(`${api}/data-sources`)).json(); const next = (data.dataSources || []).filter((item: Source) => item.type === 'Hadoop'); setSources(next); setSourceID(next.some((item: Source) => item.id === requestedSourceID) ? requestedSourceID : next[0]?.id || '') } catch { setSources([]); setSourceID('') } })() }, [requestedSourceID])
  const loadApps = async () => { if (!sourceID) return; setLoading(true); try { const params = new URLSearchParams({ page: String(appPage), pageSize: '20', keyword, user: userFilter, type: typeFilter, state: stateFilter, finalStatus: finalStatusFilter }); const response = await fetch(`${api}/hadoop/${sourceID}/apps?${params.toString()}`); const data = await response.json(); if (!response.ok) throw new Error(data.error); setApps(Array.isArray(data.data?.items) ? data.data.items : []); setAppTotal(Number(data.data?.total) || 0); setAppFacets(data.data?.facets || {}); setMessage('') } catch (err) { setMessage(err instanceof Error ? err.message : 'YARN 应用加载失败') } finally { setLoading(false) } }
  const loadContainers = async (appID: string) => { try { setSelectedAppID(appID); setContainerPage(1); setLog('正在加载日志...'); const response = await fetch(`${api}/hadoop/${sourceID}/apps/${appID}`); const data = await response.json(); if (!response.ok) throw new Error(data.error); const next = Array.isArray(data.data) ? data.data : []; setContainers(next); if (next.length === 0) { setLog('该任务没有可读取的容器日志。'); return }; setSelectedContainerID(next[0].id); setLogModalOpen(true); if (next[0].logUrl) void loadLog(next[0].logUrl); else setLog('该容器未提供日志地址。') } catch (err) { setLogModalOpen(false); setMessage(err instanceof Error ? err.message : '容器加载失败') } }
  const loadLog = async (url: string) => { try { setLog('正在加载日志...'); const response = await fetch(`${api}/hadoop/${sourceID}/log?url=${encodeURIComponent(url)}`); const data = await response.json(); if (!response.ok) throw new Error(data.error); setLog(<HadoopLogPager content={data.data || '日志为空。'} />) } catch (err) { setLog(`日志加载失败：${err instanceof Error ? err.message : '未知错误'}`) } }
  const downloadAllLogs = async () => {
    const available = containers.filter(container => container.logUrl)
    if (available.length === 0) { setMessage('当前任务没有可下载的容器日志'); return }
    setDownloadingLog(true)
    try {
      const chunks = await Promise.all(available.map(async container => {
        const response = await fetch(`${api}/hadoop/${sourceID}/log?url=${encodeURIComponent(container.logUrl)}`)
        const data = await response.json()
        const content = response.ok ? String(data.data || '') : `读取失败：${data.error || response.statusText}`
        return `===== ${container.id} =====\n${content}`
      }))
      const blob = new Blob([chunks.join('\n\n')], { type: 'text/plain;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `${selectedAppID.replace(/[^a-zA-Z0-9._-]/g, '_') || 'yarn-task'}.log`
      link.click()
      URL.revokeObjectURL(url)
    } catch (err) { setMessage(err instanceof Error ? err.message : '任务日志下载失败') } finally { setDownloadingLog(false) }
  }
  const valuesFor = (field: string) => appFacets[field] || []
  const filteredApps = apps
  const totalAppPages = Math.max(1, Math.ceil(appTotal / 20))
  const currentAppPage = Math.min(appPage, totalAppPages)
  const visibleApps = filteredApps
  const totalContainerPages = Math.max(1, Math.ceil(containers.length / 10))
  const currentContainerPage = Math.min(containerPage, totalContainerPages)
  const visibleContainers = containers.slice((currentContainerPage - 1) * 10, currentContainerPage * 10)
  void selectedContainerID
  void visibleContainers
  const appPagination = <footer className="hadoop-pagination"><span>共 {appTotal} 条，第 {currentAppPage} / {totalAppPages} 页</span><div><button type="button" disabled={currentAppPage === 1} onClick={() => setAppPage(1)}>首页</button><button type="button" disabled={currentAppPage === 1} onClick={() => setAppPage(current => Math.max(1, current - 1))}>上一页</button><input type="number" min="1" max={totalAppPages} value={currentAppPage} aria-label="页码" onChange={(event) => { const next = Number(event.target.value); if (Number.isInteger(next) && next >= 1 && next <= totalAppPages) setAppPage(next) }} /><span>/ {totalAppPages}</span><button type="button" disabled={currentAppPage === totalAppPages} onClick={() => setAppPage(current => Math.min(totalAppPages, current + 1))}>下一页</button><button type="button" disabled={currentAppPage === totalAppPages} onClick={() => setAppPage(totalAppPages)}>末页</button></div></footer>
  useEffect(() => {
    if (!logModalOpen) return
    const closeButton = document.querySelector<HTMLButtonElement>('.hadoop-log-modal .close-button')
    if (!closeButton || closeButton.parentElement?.querySelector('.hadoop-log-download')) return
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'button secondary hadoop-log-download'
    button.textContent = downloadingLog ? '下载中...' : '下载'
    button.disabled = downloadingLog
    button.addEventListener('click', () => void downloadAllLogs())
    closeButton.before(button)
    return () => button.remove()
  }, [logModalOpen, containers, downloadingLog, selectedAppID, sourceID])
  const actions = null
  useEffect(() => { if (!sourceID) return; void loadApps(); const timer = window.setInterval(() => void loadApps(), 120000); return () => window.clearInterval(timer) }, [sourceID, appPage, keyword, userFilter, typeFilter, stateFilter, finalStatusFilter])
  const resetFilters = () => { setKeyword(''); setUserFilter(''); setTypeFilter(''); setStateFilter(''); setFinalStatusFilter(''); setAppPage(1) }
  const openApplicationDetails = (appID: string) => {
    const url = hadoopApplicationDetailsURL(sources.find(source => source.id === sourceID), appID)
    if (!url) { setMessage('未配置 Hadoop Web 地址'); return }
    const detailWindow = window.open(url, '_blank')
    if (!detailWindow) { setMessage('浏览器阻止了新标签页，请允许弹窗后重试'); return }
    detailWindow.opener = null
  }
  return <div className="page"><PageHead title="Hadoop / YARN" description="应用运行状态与容器日志" actionNode={actions} />{sources.length === 0 ? <section className="surface empty-state"><b>暂无 Hadoop 数据源</b><span>请先在数据节点新增 Hadoop Web 地址。</span></section> : <section className="surface">{message && <div className="form-error">{message}</div>}<div className="hadoop-filter-bar"><input value={keyword} onChange={(event) => { setKeyword(event.target.value); setAppPage(1) }} placeholder="搜索名称、ID、用户或状态" /><AppSelect value={userFilter} placeholder="全部用户" onChange={(value) => { setUserFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部用户' }, ...valuesFor('user').map(value => ({ value, label: value }))]} /><AppSelect value={typeFilter} placeholder="全部类型" onChange={(value) => { setTypeFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部类型' }, ...valuesFor('applicationType').map(value => ({ value, label: value }))]} /><AppSelect value={stateFilter} placeholder="全部状态" onChange={(value) => { setStateFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部状态' }, ...valuesFor('state').map(value => ({ value, label: value }))]} /><AppSelect value={finalStatusFilter} placeholder="全部最终状态" onChange={(value) => { setFinalStatusFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部最终状态' }, ...valuesFor('finalStatus').map(value => ({ value, label: value }))]} /><button className="hadoop-filter-reset" type="button" onClick={resetFilters}>重置</button></div><div className="prometheus-rule-list">{visibleApps.map(app => <article className="prometheus-rule-row compact hadoop-app-row" key={app.id}><i className="rule-icon">Y</i><div><header><b>{formatHadoopApplicationName(app.name, app.id)}<code className="hadoop-application-id">{app.id}</code></b><div className="hadoop-app-actions"><button className="text-button" type="button" onClick={() => void loadContainers(app.id)}>查看日志</button><button className="text-button hadoop-app-detail" type="button" onClick={() => openApplicationDetails(app.id)}>任务详情</button></div></header><div className="hadoop-app-meta"><span>{app.user || '-'}</span><span>{app.queue || 'default'}</span><span>{app.applicationType || '-'}</span><span>进度 {Number(app.progress || 0).toFixed(0)}%</span><span>{formatHadoopTimeRange(app)}</span><span className={`hadoop-status ${hadoopStatusTone(app.state)}`}>状态 {app.state || '-'}</span><span className={`hadoop-status ${hadoopStatusTone(app.finalStatus)}`}>最终状态 {app.finalStatus || '-'}</span></div></div></article>)}</div>{appPagination}{filteredApps.length > 0 && visibleApps.length === 0 && <div className="hadoop-filter-empty">没有匹配的应用</div>}</section>}{logModalOpen && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setLogModalOpen(false) }}><section className="surface hadoop-log-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>容器日志</h2><p>{selectedAppID}</p></div><button className="close-button" type="button" onClick={() => setLogModalOpen(false)}>×</button></header>{containers.length > 1 && <div className="hadoop-container-tabs">{containers.map(container => <button className={container.logUrl && log ? 'active' : ''} type="button" key={container.id} onClick={() => container.logUrl && void loadLog(container.logUrl)}>{container.id}</button>)}</div>}<pre className="hadoop-log">{log}</pre></section></div>}</div>
}

function DataSources() {
  const [allSources, setSources] = useState<Source[]>([])
  const [sourcePage, setSourcePage] = useState(1)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Source | null>(null)
  const [selectedType, setSelectedType] = useState<'Prometheus' | 'MySQL' | 'SSH' | 'Hadoop'>('Prometheus')
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [, setRefreshing] = useState(false)
  const refreshInterval = useRefreshInterval()
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => ['Prometheus', 'MySQL', 'SSH', 'Hadoop'].includes(item.type)) : [])
    } catch { setSources([]) }
  }
  useEffect(() => { void loadSources(); const timer = window.setInterval(loadSources, refreshInterval); return () => window.clearInterval(timer) }, [refreshInterval])
  useEffect(() => {
    if (!modalOpen) return
    const passwordInput = document.querySelector<HTMLInputElement>('.source-modal input[name="password"]')
    if (!passwordInput) return
    const required = (selectedType === 'MySQL' || selectedType === 'SSH') && !editing
    passwordInput.required = required
    passwordInput.placeholder = editing ? '留空则不修改' : selectedType === 'SSH' ? 'SSH 登录密码' : 'MySQL 密码'
  }, [modalOpen, selectedType, editing])
  const openModal = (source?: Source) => { setEditing(source || null); setSelectedType(source?.type === 'MySQL' ? 'MySQL' : source?.type === 'SSH' ? 'SSH' : source?.type === 'Hadoop' ? 'Hadoop' : 'Prometheus'); setModalOpen(true); setMessage('') }
  const sourceLogo = (source: Source) => source.type === 'MySQL' ? 'M' : source.type === 'SSH' ? 'S' : source.type === 'Hadoop' ? 'H' : 'P'
  const sourceSubtitle = (source: Source) => source.type === 'Hadoop' && /^https?:\/\//i.test(source.host) ? `${source.type} · ${source.host}` : `${source.type} · ${source.host}:${source.port}`
  const sourcePages = Math.max(1, Math.ceil(allSources.length / 20))
  const currentSourcePage = Math.min(sourcePage, sourcePages)
  const sources = allSources.slice((currentSourcePage - 1) * 20, currentSourcePage * 20)
  useEffect(() => { setSourcePage(current => Math.min(current, sourcePages)) }, [sourcePages])
  const importDashboard = (source: Source) => {
    if (!['MySQL', 'SSH'].includes(source.type)) { showMessage('仅 MySQL 和 SSH 数据源支持导入大屏'); return }
    const current = dashboardsFromStorage()
    if (current.some(item => item.sourceId === source.id)) { showMessage('该数据源已导入大屏'); return }
    saveDashboards([{ id: `dash-${Date.now()}`, name: `${source.name} 大屏`, sourceId: source.id, sourceName: source.name, sourceType: source.type, createdAt: new Date().toISOString() }, ...current])
    showMessage(`${source.name} 已导入大屏`)
  }
  const importHadoopMenu = (source: Source) => {
    if (source.type !== 'Hadoop') return
    const current = hadoopMenusFromStorage()
    if (current.some(item => item.sourceId === source.id)) { showMessage('该 Hadoop 数据源已导入菜单'); return }
    saveHadoopMenus([...current, { sourceId: source.id }])
    showMessage(`${source.name} 已导入菜单`)
  }
  const buildPayload = (formElement: HTMLFormElement): Source => {
    const form = new FormData(formElement)
    const type = String(form.get('type') || selectedType) as 'Prometheus' | 'MySQL' | 'SSH' | 'Hadoop'
    const needsCredentials = type === 'MySQL' || type === 'SSH'
    const nodeManagerUrl = String(form.get('nodeManagerUrl') || '').trim()
    const jobHistoryUrl = String(form.get('jobHistoryUrl') || '').trim()
    const sshSourceId = String(form.get('sshSourceId') ?? editing?.options?.sshSourceId ?? '').trim()
    const options = type === 'Hadoop' ? { ...editing?.options, ...(nodeManagerUrl ? { nodeManagerUrl } : {}), ...(jobHistoryUrl ? { jobHistoryUrl } : {}), ...(sshSourceId ? { sshSourceId } : {}) } : undefined
    if (options && !nodeManagerUrl) delete options.nodeManagerUrl
    if (options && !jobHistoryUrl) delete options.jobHistoryUrl
    if (options && !sshSourceId) delete options.sshSourceId
    return { id: editing?.id || '', name: String(form.get('name') || ''), type, host: String(form.get('host') || ''), port: String(form.get('port') || ''), username: needsCredentials ? String(form.get('username') || '') : '', password: String(form.get(needsCredentials ? 'password' : 'token') || ''), database: type === 'MySQL' ? String(form.get('database') || '') : '', options, remark: String(form.get('remark') || ''), enabled: true, status: '待测试', lastTest: '' }
  }
  const saveSource = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    const payload = buildPayload(event.currentTarget)
    try {
      const response = await fetch(editing ? `${api}/data-sources/${editing.id}` : `${api}/data-sources`, { method: editing ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存失败')
      showMessage(`${data.name} 已保存`)
      setModalOpen(false)
      setEditing(null)
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (err) {
      showMessage(err instanceof Error ? err.message : '保存失败')
    } finally { setSaving(false) }
  }
  const testSource = async (formElement: HTMLFormElement) => {
    try {
      const response = await fetch(`${api}/data-sources/test`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(buildPayload(formElement)) })
      const data = await response.json()
      showMessage(data.message || (data.success ? '测试成功' : '测试失败'))
    } catch { showMessage('测试失败') }
  }
  const refreshHealth = async () => {
    setRefreshing(true)
    try {
      const response = await fetch(`${api}/data-sources/health-check`, { method: 'POST' })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '刷新失败')
      setSources(Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => ['Prometheus', 'MySQL', 'SSH', 'Hadoop'].includes(item.type)) : [])
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      showMessage('数据源状态已刷新')
    } catch (err) { showMessage(err instanceof Error ? err.message : '刷新失败') } finally { setRefreshing(false) }
  }
  const toggleEnabled = async (source: Source, enabled: boolean) => {
    try {
      const response = await fetch(`${api}/data-sources/${source.id}/enabled`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '状态更新失败')
      setSources(current => current.map(item => item.id === source.id ? data : item))
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
    } catch (err) { showMessage(err instanceof Error ? err.message : '状态更新失败') }
  }
  const performDeleteSource = async (source: Source) => {
    try {
      const response = await fetch(`${api}/data-sources/${source.id}`, { method: 'DELETE' })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '删除失败')
      if (source.type !== 'Prometheus') saveDashboards(dashboardsFromStorage().filter(item => item.sourceId !== source.id))
      if (source.type === 'Hadoop') saveHadoopMenus(hadoopMenusFromStorage().filter(item => item.sourceId !== source.id))
      showMessage(`${source.name} 已删除`)
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (err) { showMessage(err instanceof Error ? err.message : '删除失败') }
  }
  const deleteSource = (source: Source) => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    const close = () => { root.unmount(); host.remove() }
    root.render(<DataSourceDeleteDialog sourceName={source.name} onClose={close} onConfirm={() => { close(); void performDeleteSource(source) }} />)
  }
  void refreshHealth
  const pageAction = <div className="page-action-group"><button className="button" onClick={() => openModal()}><Icon name="plus" /> 新增数据源</button></div>
  return <div className="page"><PageHead title="数据节点" description="Prometheus、MySQL、SSH 和 Hadoop 数据源统一接入。Hadoop 可直接使用 Web 地址探测集群服务。" actionNode={pageAction} />{sources.length === 0 ? <section className="surface empty-state"><b>暂无数据源</b><span>点击右上角新增数据源。</span></section> : <><section className="source-list">{sources.map(source => <article className={`surface source-row ${source.status === '健康' ? 'healthy' : 'warning'} ${!source.enabled ? 'disabled' : ''}`} key={source.id}><div className="node-main"><span className="source-logo">{sourceLogo(source)}</span><div><h3>{source.name}</h3><p>{sourceSubtitle(source)}</p>{source.remark && <small className="source-remark">{source.remark}</small>}</div></div><div className="node-status" /><div className="node-enabled"><StatusSwitch checked={source.enabled} onChange={(checked) => void toggleEnabled(source, checked)} /></div><div className="node-meta"><span>最近检测：{formatCollectedAt(source.lastTest)}</span><span>{source.status}</span></div><div className="source-actions">{['MySQL', 'SSH'].includes(source.type) && <button type="button" onClick={() => importDashboard(source)}>导入大屏</button>}{source.type === 'Hadoop' && <button type="button" onClick={() => importHadoopMenu(source)}>导入菜单</button>}<button type="button" onClick={() => openModal(source)}>编辑</button><button className="danger" type="button" onClick={() => deleteSource(source)}>删除</button></div></article>)}</section><ListPagination total={allSources.length} page={currentSourcePage} onPageChange={setSourcePage} /></>}{modalOpen && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setModalOpen(false) }}><section className="surface source-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2>{editing ? '编辑数据源' : '新增数据源'}</h2><p>{selectedType} 接入</p></div><button className="close-button" type="button" onClick={() => setModalOpen(false)}>×</button></header><form onSubmit={saveSource}><div className="modal-form"><label>类型 <span className="required-mark">*</span><AppSelect name="type" value={selectedType} onChange={(next) => setSelectedType(next as 'Prometheus' | 'MySQL' | 'SSH' | 'Hadoop')} disabled={!!editing} options={[{ value: 'Prometheus', label: 'Prometheus' }, { value: 'MySQL', label: 'MySQL' }, { value: 'SSH', label: 'SSH' }, { value: 'Hadoop', label: 'Hadoop' }]} /></label><label>名称 <span className="required-mark">*</span><input key={`name-${selectedType}-${editing?.id || 'new'}`} name="name" defaultValue={editing?.name || ''} required /></label><label className={selectedType === 'Hadoop' ? 'wide' : ''}>{selectedType === 'Hadoop' ? 'Hadoop Web 地址' : '地址'} <span className="required-mark">*</span><input name="host" defaultValue={editing?.host || ''} placeholder={selectedType === 'MySQL' ? 'MySQL 主机地址' : selectedType === 'SSH' ? 'SSH 主机地址' : selectedType === 'Hadoop' ? '例如 http://hadoop-master:8088' : '例如 prometheus.example.com'} required /></label>{selectedType !== 'Hadoop' && <label>端口 <span className="required-mark">*</span><input key={`port-${selectedType}-${editing?.id || 'new'}`} name="port" defaultValue={editing?.port || ''} required /></label>}{selectedType === 'MySQL' || selectedType === 'SSH' ? <><label>用户名 <span className="required-mark">*</span><input name="username" defaultValue={editing?.username || ''} required /></label><label>密码<input name="password" type="password" placeholder={editing ? '留空则不修改' : selectedType === 'SSH' ? 'SSH 密码，可选' : 'MySQL 密码'} required={selectedType === 'MySQL' && !editing} /></label>{selectedType === 'MySQL' && <label className="wide">数据库<input name="database" defaultValue={editing?.database || ''} placeholder="可选，不填则采集实例级指标" /></label>}{selectedType === 'SSH' && <span className="form-error wide">SSH 仅采集 CPU、负载、内存和磁盘使用率。</span>}</> : selectedType === 'Hadoop' ? <><label className="wide">NodeManager 日志地址<input name="nodeManagerUrl" defaultValue={editing?.options?.nodeManagerUrl || ''} placeholder="选填，例如 http://hadoop-node-1:8042" /></label><label className="wide">JobHistory 日志地址<input name="jobHistoryUrl" defaultValue={editing?.options?.jobHistoryUrl || ''} placeholder="选填，例如 http://hadoop-history:19888" /></label><span className="form-error wide">NodeManager 与 JobHistory 地址仅用于读取 YARN 容器日志；不填时系统使用 YARN 返回的日志地址。</span></> : <label>Token<input name="token" type="password" placeholder={editing ? '留空则不修改' : '可选'} /></label>}<label className="wide">备注<textarea name="remark" defaultValue={editing?.remark || ''} placeholder="记录数据源用途或环境" /></label></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.closest('form'); if (form) void testSource(form) }}>测试连接</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
}

function StatusSwitch({ checked, disabled, onChange }: { checked: boolean; disabled?: boolean; onChange: (next: boolean) => void }) { return <button type="button" className={`status-switch ${checked ? 'checked' : ''}`} aria-pressed={checked} disabled={disabled} onClick={() => onChange(!checked)}><span className="status-switch-track"><span className="status-switch-thumb" /></span><span>{checked ? '启用' : '停用'}</span></button> }

function Alerts() {
  const [sources, setSources] = useState<Source[]>([])
  const [promRules, setPromRules] = useState<PrometheusRule[]>([])
  const [customRules, setCustomRules] = useState<CollectionRule[]>([])
  const [databaseOptions, setDatabaseOptions] = useState<string[]>([])
  const [tableOptions, setTableOptions] = useState<string[]>([])
  const [fieldOptions, setFieldOptions] = useState<string[]>([])
  const [selectedMySQLSource, setSelectedMySQLSource] = useState('')
  const [selectedDatabase, setSelectedDatabase] = useState('')
  const [selectedTable, setSelectedTable] = useState('')
  const [selectedField, setSelectedField] = useState('')
  const [httpCondition, setHTTPCondition] = useState('状态码小于400')
  const [selectedPrometheusSource, setSelectedPrometheusSource] = useState('')
  const [selectedSSHSources, setSelectedSSHSources] = useState<string[]>([])
  const [scriptCondition, setScriptCondition] = useState('退出码等于')
  const [frequencyUnit, setFrequencyUnit] = useState('m')
  const [deadline, setDeadline] = useState('03:00')
  const [ruleKind, setRuleKind] = useState<'data-monitor' | 'file-monitor' | 'script-monitor' | 'prometheus' | 'http' | 'https' | 'tcp' | 'udp'>('http')
  const [schemaLoading, setSchemaLoading] = useState(false)
  const [schemaError, setSchemaError] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [editingRule, setEditingRule] = useState<CollectionRule | null>(null)
  const [resultRule, setResultRule] = useState<CollectionRule | null>(null)
  const [message, setMessage] = useState('')
  const [, setLoading] = useState(false)
  const refreshInterval = useRefreshInterval()
  const [category, setCategory] = useState<'prometheus' | 'custom'>('custom')
  const [rulePage, setRulePage] = useState(1)
  const prometheusSources = sources.filter(item => item.enabled && item.type === 'Prometheus')
  const mysqlSources = sources.filter(item => item.enabled && item.type === 'MySQL')
  const sshSources = sources.filter(item => item.enabled && item.type === 'SSH')
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      const next = Array.isArray(data.dataSources) ? data.dataSources : []
      setSources(next)
      setSelectedMySQLSource(current => next.some((item: Source) => item.id === current && item.enabled && item.type === 'MySQL') ? current : next.find((item: Source) => item.enabled && item.type === 'MySQL')?.id || '')
      setSelectedPrometheusSource(current => next.some((item: Source) => item.id === current && item.enabled && item.type === 'Prometheus') ? current : next.find((item: Source) => item.enabled && item.type === 'Prometheus')?.id || '')
      void loadPromRules(next.filter((item: Source) => item.enabled && item.type === 'Prometheus'))
    } catch { setSources([]) }
  }
  const loadPromRules = async (targets = prometheusSources) => {
    if (targets.length === 0) { setPromRules([]); return }
    setLoading(true)
    setMessage('')
    const results = await Promise.allSettled(targets.map(async source => {
      const response = await fetch(`${api}/prometheus/${source.id}/rules`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || `${source.name} 告警规则获取失败`)
      return (Array.isArray(data.rules) ? data.rules : []).map((rule: PrometheusRule) => ({ ...rule, sourceId: source.id, sourceName: source.name }))
    }))
    const successful = results.filter((result): result is PromiseFulfilledResult<PrometheusRule[]> => result.status === 'fulfilled')
    if (successful.length > 0) {
      setPromRules(successful.flatMap(result => result.value))
    } else {
      setPromRules([])
      const failure = results.find((result): result is PromiseRejectedResult => result.status === 'rejected')
      showMessage(failure?.reason instanceof Error ? failure.reason.message : 'Prometheus 告警规则获取失败')
    }
    setLoading(false)
  }
  const loadCustomRules = async () => {
    try {
      const response = await fetch(`${api}/collection-rules`)
      const data = await response.json()
      setCustomRules(Array.isArray(data.rules) ? data.rules : [])
    } catch { setCustomRules([]) }
  }
  const loadSchema = async (source = selectedMySQLSource, database = selectedDatabase, table = selectedTable) => {
    if (!source) { setDatabaseOptions([]); setTableOptions([]); setFieldOptions([]); setSchemaError(''); return }
    setSchemaLoading(true)
    setSchemaError('')
    const params = new URLSearchParams()
    if (database) params.set('database', database)
    if (table) params.set('table', table)
    try {
      const response = await fetch(`${api}/data-sources/${source}/schema?${params.toString()}`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || 'MySQL 库表加载失败')
      const nextSchema = data.schema || {}
      if (database && table) setFieldOptions(Array.isArray(nextSchema[database]?.[table]) ? nextSchema[database][table] : [])
      else if (database) setTableOptions(Object.keys(nextSchema[database] || {}).sort((a, b) => a.localeCompare(b)))
      else setDatabaseOptions(Object.keys(nextSchema).sort((a, b) => a.localeCompare(b)))
    } catch (err) {
      if (database && table) setFieldOptions([])
      else if (database) setTableOptions([])
      else setDatabaseOptions([])
      setSchemaError(err instanceof Error ? err.message : 'MySQL 库表加载失败')
    } finally { setSchemaLoading(false) }
  }
  const loadEditSchema = async (rule: CollectionRule) => {
    try {
      const base = `${api}/data-sources/${rule.source}/schema`
      const databasesResponse = await fetch(base)
      const databasesData = await databasesResponse.json()
      const databases = databasesData.schema || {}
      setDatabaseOptions(Object.keys(databases).sort((a, b) => a.localeCompare(b)))
      const tablesResponse = await fetch(`${base}?database=${encodeURIComponent(rule.database)}`)
      const tablesData = await tablesResponse.json()
      setTableOptions(Object.keys(tablesData.schema?.[rule.database] || {}).sort((a, b) => a.localeCompare(b)))
      const fieldsResponse = await fetch(`${base}?database=${encodeURIComponent(rule.database)}&table=${encodeURIComponent(rule.table)}`)
      const fieldsData = await fieldsResponse.json()
      setFieldOptions(Array.isArray(fieldsData.schema?.[rule.database]?.[rule.table]) ? fieldsData.schema[rule.database][rule.table] : [])
    } catch { setSchemaError('MySQL 库表加载失败') }
  }
  useEffect(() => { void loadSources(); void loadCustomRules(); const reload = () => void loadSources(); window.addEventListener('opsguard-data-sources-change', reload); return () => window.removeEventListener('opsguard-data-sources-change', reload) }, [])
  useEffect(() => {
    const refresh = () => { if (category === 'prometheus') void loadPromRules(); else void loadCustomRules() }
    refresh()
    const timer = window.setInterval(refresh, refreshInterval)
    return () => window.clearInterval(timer)
  }, [category, refreshInterval])
  useEffect(() => { if (modalOpen && ruleKind === 'data-monitor') void loadSchema(selectedMySQLSource, selectedDatabase, selectedTable) }, [modalOpen, ruleKind, selectedMySQLSource, selectedDatabase, selectedTable])
  const stateClass = (state?: string, health?: string) => state === 'firing' ? 'danger' : state === 'pending' || health !== 'ok' ? 'pending' : 'success'
  const stateLabel = (value?: string) => value === 'inactive' ? '未触发' : value === 'firing' ? '告警中' : value === 'pending' ? '待触发' : value === 'ok' ? '正常' : (value || '-')
  const customStateClass = (lastRun: string) => lastRun.startsWith('告警') || lastRun.startsWith('执行失败') ? 'danger' : lastRun.startsWith('正常') ? 'success' : 'pending'
  const configuredPromRules = customRules.filter(rule => rule.database === 'prometheus')
  const visibleCustomRules = customRules.filter(rule => rule.database !== 'prometheus')
  const prometheusTotal = promRules.length + configuredPromRules.length
  const ruleTotal = category === 'prometheus' ? prometheusTotal : visibleCustomRules.length
  const rulePages = Math.max(1, Math.ceil(ruleTotal / 20))
  const currentRulePage = Math.min(rulePage, rulePages)
  const promOffset = (currentRulePage - 1) * 20
  const paginatedPromRules = promRules.slice(promOffset, promOffset + 20)
  const configuredStart = Math.max(0, promOffset - promRules.length)
  const configuredSlots = Math.max(0, 20 - paginatedPromRules.length)
  const paginatedConfiguredPromRules = configuredPromRules.slice(configuredStart, configuredStart + configuredSlots)
  const paginatedCustomRules = visibleCustomRules.slice((currentRulePage - 1) * 20, currentRulePage * 20)
  useEffect(() => { setRulePage(1) }, [category])
  useEffect(() => { setRulePage(current => Math.min(current, rulePages)) }, [rulePages])
  const openRuleModal = () => {
    setEditingRule(null)
    setMessage('')
    setRuleKind('http')
    setSelectedMySQLSource(current => current || mysqlSources[0]?.id || '')
    setSelectedPrometheusSource(current => current || prometheusSources[0]?.id || '')
    setSelectedSSHSources([])
    setSelectedDatabase('')
    setSelectedTable('')
    setSelectedField('')
    setHTTPCondition('状态码小于400')
    setScriptCondition('退出码等于')
    setFrequencyUnit('m')
    setDeadline('03:00')
    setDatabaseOptions([])
    setTableOptions([])
    setFieldOptions([])
    setSchemaError('')
    setModalOpen(true)
  }
  const openEditRule = (rule: CollectionRule) => {
    setEditingRule(rule)
    const kind = rule.database === 'prometheus' ? 'prometheus' : rule.database === 'file-monitor' ? 'file-monitor' : rule.database === 'script-monitor' ? 'script-monitor' : rule.source === 'custom-probe' ? (['http', 'https', 'tcp', 'udp'].includes(rule.database) ? rule.database as 'http' | 'https' | 'tcp' | 'udp' : 'http') : 'data-monitor'
    setRuleKind(kind)
    setSelectedMySQLSource(kind === 'data-monitor' ? rule.source : '')
    setSelectedPrometheusSource(kind === 'prometheus' ? rule.source : '')
    setSelectedSSHSources(['file-monitor', 'script-monitor'].includes(kind) ? rule.source.split(',').map(value => value.trim()).filter(Boolean) : [])
    setSelectedDatabase(kind === 'data-monitor' ? rule.database : '')
    setSelectedTable(kind === 'data-monitor' ? rule.table : '')
    setSelectedField(kind === 'data-monitor' ? rule.field : '')
    setHTTPCondition(kind === 'http' ? rule.condition : '状态码小于400')
    setScriptCondition(kind === 'script-monitor' ? rule.condition : '退出码等于')
    setFrequencyUnit(frequencyParts(rule.frequency).unit)
    setDeadline(kind === 'data-monitor' || kind === 'file-monitor' ? rule.timeWindow : '03:00')
    if (kind === 'data-monitor') void loadEditSchema(rule)
    setModalOpen(true)
  }
  const deleteRule = async (rule: CollectionRule) => {
    if (!window.confirm(`确认删除告警规则“${rule.name}”？`)) return
    try {
      const response = await fetch(`${api}/collection-rules/${rule.id}`, { method: 'DELETE' })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '删除失败')
      showMessage('告警规则已删除')
      void loadCustomRules()
    } catch (err) { showMessage(err instanceof Error ? err.message : '删除失败') }
  }
  const saveRule = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const name = String(form.get('name') || '').trim()
    if (!name) { showMessage('请填写规则名称'); return }
    if (ruleKind === 'data-monitor' && (!selectedMySQLSource || !selectedDatabase || !selectedTable || !selectedField)) { showMessage('请完整选择数据源、数据库、表和日期字段'); return }
    if ((ruleKind === 'file-monitor' || ruleKind === 'script-monitor') && selectedSSHSources.length === 0) { showMessage('请至少选择一个 SSH 数据源'); return }
    if (ruleKind === 'prometheus' && !selectedPrometheusSource) { showMessage('请选择 Prometheus 数据源'); return }
    const payload: CollectionRule = ruleKind === 'data-monitor'
      ? { id: editingRule?.id || '', name, source: selectedMySQLSource, database: selectedDatabase, table: selectedTable, field: selectedField, condition: '当天有数据', threshold: '', timeWindow: deadline, frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
      : ruleKind === 'file-monitor'
        ? { id: editingRule?.id || '', name, source: selectedSSHSources.join(','), database: 'file-monitor', table: String(form.get('filePath') || ''), field: String(form.get('filePattern') || ''), condition: '当天生成文件', threshold: '', timeWindow: deadline, frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
        : ruleKind === 'script-monitor'
          ? { id: editingRule?.id || '', name, source: selectedSSHSources.join(','), database: 'script-monitor', table: String(form.get('script') || ''), field: '', condition: scriptCondition, threshold: String(form.get('expectedExitCode') || '0'), timeWindow: '', frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
        : ruleKind === 'prometheus'
        ? { id: editingRule?.id || '', name, source: selectedPrometheusSource, database: 'prometheus', table: String(form.get('promql') || ''), field: '', condition: 'PromQL 表达式', threshold: '', timeWindow: `${String(form.get('alertTimeout') || '5')}s`, frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
        : { id: editingRule?.id || '', name, source: 'custom-probe', database: ruleKind, table: String(form.get('target') || ''), field: '', condition: ['http', 'https'].includes(ruleKind) ? String(form.get('condition') || '状态码小于400') : `${ruleKind.toUpperCase()}端口可连接`, threshold: String(form.get('threshold') || ''), timeWindow: `${String(form.get('timeout') || '5')}s`, frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
    try {
      const response = await fetch(editingRule ? `${api}/collection-rules/${editingRule.id}` : `${api}/collection-rules`, { method: editingRule ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存失败')
      setModalOpen(false)
      showMessage(editingRule ? '告警规则已更新' : '告警规则已添加')
      setCategory(ruleKind === 'prometheus' ? 'prometheus' : 'custom')
      void loadCustomRules()
    } catch (err) { showMessage(err instanceof Error ? err.message : '保存失败') }
  }
  const alertAction = <div className="alert-section-actions"><div className="alert-category-tabs" role="tablist" aria-label="告警规则分类"><button className={category === 'custom' ? 'active' : ''} type="button" role="tab" aria-selected={category === 'custom'} onClick={() => setCategory('custom')}>自定义</button><button className={category === 'prometheus' ? 'active' : ''} type="button" role="tab" aria-selected={category === 'prometheus'} onClick={() => setCategory('prometheus')}>Prometheus</button></div><button className="button" type="button" onClick={openRuleModal}>新增规则</button></div>
  return (
    <div className="page">
      <PageHead title="告警规则" description="" actionNode={alertAction} />
      {message && <div className="toast">{message}</div>}
      {category === 'prometheus' ? (
        <section className="surface rules prometheus-rules">
          {prometheusSources.length === 0 ? (
            <div className="empty-state alert-empty-state"><b>暂无 Prometheus 数据源</b><span>请先到数据节点新增并启用 Prometheus。</span></div>
          ) : promRules.length === 0 && configuredPromRules.length === 0 ? (
            <div className="empty-state alert-empty-state"><b>暂无 Prometheus 告警规则</b><span>当前 Prometheus 没有返回 alerting 规则。</span></div>
          ) : (
            <div className="prometheus-rule-list">
              {paginatedPromRules.map((rule, index) => (
                <article className="prometheus-rule-row compact" key={`${rule.sourceId}-${rule.group}-${rule.name}-${index}`}>
                  <i className="rule-icon">P</i>
                  <div>
                    <header>
                      <b>{rule.name}<small className="rule-inline-note">{rule.summary || rule.description || rule.sourceName || '未配置说明'}</small></b>
                      <span className={`alert-result ${stateClass(rule.state, rule.health)}`}>{stateLabel(rule.state || rule.health)}</span>
                    </header>
                    <code>{rule.query}</code>
                  </div>
                </article>
              ))}
              {paginatedConfiguredPromRules.map(rule => (
                <article className="prometheus-rule-row compact" key={rule.id}>
                  <i className="rule-icon">P</i>
                  <div>
                    <header>
                      <b>{rule.name}<small className="rule-inline-note">{rule.lastRun || '待执行'}</small></b>
                      <span className={`alert-result ${customStateClass(rule.lastRun)}`}>{rule.status}</span>
                      <span className="rule-actions"><button className="text-button" type="button" onClick={() => openEditRule(rule)}>编辑</button><button className="text-button danger" type="button" onClick={() => void deleteRule(rule)}>删除</button></span>
                    </header>
                    <code>{rule.table}</code>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      ) : (
        <section className="surface rules prometheus-rules">
          {visibleCustomRules.length === 0 ? (
            <div className="empty-state alert-empty-state"><b>暂无自定义告警规则</b><span>点击右上角新增规则。</span></div>
          ) : (
            <div className="prometheus-rule-list">
              {paginatedCustomRules.map(rule => (
                <article className="prometheus-rule-row compact" key={rule.id}>
                  <i className="rule-icon">C</i>
                  <div>
                    <header>
                      <b>{rule.name}<small className="rule-inline-note">{rule.lastRun || '待执行'}</small></b>
                      {['file-monitor', 'script-monitor'].includes(rule.database) && rule.resultDetails && <button className="rule-result-button" type="button" onClick={() => setResultRule(rule)}>结果详情</button>}
                      <span className={`alert-result ${customStateClass(rule.lastRun)}`}>{rule.status}</span>
                      <span className="rule-actions"><button className="text-button" type="button" onClick={() => openEditRule(rule)}>编辑</button><button className="text-button danger" type="button" onClick={() => void deleteRule(rule)}>删除</button></span>
                    </header>
                    <code>{rule.source === 'custom-probe' ? `${rule.database} · ${rule.table}` : rule.database === 'file-monitor' ? `文件检测 · ${rule.table} · ${rule.field}` : rule.database === 'script-monitor' ? `脚本检测 · ${rule.source.split(',').map(id => sources.find(source => source.id === id)?.name || id).join('、')}` : `${rule.database}.${rule.table}${rule.field ? ` · ${rule.field}` : ''}`}</code>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      )}
      <ListPagination total={ruleTotal} page={currentRulePage} onPageChange={setRulePage} />
      {modalOpen && (
        <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setModalOpen(false) }}>
          <section className="surface source-modal alert-rule-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}>
            <header className="modal-head">
              <div><h2>{editingRule ? '编辑规则' : '新增规则'}</h2><p>Alert rule</p></div>
              <div className="alert-rule-modal-actions"><button className="button secondary" type="button" onClick={() => setModalOpen(false)}>取消</button><button className="button" type="submit" form="alert-rule-form">保存</button><button className="close-button" type="button" aria-label="关闭" onClick={() => setModalOpen(false)}>×</button></div>
            </header>
            <form id="alert-rule-form" key={editingRule?.id || 'new'} onSubmit={saveRule}>
              <div className="modal-form">
                <div className="rule-type-field wide">
                  <span className="field-label">规则类型 <span className="required-mark">*</span></span>
                  <div className="rule-type-options" role="radiogroup" aria-label="规则类型">
                    <button className={ruleKind === 'data-monitor' ? 'active' : ''} type="button" role="radio" aria-checked={ruleKind === 'data-monitor'} onClick={() => setRuleKind('data-monitor')}>数据监控</button>
                    <button className={ruleKind === 'file-monitor' ? 'active' : ''} type="button" role="radio" aria-checked={ruleKind === 'file-monitor'} onClick={() => setRuleKind('file-monitor')}>文件检测</button>
                    <button className={ruleKind === 'script-monitor' ? 'active' : ''} type="button" role="radio" aria-checked={ruleKind === 'script-monitor'} onClick={() => setRuleKind('script-monitor')}>脚本检测</button>
                    <button className={ruleKind === 'prometheus' ? 'active' : ''} type="button" role="radio" aria-checked={ruleKind === 'prometheus'} onClick={() => setRuleKind('prometheus')}>Prometheus</button>
                    <button className={['http', 'https', 'tcp', 'udp'].includes(ruleKind) ? 'active' : ''} type="button" role="radio" aria-checked={['http', 'https', 'tcp', 'udp'].includes(ruleKind)} onClick={() => setRuleKind('http')}>端口检测</button>
                  </div>
                </div>
                <label>规则名称 <span className="required-mark">*</span><input name="name" defaultValue={editingRule?.name || ''} autoComplete="off" required /></label>
                <label>采集频率 <span className="required-mark">*</span><span className="frequency-input"><input name="frequencyValue" type="number" min="1" step="1" defaultValue={frequencyParts(editingRule?.frequency).value} required /><AppSelect value={frequencyUnit} onChange={setFrequencyUnit} options={[{ value: 's', label: '秒' }, { value: 'm', label: '分钟' }, { value: 'h', label: '小时' }]} /></span></label>
                {ruleKind === 'data-monitor' ? (
                  <>
                    <label>数据源 <span className="required-mark">*</span><AppSelect value={selectedMySQLSource} placeholder="请选择数据源" onChange={(next) => { setSelectedMySQLSource(next); setSelectedDatabase(''); setSelectedTable(''); setSelectedField(''); setDatabaseOptions([]); setTableOptions([]); setFieldOptions([]) }} options={mysqlSources.map(source => ({ value: source.id, label: source.name }))} /></label>
                    <label>数据库 <span className="required-mark">*</span><AppSelect value={selectedDatabase} placeholder={schemaLoading && databaseOptions.length === 0 ? '正在加载数据库...' : '请选择数据库'} disabled={!selectedMySQLSource || (schemaLoading && databaseOptions.length === 0)} onChange={(next) => { setSelectedDatabase(next); setSelectedTable(''); setSelectedField(''); setTableOptions([]); setFieldOptions([]) }} options={databaseOptions.map(name => ({ value: name, label: name }))} /></label>
                    <label>表 <span className="required-mark">*</span><AppSelect value={selectedTable} placeholder={schemaLoading && tableOptions.length === 0 ? '正在加载表...' : '请选择表'} disabled={!selectedDatabase || (schemaLoading && tableOptions.length === 0)} onChange={(next) => { setSelectedTable(next); setSelectedField(''); setFieldOptions([]) }} options={tableOptions.map(name => ({ value: name, label: name }))} /></label>
                    <label>日期字段 <span className="required-mark">*</span><AppSelect name="field" value={selectedField} placeholder={schemaLoading && fieldOptions.length === 0 ? '正在加载字段...' : '请选择日期字段'} disabled={!selectedTable || (schemaLoading && fieldOptions.length === 0)} onChange={setSelectedField} options={fieldOptions.map(name => ({ value: name, label: name }))} /></label>
                    <label>告警策略<span className="static-field">当天有新数据</span></label>
                    <label>告警判定时间<AppSelect name="deadline" value={deadline} onChange={setDeadline} options={['00:00', '01:00', '02:00', '03:00', '06:00', '09:00', '12:00'].map(value => ({ value, label: value }))} /></label>
                    {schemaError && <span className="form-error wide">{schemaError}</span>}
                  </>
                ) : ruleKind === 'file-monitor' ? (
                  <>
                    <label className="wide">SSH 数据源 <span className="required-mark">*</span><span className="ssh-source-multi">{sshSources.map(source => <label key={source.id}><input type="checkbox" checked={selectedSSHSources.includes(source.id)} onChange={(event) => setSelectedSSHSources(current => event.target.checked ? [...current, source.id] : current.filter(id => id !== source.id))} /><span>{source.name}</span></label>)}</span></label>
                    <label>告警判定时间 <span className="required-mark">*</span><AppSelect name="deadline" value={deadline} onChange={setDeadline} options={['00:00', '01:00', '02:00', '03:00', '06:00', '09:00', '12:00', '18:00'].map(value => ({ value, label: value }))} /></label>
                    <label className="wide">路径 <span className="required-mark">*</span><input name="filePath" defaultValue={editingRule?.database === 'file-monitor' ? editingRule.table : ''} placeholder="/var/data/reports" required /></label>
                    <label className="wide">文件名正则 <span className="required-mark">*</span><input name="filePattern" defaultValue={editingRule?.database === 'file-monitor' ? editingRule.field : ''} placeholder="^report_\\d{8}\\.csv$" required /></label>
                  </>
                ) : ruleKind === 'script-monitor' ? (
                  <>
                    <label className="wide">SSH 数据源 <span className="required-mark">*</span><span className="ssh-source-multi">{sshSources.map(source => <label key={source.id}><input type="checkbox" checked={selectedSSHSources.includes(source.id)} onChange={(event) => setSelectedSSHSources(current => event.target.checked ? [...current, source.id] : current.filter(id => id !== source.id))} /><span>{source.name}</span></label>)}</span></label>
                    <label>判断逻辑 <span className="required-mark">*</span><AppSelect value={scriptCondition} onChange={setScriptCondition} options={[{ value: '退出码等于', label: '退出码等于' }]} /></label>
                    <label>预期退出码 <span className="required-mark">*</span><input name="expectedExitCode" type="number" min="0" max="255" step="1" defaultValue={editingRule?.database === 'script-monitor' ? editingRule.threshold || '0' : '0'} required /></label>
                    <label className="wide">检测脚本 <span className="required-mark">*</span><textarea name="script" rows={8} defaultValue={editingRule?.database === 'script-monitor' ? editingRule.table : ''} placeholder={'例如：\\n/usr/local/bin/check_service.sh\\nexit $?'} required /></label>
                  </>
                ) : ruleKind === 'prometheus' ? (
                  <>
                    <label>数据源 <span className="required-mark">*</span><AppSelect value={selectedPrometheusSource} placeholder="请选择 Prometheus 数据源" onChange={setSelectedPrometheusSource} options={prometheusSources.map(source => ({ value: source.id, label: source.name }))} /></label>
                    <label>持续时间（秒）<input name="alertTimeout" type="number" min="1" step="1" defaultValue={timeoutSeconds(editingRule?.timeWindow)} required /></label>
                    <label className="wide">PromQL <span className="required-mark">*</span><input name="promql" defaultValue={editingRule?.table || ''} placeholder="例如 up == 0" required /></label>
                  </>
                ) : (
                  <>
                    <label>检测协议<AppSelect value={ruleKind} onChange={(value) => setRuleKind(value as 'http' | 'https' | 'tcp' | 'udp')} options={[{ value: 'http', label: 'HTTP' }, { value: 'https', label: 'HTTPS' }, { value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} /></label>
                    <label className="wide">目标 <span className="required-mark">*</span><input name="target" defaultValue={editingRule?.table || ''} placeholder={['http', 'https'].includes(ruleKind) ? 'https://example.com:443/health' : '127.0.0.1:3306'} required /></label>
                    {['http', 'https'].includes(ruleKind) && (
                      <>
                        <label>判断方式<AppSelect name="condition" value={httpCondition} onChange={setHTTPCondition} options={[{ value: '状态码小于400', label: '状态码小于 400' }, { value: '状态码等于', label: '状态码等于' }, { value: '页面包含', label: '页面包含' }]} /></label>
                        {httpCondition !== '状态码小于400' && <label>期望值 <span className="required-mark">*</span><input name="threshold" placeholder={httpCondition === '页面包含' ? '页面关键字' : '200'} required /></label>}
                      </>
                    )}
                    <label>超时时间（秒）<input name="timeout" type="number" min="1" step="1" defaultValue={timeoutSeconds(editingRule?.timeWindow)} required /></label>
                  </>
                )}
                <label className="wide rule-remark-field">备注<input name="remark" defaultValue={editingRule?.remark || ''} placeholder="可选，记录告警用途或处理说明" /></label>
              </div>
            </form>
          </section>
        </div>
      )}
      {resultRule && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setResultRule(null) }}><section className="surface result-details-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>结果详情</h2><p>{resultRule.name}</p></div><button className="close-button" type="button" onClick={() => setResultRule(null)}>×</button></header><pre>{resultRule.resultDetails}</pre></section></div>}
    </div>
  )

}

function Notifications() {
  const [allItems, setItems] = useState<NotificationItem[]>([])
  const currentDate = new Date()
  const today = `${currentDate.getFullYear()}-${String(currentDate.getMonth() + 1).padStart(2, '0')}-${String(currentDate.getDate()).padStart(2, '0')}`
  const [startDate, setStartDate] = useState(today)
  const [endDate, setEndDate] = useState(today)
  const [notificationType, setNotificationType] = useState('all')
  const [loading, setLoading] = useState(false)
  const [page, setPage] = useState(1)
  const [expandedRuleID, setExpandedRuleID] = useState('')
  const [prometheusRuleDetails, setPrometheusRuleDetails] = useState<Record<string, PrometheusRule>>({})
  const refreshInterval = useRefreshInterval()
  const markAllRead = async () => {
    try {
      const response = await fetch(`${api}/notifications/read`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({}) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '操作失败')
      setItems(current => current.map(item => ({ ...item, unread: false })))
      window.dispatchEvent(new Event('opsguard-notifications-change'))
    } catch { /* Keep the current notification state when marking read fails. */ }
  }
  const toggleMuted = async (item: NotificationItem, muted: boolean) => {
    try {
      const response = await fetch(`${api}/notifications/mute`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id: item.id, muted }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '隐秘告警设置失败')
      setItems(current => current.map(currentItem => currentItem.id === item.id ? { ...currentItem, muted, unread: muted ? false : currentItem.unread } : currentItem))
      window.dispatchEvent(new Event('opsguard-notifications-change'))
    } catch { /* Keep the toggle state unchanged when the request fails. */ }
  }
  const toggleRuleDetails = async (item: NotificationItem) => {
    if (expandedRuleID === item.id) { setExpandedRuleID(''); return }
    setExpandedRuleID(item.id)
    if (item.database !== 'prometheus' || !item.source || prometheusRuleDetails[item.id]) return
    try {
      const response = await fetch(`${api}/prometheus/${item.source}/rules`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error)
      const rule = Array.isArray(data.rules) ? data.rules.find((candidate: PrometheusRule) => candidate.name === item.ruleName) : undefined
      if (rule) setPrometheusRuleDetails(current => ({ ...current, [item.id]: rule }))
    } catch { /* The notification itself remains visible when a rule was removed upstream. */ }
  }
  const load = async () => {
    setLoading(true)
    try {
      const params = new URLSearchParams({ limit: '1000', start: startDate, end: endDate })
      if (notificationType !== 'all') params.set('status', notificationType)
      const response = await fetch(`${api}/notifications?${params.toString()}`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '通知加载失败')
      const rangeStart = new Date(`${startDate}T00:00:00`).getTime()
      const rangeEnd = new Date(`${endDate}T00:00:00`).getTime() + 24 * 60 * 60 * 1000
      const notifications = Array.isArray(data.notifications) ? data.notifications : []
      setItems(notifications.filter((item: NotificationItem) => {
        const occurredAt = new Date(item.lastSeenAt).getTime()
        const matchesType = notificationType === 'all' || (notificationType === 'alerts' && ['alert', 'active'].includes(item.status)) || item.status === notificationType
        return Number.isFinite(occurredAt) && occurredAt >= rangeStart && occurredAt < rangeEnd && matchesType
      }))
    } catch { setItems([]) } finally { setLoading(false) }
  }
  const pageSize = 20
  const totalPages = Math.max(1, Math.ceil(allItems.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const items = allItems.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  const notificationPagination = allItems.length > 0 && <footer className="notification-pagination"><span>共 {allItems.length} 条，第 {currentPage} / {totalPages} 页</span><div><button type="button" disabled={currentPage === 1} onClick={() => setPage(1)}>首页</button><button type="button" disabled={currentPage === 1} onClick={() => setPage(value => Math.max(1, value - 1))}>上一页</button><input type="number" min="1" max={totalPages} value={currentPage} aria-label="通知页码" onChange={(event) => { const next = Number(event.target.value); if (Number.isInteger(next) && next >= 1 && next <= totalPages) setPage(next) }} /><span>/ {totalPages}</span><button type="button" disabled={currentPage === totalPages} onClick={() => setPage(value => Math.min(totalPages, value + 1))}>下一页</button><button type="button" disabled={currentPage === totalPages} onClick={() => setPage(totalPages)}>末页</button></div></footer>
  useEffect(() => { void load() }, [startDate, endDate, notificationType])
  useEffect(() => { setPage(1) }, [startDate, endDate, notificationType])
  useEffect(() => { const refresh = () => void load(); window.addEventListener('opsguard-notifications-change', refresh); return () => window.removeEventListener('opsguard-notifications-change', refresh) }, [startDate, endDate, notificationType])
  useEffect(() => { const timer = window.setInterval(() => void load(), refreshInterval); return () => window.clearInterval(timer) }, [startDate, endDate, notificationType, refreshInterval])
  useEffect(() => {
    const updateLabels = () => {
      document.querySelectorAll<HTMLElement>('.notification-list .notification-row .alert-result').forEach((badge, index) => {
        const status = items[index]?.status
        const label = status === 'active' ? '活跃' : status === 'resolved' ? '已恢复' : '告警'
        if (badge.textContent !== label) badge.textContent = label
        badge.classList.toggle('active-alert', status === 'active')
        badge.closest('.notification-row')?.classList.toggle('active-alert', status === 'active')
      })
    }
    updateLabels()
    const observer = new MutationObserver(updateLabels)
    observer.observe(document.body, { childList: true, subtree: true })
    return () => observer.disconnect()
  }, [items, notificationType])
  return <div className="page">
    <section className="notification-toolbar">
      <form onSubmit={(event) => { event.preventDefault(); void load() }}>
        <input type="date" value={startDate} max={endDate || today} onChange={(event) => setStartDate(event.target.value)} aria-label="开始日期" />
        <span>至</span>
        <input type="date" value={endDate} min={startDate} max={today} onChange={(event) => setEndDate(event.target.value)} aria-label="结束日期" />
        <AppSelect className="notification-kind-select" value={notificationType} onChange={setNotificationType} options={[{ value: 'all', label: '全部通知' }, { value: 'alerts', label: '告警通知' }, { value: 'active', label: '活跃告警' }, { value: 'resolved', label: '恢复通知' }]} />
        <button className="button secondary" type="submit" disabled={loading}>{loading ? '加载中...' : '查询'}</button>
      </form>
      <button className="button secondary notification-read-all" type="button" disabled={!allItems.some(item => item.unread)} onClick={() => void markAllRead()}>全部已读</button>
    </section>
    <section className="surface notification-list">
      {items.length === 0 ? <div className="empty-state alert-empty-state"><b>{loading ? '正在加载通知' : '暂无通知'}</b><span>{loading ? '请稍候。' : '当前时间区间内没有匹配通知。'}</span></div> : items.map(item => { const rule = prometheusRuleDetails[item.id]; const hasDetails = item.database === 'prometheus'; return <article className={`notification-row ${item.unread ? 'unread' : ''}`} key={item.id}><i className={`notification-dot ${item.status === 'resolved' ? 'success' : 'danger'}`} /><div><header><b title={item.ruleName}>{item.ruleName}</b><span className={`alert-result ${item.status === 'resolved' ? 'success' : 'danger'}`}>{item.status === 'resolved' ? '已恢复' : item.status === 'active' ? '活跃' : '告警'}</span></header><p title={item.message}>{item.message}</p><div className="notification-times"><small>首次告警：{formatCollectedAt(item.firstSeenAt)}</small><small>持续时间：{notificationDuration(item.firstSeenAt, item.status === 'resolved' ? item.lastSeenAt : undefined)}</small><small>{item.status === 'resolved' ? '恢复时间' : '最近通知'}：{formatCollectedAt(item.lastSeenAt)}</small></div>{hasDetails && expandedRuleID === item.id && <div className="notification-rule-details">{rule ? <><code>{rule.query || '未提供 PromQL'}</code>{rule.description && <p>{rule.description}</p>}{Object.keys(rule.labels || {}).length > 0 && <small>标签：{Object.entries(rule.labels || {}).map(([key, value]) => `${key}=${value}`).join(' · ')}</small>}{Object.keys(rule.annotations || {}).length > 0 && <small>注解：{Object.entries(rule.annotations || {}).map(([key, value]) => `${key}=${value}`).join(' · ')}</small>}</> : <span>正在加载规则详情，或该规则已在 Prometheus 中删除。</span>}</div>}</div><div className="notification-row-actions">{hasDetails && <button className="notification-rule-toggle" type="button" onClick={() => void toggleRuleDetails(item)}>{expandedRuleID === item.id ? '收起通知详情' : '通知详情'}</button>}{item.status !== 'resolved' && <label className={`notification-mute ${item.muted ? 'active' : ''}`} title="开启后，该持续告警不再计入未读通知"><input type="checkbox" checked={Boolean(item.muted)} onChange={(event) => void toggleMuted(item, event.target.checked)} /><i /><span>隐秘告警</span></label>}</div></article>})}
      {notificationPagination}
    </section>
  </div>
  return <div className="page"><section className="notification-toolbar"><form onSubmit={(event) => { event.preventDefault(); void load() }}><input type="date" value={startDate} max={endDate || today} onChange={(event) => setStartDate(event.target.value)} aria-label="开始日期" /><span>至</span><input type="date" value={endDate} min={startDate} max={today} onChange={(event) => setEndDate(event.target.value)} aria-label="结束日期" /><AppSelect className="notification-kind-select" value={notificationType} onChange={setNotificationType} options={[{ value: 'all', label: '全部通知' }, { value: 'alerts', label: '告警通知' }, { value: 'active', label: '活跃告警' }, { value: 'resolved', label: '恢复通知' }]} /><button className="button secondary" type="submit" disabled={loading}>{loading ? '加载中...' : '查询'}</button></form><button className="button secondary notification-read-all" type="button" disabled={!items.some(item => item.unread)} onClick={() => void markAllRead()}>全部已读</button></section><section className="surface notification-list">{items.length === 0 ? <div className="empty-state alert-empty-state"><b>{loading ? '正在加载通知' : '暂无通知'}</b><span>{loading ? '请稍候。' : '当前时间区间内没有匹配通知。'}</span></div> : items.map(item => <article className={`notification-row ${item.unread ? 'unread' : ''}`} key={item.id}><i className={`notification-dot ${item.status === 'resolved' ? 'success' : 'danger'}`} /><div><header><b title={item.ruleName}>{item.ruleName}</b><span className={`alert-result ${item.status === 'resolved' ? 'success' : 'danger'}`}>{item.status === 'resolved' ? '已恢复' : '告警'}</span></header><p title={item.message}>{item.message}</p><div className="notification-times"><small>首次告警：{formatCollectedAt(item.firstSeenAt)}</small><small>持续时间：{notificationDuration(item.firstSeenAt, item.status === 'resolved' ? item.lastSeenAt : undefined)}</small><small>{item.status === 'resolved' ? '恢复时间' : '最近通知'}：{formatCollectedAt(item.lastSeenAt)}</small></div></div>{item.status !== 'resolved' && <label className={`notification-mute ${item.muted ? 'active' : ''}`} title="开启后，该持续告警不再计入未读通知"><input type="checkbox" checked={Boolean(item.muted)} onChange={(event) => void toggleMuted(item, event.target.checked)} /><i /><span>隐秘告警</span></label>}</article>)}</section></div>
}

function Settings() {
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [activeSection, setActiveSection] = useState<'refresh' | 'profile'>('refresh')
  const [refreshValue, setRefreshValue] = useState(() => String(readRefreshInterval().value))
  const [refreshUnit, setRefreshUnit] = useState(() => readRefreshInterval().unit)
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const changePassword = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setMessage('')
    const form = new FormData(event.currentTarget)
    const newPassword = String(form.get('newPassword') || '')
    if (newPassword !== String(form.get('confirmPassword') || '')) { showMessage('两次新密码不一致'); return }
    setSaving(true)
    try {
      const response = await fetch(`${api}/change-password`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ oldPassword: form.get('oldPassword'), newPassword }) })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '修改失败')
      showMessage('密码已修改')
      event.currentTarget.reset()
    } catch (err) { showMessage(err instanceof Error ? err.message : '修改失败') } finally { setSaving(false) }
  }
  const saveRefreshInterval = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const value = Math.max(1, Number(refreshValue) || 15)
    localStorage.setItem(refreshIntervalStorageKey, JSON.stringify({ value, unit: refreshUnit }))
    setRefreshValue(String(value))
    window.dispatchEvent(new Event(refreshIntervalEvent))
    showMessage('自动刷新频率已更新')
  }
  return <div className="page"><PageHead title="系统设置" description="Prometheus 通过数据源接入；可统一调整页面自动刷新频率。" />{message && <div className="toast">{message}</div>}<section className="settings-layout"><aside className="surface settings-tabs" aria-label="配置目录"><button type="button" className={activeSection === 'refresh' ? 'active' : ''} onClick={() => setActiveSection('refresh')}>自动刷新</button><button type="button" className={activeSection === 'profile' ? 'active' : ''} onClick={() => setActiveSection('profile')}>密码修改</button></aside><section className="surface settings">{activeSection === 'refresh' ? <div className="form-section"><h3>自动刷新</h3><form className="settings-form" onSubmit={saveRefreshInterval}><label>刷新频率 <span className="required-mark">*</span><span className="frequency-input"><input value={refreshValue} type="number" min="1" step="1" onChange={(event) => setRefreshValue(event.target.value)} required /><AppSelect value={refreshUnit} onChange={setRefreshUnit} options={[{ value: 's', label: '秒' }, { value: 'm', label: '分钟' }, { value: 'h', label: '小时' }]} /></span></label><div className="settings-actions"><button className="button" type="submit">保存刷新频率</button></div></form></div> : <div className="form-section"><h3>密码修改</h3><form className="settings-form" onSubmit={changePassword}><label>原密码 <span className="required-mark">*</span><input name="oldPassword" type="password" autoComplete="current-password" required /></label><label>新密码 <span className="required-mark">*</span><input name="newPassword" type="password" autoComplete="new-password" required /></label><label>确认新密码 <span className="required-mark">*</span><input name="confirmPassword" type="password" autoComplete="new-password" required /></label><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存密码'}</button></div></form></div>}</section></section></div>
}

function formatCollectedAt(value?: string) {
  if (!value) return '待检测'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export default App
