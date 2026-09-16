import { useEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties, FormEvent, KeyboardEvent, ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { BrowserRouter, NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import './App.css'
import './DashboardWater.css'
import ConsulWorkspace from './ConsulWorkspace'

type Source = { id: string; name: string; type: string; host: string; port: string; enabled: boolean; status: string; lastTest: string; username?: string; password?: string; database?: string; remark?: string; options?: Record<string, string> }
type PrometheusRule = { name: string; type: string; query: string; duration: number; frequency?: number; health: string; state?: string; severity?: string; summary?: string; description?: string; group: string; file?: string; labels?: Record<string, string>; annotations?: Record<string, string>; sourceId?: string; sourceName?: string }
type NotificationItem = { id: string; ruleId?: string; ruleName: string; source?: string; database?: string; table?: string; field?: string; status: string; message: string; unread: boolean; muted?: boolean; firstSeenAt: string; lastSeenAt: string; resolvedAt?: string }
type MySQLAsyncJob = { id: string; sourceId: string; kind: string; status: string; progress: number; message: string; result?: any; fileName?: string; createdAt: string; finishedAt?: string }
type SQLHistoryItem = { id: number; database: string; statement: string; status: string; resultRows: number; affected: number; durationMs: number; error?: string; username: string; createdAt: string }
type NotificationGroup = { key: string; ruleName: string; status: string; unread: boolean; muted: boolean; message: string; alertMessage: string; firstSeenAt: string; lastSeenAt: string; latestAlertAt?: string; resolvedAt?: string; representative: NotificationItem }
type CollectionRule = { id: string; name: string; source: string; database: string; table: string; field: string; condition: string; threshold?: string; timeWindow: string; frequency?: string; remark?: string; lastRun: string; lastEvaluatedAt?: string; resultDetails?: string; status: string }
type DashboardItem = { id: string; name: string; sourceId: string; sourceName: string; sourceType: string; createdAt: string }
type HadoopMenuItem = { sourceId: string; name?: string }
type AmbariMenuItem = { sourceId: string; name?: string }
type MySQLMenuItem = { sourceId: string; name?: string }
type ConsulMenuItem = { sourceId: string; name?: string }
type AmbariOverview = { clusterName: string; version: string; stack: string; services: Array<{ name: string; state: string; maintenance: string }>; hosts: Array<{ name: string; status: string; maintenance: string }>; alerts: Array<{ label: string; state: string; text: string; host: string; service: string; time: number }>; requests: Array<{ id: number; context: string; status: string; startTime: number; endTime: number }>; configs: Record<string, string> }
type MySQLSQLSample = { schemaName?: string; digest?: string; queryText: string; count: number; totalLatencyMs: number; averageLatencyMs: number; maxLatencyMs: number; rowsExamined: number; rowsSent: number; firstSeen?: string; lastSeen?: string }
type PrometheusMetric = { name: string }
type MetricCategory = { id: string; label: string; matches: (name: string) => boolean }
type SelectOption = { value: string; label: string; disabled?: boolean }
type PlatformLink = { id: string; name: string; url: string }
type RefreshSetting = { value: number; unit: 's' | 'm' | 'h'; configured?: boolean }
type UpdateFrequencyScope = 'general' | 'datasource' | 'hadoop' | 'dashboard' | 'consul'

function DataSourceDeleteDialog({ sourceName, onClose, onConfirm }: { sourceName: string; onClose: () => void; onConfirm: () => void }) {
  return <div className="modal-backdrop source-delete-backdrop" role="presentation" onClick={onClose}><section className="surface source-delete-modal" role="dialog" aria-modal="true" aria-labelledby="source-delete-title" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="source-delete-title">删除数据源</h2><p>{sourceName}</p></div><button className="close-button" type="button" aria-label="关闭" onClick={onClose}>×</button></header><div className="source-delete-content"><b>删除后无法恢复</b><span>该数据源配置、关联告警规则和导入的大屏将一并删除。</span></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={onClose}>取消</button><button className="button danger-button" type="button" onClick={onConfirm}>确认删除</button></footer></section></div>
}

const api = '/api'
const refreshIntervalEvent = 'opsguard-refresh-interval-change'
const platformLinksEvent = 'opsguard-platform-links-change'
const platformNameEvent = 'opsguard-platform-name-change'
let refreshIntervalCache: RefreshSetting = { value: 15, unit: 's' }
let platformNameCache = 'OpsGuard'

async function loadPlatformName() {
  try {
    const response = await fetch(`${api}/platform-settings`)
    const data = await response.json()
    if (response.ok && String(data.platformName || '').trim()) platformNameCache = String(data.platformName).trim()
  } catch { /* Keep the local default when the login page is offline. */ }
  return platformNameCache
}

function usePlatformName() {
  const [name, setName] = useState(platformNameCache)
  useEffect(() => {
    void loadPlatformName().then(setName)
    const update = () => setName(platformNameCache)
    window.addEventListener(platformNameEvent, update)
    return () => window.removeEventListener(platformNameEvent, update)
  }, [])
  return name
}

async function loadPlatformLinks(): Promise<PlatformLink[]> {
  try {
    const response = await fetch(`${api}/platform-links`)
    const data = await response.json()
    return response.ok && Array.isArray(data.links) ? data.links : []
  } catch { return [] }
}

async function savePlatformLinks(links: PlatformLink[]): Promise<PlatformLink[]> {
  const response = await fetch(`${api}/platform-links`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ links }) })
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || '保存常用平台失败')
  window.dispatchEvent(new Event(platformLinksEvent))
  return Array.isArray(data.links) ? data.links : links
}

function readRefreshInterval() {
  const { value, unit } = refreshIntervalCache
  return { value, unit, milliseconds: value * (unit === 'h' ? 3600000 : unit === 'm' ? 60000 : 1000) }
}

async function loadRefreshSettings(): Promise<RefreshSetting> {
  try {
    const response = await fetch(`${api}/settings/refresh`)
    const data = await response.json()
    if (!response.ok) return refreshIntervalCache
    const setting: RefreshSetting = { value: Math.max(1, Number(data.value) || 15), unit: ['s', 'm', 'h'].includes(data.unit) ? data.unit : 's', configured: Boolean(data.configured) }
    refreshIntervalCache = setting
    window.dispatchEvent(new Event(refreshIntervalEvent))
    return setting
  } catch { return refreshIntervalCache }
}

function useRefreshInterval() {
  const [interval, setIntervalValue] = useState(() => readRefreshInterval().milliseconds)
  useEffect(() => {
    const update = () => setIntervalValue(readRefreshInterval().milliseconds)
    void loadRefreshSettings().then(() => update())
    window.addEventListener(refreshIntervalEvent, update)
    return () => window.removeEventListener(refreshIntervalEvent, update)
  }, [])
  return interval
}

const updateFrequencyDefaults: Record<Exclude<UpdateFrequencyScope, 'general'>, RefreshSetting> = {
  datasource: { value: 2, unit: 'm' },
  hadoop: { value: 5, unit: 'm' },
  dashboard: { value: 1, unit: 'm' },
  consul: { value: 1, unit: 'm' },
}

async function loadScopedRefreshSettings(scope: UpdateFrequencyScope): Promise<RefreshSetting> {
  const fallback = scope === 'general' ? refreshIntervalCache : updateFrequencyDefaults[scope]
  try {
    const suffix = scope === 'general' ? '' : `?scope=${encodeURIComponent(scope)}`
    const response = await fetch(`${api}/settings/refresh${suffix}`)
    const data = await response.json()
    if (!response.ok) return fallback
    return { value: Math.max(1, Number(data.value) || fallback.value), unit: ['s', 'm', 'h'].includes(data.unit) ? data.unit : fallback.unit, configured: Boolean(data.configured) }
  } catch { return fallback }
}

async function saveScopedRefreshSettings(scope: UpdateFrequencyScope, setting: RefreshSetting): Promise<RefreshSetting> {
  const suffix = scope === 'general' ? '' : `?scope=${encodeURIComponent(scope)}`
  const response = await fetch(`${api}/settings/refresh${suffix}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(setting) })
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || '保存更新频率失败')
  const saved: RefreshSetting = { value: Math.max(1, Number(data.value) || setting.value), unit: ['s', 'm', 'h'].includes(data.unit) ? data.unit : setting.unit, configured: true }
  if (scope === 'general') refreshIntervalCache = saved
  window.dispatchEvent(new Event('opsguard-update-frequency-change'))
  return saved
}

function useScopedRefreshInterval(scope: Exclude<UpdateFrequencyScope, 'general'>) {
  const fallback = updateFrequencyDefaults[scope]
  const [interval, setIntervalValue] = useState(() => fallback.value * (fallback.unit === 'h' ? 3600000 : fallback.unit === 'm' ? 60000 : 1000))
  useEffect(() => {
    const update = () => { void loadScopedRefreshSettings(scope).then(setting => setIntervalValue(setting.value * (setting.unit === 'h' ? 3600000 : setting.unit === 'm' ? 60000 : 1000))) }
    update()
    window.addEventListener('opsguard-update-frequency-change', update)
    return () => window.removeEventListener('opsguard-update-frequency-change', update)
  }, [scope])
  return interval
}
const icons: Record<string, string> = { query: '◎', dashboard: '▦', data: '◫', alert: '◇', notify: '◉', audit: '◷', settings: '⚙', plus: '+', arrow: '→', bell: '' }
const metricCategories: MetricCategory[] = [
  { id: 'mysql', label: 'MySQL', matches: name => name.includes('mysql') },
  { id: 'node', label: '主机', matches: name => name.startsWith('node_') },
  { id: 'prometheus', label: 'Prometheus', matches: name => name.startsWith('prometheus_') },
  { id: 'scrape', label: '采集', matches: name => name.startsWith('scrape_') || name === 'up' },
]
function Icon({ name }: { name: string }) { return <span className={`icon icon-${name}`} aria-hidden="true">{icons[name]}</span> }

function SidebarIcon({ name }: { name: string }) {
  const paths: Record<string, ReactNode> = {
    dashboard: <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></>,
    query: <><path d="M4 19V5" /><path d="M4 19h17" /><path d="m7 15 4-4 3 2 5-6" /><circle cx="19" cy="7" r="1" fill="currentColor" stroke="none" /></>,
    alert: <><path d="M12 4 3.7 19h16.6L12 4Z" /><path d="M12 9v4" /><path d="M12 16h.01" /></>,
    notify: <><path d="M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9" /><path d="M10 22h4" /></>,
    data: <><rect x="3" y="4" width="18" height="6" rx="1.5" /><rect x="3" y="14" width="18" height="6" rx="1.5" /><path d="M7 7h.01M7 17h.01M11 7h6M11 17h6" /></>,
    audit: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3.5 2" /></>,
    consul: <><path d="M6 4h12v16H6z" /><path d="M9 8h6M9 12h6M9 16h3" /></>,
    settings: <><path d="M4 6h3m4 0h9M4 12h9m4 0h3M4 18h1m4 0h11" /><circle cx="9" cy="6" r="2" /><circle cx="15" cy="12" r="2" /><circle cx="7" cy="18" r="2" /></>,
  }
  return <svg className="sidebar-nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>
}

function Tooltip({ content, children }: { content: string; children: ReactNode }) {
  return <span className="platform-tooltip" tabIndex={0}>{children}<span className="platform-tooltip-content" role="tooltip">{content}</span></span>
}

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

function nextCollectionCheckTime(rule: CollectionRule) {
  const lastEvaluatedAt = new Date(rule.lastEvaluatedAt || '').getTime()
  if (!Number.isFinite(lastEvaluatedAt)) return '待首次检测'
  const parts = frequencyParts(rule.frequency)
  const milliseconds = Number(parts.value) * (parts.unit === 'h' ? 3600000 : parts.unit === 'm' ? 60000 : 1000)
  return new Date(lastEvaluatedAt + milliseconds).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
}

function collectionRuleResultLabel(lastRun?: string) {
  const value = String(lastRun || '').trim()
  if (!value || value === '待执行') return '待执行'
  if (/告警|异常|失败/.test(value)) return '检测异常'
  if (/恢复/.test(value)) return '恢复信息'
  return '检测正常'
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

function groupNotifications(items: NotificationItem[]): NotificationGroup[] {
  const groups = new Map<string, NotificationItem[]>()
  for (const item of items) {
    // Some upstream Prometheus notifications share a source-level rule ID.
    // The rule name keeps distinct upstream rules from being collapsed while
    // repeated events for the same rule (including data-source alerts) merge.
    const key = `${item.ruleId || item.source || ''}:${item.ruleName}`
    groups.set(key, [...(groups.get(key) || []), item])
  }
  return [...groups.entries()].map(([key, entries]) => {
    const sorted = [...entries].sort((left, right) => new Date(right.lastSeenAt).getTime() - new Date(left.lastSeenAt).getTime())
    const active = sorted.find(item => item.status === 'active')
    const latestAlert = sorted.find(item => item.status === 'active' || item.status === 'alert')
    const latestResolved = sorted.find(item => item.status === 'resolved')
    const first = [...entries].sort((left, right) => new Date(left.firstSeenAt).getTime() - new Date(right.firstSeenAt).getTime())[0]
    const representative = active || latestAlert || sorted[0]
    const resolvedIsLatest = Boolean(latestResolved && (!latestAlert || new Date(latestResolved.lastSeenAt).getTime() >= new Date(latestAlert.lastSeenAt).getTime()))
    return {
      key,
      ruleName: representative.ruleName,
      status: active ? 'active' : resolvedIsLatest ? 'resolved' : sorted[0].status,
      unread: entries.some(item => item.unread),
      muted: Boolean(active?.muted),
      message: resolvedIsLatest ? latestResolved!.message : (latestAlert || sorted[0]).message,
      alertMessage: (latestAlert || sorted[0]).message,
      firstSeenAt: first.firstSeenAt,
      lastSeenAt: sorted[0].lastSeenAt,
      latestAlertAt: latestAlert?.lastSeenAt,
      resolvedAt: latestResolved?.lastSeenAt,
      representative,
    }
  }).sort((left, right) => new Date(right.lastSeenAt).getTime() - new Date(left.lastSeenAt).getTime())
}

function timeoutSeconds(value?: string) {
  const matched = String(value || '').match(/\d+(?:\.\d+)?/)
  return matched?.[0] || '5'
}

function ModalScrollLock() {
  useEffect(() => {
    const body = document.body
    const initialOverflow = body.style.overflow
    const initialPaddingRight = body.style.paddingRight
    let locked = false
    const sync = () => {
      const hasModal = Boolean(document.querySelector('.modal-backdrop'))
      if (hasModal && !locked) {
        const scrollbarWidth = window.innerWidth - document.documentElement.clientWidth
        body.style.overflow = 'hidden'
        if (scrollbarWidth > 0) body.style.paddingRight = `${scrollbarWidth}px`
        locked = true
      } else if (!hasModal && locked) {
        body.style.overflow = initialOverflow
        body.style.paddingRight = initialPaddingRight
        locked = false
      }
    }
    const observer = new MutationObserver(sync)
    observer.observe(document.body, { childList: true, subtree: true })
    sync()
    return () => {
      observer.disconnect()
      body.style.overflow = initialOverflow
      body.style.paddingRight = initialPaddingRight
    }
  }, [])
  return null
}

function App() {
	const [authed, setAuthed] = useState<boolean | null>(null)
	const [sidebarCollapsed, setSidebarCollapsed] = useState(() => window.localStorage.getItem('opsguard-sidebar-collapsed') === 'true')
	useEffect(() => {
		void fetch(`${api}/session`)
			.then(response => setAuthed(response.ok))
			.catch(() => setAuthed(false))
	}, [])
  const logout = async () => {
    await fetch(`${api}/logout`, { method: 'POST' }).catch(() => {})
    setAuthed(false)
  }
	if (authed === null) return <div className="login-page"><section className="surface login-panel"><p>正在验证登录状态...</p></section></div>
  if (!authed) return <Login onLogin={() => setAuthed(true)} />
	const toggleSidebar = () => setSidebarCollapsed(current => { const next = !current; window.localStorage.setItem('opsguard-sidebar-collapsed', String(next)); return next })
	return <BrowserRouter><ModalScrollLock /><div className={`app-shell ${sidebarCollapsed ? 'sidebar-collapsed' : ''}`}><Sidebar collapsed={sidebarCollapsed} onToggle={toggleSidebar} /><main className="workspace"><TopNav onLogout={logout} /><Routes><Route path="/" element={<Dashboards />} /><Route path="/metrics" element={<MetricQuery />} /><Route path="/hadoop" element={<HadoopYarn />} /><Route path="/ambari" element={<Ambari />} /><Route path="/mysql" element={<MySQLWorkspace />} /><Route path="/consul" element={<ConsulWorkspace />} /><Route path="/datasources" element={<DataSources />} /><Route path="/alerts" element={<Alerts />} /><Route path="/notifications" element={<Notifications />} /><Route path="/logs" element={<AuditLogs />} /><Route path="/config" element={<Settings />} /></Routes></main></div></BrowserRouter>
}

function Login({ onLogin }: { onLogin: () => void }) {
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const platformName = usePlatformName()
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setLoading(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      const response = await fetch(`${api}/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: form.get('username'), password: form.get('password') }) })
      const text = await response.text()
      let result: { error?: string } = {}
      try { result = text ? JSON.parse(text) : {} } catch { throw new Error(response.ok ? '登录服务未返回有效数据，请稍后重试' : '登录服务暂不可用，请稍后重试') }
      if (!response.ok) throw new Error(result.error || '登录失败')
      onLogin()
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setLoading(false)
    }
  }
  return <div className="login-page"><div className="login-scene" aria-hidden="true"><i className="scene-orbit orbit-one" /><i className="scene-orbit orbit-two" /><i className="scene-scan" /><span className="scene-node node-one" /><span className="scene-node node-two" /><span className="scene-node node-three" /><span className="scene-node node-four" /></div><div className="login-content"><section className="login-value"><span className="login-kicker">OPS INTELLIGENCE / 24×7</span><h2>让每一次巡检<br /><em>都可观测、可追溯</em></h2><p>统一接入 Agent、Hadoop、MySQL 与主机节点，实时发现风险，让复杂基础设施保持稳定运行。</p><div className="login-value-grid"><span><b>实时监控</b><small>指标持续采集</small></span><span><b>智能告警</b><small>异常快速定位</small></span><span><b>安全审计</b><small>操作全程留痕</small></span></div><div className="login-signal"><i /> PLATFORM ONLINE <b>SECURE CHANNEL</b></div></section><section className="surface login-panel"><div className="login-brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>{platformName}</b><span>运维巡检平台</span></div></div><h1>登录平台</h1><p>请输入管理员账号继续。</p><form onSubmit={submit}><label>用户名<input name="username" defaultValue="admin" autoComplete="username" required /></label><label>密码<input name="password" type="password" autoComplete="current-password" required /></label>{error && <span className="login-error">{error}</span>}<button className="button" type="submit" disabled={loading}>{loading ? '登录中...' : '登录'}</button></form></section></div></div>
}

function TopNav({ onLogout }: { onLogout: () => void }) {
  const location = useLocation()
  const navigate = useNavigate()
  const titles: Record<string, string> = { '/': '监控大屏', '/metrics': '指标查询', '/mysql': 'MySQL 工作台', '/consul': 'Consul KV 工作台', '/hadoop': 'Hadoop / YARN', '/ambari': 'Ambari', '/datasources': '数据节点', '/alerts': '告警规则', '/notifications': '通知中心', '/logs': '日志中心', '/config': '系统设置' }
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
  return <header className="page-nav"><div className="nav-path"><b>{titles[location.pathname] || '指标查询'}</b></div><div className="nav-tools"><PlatformMenu />{location.pathname !== '/config' && location.pathname !== '/logs' && <RefreshButton onClick={() => window.setTimeout(() => window.location.reload(), 360)} />}<div ref={notificationRoot} className="notification-menu"><button className={`bell-button ${notificationOpen ? 'active' : ''}`} type="button" aria-label="通知" aria-expanded={notificationOpen} onClick={toggleNotifications}><Icon name="bell" />{unread > 0 && <i>{unread > 99 ? '99+' : unread}</i>}</button>{notificationOpen && <section className="surface notification-popover"><header><b>未读消息</b><span>{unread > 0 ? `${unread} 条未读` : '已全部阅读'}</span></header><div className="notification-popover-list">{notificationsLoading ? <span className="notification-popover-empty">正在加载...</span> : notifications.length === 0 ? <span className="notification-popover-empty">暂无未读消息</span> : notifications.map(item => <button type="button" key={item.id} onClick={() => { setNotificationOpen(false); navigate('/notifications') }}><i className={`notification-dot ${item.status === 'resolved' ? 'success' : 'danger'}`} /><span><b>{item.ruleName}</b><small>{item.message}</small><time>{formatCollectedAt(item.lastSeenAt)}</time></span></button>)}</div><footer><button type="button" disabled={unread === 0 || notificationsLoading} onClick={() => void markAllRead()}>全部已读</button></footer></section>}</div><AccountMenu onLogout={onLogout} /></div></header>
}

function PlatformMenu() {
  const [open, setOpen] = useState(false)
  const [links, setLinks] = useState<PlatformLink[]>([])
  const root = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const refresh = () => { void loadPlatformLinks().then(setLinks) }
    refresh()
    window.addEventListener(platformLinksEvent, refresh)
    return () => window.removeEventListener(platformLinksEvent, refresh)
  }, [])
  useEffect(() => {
    if (!open) return
    const close = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('pointerdown', close)
    return () => document.removeEventListener('pointerdown', close)
  }, [open])
  return <div ref={root} className="platform-menu"><button className={`platform-menu-trigger ${open ? 'active' : ''}`} type="button" onClick={() => setOpen(value => !value)}>常用平台</button>{open && <section className="surface platform-popover">{links.length === 0 ? <span>暂未配置常用平台</span> : links.map(link => <a key={link.id} href={link.url} target="_blank" rel="noreferrer" onClick={() => setOpen(false)}><b>{link.name}</b><small>{link.url}</small></a>)}<a className="platform-manage" href="/config#platforms" onClick={() => setOpen(false)}>管理常用平台</a></section>}</div>
}

function PlatformNameSettings() {
  const [name, setName] = useState(platformNameCache)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  useEffect(() => { void loadPlatformName().then(setName) }, [])
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setSaving(true)
    try {
      const response = await fetch(`${api}/platform-settings`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ platformName: name.trim() }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存平台名称失败')
      platformNameCache = String(data.platformName || name).trim() || 'OpsGuard'
      setName(platformNameCache); window.dispatchEvent(new Event(platformNameEvent)); setMessage('平台名称已保存')
    } catch (error) { setMessage(error instanceof Error ? error.message : '保存平台名称失败') } finally { setSaving(false) }
  }
  return <div className="form-section"><h3>平台名称</h3><form className="settings-form" onSubmit={save}><label>名称 <span className="required-mark">*</span><input value={name} maxLength={40} placeholder="例如：生产运维平台" onChange={event => setName(event.target.value)} required /><small>用于区分多套平台，会同步显示在左上角和登录页。</small></label><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存平台名称'}</button></div>{message && <span className="settings-inline-message">{message}</span>}</form></div>
}

function PlatformLinkSettings() {
  const [links, setLinks] = useState<PlatformLink[]>([])
  const [name, setName] = useState('')
  const [url, setURL] = useState('')
  const [error, setError] = useState('')
  useEffect(() => { void loadPlatformLinks().then(setLinks) }, [])
  const save = async (next: PlatformLink[]) => {
    try {
      const saved = await savePlatformLinks(next)
      setLinks(saved)
      setError('')
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存常用平台失败')
      return false
    }
  }
  const add = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const rawURL = url.trim()
    const normalizedURL = /^https?:\/\//i.test(rawURL) ? rawURL : `http://${rawURL}`
    try { new URL(normalizedURL) } catch { setError('请输入有效的平台地址'); return }
    if (!name.trim()) { setError('请输入平台名称'); return }
    if (await save([...links, { id: `${Date.now()}`, name: name.trim(), url: normalizedURL }])) { setName(''); setURL('') }
  }
  return <div className="platform-settings"><h3>常用平台</h3><p>配置后会显示在每个页面标题右侧，点击将在新标签页打开。</p><form onSubmit={add}><input value={name} maxLength={30} placeholder="平台名称，例如 Grafana" onChange={(event) => setName(event.target.value)} /><input value={url} placeholder="平台地址，例如 127.0.0.1:3000" onChange={(event) => setURL(event.target.value)} /><button className="button" type="submit">添加链接</button></form>{error && <span className="platform-link-error">{error}</span>}<div className="platform-settings-list">{links.length === 0 ? <span>暂未添加平台链接</span> : links.map(link => <div key={link.id}><div><b>{link.name}</b><small>{link.url}</small></div><button type="button" onClick={() => save(links.filter(item => item.id !== link.id))}>删除</button></div>)}</div></div>
}

function AccountMenu({ onLogout }: { onLogout: () => void }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
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
  return <div ref={root} className="account-menu"><button className="user-avatar" type="button" aria-expanded={open} aria-haspopup="menu" onClick={() => setOpen(current => !current)}><span>管</span></button>{open && <div className="surface account-popover" role="menu"><button type="button" role="menuitem" onClick={() => setOpen(false)}>平台管理员</button><button className="danger" type="button" role="menuitem" onClick={onLogout}>退出登录</button></div>}</div>
}

function Sidebar({ collapsed, onToggle }: { collapsed: boolean; onToggle: () => void }) {
  const location = useLocation()
  const platformName = usePlatformName()
  const [sources, setSources] = useState<Source[]>([])
  const [hadoopMenus, setHadoopMenus] = useState<HadoopMenuItem[]>([])
  const [ambariMenus, setAmbariMenus] = useState<AmbariMenuItem[]>([])
  const [mysqlMenus, setMySQLMenus] = useState<MySQLMenuItem[]>([])
  const [consulMenus, setConsulMenus] = useState<ConsulMenuItem[]>([])
  const refreshInterval = useRefreshInterval()
  const load = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources : [])
    } catch { setSources([]) }
  }
  useEffect(() => {
    const reloadHadoopMenus = () => { void loadHadoopMenus().then(setHadoopMenus) }
    const reloadAmbariMenus = () => { void loadAmbariMenus().then(setAmbariMenus) }
    const reloadMySQLMenus = () => { void loadMySQLMenus().then(setMySQLMenus) }
    const reloadConsulMenus = () => { void loadConsulMenus().then(setConsulMenus) }
    void load()
    reloadHadoopMenus()
    reloadAmbariMenus()
    reloadMySQLMenus()
    reloadConsulMenus()
    const timer = window.setInterval(load, refreshInterval)
    window.addEventListener('opsguard-data-sources-change', load)
    window.addEventListener('opsguard-hadoop-menus-change', reloadHadoopMenus)
    window.addEventListener('opsguard-ambari-menus-change', reloadAmbariMenus)
    window.addEventListener('opsguard-mysql-menus-change', reloadMySQLMenus)
    window.addEventListener('opsguard-consul-menus-change', reloadConsulMenus)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('opsguard-data-sources-change', load)
      window.removeEventListener('opsguard-hadoop-menus-change', reloadHadoopMenus)
      window.removeEventListener('opsguard-ambari-menus-change', reloadAmbariMenus)
      window.removeEventListener('opsguard-mysql-menus-change', reloadMySQLMenus)
      window.removeEventListener('opsguard-consul-menus-change', reloadConsulMenus)
    }
  }, [refreshInterval])
  const online = sources.filter(item => item.enabled && item.status === '健康').length
  const importedHadoopSources = hadoopMenus.map(item => ({ item, source: sources.find(source => source.id === item.sourceId && source.type === 'Hadoop' && source.enabled) })).filter((entry): entry is { item: HadoopMenuItem; source: Source } => Boolean(entry.source))
  const importedAmbariSources = ambariMenus.map(item => ({ item, source: sources.find(source => source.id === item.sourceId && source.type === 'Ambari' && source.enabled) })).filter((entry): entry is { item: AmbariMenuItem; source: Source } => Boolean(entry.source))
  const importedMySQLSources = mysqlMenus.map(item => ({ item, source: sources.find(source => source.id === item.sourceId && source.type === 'MySQL' && source.enabled) })).filter((entry): entry is { item: MySQLMenuItem; source: Source } => Boolean(entry.source))
  const importedConsulSources = consulMenus.map(item => ({ item, source: sources.find(source => source.id === item.sourceId && source.type === 'Consul' && source.enabled) })).filter((entry): entry is { item: ConsulMenuItem; source: Source } => Boolean(entry.source))
  const items = [['dashboard', '监控大屏', '/'], ['query', '指标查询', '/metrics'], ...importedMySQLSources.map(({ item, source }) => ['mysql', item.name || source.name, `/mysql?source=${encodeURIComponent(source.id)}`]), ...importedConsulSources.map(({ item, source }) => ['consul', item.name || source.name, `/consul?source=${encodeURIComponent(source.id)}`]), ...importedHadoopSources.map(({ item, source }) => ['hadoop', item.name || source.name, `/hadoop?source=${encodeURIComponent(source.id)}`]), ...importedAmbariSources.map(({ item, source }) => ['ambari', item.name || source.name, `/ambari?source=${encodeURIComponent(source.id)}`]), ['alert', '告警规则', '/alerts'], ['notify', '通知中心', '/notifications'], ['audit', '日志中心', '/logs'], ['data', '数据节点', '/datasources'], ['settings', '系统设置', '/config']]
  const selectedHadoopSourceID = new URLSearchParams(location.search).get('source') || ''
  const selectedAmbariSourceID = new URLSearchParams(location.search).get('source') || ''
  const selectedMySQLSourceID = new URLSearchParams(location.search).get('source') || ''
  const selectedConsulSourceID = new URLSearchParams(location.search).get('source') || ''
  return <aside className={`sidebar ${collapsed ? 'collapsed' : ''}`}><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>{platformName}</b><small>巡检平台</small></div></div><nav>{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} title={collapsed ? label : undefined} className={({ isActive }) => {
    const hadoopSourceID = path.startsWith('/hadoop?source=') ? new URLSearchParams(path.slice(path.indexOf('?') + 1)).get('source') : ''
    const ambariSourceID = path.startsWith('/ambari?source=') ? new URLSearchParams(path.slice(path.indexOf('?') + 1)).get('source') : ''
    const mysqlSourceID = path.startsWith('/mysql?source=') ? new URLSearchParams(path.slice(path.indexOf('?') + 1)).get('source') : ''
    const consulSourceID = path.startsWith('/consul?source=') ? new URLSearchParams(path.slice(path.indexOf('?') + 1)).get('source') : ''
    const active = hadoopSourceID ? location.pathname === '/hadoop' && selectedHadoopSourceID === hadoopSourceID : ambariSourceID ? location.pathname === '/ambari' && selectedAmbariSourceID === ambariSourceID : mysqlSourceID ? location.pathname === '/mysql' && selectedMySQLSourceID === mysqlSourceID : consulSourceID ? location.pathname === '/consul' && selectedConsulSourceID === consulSourceID : isActive
    return `nav-link ${active ? 'active' : ''}`
  }}>{icon === 'hadoop' ? <img className="hadoop-nav-icon" src="/hadoop.svg" alt="" aria-hidden="true" /> : icon === 'mysql' ? <img className="mysql-nav-icon" src="/mysql.svg" alt="" aria-hidden="true" /> : <SidebarIcon name={icon} />}<span>{label}</span></NavLink>)}</nav><div className="sidebar-bottom-actions"><div className="sidebar-footer"><span className="online-dot" /><span>{online} / {sources.length} 数据源在线</span></div><button className="sidebar-collapse" type="button" aria-label={collapsed ? '展开菜单' : '折叠菜单'} title={collapsed ? '展开菜单' : '折叠菜单'} onClick={onToggle}><span aria-hidden="true">{collapsed ? '›' : '‹'}</span></button></div></aside>
}

function PageHead({ action, onAction, actionNode }: { title: string; description: string; action?: string; onAction?: () => void; actionNode?: any }) {
  if (!action && !actionNode) return null
  return <header className="page-head compact"><span />{actionNode || <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header>
}

function AppSelect({ value, options, onChange, name, placeholder = '请选择', disabled = false, className = '' }: { value: string; options: SelectOption[]; onChange: (value: string) => void; name?: string; placeholder?: string; disabled?: boolean; className?: string }) {
  const [open, setOpen] = useState(false)
  const [openUpward, setOpenUpward] = useState(false)
  const root = useRef<HTMLSpanElement>(null)
  const menu = useRef<HTMLSpanElement>(null)
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({})
  const typeExtras: SelectOption[] = [{ value: 'Ambari', label: 'Ambari' }, { value: 'Redis', label: 'Redis' }, { value: 'ClickHouse', label: 'ClickHouse' }, { value: 'Kafka', label: 'Kafka' }]
  const effectiveOptions = name === 'type'
    ? [...options, ...typeExtras.filter(extra => !options.some(option => option.value === extra.value))]
    : options
  const selected = effectiveOptions.find(option => option.value === value)
  const updateMenuPosition = () => {
    if (!root.current) return
    const rect = root.current.getBoundingClientRect()
    const below = window.innerHeight - rect.bottom - 12
    const above = rect.top - 12
    const upward = below < 220 && above > below
    const available = upward ? above : below
    setOpenUpward(upward)
    setMenuStyle({
      left: Math.max(8, rect.left),
      top: upward ? undefined : rect.bottom + 6,
      bottom: upward ? window.innerHeight - rect.top + 6 : undefined,
      width: rect.width,
      maxHeight: Math.max(80, Math.min(248, available - 6)),
    })
  }
  useEffect(() => {
    if (!open) return
    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node) && !menu.current?.contains(event.target as Node)) setOpen(false)
    }
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', closeOnOutsideClick)
    document.addEventListener('keydown', closeOnEscape)
    window.addEventListener('resize', updateMenuPosition)
    window.addEventListener('scroll', updateMenuPosition, true)
    updateMenuPosition()
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsideClick)
      document.removeEventListener('keydown', closeOnEscape)
      window.removeEventListener('resize', updateMenuPosition)
      window.removeEventListener('scroll', updateMenuPosition, true)
    }
  }, [open])
  const toggle = () => {
    if (!open) updateMenuPosition()
    setOpen(current => !current)
  }
  return <span ref={root} className={`app-select ${open ? 'open' : ''} ${openUpward ? 'open-upward' : ''} ${disabled ? 'disabled' : ''} ${className}`}>
    {name && <input type="hidden" name={name} value={value} />}
    <button className="app-select-trigger" type="button" disabled={disabled} aria-haspopup="listbox" aria-expanded={open} onClick={toggle}><span className={selected ? '' : 'placeholder'}>{selected?.label || placeholder}</span></button>
    {open && createPortal(<span ref={menu} className={`app-select-menu ${openUpward ? 'open-upward' : ''}`} style={menuStyle} role="listbox">{effectiveOptions.map(option => <button className={option.value === value ? 'active' : ''} type="button" role="option" aria-selected={option.value === value} disabled={option.disabled} key={option.value} onClick={() => { onChange(option.value); setOpen(false) }}>{option.label}</button>)}</span>, document.body)}
  </span>
}

function MySQLPagination({ page, pageSize, total, totalPages, onPageChange, onPageSizeChange, ariaLabel, truncated = false }: { page: number; pageSize: number; total: number; totalPages: number; onPageChange: (page: number) => void; onPageSizeChange: (pageSize: number) => void; ariaLabel: string; truncated?: boolean }) {
  const safeTotalPages = Math.max(1, totalPages)
  return <footer className="mysql-pagination"><div><span>共 {total}{truncated ? '+' : ''} 条{truncated && <em>最多展示 {total} 条</em>}</span><span className="mysql-page-size">每页 <AppSelect className="mysql-page-size-select" value={String(pageSize)} onChange={value => onPageSizeChange(Number(value))} options={[50, 100, 200, 500].map(value => ({ value: String(value), label: String(value) }))} /> 条</span></div><nav aria-label={ariaLabel}><button type="button" disabled={page <= 1} onClick={() => onPageChange(1)}>首页</button><button type="button" disabled={page <= 1} onClick={() => onPageChange(Math.max(1, page - 1))}>上一页</button><span className="mysql-page-current">{page} / {safeTotalPages}</span><button type="button" disabled={page >= safeTotalPages} onClick={() => onPageChange(Math.min(safeTotalPages, page + 1))}>下一页</button><button type="button" disabled={page >= safeTotalPages} onClick={() => onPageChange(safeTotalPages)}>尾页</button></nav></footer>
}

function SQLResultView({ columns, rows, page, pageSize, total, totalPages, truncated, onPageChange, onPageSizeChange }: { columns: string[]; rows: any[][]; page: number; pageSize: number; total: number; totalPages: number; truncated: boolean; onPageChange: (page: number) => void; onPageSizeChange: (pageSize: number) => void }) {
  const [fullscreen, setFullscreen] = useState(false)
  const resultTable = <div className="mysql-sql-result"><table><thead><tr>{columns.map(column => <th key={column}>{column}</th>)}</tr></thead><tbody>{rows.map((row, index) => <tr key={(page - 1) * pageSize + index}>{row.map((value, cell) => <td key={cell}>{value === null ? <i>NULL</i> : String(value)}</td>)}</tr>)}</tbody></table></div>
  const pagination = <MySQLPagination page={page} pageSize={pageSize} total={total} totalPages={totalPages} truncated={truncated} onPageChange={onPageChange} onPageSizeChange={onPageSizeChange} ariaLabel="SQL 查询结果分页" />
  return <><section className="mysql-sql-result-view"><header className="mysql-sql-result-head"><span>查询结果</span><button className="button secondary" type="button" onClick={() => setFullscreen(true)}>全屏查看</button></header>{resultTable}{pagination}</section>{fullscreen && <div className="sql-result-fullscreen" role="dialog" aria-modal="true"><header><div><b>SQL 查询结果</b><span>共 {total}{truncated ? '+' : ''} 条</span></div><button className="close-button" type="button" aria-label="关闭全屏结果" onClick={() => setFullscreen(false)}>×</button></header>{resultTable}{pagination}</div>}</>
}

function UnifiedPagination({ total, page, totalPages, onPageChange, ariaLabel, truncated = false }: { total: number; page: number; totalPages: number; onPageChange: (page: number) => void; ariaLabel: string; truncated?: boolean }) {
  const pages = Math.max(1, totalPages)
  const current = Math.min(Math.max(1, page), pages)
  return <footer className="mysql-pagination platform-pagination"><div><span>共 {total}{truncated ? '+' : ''} 条</span></div><nav aria-label={ariaLabel}><button type="button" disabled={current <= 1} onClick={() => onPageChange(1)}>首页</button><button type="button" disabled={current <= 1} onClick={() => onPageChange(current - 1)}>上一页</button><span className="mysql-page-current">{current} / {pages}</span><button type="button" disabled={current >= pages} onClick={() => onPageChange(current + 1)}>下一页</button><button type="button" disabled={current >= pages} onClick={() => onPageChange(pages)}>尾页</button></nav></footer>
}

function downloadCSV(filename: string, columns: string[], rows: any[][]) {
  const escapeCell = (value: any) => {
    let text = value === null || value === undefined ? 'NULL' : String(value)
    if (/^[=+\-@]/.test(text)) text = `'${text}`
    return `"${text.replaceAll('"', '""')}"`
  }
  const content = '\uFEFF' + [columns, ...rows].map(row => row.map(escapeCell).join(',')).join('\r\n')
  const url = URL.createObjectURL(new Blob([content], { type: 'text/csv;charset=utf-8' }))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename.replace(/[\\/:*?"<>|]+/g, '_')
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function SSHSourceMultiSelect({ sources, value, onChange }: { sources: Source[]; value: string[]; onChange: (value: string[]) => void }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLSpanElement>(null)
  const menu = useRef<HTMLSpanElement>(null)
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({})
  const selectedIDs = value.filter(id => sources.some(source => source.id === id))
  const allSelected = sources.length > 0 && selectedIDs.length === sources.length
  const label = sources.length === 0 ? '暂无可用主机节点' : allSelected ? `全部节点（${sources.length}）` : selectedIDs.length > 0 ? `已选 ${selectedIDs.length} 个节点` : '请选择节点'
  const updateMenuPosition = () => {
    if (!root.current) return
    const rect = root.current.getBoundingClientRect()
    const below = window.innerHeight - rect.bottom - 12
    const above = rect.top - 12
    const upward = below < 240 && above > below
    const available = upward ? above : below
    setMenuStyle({ left: Math.max(8, rect.left), top: upward ? undefined : rect.bottom + 6, bottom: upward ? window.innerHeight - rect.top + 6 : undefined, width: rect.width, maxHeight: Math.max(100, Math.min(280, available - 6)) })
  }
  useEffect(() => {
    if (!open) return
    const close = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node) && !menu.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: globalThis.KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('pointerdown', close)
    document.addEventListener('keydown', escape)
    window.addEventListener('resize', updateMenuPosition)
    window.addEventListener('scroll', updateMenuPosition, true)
    updateMenuPosition()
    return () => { document.removeEventListener('pointerdown', close); document.removeEventListener('keydown', escape); window.removeEventListener('resize', updateMenuPosition); window.removeEventListener('scroll', updateMenuPosition, true) }
  }, [open])
  const toggleAll = () => onChange(allSelected ? [] : sources.map(source => source.id))
  const toggleSource = (id: string) => onChange(selectedIDs.includes(id) ? selectedIDs.filter(value => value !== id) : [...selectedIDs, id])
  return <span ref={root} className={`ssh-source-select ${open ? 'open' : ''} ${sources.length === 0 ? 'disabled' : ''}`}><button className="ssh-source-select-trigger" type="button" disabled={sources.length === 0} aria-haspopup="listbox" aria-expanded={open} onClick={() => { if (!open) updateMenuPosition(); setOpen(current => !current) }}><span className={selectedIDs.length > 0 ? '' : 'placeholder'}>{label}</span></button>{open && createPortal(<span ref={menu} className="ssh-source-select-menu" style={menuStyle} role="listbox" aria-multiselectable="true"><button className={`ssh-source-select-all ${allSelected ? 'active' : ''}`} type="button" role="option" aria-selected={allSelected} onClick={toggleAll}><i>{allSelected ? '✓' : ''}</i><span>全部节点</span><small>{sources.length} 个可用节点</small></button>{sources.map(source => <button className={selectedIDs.includes(source.id) ? 'active' : ''} type="button" role="option" aria-selected={selectedIDs.includes(source.id)} key={source.id} onClick={() => toggleSource(source.id)}><i>{selectedIDs.includes(source.id) ? '✓' : ''}</i><span>{source.name}</span><small>{source.host}{source.port ? `:${source.port}` : ''}</small></button>)}</span>, document.body)}</span>
}

function RefreshButton({ loading, disabled, onClick }: { loading?: boolean; disabled?: boolean; onClick: () => void }) {
  const [clicked, setClicked] = useState(false)
  const refreshing = Boolean(loading || clicked)
  const handleClick = () => {
    if (refreshing || disabled) return
    setClicked(true)
    window.setTimeout(() => setClicked(false), 900)
    onClick()
  }
  return <button className={`refresh-button ${refreshing ? 'refreshing' : ''}`} type="button" onClick={handleClick} disabled={disabled || refreshing}>{refreshing ? '刷新中...' : '刷新'}</button>
}

function ListPagination({ total, page, onPageChange }: { total: number; page: number; onPageChange: (page: number) => void }) {
  const pageSize = 20
  const pages = Math.max(1, Math.ceil(total / pageSize))
  if (total === 0) return null
  return <UnifiedPagination total={total} page={page} totalPages={pages} onPageChange={onPageChange} ariaLabel="列表分页" />
}

async function loadDashboards(): Promise<DashboardItem[]> {
  try {
    const response = await fetch(`${api}/dashboards`)
    const data = await response.json()
    return response.ok && Array.isArray(data.items) ? data.items : []
  } catch { return [] }
}

async function saveDashboardItem(item: DashboardItem): Promise<DashboardItem> {
  const response = await fetch(`${api}/dashboards`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(item) })
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || '导入监控大屏失败')
  window.dispatchEvent(new Event('opsguard-dashboards-change'))
  return data as DashboardItem
}

async function deleteDashboardItem(id: string): Promise<void> {
  const response = await fetch(`${api}/dashboards/${encodeURIComponent(id)}`, { method: 'DELETE' })
  const data = await response.json()
  if (!response.ok && response.status !== 404) throw new Error(data.error || '删除监控大屏失败')
  window.dispatchEvent(new Event('opsguard-dashboards-change'))
}

async function loadHadoopMenus(): Promise<HadoopMenuItem[]> {
  try {
    const response = await fetch(`${api}/hadoop-menu`)
    const data = await response.json()
    return response.ok && Array.isArray(data.items) ? data.items : []
  } catch { return [] }
}

async function saveHadoopMenuItem(sourceID: string, name: string): Promise<HadoopMenuItem> {
  const response = await fetch(`${api}/hadoop-menu`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ sourceId: sourceID, name }) })
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || '导入 Hadoop 菜单失败')
  window.dispatchEvent(new Event('opsguard-hadoop-menus-change'))
  return data as HadoopMenuItem
}

async function loadAmbariMenus(): Promise<AmbariMenuItem[]> {
  try { const response = await fetch(`${api}/ambari-menu`); const data = await response.json(); return response.ok && Array.isArray(data.items) ? data.items : [] } catch { return [] }
}

async function saveAmbariMenuItem(sourceID: string, name: string): Promise<AmbariMenuItem> {
  const response = await fetch(`${api}/ambari-menu`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ sourceId: sourceID, name }) })
  const data = await response.json(); if (!response.ok) throw new Error(data.error || '导入 Ambari 菜单失败')
  window.dispatchEvent(new Event('opsguard-ambari-menus-change')); return data as AmbariMenuItem
}

type DashboardMetricCard = { label: string; value: string; progress?: number; tone?: 'success' | 'danger' }
type DashboardFilesystem = { filesystem: string; mountPoint: string; totalBytes: number; usedBytes: number; usedPercent: number }

function dashboardPercent(value?: string) {
  const numeric = Number(value)
  // SSH metrics are returned as 0-100 percentages, while MySQL hit rate is a 0-1 ratio.
  const percent = numeric >= 0 && numeric <= 1 ? numeric * 100 : numeric
  return Number.isFinite(percent) ? Math.max(0, Math.min(100, percent)) : undefined
}

function Dashboards() {
  const [items, setItems] = useState<DashboardItem[]>([])
  const [values, setValues] = useState<Record<string, Record<string, string>>>({})
  const [filesystemValues, setFilesystemValues] = useState<Record<string, DashboardFilesystem[]>>({})
  const [filesystemSource, setFilesystemSource] = useState<DashboardItem | null>(null)
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
  const refreshInterval = useScopedRefreshInterval('dashboard')
  const dashboards = items.filter(item => ['MySQL', 'SSH', 'Agent', 'Redis', 'ClickHouse', 'Kafka'].includes(item.sourceType))
  const refreshOverview = async () => {
    try {
      const sourceResponse = await fetch(`${api}/data-sources`)
      const sourceData = await sourceResponse.json()
      const nextSources: Source[] = Array.isArray(sourceData.dataSources) ? sourceData.dataSources : []
      setSources(nextSources)
      const [customResult, notificationsResult, ...prometheusResults] = await Promise.allSettled([
        fetch(`${api}/collection-rules`).then(response => response.json()),
        fetch(`${api}/notifications?limit=1000`).then(response => response.json()),
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
    const nextFilesystems: Record<string, DashboardFilesystem[]> = {}
    for (const item of dashboards) {
      try {
        const response = await fetch(`${api}/data-sources/${item.sourceId}/dashboard-metrics`)
        const data = await response.json()
        next[item.sourceId] = Object.fromEntries(Object.entries(data.metrics || {}).map(([key, value]) => [key, String(value)]))
        nextFilesystems[item.sourceId] = Array.isArray(data.filesystems) ? data.filesystems : []
      } catch { next[item.sourceId] = {}; nextFilesystems[item.sourceId] = [] }
    }
    setValues(next)
    setFilesystemValues(nextFilesystems)
    window.requestAnimationFrame(() => {
      document.querySelectorAll<HTMLElement>('.dashboard-water > i').forEach((element) => {
        element.animate([{ transform: 'scaleX(0)', transformOrigin: 'left center' }, { transform: 'scaleX(1)', transformOrigin: 'left center' }], { duration: 760, easing: 'cubic-bezier(.2,.8,.25,1)', fill: 'both' })
      })
    })
  }
  useEffect(() => { const onChange = () => { void loadDashboards().then(setItems) }; onChange(); window.addEventListener('opsguard-dashboards-change', onChange); return () => window.removeEventListener('opsguard-dashboards-change', onChange) }, [])
  useEffect(() => { void refresh(); const timer = window.setInterval(refresh, refreshInterval); return () => window.clearInterval(timer) }, [items, refreshInterval])
  useEffect(() => { void refreshOverview(); const timer = window.setInterval(refreshOverview, refreshInterval); return () => window.clearInterval(timer) }, [refreshInterval])
  const remove = async (id: string) => {
    setItems(current => current.filter(item => item.id !== id))
    try { await deleteDashboardItem(id) } catch { void loadDashboards().then(setItems) }
  }
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
  const activeNotifications = notifications.filter(item => item.status === 'active')
  const totalRules = customRuleCount + prometheusRuleCount
  const unreadNotifications = notifications.filter(item => item.unread).length
  useEffect(() => {
    const frames: number[] = []
    const animateNumber = (element: HTMLElement, target: number, suffix = '') => {
      const started = performance.now()
      const tick = (now: number) => {
        const progress = Math.min(1, (now - started) / 560)
        const eased = 1 - Math.pow(1 - progress, 3)
        element.textContent = `${Math.round(target * eased)}${suffix}`
        if (progress < 1) frames.push(requestAnimationFrame(tick))
      }
      frames.push(requestAnimationFrame(tick))
    }
    const statTargets = [activeNotifications.length, totalRules, enabledSources.length, unreadNotifications]
    document.querySelectorAll<HTMLElement>('.overview-stat b').forEach((element, index) => {
      if (index < statTargets.length) animateNumber(element, statTargets[index])
    })
    return () => frames.forEach(frame => cancelAnimationFrame(frame))
  }, [activeNotifications.length, totalRules, enabledSources.length, unreadNotifications, values])
  return <div className="page"><section className="overview-stat-grid"><article className="overview-stat alert"><span>当前告警</span><b>{activeNotifications.length}</b><small>未恢复的告警事件</small></article><article className="overview-stat rule"><span>告警规则</span><b>{totalRules}</b><small>自定义 {customRuleCount} · Prometheus {prometheusRuleCount}</small></article><article className="overview-stat source"><span>启用数据源</span><b>{enabledSources.length}</b><small>接入 {sources.length} 个</small></article><article className="overview-stat notice"><span>未读通知</span><b>{unreadNotifications}</b><small>全部通知 {notifications.length} 条</small></article></section><section className="overview-section-title"><div><h2>数据源运行指标</h2><p>数据库、中间件与主机采集指标</p></div><span>{dashboards.length} 个数据源</span></section>{dashboards.length === 0 ? <section className="surface empty-state"><b>暂无数据源运行指标</b><span>可在数据节点中将支持的数据源导入监控大屏。</span></section> : <section className="dashboard-list">{dashboards.map(item => { const v = values[item.sourceId] || {}; const isHost = item.sourceType === 'SSH' || item.sourceType === 'Agent'; const cards: DashboardMetricCard[] = isHost ? [{ label: '状态', value: v.up === '1' ? '正常' : '采集中' }, { label: 'CPU 使用率', value: formatPercent100(v.cpu), progress: dashboardPercent(v.cpu) }, { label: '内存使用率', value: formatPercent100(v.memory), progress: dashboardPercent(v.memory) }, { label: '根分区 / 使用率', value: formatPercent100(v.disk), progress: dashboardPercent(v.disk) }, { label: '1 分钟负载', value: v.load1 || '-' }, { label: '5 分钟负载', value: v.load5 || '-' }] : [{ label: '状态', value: v.up === '1' ? '正常' : '采集中' }, { label: '连接数', value: formatInteger(v.threads || v.connected_clients) }, { label: '内存使用率', value: formatPercent(v.memory_usage_ratio) }, { label: '命中率', value: formatPercent(v.hit_ratio || v.hit) }]; return <section className="surface mysql-dashboard-card" key={item.id}><header><div><h3>{item.name}</h3><span>{item.sourceName} · {item.sourceType === 'Agent' ? 'SSH - Agent' : item.sourceType}</span></div><div className="dashboard-card-actions">{item.sourceType === 'MySQL' && <button className="text-button" type="button" onClick={() => void openSQL(item)}>更多</button>}<button className="text-button danger" type="button" onClick={() => void remove(item.id)}>删除</button></div></header><div className="dashboard-metrics">{cards.map(card => <div className={`dashboard-metric ${card.tone || 'neutral'} ${card.label === '根分区 / 使用率' ? 'clickable' : ''}`} key={card.label} role={card.label === '根分区 / 使用率' ? 'button' : undefined} tabIndex={card.label === '根分区 / 使用率' ? 0 : undefined} onClick={card.label === '根分区 / 使用率' ? () => setFilesystemSource(item) : undefined} onKeyDown={card.label === '根分区 / 使用率' ? (event) => { if (event.key === 'Enter' || event.key === ' ') setFilesystemSource(item) } : undefined}><span>{card.label}</span><b>{card.value}</b>{card.progress !== undefined && <i className="dashboard-water"><i style={{ width: `${card.progress}%` }} /></i>}</div>)}</div></section> })}</section>}{sqlSource && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setSQLSource(null) }}><section className="surface sql-insight-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>{sqlMode === 'slow' ? '慢查询 SQL' : '耗时最高 SQL'}</h2><p>{sqlSource.sourceName}</p></div><button className="close-button" type="button" onClick={() => setSQLSource(null)}>×</button></header>{sqlLoading ? <div className="empty-state"><b>正在加载 SQL 明细</b></div> : sqlError ? <div className="empty-state"><b>加载失败</b><span>{sqlError}</span></div> : <><div className="sql-insight-list">{pageItems.map((entry, index) => <article key={`${entry.digest || entry.queryText}-${index}`}><header><span>{(sqlPage - 1) * 10 + index + 1}</span><b>{entry.schemaName || '未指定库'}</b><small>{formatMilliseconds(entry.maxLatencyMs)}</small></header><code>{entry.queryText}</code></article>)}</div><footer className="sql-pagination"><button type="button" disabled={sqlPage === 1} onClick={() => setSQLPage(current => current - 1)}>上一页</button><span>{sqlPage} / {pages}</span><button type="button" disabled={sqlPage === pages} onClick={() => setSQLPage(current => current + 1)}>下一页</button></footer></>}</section></div>}{filesystemSource && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setFilesystemSource(null) }}><section className="surface filesystem-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>分区使用率</h2><p>{filesystemSource.name} · 已排除 Docker、Kubernetes kubelet 及临时文件系统</p></div><button className="close-button" type="button" onClick={() => setFilesystemSource(null)}>×</button></header><div className="filesystem-modal-list">{(filesystemValues[filesystemSource.sourceId] || []).length === 0 ? <div className="empty-state"><b>暂未采集到可展示的挂载分区</b><span>采集完成后将自动显示。</span></div> : (filesystemValues[filesystemSource.sourceId] || []).map(fs => <article className="filesystem-modal-row" key={`${fs.filesystem}-${fs.mountPoint}`}><header><div><b title={fs.mountPoint}>{fs.mountPoint}</b><span>{formatBytes(fs.usedBytes)} / {formatBytes(fs.totalBytes)}</span></div><strong>{fs.usedPercent.toFixed(1)}%</strong></header><i className="dashboard-water"><i style={{ width: `${Math.max(0, Math.min(100, fs.usedPercent))}%` }} /></i></article>)}</div></section></div>}</div>
}
async function loadMySQLMenus(): Promise<MySQLMenuItem[]> { try { const response = await fetch(`${api}/mysql-menu`); const data = await response.json(); return response.ok && Array.isArray(data.items) ? data.items : [] } catch { return [] } }
async function saveMySQLMenuItem(sourceID: string, name: string): Promise<MySQLMenuItem> { const response = await fetch(`${api}/mysql-menu`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ sourceId: sourceID, name }) }); const data = await response.json(); if (!response.ok) throw new Error(data.error || '导入 MySQL 工作台失败'); window.dispatchEvent(new Event('opsguard-mysql-menus-change')); return data as MySQLMenuItem }
async function loadConsulMenus(): Promise<ConsulMenuItem[]> { try { const response = await fetch(`${api}/consul-menu`); const data = await response.json(); return response.ok && Array.isArray(data.items) ? data.items : [] } catch { return [] } }
async function saveConsulMenuItem(sourceID: string, name: string): Promise<ConsulMenuItem> { const response = await fetch(`${api}/consul-menu`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ sourceId: sourceID, name }) }); const data = await response.json(); if (!response.ok) throw new Error(data.error || '导入 Consul 工作台失败'); window.dispatchEvent(new Event('opsguard-consul-menus-change')); return data as ConsulMenuItem }

function AgentTokenDialog({ token, host, port, onClose }: { token: string; host: string; port: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false)
  const config = `# OpsGuard Agent 配置\nLISTEN=0.0.0.0:${port || '4001'}\nTOKEN=${token}\n# 请填写平台实际来源 IP\nALLOWED_PLATFORM_IPS=填写平台来源IP/32\nCOMMAND_TIMEOUT_SECONDS=30\nMAX_OUTPUT_BYTES=65536`
  const copy = async () => {
    try { await navigator.clipboard.writeText(config); setCopied(true) } catch { setCopied(false) }
  }
  return <div className="modal-backdrop" role="presentation" onClick={onClose}><section className="surface import-name-modal" role="dialog" aria-modal="true" aria-labelledby="agent-token-title" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2 id="agent-token-title">Agent 配置</h2><p>{host}:{port || '4001'} 的令牌只生成一次，请保存下面配置。</p></div><button className="close-button" type="button" onClick={onClose}>×</button></header><label className="import-name-field">agent.conf 配置<textarea value={config} readOnly rows={8} onFocus={(event) => event.currentTarget.select()} /></label><footer className="modal-actions"><button className="button secondary" type="button" onClick={() => void copy()}>{copied ? '已复制配置' : '复制完整配置'}</button><button className="button" type="button" onClick={onClose}>确认</button></footer></section></div>
}

function formatPercent(value?: string) {
  const n = Number(value)
  if (!Number.isFinite(n)) return '-'
  return `${(n * 100).toFixed(2)}%`
}

function formatBytes(value?: number) {
  const bytes = Number(value)
  if (!Number.isFinite(bytes) || bytes < 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let current = bytes
  let unit = 0
  while (current >= 1024 && unit < units.length - 1) { current /= 1024; unit++ }
  return `${current >= 100 || unit === 0 ? current.toFixed(0) : current.toFixed(1)} ${units[unit]}`
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
function formatInteger(value?: string) { const n = Number(value); return Number.isFinite(n) ? Math.round(n).toLocaleString('zh-CN') : '-' }
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

function hadoopApplicationDetailsURL(source: Source | undefined, app: { id: string; applicationType?: string; state?: string }) {
  const host = String(source?.host || '').trim()
  if (!host || !app.id) return ''
  const type = String(app.applicationType || '').toUpperCase()
  const jobHistory = String(source?.options?.jobHistoryUrl || '').trim()
  if (type === 'MAPREDUCE' && jobHistory) {
    const jobID = app.id.replace(/^application_/, 'job_')
    return `${jobHistory.replace(/\/+$/, '')}/jobhistory/job/${encodeURIComponent(jobID)}`
  }
  const withProtocol = /^https?:\/\//i.test(host) ? host : `http://${host}${source?.port ? `:${source.port}` : ''}`
  return `${withProtocol.replace(/\/+$/, '')}/cluster/app/${encodeURIComponent(app.id)}`
}

function splitHadoopLogSections(content: string) {
  const headings = Array.from(content.matchAll(/^=====\s+(.+?)\s+=====$/gm))
  return headings.map((heading, index) => ({
    name: heading[1].trim(),
    content: content.slice((heading.index || 0) + heading[0].length, index + 1 < headings.length ? headings[index + 1].index : content.length).replace(/^\r?\n/, '').replace(/\r?\n$/, ''),
  }))
}

function HadoopLogPager({ content }: { content: string }) {
  const parsedSections = splitHadoopLogSections(content)
  // NodeManager may return one raw file instead of its directory index. Keep
  // the file selector visible in that case, rather than making the control
  // disappear for terminal applications.
  const sections = parsedSections.length > 0 ? parsedSections : [{ name: '当前日志', content }]
  const [selectedFile, setSelectedFile] = useState('__all__')
  const selectedSection = sections.find(section => section.name === selectedFile)
  const displayedContent = selectedSection ? `===== ${selectedSection.name} =====\n${selectedSection.content}` : content
  const lines = displayedContent.split(/\r?\n/)
  const pageSize = 120
  const totalPages = Math.max(1, Math.ceil(lines.length / pageSize))
  const [page, setPage] = useState(1)
  const currentPage = Math.min(page, totalPages)
  const visibleLines = lines.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  useEffect(() => { setPage(1); setSelectedFile('__all__') }, [content])
  useEffect(() => setPage(1), [selectedFile])
  return <div className="hadoop-log-pager"><div className="hadoop-log-toolbar"><div className="hadoop-log-view-controls"><span>日志文件</span><AppSelect className="hadoop-log-file-select" value={selectedFile} placeholder="全部日志" onChange={setSelectedFile} options={[{ value: '__all__', label: '全部日志' }, ...sections.map(section => ({ value: section.name, label: section.name }))]} /></div><span className="hadoop-log-summary">共 {lines.length} 行 <i /> 每页 {pageSize} 行</span><span className="hadoop-log-pagination"><button type="button" disabled={currentPage === 1} onClick={() => setPage(1)}>首页</button><button type="button" disabled={currentPage === 1} onClick={() => setPage(value => Math.max(1, value - 1))}>上一页</button><input type="number" min="1" max={totalPages} value={currentPage} aria-label="日志页码" onChange={(event) => { const next = Number(event.target.value); if (Number.isInteger(next) && next >= 1 && next <= totalPages) setPage(next) }} /><span>/ {totalPages}</span><button type="button" disabled={currentPage === totalPages} onClick={() => setPage(value => Math.min(totalPages, value + 1))}>下一页</button><button type="button" disabled={currentPage === totalPages} onClick={() => setPage(totalPages)}>末页</button></span></div><pre className="hadoop-log-content">{visibleLines.join('\n')}</pre></div>
}

function hadoopContainerLabel(container: { id: string; priority?: string; isAM?: boolean }, applicationType?: string) {
  const sequence = container.id.match(/_(\d{6})$/)?.[1] || container.id
  const isAM = container.isAM === true || (container.isAM === undefined && String(container.priority) === '0')
  if (String(applicationType || '').toUpperCase() === 'APACHE FLINK') {
    return isAM ? `JobManager（主容器） · ${sequence}` : `TaskManager（工作容器） · ${sequence}`
  }
  return isAM ? `ApplicationMaster · ${sequence}` : `工作容器 · ${sequence}`
}

function normalizeFileMonitorResultDetails(value: string) {
  return value
    .replace(/-\s+find:\s+['"]?([^'"\n:]+)['"]?:\s+No such file or directory/gi, '- 目录不存在：$1')
    .replace(/-\s+read directory failed:\s+open\s+([^\n:]+):\s+no such file or directory/gi, '- 目录不存在：$1')
}

function sortHadoopContainers(containers: any[]) {
  return [...containers].sort((left, right) => {
    const leftAM = left.isAM === true || (left.isAM === undefined && String(left.priority) === '0')
    const rightAM = right.isAM === true || (right.isAM === undefined && String(right.priority) === '0')
    if (leftAM !== rightAM) return leftAM ? -1 : 1
    return String(left.id || '').localeCompare(String(right.id || ''), undefined, { numeric: true })
  })
}

function HadoopYarn() {
  const location = useLocation()
  const requestedSourceID = new URLSearchParams(location.search).get('source') || ''
  const [sources, setSources] = useState<Source[]>([]); const [sourceID, setSourceID] = useState(''); const [apps, setApps] = useState<any[]>([]); const [appTotal, setAppTotal] = useState(0); const [appFacets, setAppFacets] = useState<Record<string, string[]>>({}); const [containers, setContainers] = useState<any[]>([]); const [selectedContainerID, setSelectedContainerID] = useState(''); const [log, setLog] = useState<ReactNode>(''); const [logModalOpen, setLogModalOpen] = useState(false); const [logFullscreen, setLogFullscreen] = useState(false); const [selectedAppID, setSelectedAppID] = useState(''); const [message, setMessage] = useState(''); const [downloadingLog, setDownloadingLog] = useState(false); const [, setLoading] = useState(false); const appLoadSequence = useRef(0)
  const [keyword, setKeyword] = useState(''); const [userFilter, setUserFilter] = useState(''); const [typeFilter, setTypeFilter] = useState(''); const [stateFilter, setStateFilter] = useState(''); const [finalStatusFilter, setFinalStatusFilter] = useState(''); const [appPage, setAppPage] = useState(1)
  const [appContainers, setAppContainers] = useState<Record<string, any[]>>({})
  const taskRefreshInterval = useScopedRefreshInterval('hadoop')
  const containerSummaryCache = useRef<Record<string, { at: number; items: any[] }>>({})
  useEffect(() => {
    if (!sourceID || apps.length === 0) return
    let cancelled = false
    void Promise.all(apps.map(async (app: any) => { const cached = containerSummaryCache.current[app.id]; if (cached && Date.now() - cached.at < 30000) return [app.id, cached.items] as const; try { const response = await fetch(`${api}/hadoop/${sourceID}/apps/${app.id}`); const data = await response.json(); const items = sortHadoopContainers(Array.isArray(data.data) ? data.data : []); containerSummaryCache.current[app.id] = { at: Date.now(), items }; return [app.id, items] as const } catch { return [app.id, cached?.items || []] as const } })).then(items => { if (!cancelled) setAppContainers(Object.fromEntries(items)) })
    return () => { cancelled = true }
  }, [sourceID, apps])
  useEffect(() => { void (async () => { try { const data = await (await fetch(`${api}/data-sources`)).json(); const next = (data.dataSources || []).filter((item: Source) => item.type === 'Hadoop'); setSources(next); setSourceID(next.some((item: Source) => item.id === requestedSourceID) ? requestedSourceID : next[0]?.id || '') } catch { setSources([]); setSourceID('') } })() }, [requestedSourceID])
  const loadApps = async () => { if (!sourceID) return; const sequence = ++appLoadSequence.current; setLoading(true); try { const params = new URLSearchParams({ page: String(appPage), pageSize: '20', keyword, user: userFilter, type: typeFilter, state: stateFilter, finalStatus: finalStatusFilter }); const response = await fetch(`${api}/hadoop/${sourceID}/apps?${params.toString()}`); const data = await response.json(); if (!response.ok) throw new Error(data.error); if (sequence !== appLoadSequence.current) return; const payload = data.data || data; const next = Array.isArray(payload.items) ? payload.items : []; const total = Number(payload.total); setApps(next); setAppTotal(Number.isFinite(total) ? total : next.length); setAppFacets(payload.facets || {}); if (appPage > 1 && next.length === 0 && total > 0) setAppPage(Math.max(1, Math.ceil(total / 20))); setMessage('') } catch (err) { if (sequence === appLoadSequence.current) setMessage(err instanceof Error ? err.message : 'YARN 应用加载失败') } finally { if (sequence === appLoadSequence.current) setLoading(false) } }
  const loadContainers = async (appID: string) => { try { setSelectedAppID(appID); setLog('正在加载日志...'); const response = await fetch(`${api}/hadoop/${sourceID}/apps/${appID}`); const data = await response.json(); if (!response.ok) throw new Error(data.error); const next = sortHadoopContainers(Array.isArray(data.data) ? data.data : []); setContainers(next); if (next.length === 0) { setLog('该任务没有可读取的容器日志。'); return }; setSelectedContainerID(next[0].id); setLogModalOpen(true); if (next[0].logUrl) void loadLog(next[0].logUrl, next[0].id); else setLog('该容器未提供日志地址。') } catch (err) { setLogModalOpen(false); setMessage(err instanceof Error ? err.message : '容器加载失败') } }
  const loadLog = async (url: string, containerID?: string) => { try { if (containerID) setSelectedContainerID(containerID); setLog('正在加载日志...'); const response = await fetch(`${api}/hadoop/${sourceID}/log?url=${encodeURIComponent(url)}`); const data = await response.json(); if (!response.ok) throw new Error(data.error || '节点日志服务不可用'); const content = String(data.data || ''); setLog(<HadoopLogPager content={content || '该容器暂无可用日志，可能尚未产生日志或日志已被 YARN 清理。'} />) } catch (err) { setLog(`日志读取失败：${err instanceof Error ? err.message : '节点日志服务不可用'}。请检查 NodeManager 或 JobHistory 地址。`) } }
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
      const task = apps.find(app => app.id === selectedAppID)
      const fileName = `${formatHadoopApplicationName(task?.name, 'yarn-task')}-${selectedAppID || 'unknown'}`.replace(/[\\/:*?"<>|]/g, '_')
      link.download = `${fileName}.log`
      link.click()
      URL.revokeObjectURL(url)
    } catch (err) { setMessage(err instanceof Error ? err.message : '任务日志下载失败') } finally { setDownloadingLog(false) }
  }
  useEffect(() => {
    if (!logModalOpen && logFullscreen) setLogFullscreen(false)
  }, [logModalOpen, logFullscreen])
  const valuesFor = (field: string) => appFacets[field] || []
  const filteredApps = apps
  const totalAppPages = Math.max(1, Math.ceil(appTotal / 20))
  const currentAppPage = Math.min(appPage, totalAppPages)
  const visibleApps = filteredApps
  const appPagination = <UnifiedPagination total={appTotal} page={currentAppPage} totalPages={totalAppPages} onPageChange={setAppPage} ariaLabel="Hadoop 任务分页" />
  const actions = null
  useEffect(() => { if (!sourceID) return; void loadApps(); const timer = window.setInterval(() => void loadApps(), taskRefreshInterval); return () => window.clearInterval(timer) }, [sourceID, appPage, keyword, userFilter, typeFilter, stateFilter, finalStatusFilter, taskRefreshInterval])
  const resetFilters = () => { setKeyword(''); setUserFilter(''); setTypeFilter(''); setStateFilter(''); setFinalStatusFilter(''); setAppPage(1) }
  const openApplicationDetails = (app: { id: string; applicationType?: string; state?: string }) => {
    const url = hadoopApplicationDetailsURL(sources.find(source => source.id === sourceID), app)
    if (!url) { setMessage('未配置 Hadoop Web 地址'); return }
    const detailWindow = window.open(url, '_blank')
    if (!detailWindow) { setMessage('浏览器阻止了新标签页，请允许弹窗后重试'); return }
    detailWindow.opener = null
  }
  const selectedApplication = apps.find(app => app.id === selectedAppID)
  const selectedContainer = containers.find(container => container.id === selectedContainerID)
	const containerPerformanceNote = (appID: string) => {
		const details = appContainers[appID] || (appID === selectedAppID && selectedContainer ? [selectedContainer] : [])
		if (!details.length) return ''
		const nodes = Array.from(new Set(details.map((container: any) => String(container.nodeHttpAddress || '').trim()).filter(Boolean)))
		const memory = details.reduce((total: number, container: any) => total + (Number(container.allocatedMB) || 0), 0)
		const vcores = details.reduce((total: number, container: any) => total + (Number(container.allocatedVCores) || 0), 0)
		return `节点 ${nodes.length ? nodes.join('、') : '-'} · 内存 ${memory || '-'} MB · CPU ${vcores || '-'} vCores`
	}
  return <div className="page"><PageHead title="Hadoop / YARN" description="应用运行状态与容器日志" actionNode={actions} />{sources.length === 0 ? <section className="surface empty-state"><b>暂无 Hadoop 数据源</b><span>请先在数据节点新增 Hadoop Web 地址。</span></section> : <section className="surface">{message && <div className="form-error">{message}</div>}<div className="hadoop-filter-bar"><input value={keyword} onChange={(event) => { setKeyword(event.target.value); setAppPage(1) }} placeholder="搜索名称、ID、用户或状态" /><AppSelect value={userFilter} placeholder="全部用户" onChange={(value) => { setUserFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部用户' }, ...valuesFor('user').map(value => ({ value, label: value }))]} /><AppSelect value={typeFilter} placeholder="全部类型" onChange={(value) => { setTypeFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部类型' }, ...valuesFor('applicationType').map(value => ({ value, label: value }))]} /><AppSelect value={stateFilter} placeholder="全部状态" onChange={(value) => { setStateFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部状态' }, ...valuesFor('state').map(value => ({ value, label: value }))]} /><AppSelect value={finalStatusFilter} placeholder="全部最终状态" onChange={(value) => { setFinalStatusFilter(value); setAppPage(1) }} options={[{ value: '', label: '全部最终状态' }, ...valuesFor('finalStatus').map(value => ({ value, label: value }))]} /><button className="hadoop-filter-reset" type="button" onClick={resetFilters}>重置</button></div><div className="prometheus-rule-list">{visibleApps.map(app => { const note = containerPerformanceNote(app.id); return <article className="prometheus-rule-row compact hadoop-app-row" key={app.id}><i className="rule-icon">Y</i><div><header><b>{formatHadoopApplicationName(app.name, app.id)}<code className="hadoop-application-id">{app.id}</code></b><div className="hadoop-app-actions"><button className="text-button" type="button" onClick={() => void loadContainers(app.id)}>查看日志</button><button className="text-button hadoop-app-detail" type="button" onClick={() => openApplicationDetails(app)}>任务详情</button></div></header><div className="hadoop-app-meta"><span>{app.user || '-'}</span><span>{app.queue || 'default'}</span><span>{app.applicationType || '-'}</span><span>进度 {Number(app.progress || 0).toFixed(0)}%</span>{note && <span className="hadoop-container-progress-note">{note}</span>}<span>{formatHadoopTimeRange(app)}</span><span className={`hadoop-status ${hadoopStatusTone(app.state)}`}>状态 {app.state || '-'}</span><span className={`hadoop-status ${hadoopStatusTone(app.finalStatus)}`}>最终状态 {app.finalStatus || '-'}</span></div></div></article> })}</div>{appPagination}{filteredApps.length > 0 && visibleApps.length === 0 && <div className="hadoop-filter-empty">没有匹配的应用</div>}</section>}{logModalOpen && <div className={`modal-backdrop ${logFullscreen ? 'hadoop-log-fullscreen-backdrop' : ''}`} role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setLogModalOpen(false) }}><section className={`surface hadoop-log-modal ${logFullscreen ? 'fullscreen' : ''}`} role="dialog" aria-modal="true"><header className="modal-head"><div><h2>容器日志</h2><p>{selectedAppID}</p></div><button className="button secondary hadoop-log-download" type="button" disabled={downloadingLog} onClick={() => void downloadAllLogs()}>{downloadingLog ? '下载中...' : '下载全部'}</button><button className="button secondary hadoop-log-fullscreen" type="button" aria-label={logFullscreen ? '退出全屏' : '全屏查看'} title={logFullscreen ? '退出全屏' : '全屏查看'} onClick={() => setLogFullscreen(value => !value)}><span aria-hidden="true">{logFullscreen ? '↙' : '↗'}</span><em>{logFullscreen ? '退出全屏' : '全屏'}</em></button><button className="close-button" type="button" aria-label="关闭" onClick={() => setLogModalOpen(false)}>×</button></header>{containers.length > 1 && <div className="hadoop-container-tabs">{containers.map(container => <button className={container.id === selectedContainerID ? 'active' : ''} type="button" key={container.id} onClick={() => container.logUrl && void loadLog(container.logUrl, container.id)}>{hadoopContainerLabel(container, selectedApplication?.applicationType)}</button>)}</div>}{selectedContainer && <div className="hadoop-container-resource">Attempt：{selectedContainer.attemptId || '-'} · 节点：{selectedContainer.nodeHttpAddress || '-'} · 内存：{selectedContainer.allocatedMB || '-'} MB · vCores：{selectedContainer.allocatedVCores || '-'}</div>}<div className="hadoop-log">{log}</div></section></div>}</div>
}

function MySQLWorkspace() {
  const sourceID = new URLSearchParams(useLocation().search).get('source') || ''
  type ColumnDetail = { name: string; type: string; collation: string | null; nullable: boolean; key: string; default: string | null; extra: string; privileges: string; comment: string }
  type RowEditor = { mode: 'insert' | 'edit'; rowIndex: number; values: Record<string, string>; nulls: Record<string, boolean> }
  const [databases, setDatabases] = useState<string[]>([])
  const [tablesByDatabase, setTablesByDatabase] = useState<Record<string, string[]>>({})
  const [expandedDatabases, setExpandedDatabases] = useState<string[]>([])
  const [database, setDatabase] = useState('')
  const [table, setTable] = useState('')
  const [filter, setFilter] = useState('')
  const [treeCollapsed, setTreeCollapsed] = useState(false)
  const [activeView, setActiveView] = useState<'data' | 'structure' | 'sql'>('data')
  const [structure, setStructure] = useState<ColumnDetail[]>([])
  const [preview, setPreview] = useState<{ columns: string[]; rows: any[][]; page: number; pageSize: number; total: number; totalPages: number; totalExact: boolean }>({ columns: [], rows: [], page: 1, pageSize: 50, total: 0, totalPages: 1, totalExact: true })
  const [tablePage, setTablePage] = useState(1)
  const [tablePageSize, setTablePageSize] = useState(50)
  const [tableFilterColumn, setTableFilterColumn] = useState('')
  const [tableFilterDraft, setTableFilterDraft] = useState('')
  const [tableFilterValue, setTableFilterValue] = useState('')
  const [tableSortColumn, setTableSortColumn] = useState('')
  const [tableSortDirection, setTableSortDirection] = useState('asc')
  const [loading, setLoading] = useState(false)
  const [loadingDatabase, setLoadingDatabase] = useState('')
  const [loadingTable, setLoadingTable] = useState(false)
  const [error, setError] = useState('')
  const [reloadSequence, setReloadSequence] = useState(0)
  const [rowEditor, setRowEditor] = useState<RowEditor | null>(null)
  const [deleteRowIndex, setDeleteRowIndex] = useState<number | null>(null)
  const [mutating, setMutating] = useState(false)
  const [mutationMessage, setMutationMessage] = useState('')
  const [transferMode, setTransferMode] = useState<'export' | 'import' | null>(null)
  const [sqlDatabase, setSQLDatabase] = useState('')
  const [sqlText, setSQLText] = useState('')
  const [sqlRunning, setSQLRunning] = useState(false)
  const [sqlNeedsConfirm, setSQLNeedsConfirm] = useState(false)
  const [sqlError, setSQLError] = useState('')
  const [sqlResult, setSQLResult] = useState<{ columns: string[]; rows: any[][]; affected: number; message: string; truncated: boolean } | null>(null)
  const [sqlResultPage, setSQLResultPage] = useState(1)
  const [sqlResultPageSize, setSQLResultPageSize] = useState(50)
  const [sqlHistory, setSQLHistory] = useState<SQLHistoryItem[]>([])
  const [sqlHistoryLoading, setSQLHistoryLoading] = useState(false)
  const [sqlHistoryPage, setSQLHistoryPage] = useState(1)
  const [sqlHistoryTotal, setSQLHistoryTotal] = useState(0)
  useEffect(() => {
    if (!sourceID) return
    setLoading(true)
    fetch(`${api}/data-sources/${sourceID}/schema`).then(r => r.json().then(data => ({ r, data }))).then(({ r, data }) => {
      if (!r.ok) throw new Error(data.error)
      setDatabases(Object.keys(data.schema || {}))
      setTablesByDatabase({}); setExpandedDatabases([]); setDatabase(''); setTable(''); setStructure([]); setPreview({ columns: [], rows: [], page: 1, pageSize: 50, total: 0, totalPages: 1, totalExact: true }); setTablePage(1); setError('')
    }).catch(err => setError(err.message || '读取数据库失败')).finally(() => setLoading(false))
  }, [sourceID])
  const toggleDatabase = async (name: string) => {
    if (expandedDatabases.includes(name)) { setExpandedDatabases(current => current.filter(item => item !== name)); return }
    setExpandedDatabases(current => [...current, name])
    if (tablesByDatabase[name]) return
    setLoadingDatabase(name)
    try {
      const response = await fetch(`${api}/data-sources/${sourceID}/schema?database=${encodeURIComponent(name)}`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '读取数据表失败')
      setTablesByDatabase(current => ({ ...current, [name]: Object.keys(data.schema?.[name] || {}) }))
      setError('')
    } catch (err) { setError(err instanceof Error ? err.message : '读取数据表失败') } finally { setLoadingDatabase('') }
  }
  const selectTable = (databaseName: string, tableName: string) => { setDatabase(databaseName); setTable(tableName); setTablePage(1); setTableFilterColumn(''); setTableFilterDraft(''); setTableFilterValue(''); setTableSortColumn(''); setTableSortDirection('asc'); setActiveView('data'); setRowEditor(null); setDeleteRowIndex(null) }
  useEffect(() => {
    if (!sourceID || !database || !table) return
    setLoadingTable(true)
    const query = `database=${encodeURIComponent(database)}&table=${encodeURIComponent(table)}`
    const previewParams = new URLSearchParams({ database, table, page: String(tablePage), pageSize: String(tablePageSize) })
    if (tableFilterColumn && tableFilterValue) { previewParams.set('filterColumn', tableFilterColumn); previewParams.set('filterValue', tableFilterValue) }
    if (tableSortColumn) { previewParams.set('sortColumn', tableSortColumn); previewParams.set('sortDirection', tableSortDirection) }
    const previewQuery = previewParams.toString()
    Promise.all([fetch(`${api}/data-sources/${sourceID}/preview?${previewQuery}`), fetch(`${api}/data-sources/${sourceID}/structure?${query}`)]).then(async ([previewResponse, structureResponse]) => {
      const [previewData, structureData] = await Promise.all([previewResponse.json(), structureResponse.json()])
      if (!previewResponse.ok) throw new Error(previewData.error || '读取表数据失败')
      if (!structureResponse.ok) throw new Error(structureData.error || '读取表结构失败')
      const nextPage = Number(previewData.page || 1)
      setPreview({ columns: previewData.columns || [], rows: previewData.rows || [], page: nextPage, pageSize: Number(previewData.pageSize || tablePageSize), total: Number(previewData.total || 0), totalPages: Number(previewData.totalPages || 1), totalExact: previewData.totalExact !== false }); setStructure(structureData.columns || []); setError('')
      if (nextPage !== tablePage) setTablePage(nextPage)
    }).catch(err => { setPreview({ columns: [], rows: [], page: tablePage, pageSize: tablePageSize, total: 0, totalPages: 1, totalExact: true }); setStructure([]); setError(err.message || '读取数据失败') }).finally(() => setLoadingTable(false))
  }, [sourceID, database, table, tablePage, tablePageSize, tableFilterColumn, tableFilterValue, tableSortColumn, tableSortDirection, reloadSequence])
  const primaryKeys = structure.filter(column => column.key === 'PRI').map(column => column.name)
  const editableColumns = structure.filter(column => !column.extra.toLowerCase().includes('generated'))
  const rowKeys = (rowIndex: number) => {
    const row = preview.rows[rowIndex] || []
    return Object.fromEntries(primaryKeys.map(name => [name, row[preview.columns.indexOf(name)]]))
  }
  const openInsert = () => setRowEditor({ mode: 'insert', rowIndex: -1, values: Object.fromEntries(editableColumns.map(column => [column.name, column.default ?? ''])), nulls: {} })
  const openEdit = (rowIndex: number) => {
    const row = preview.rows[rowIndex] || []
    const values: Record<string, string> = {}; const nulls: Record<string, boolean> = {}
    editableColumns.forEach(column => { const value = row[preview.columns.indexOf(column.name)]; values[column.name] = value === null || value === undefined ? '' : String(value); nulls[column.name] = value === null })
    setRowEditor({ mode: 'edit', rowIndex, values, nulls })
  }
  const showMutationMessage = (message: string) => { setMutationMessage(message); window.setTimeout(() => setMutationMessage(''), 3000) }
  const saveRow = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!rowEditor) return
    const values: Record<string, any> = {}
    editableColumns.forEach(column => {
      const value = rowEditor.values[column.name] ?? ''
      if (rowEditor.mode === 'insert' && column.extra.toLowerCase().includes('auto_increment') && value === '') return
      values[column.name] = rowEditor.nulls[column.name] ? null : value
    })
    setMutating(true)
    try {
      const response = await fetch(`${api}/data-sources/${sourceID}/rows`, { method: rowEditor.mode === 'insert' ? 'POST' : 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ database, table, keys: rowEditor.mode === 'edit' ? rowKeys(rowEditor.rowIndex) : {}, values }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存失败')
      setRowEditor(null); setReloadSequence(current => current + 1); showMutationMessage(rowEditor.mode === 'insert' ? '记录已新增' : '记录已更新')
    } catch (err) { setError(err instanceof Error ? err.message : '保存失败') } finally { setMutating(false) }
  }
  const deleteRow = async () => {
    if (deleteRowIndex === null) return
    setMutating(true)
    try {
      const response = await fetch(`${api}/data-sources/${sourceID}/rows`, { method: 'DELETE', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ database, table, keys: rowKeys(deleteRowIndex) }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '删除失败')
      setDeleteRowIndex(null); setReloadSequence(current => current + 1); showMutationMessage('记录已删除')
    } catch (err) { setError(err instanceof Error ? err.message : '删除失败') } finally { setMutating(false) }
  }
  const loadSQLHistory = async (page = sqlHistoryPage) => {
    setSQLHistoryLoading(true)
    try {
      const response = await fetch(`${api}/data-sources/${sourceID}/sql-history?page=${page}&pageSize=10`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || 'SQL 历史加载失败')
      setSQLHistory(Array.isArray(data.items) ? data.items : []); setSQLHistoryTotal(Number(data.total || (Array.isArray(data.items) ? data.items.length : 0))); setSQLHistoryPage(Number(data.page || page))
    } catch { setSQLHistory([]) } finally { setSQLHistoryLoading(false) }
  }
  const openSQL = () => { setActiveView('sql'); setSQLDatabase(current => current || database); setSQLNeedsConfirm(false); setSQLError(''); setSQLResultPage(1) }
  const isReadOnlySQL = (statement: string) => {
    const first = statement.trim().match(/^([a-z]+)/i)?.[1]?.toLowerCase() || ''
    if (['select', 'show', 'describe', 'desc', 'explain'].includes(first)) return true
    if (first !== 'with') return false
    const commands = Array.from(statement.matchAll(/\)\s*(select|insert|update|delete|replace)\b/gi))
    return commands.length > 0 && commands[commands.length - 1][1].toLowerCase() === 'select'
  }
  const canExplainSQL = (statement: string) => /^(select|with|explain|update|delete)\b/i.test(statement.trim())
  const executeSQL = async (confirmed = false, explain = false) => {
    const enteredStatement = sqlText.trim()
    if (!enteredStatement) { setSQLError('请输入 SQL 语句'); return }
    if (explain && !canExplainSQL(enteredStatement)) { setSQLError('影响预览仅支持 SELECT、UPDATE 或 DELETE'); return }
    const statement = explain ? `EXPLAIN ${enteredStatement.replace(/^EXPLAIN\s+/i, '')}` : enteredStatement
    const readOnly = isReadOnlySQL(statement)
    if (!readOnly && !confirmed) { setSQLNeedsConfirm(true); setSQLError(''); return }
    setSQLRunning(true); setSQLNeedsConfirm(false); setSQLError('')
    try {
      const response = await fetch(`${api}/data-sources/${sourceID}/execute`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ sql: statement, confirmed }) })
      const responseText = await response.text()
      let data: { columns?: string[]; rows?: any[][]; affected?: number; message?: string; truncated?: boolean; error?: string } = {}
      try { data = responseText ? JSON.parse(responseText) : {} } catch { throw new Error(`服务端返回了无效响应（HTTP ${response.status}）。请缩小查询范围或稍后重试。`) }
      if (!responseText && response.ok) throw new Error('服务端未返回查询结果。请缩小查询范围或稍后重试。')
      if (!response.ok) throw new Error(data.error || 'SQL 执行失败')
      setSQLResult({ columns: data.columns || [], rows: data.rows || [], affected: Number(data.affected || 0), message: data.message || '', truncated: data.truncated === true })
      setSQLResultPage(1)
      if (!readOnly && table) setReloadSequence(current => current + 1)
    } catch (err) { setSQLResult(null); setSQLError(err instanceof Error ? err.message : 'SQL 执行失败') } finally { setSQLRunning(false); void loadSQLHistory() }
  }
  const applyTableFilter = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); setTableFilterValue(tableFilterDraft.trim()); setTablePage(1) }
  const clearTableFilter = () => { setTableFilterDraft(''); setTableFilterValue(''); setTablePage(1) }
  const query = filter.trim().toLowerCase()
  const visibleDatabases = databases.filter(name => !query || name.toLowerCase().includes(query) || (tablesByDatabase[name] || []).some(item => item.toLowerCase().includes(query)))
  const sqlResultTotal = sqlResult?.rows.length || 0
  const sqlResultTotalPages = Math.max(1, Math.ceil(sqlResultTotal / sqlResultPageSize))
  const visibleSQLRows = sqlResult?.rows.slice((sqlResultPage - 1) * sqlResultPageSize, sqlResultPage * sqlResultPageSize) || []
  const sqlWorkspace = <div className="mysql-sql-panel"><div className="mysql-sql-layout"><aside className="mysql-sql-history"><header><b>最近执行</b><span>最近 10 条</span></header><div>{sqlHistoryLoading ? <span className="mysql-sql-history-empty">正在加载...</span> : sqlHistory.length === 0 ? <span className="mysql-sql-history-empty">暂无执行记录</span> : sqlHistory.map(item => <button type="button" key={item.id} onClick={() => { setSQLText(item.statement); setSQLDatabase(item.database); setSQLNeedsConfirm(false); setSQLError(''); setSQLResult(null); setSQLResultPage(1) }}><span><i className={item.status === 'success' ? 'success' : 'failed'} />{item.database || '默认数据库'}<time>{item.createdAt}</time></span><code>{item.statement}</code><small>{item.status === 'success' ? `${item.resultRows > 0 ? `${item.resultRows} 行结果` : `影响 ${item.affected} 行`}` : item.error || '执行失败'} · {item.durationMs}ms</small></button>)}</div></aside><section className="mysql-sql-workspace"><div className="mysql-sql-database"><span>执行数据库</span><AppSelect className="mysql-sql-database-select" value={sqlDatabase} onChange={setSQLDatabase} options={[{ value: '', label: '数据源默认数据库' }, ...databases.map(item => ({ value: item, label: item }))]} /></div><textarea className="mysql-sql-editor" value={sqlText} spellCheck={false} placeholder="SELECT * FROM table_name LIMIT 100" onChange={event => { setSQLText(event.target.value); setSQLNeedsConfirm(false); setSQLError('') }} />{sqlError && <div className="form-error">{sqlError}</div>}{sqlNeedsConfirm && <div className="mysql-sql-warning"><b>确认执行写操作？</b><span>该语句可能修改数据库结构或数据，执行前可先使用“影响预览”查看执行计划。</span><button type="button" disabled={sqlRunning} onClick={() => void executeSQL(true)}>确认执行</button></div>}<div className="mysql-sql-actions"><span>{sqlResult && (sqlResult.columns.length > 0 ? `${sqlResult.rows.length}${sqlResult.truncated ? '+' : ''} 行结果` : `已影响 ${sqlResult.affected} 行`)}</span><div><button className="button secondary" type="button" disabled={sqlRunning || !canExplainSQL(sqlText)} onClick={() => void executeSQL(false, true)}>影响预览</button>{sqlResult?.columns.length ? <button className="button secondary" type="button" onClick={() => downloadCSV(`sql-result-${Date.now()}.csv`, sqlResult.columns, sqlResult.rows)}>下载 CSV</button> : null}<button className="button" type="button" disabled={sqlRunning || !sqlText.trim()} onClick={() => void executeSQL(false)}>{sqlRunning ? '执行中...' : '执行'}</button><MySQLBackgroundQuery sourceID={sourceID} database="" sql={sqlText} confirmed={sqlNeedsConfirm} /><SQLHistoryControl items={sqlHistory} loading={sqlHistoryLoading} total={sqlHistoryTotal} page={sqlHistoryPage} onOpen={() => void loadSQLHistory(sqlHistoryPage)} onPageChange={value => void loadSQLHistory(value)} onCopy={sql => { void navigator.clipboard.writeText(sql) }} onSelect={item => { setSQLText(item.statement); setSQLDatabase(item.database) }} /></div></div>{sqlResult && (sqlResult.columns.length > 0 ? <SQLResultView columns={sqlResult.columns} rows={visibleSQLRows} page={sqlResultPage} pageSize={sqlResultPageSize} total={sqlResultTotal} totalPages={sqlResultTotalPages} truncated={sqlResult.truncated} onPageChange={setSQLResultPage} onPageSizeChange={value => { setSQLResultPageSize(value); setSQLResultPage(1) }} /> : <div className="mysql-sql-result mysql-sql-success">SQL 执行成功，影响 {sqlResult.affected} 行。</div>)}</section></div></div>
  return <div className="page mysql-workspace">
    <section className="overview-section-title"><div><h2>MySQL 工作台</h2><p>浏览数据库、查看并维护表数据</p></div></section>
    {error && <div className="form-error">{error}</div>}{mutationMessage && <div className="toast">{mutationMessage}</div>}
    <section className={`surface mysql-workspace-grid ${treeCollapsed ? 'tree-collapsed' : ''}`}>
      <button className="mysql-tree-collapse-control" type="button" onClick={() => setTreeCollapsed(value => !value)} title={treeCollapsed ? '展开数据库对象' : '折叠数据库对象'} aria-label={treeCollapsed ? '展开数据库对象' : '折叠数据库对象'}>{treeCollapsed ? '›' : '‹'}</button>
      <aside className="mysql-tree-panel"><header><b>数据库对象</b><span>{databases.length} 个库</span></header><input value={filter} placeholder="筛选数据库或表" onChange={event => setFilter(event.target.value)} /><div className="mysql-tree">{loading ? <span className="mysql-tree-status">正在加载...</span> : visibleDatabases.map(databaseName => { const expanded = expandedDatabases.includes(databaseName); const visibleTables = (tablesByDatabase[databaseName] || []).filter(name => !query || databaseName.toLowerCase().includes(query) || name.toLowerCase().includes(query)); return <div className="mysql-tree-database" key={databaseName}><button className={database === databaseName ? 'selected' : ''} type="button" onClick={() => void toggleDatabase(databaseName)}><span className={`mysql-tree-toggle ${expanded ? 'expanded' : ''}`} aria-hidden="true"><svg viewBox="0 0 16 16"><path d="m6 3 5 5-5 5" /></svg></span><span className="mysql-tree-icon" aria-hidden="true" /><b>{databaseName}</b><small>{tablesByDatabase[databaseName]?.length ?? ''}</small></button>{expanded && <div className="mysql-tree-tables">{loadingDatabase === databaseName ? <span className="mysql-tree-status">正在读取表...</span> : visibleTables.length > 0 ? visibleTables.map(tableName => <button className={database === databaseName && table === tableName ? 'active' : ''} type="button" onClick={() => selectTable(databaseName, tableName)} key={tableName}><span aria-hidden="true" /><b>{tableName}</b></button>) : <span className="mysql-tree-status">暂无数据表</span>}</div>}</div>})}{!loading && visibleDatabases.length === 0 && <span className="mysql-tree-status">没有匹配的对象</span>}</div></aside>
      <section className="mysql-table-workspace">
        <header className="mysql-table-head"><div><b>{table || '请选择数据表'}</b>{table && <span>{database} / {table}</span>}</div><div className="mysql-table-actions"><small>{table ? `共 ${preview.totalExact ? '' : '约 '}${preview.total} 行 · ${structure.length} 个字段` : database || '默认数据库'}</small><button className="secondary" type="button" onClick={() => setTransferMode('import')}>导入</button><button className="secondary" type="button" onClick={() => setTransferMode('export')}>导出</button><button className="secondary" type="button" onClick={openSQL}>执行 SQL</button><MySQLTaskCenter sourceID={sourceID} />{table && <button type="button" onClick={openInsert}>新增行</button>}</div></header>
        <nav className="mysql-view-tabs" aria-label="MySQL 工作区视图">{table && <button className={activeView === 'data' ? 'active' : ''} type="button" onClick={() => setActiveView('data')}>数据</button>}{table && <button className={activeView === 'structure' ? 'active' : ''} type="button" onClick={() => setActiveView('structure')}>表结构</button>}<button className={activeView === 'sql' ? 'active' : ''} type="button" onClick={openSQL}>SQL</button></nav>
        {activeView === 'sql' ? sqlWorkspace : table ? loadingTable ? <div className="mysql-workspace-empty">正在读取 {database}.{table}...</div> : activeView === 'data' ? <div className="mysql-data-view"><form className="mysql-table-query-bar" onSubmit={applyTableFilter}><AppSelect value={tableFilterColumn} placeholder="选择筛选字段" onChange={value => { setTableFilterColumn(value); if (!value) clearTableFilter() }} options={[{ value: '', label: '选择筛选字段' }, ...preview.columns.map(column => ({ value: column, label: column }))]} /><input value={tableFilterDraft} disabled={!tableFilterColumn} placeholder="输入包含的内容" onChange={event => setTableFilterDraft(event.target.value)} /><button type="submit" disabled={!tableFilterColumn || !tableFilterDraft.trim()}>查询</button><button className="secondary" type="button" disabled={!tableFilterValue && !tableFilterDraft} onClick={clearTableFilter}>清除</button><span>排序</span><AppSelect value={tableSortColumn} placeholder="默认主键" onChange={value => { setTableSortColumn(value); setTablePage(1) }} options={[{ value: '', label: '默认主键' }, ...preview.columns.map(column => ({ value: column, label: column }))]} /><AppSelect className="mysql-sort-direction" value={tableSortDirection} onChange={value => { setTableSortDirection(value); setTablePage(1) }} disabled={!tableSortColumn} options={[{ value: 'asc', label: '升序' }, { value: 'desc', label: '降序' }]} /></form><div className="mysql-preview"><table><thead><tr><th className="mysql-row-actions-column">操作</th>{preview.columns.map(column => <th key={column}>{column}</th>)}</tr></thead><tbody>{preview.rows.map((row, index) => <tr key={index}><td className="mysql-row-actions"><button type="button" disabled={primaryKeys.length === 0} title={primaryKeys.length === 0 ? '该表没有主键，不能安全编辑' : '编辑记录'} onClick={() => openEdit(index)}>编辑</button><button className="danger" type="button" disabled={primaryKeys.length === 0} title={primaryKeys.length === 0 ? '该表没有主键，不能安全删除' : '删除记录'} onClick={() => setDeleteRowIndex(index)}>删除</button></td>{row.map((value, cell) => <td key={cell}>{value === null ? <i>NULL</i> : String(value)}</td>)}</tr>)}</tbody></table>{preview.rows.length === 0 && <div className="mysql-workspace-empty">该表暂无匹配数据</div>}{primaryKeys.length === 0 && preview.rows.length > 0 && <div className="mysql-readonly-notice">该表没有主键，记录编辑与删除已禁用。</div>}</div><MySQLPagination page={tablePage} pageSize={tablePageSize} total={preview.total} totalPages={preview.totalPages} onPageChange={setTablePage} onPageSizeChange={value => { setTablePageSize(value); setTablePage(1) }} ariaLabel="表数据分页" /></div> : <div className="mysql-preview mysql-structure"><table><thead><tr><th>#</th><th>字段</th><th>类型</th><th>允许空</th><th>键</th><th>默认值</th><th>Extra</th><th>注释</th></tr></thead><tbody>{structure.map((column, index) => <tr key={column.name}><td>{index + 1}</td><td><b>{column.name}</b></td><td><code>{column.type}</code></td><td>{column.nullable ? '是' : '否'}</td><td>{column.key || '-'}</td><td>{column.default === null ? <i>NULL</i> : column.default}</td><td>{column.extra || '-'}</td><td>{column.comment || '-'}</td></tr>)}</tbody></table></div> : <div className="mysql-workspace-empty"><b>从左侧数据库树选择一张表</b><span>也可以直接进入 SQL 标签执行查询</span></div>}
      </section>
    </section>
    {rowEditor && <div className="modal-backdrop" role="presentation" onClick={event => { if (event.target === event.currentTarget && !mutating) setRowEditor(null) }}><section className="surface mysql-row-editor" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>{rowEditor.mode === 'insert' ? '新增记录' : '编辑记录'}</h2><p>{database}.{table}</p></div><button className="close-button" type="button" disabled={mutating} onClick={() => setRowEditor(null)}>×</button></header><form onSubmit={saveRow}><div className="mysql-row-fields">{editableColumns.map(column => { const longValue = /text|json|blob/i.test(column.type); const nullValue = Boolean(rowEditor.nulls[column.name]); return <label key={column.name}><span><b>{column.name}</b><code>{column.type}</code>{column.key === 'PRI' && <em>主键</em>}</span>{longValue ? <textarea disabled={nullValue} value={rowEditor.values[column.name] || ''} onChange={event => setRowEditor(current => current ? { ...current, values: { ...current.values, [column.name]: event.target.value } } : current)} /> : <input disabled={nullValue} value={rowEditor.values[column.name] || ''} onChange={event => setRowEditor(current => current ? { ...current, values: { ...current.values, [column.name]: event.target.value } } : current)} />}<small><input type="checkbox" checked={nullValue} disabled={!column.nullable} onChange={event => setRowEditor(current => current ? { ...current, nulls: { ...current.nulls, [column.name]: event.target.checked } } : current)} /> NULL</small></label>})}</div><footer className="modal-actions"><button className="button secondary" type="button" disabled={mutating} onClick={() => setRowEditor(null)}>取消</button><button className="button" type="submit" disabled={mutating}>{mutating ? '保存中...' : '保存'}</button></footer></form></section></div>}
    {deleteRowIndex !== null && <div className="modal-backdrop" role="presentation" onClick={event => { if (event.target === event.currentTarget && !mutating) setDeleteRowIndex(null) }}><section className="surface mysql-delete-confirm" role="alertdialog" aria-modal="true"><header><b>确认删除这条记录？</b><span>将按主键从 {database}.{table} 删除，操作不可撤销。</span></header><footer className="modal-actions"><button className="button secondary" type="button" disabled={mutating} onClick={() => setDeleteRowIndex(null)}>取消</button><button className="button danger" type="button" disabled={mutating} onClick={() => void deleteRow()}>{mutating ? '删除中...' : '确认删除'}</button></footer></section></div>}
    {transferMode && <MySQLTransferDialog mode={transferMode} sourceID={sourceID} database={database} table={table} onClose={() => setTransferMode(null)} onImported={() => setReloadSequence(current => current + 1)} onQueued={() => showMutationMessage('导入任务已创建，可在“后台任务”中查看执行结果')} />}
  </div>
}

function MySQLTransferDialog({ mode, sourceID, database, table, onClose, onImported, onQueued }: { mode: 'export' | 'import'; sourceID: string; database: string; table: string; onClose: () => void; onImported: () => void; onQueued: () => void }) {
  const [format, setFormat] = useState('sql')
  const [scope, setScope] = useState('data')
  const [file, setFile] = useState<File | null>(null)
  const [confirmed, setConfirmed] = useState(false)
  const [confirmationRequired, setConfirmationRequired] = useState(true)
  const [running, setRunning] = useState(false)
  const [message, setMessage] = useState('')
	useEffect(() => { void fetch(`${api}/system-settings/confirmations`).then(response => response.json()).then(data => setConfirmationRequired(data.requireWriteConfirmation !== false)).catch(() => setConfirmationRequired(true)) }, [])
	const [job, setJob] = useState<MySQLAsyncJob | null>(null)
	const [importResult, setImportResult] = useState<{ affected: number; completed: boolean; results: Array<{ index: number; statement: string; status: string; affected: number; error?: string }> } | null>(null)
  const jobID = job?.id
  const jobStatus = job?.status
  useEffect(() => { if (!jobID || !['queued', 'running'].includes(jobStatus || '')) return; let active = true; const poll = async () => { try { const response = await fetch(`${api}/mysql-jobs/${jobID}`); const data = await response.json(); if (!response.ok) throw new Error(data.error || '读取任务失败'); if (!active) return; setJob(data); setRunning(['queued', 'running'].includes(data.status)); if (data.status === 'succeeded' && mode === 'import') { setImportResult(data.result || null); onImported() } } catch (err) { if (active) { setMessage(err instanceof Error ? err.message : '读取任务失败'); setRunning(false) } } }; void poll(); const timer = window.setInterval(() => void poll(), 1000); return () => { active = false; window.clearInterval(timer) } }, [jobID, jobStatus, mode, onImported])
  const exportFile = async () => { if (!database) { setMessage('请先选择一个数据库'); return }; if (!table && format !== 'sql') { setMessage('未选择表时仅支持导出 SQL；请选择表后再导出 CSV 或 JSON'); return }; if (format === 'csv' && scope !== 'data') { setMessage('CSV 仅支持导出库表数据'); return }; setRunning(true); setMessage(''); try { const response = await fetch(`${api}/data-sources/${sourceID}/export-jobs`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ database, table, format, scope }) }); const data = await response.json(); if (!response.ok) throw new Error(data.error || '创建导出任务失败'); setJob(data) } catch (err) { setMessage(err instanceof Error ? err.message : '创建导出任务失败'); setRunning(false) } }
  const importFile = async () => { if (!file) { setMessage('请选择 SQL、CSV 或 JSON 文件'); return }; if (confirmationRequired && !confirmed) { setMessage('请确认导入会写入目标数据库'); return }; const resolvedFormat = file.name.split('.').pop()?.toLowerCase() || ''; if (!['sql', 'csv', 'json'].includes(resolvedFormat)) { setMessage('仅支持 SQL、CSV、JSON 文件'); return }; if (resolvedFormat !== 'sql' && (!database || !table)) { setMessage('CSV 和 JSON 导入前请先选择目标库和表'); return }; setRunning(true); setMessage(''); try { const body = new FormData(); body.append('file', file); body.append('format', resolvedFormat); body.append('database', database); body.append('table', table); body.append('confirmed', String(confirmed)); const response = await fetch(`${api}/data-sources/${sourceID}/import-jobs`, { method: 'POST', body }); const data = await response.json(); if (!response.ok) throw new Error(data.error || '创建导入任务失败'); onQueued(); onClose() } catch (err) { setMessage(err instanceof Error ? err.message : '创建导入任务失败'); setRunning(false) } }
  const cancelJob = async () => { if (!job) return; const response = await fetch(`${api}/mysql-jobs/${job.id}/cancel`, { method: 'POST' }); const data = await response.json(); if (!response.ok) { setMessage(data.error || '取消失败'); return }; setJob(current => current ? { ...current, message: '正在取消' } : current) }
	useEffect(() => { if (jobStatus !== 'succeeded' || mode !== 'export' || !jobID) return; const link = document.createElement('a'); link.href = `${api}/mysql-jobs/${jobID}/download`; link.download = job?.fileName || `${database}_${table}.${format}`; document.body.append(link); link.click(); link.remove() }, [jobID, jobStatus, job?.fileName, mode, database, table, format])
	// Cancellation is kept with the task state for the next dialog interaction update.
	void cancelJob
  const title = mode === 'export' ? '导出数据库对象' : '导入数据库文件'
  return <div className="modal-backdrop" role="presentation" onClick={event => { if (event.target === event.currentTarget && !running) onClose() }}><section className="surface mysql-transfer-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>{title}</h2><p>{database && table ? `${database}.${table}` : database ? `${database}（整个数据库）` : '请先选择目标库'}</p></div><button className="close-button" type="button" disabled={running} onClick={onClose}>×</button></header>{mode === 'export' ? <div className="mysql-transfer-form"><label>导出格式<AppSelect value={format} onChange={setFormat} options={[{ value: 'sql', label: 'SQL' }, { value: 'csv', label: 'CSV' }, { value: 'json', label: 'JSON' }]} /></label><label>导出内容<AppSelect value={scope} onChange={setScope} options={[{ value: 'structure', label: '仅表结构' }, { value: 'schema', label: '库和表结构' }, { value: 'data', label: '库表数据' }]} /></label><p>{format === 'csv' ? 'CSV 导出包含所选表的全部数据和字段表头。' : scope === 'data' ? '导出建库、建表语句及表数据。' : scope === 'schema' ? '导出建库和建表语句。' : '仅导出所选表的建表语句。'}</p></div> : <div className="mysql-transfer-form"><label>选择文件<span className="mysql-transfer-file-picker"><input type="file" accept=".sql,.csv,.json,application/sql,text/csv,application/json" onChange={event => setFile(event.target.files?.[0] || null)} /><b>选择文件</b><em>{file?.name || '未选择任何文件'}</em></span></label><p>SQL 按文件中的语句执行；CSV 和 JSON 写入当前选择的 <strong className="mysql-transfer-target-name">{database && table ? `${database}.${table}` : '目标表'}</strong>。</p><label className="mysql-transfer-confirm"><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} /> 我确认导入会修改目标数据库的数据或结构</label>{importResult && <div className="mysql-import-results"><b>{importResult.completed ? `导入完成，受影响 ${importResult.affected} 行` : '导入未完成，已执行的语句不会自动回滚'}</b>{importResult.results.map(item => <article key={item.index} className={item.status}><span>#{item.index}</span><code>{item.statement}</code><em>{item.status === 'success' ? `成功${item.affected ? `，${item.affected} 行` : ''}` : item.status === 'skipped' ? '未执行' : item.error || '失败'}</em></article>)}</div>}</div>}{message && <div className="form-error">{message}</div>}<footer className="modal-actions"><button className="button secondary" type="button" disabled={running} onClick={onClose}>{importResult ? '关闭' : '取消'}</button>{!importResult && <button className="button" type="button" disabled={running || (mode === 'export' && !database)} onClick={() => void (mode === 'export' ? exportFile() : importFile())}>{running ? (mode === 'export' ? '导出中...' : '导入中...') : mode === 'export' ? '开始导出' : '确认导入'}</button>}</footer></section></div>
}

function MySQLBackgroundQuery({ sourceID, database, sql, confirmed }: { sourceID: string; database: string; sql: string; confirmed: boolean }) {
  const [job, setJob] = useState<MySQLAsyncJob | null>(null); const [message, setMessage] = useState('')
  const jobID = job?.id
  const jobRunning = Boolean(job && ['queued', 'running'].includes(job.status))
  useEffect(() => { if (!jobID || !jobRunning) return; let active = true; const poll = async () => { try { const response = await fetch(`${api}/mysql-jobs/${jobID}`); const data = await response.json(); if (active && response.ok) setJob(data) } catch { /* Keep the last visible task state until the next poll. */ } }; void poll(); const timer = window.setInterval(() => void poll(), 1000); return () => { active = false; window.clearInterval(timer) } }, [jobID, jobRunning])
  const start = async () => { if (!sql.trim()) { setMessage('请输入 SQL 语句'); return }; const readOnly = /^(select|show|describe|desc|explain|with)\b/i.test(sql.trim()); if (!readOnly && !confirmed) { setMessage('请先在“执行”流程确认写操作'); return }; setMessage(''); const response = await fetch(`${api}/data-sources/${sourceID}/query-jobs`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ database, sql, confirmed: readOnly || confirmed }) }); const data = await response.json(); if (!response.ok) { setMessage(data.error || '创建后台任务失败'); return }; setJob(data) }
  const cancel = async () => { if (!job) return; await fetch(`${api}/mysql-jobs/${job.id}/cancel`, { method: 'POST' }); setJob({ ...job, message: '正在取消' }) }
  return <div className="mysql-background-query"><button className="button secondary" type="button" onClick={() => void start()} disabled={jobRunning}>后台执行</button>{job && <span>{job.message} · {job.progress}%</span>}{jobRunning && <button className="text-button" type="button" onClick={() => void cancel()}>取消</button>}{message && <em>{message}</em>}</div>
}

function SQLHistoryControl({ items, loading, total, page, onOpen, onPageChange, onCopy, onSelect }: { items: SQLHistoryItem[]; loading: boolean; total: number; page: number; onOpen: () => void; onPageChange: (page: number) => void; onCopy: (sql: string) => void; onSelect: (item: SQLHistoryItem) => void }) {
  const [open, setOpen] = useState(false)
  const pages = Math.max(1, Math.ceil(total / 10))
  return <><button className="button secondary sql-history-trigger" type="button" onClick={() => { setOpen(true); onOpen() }}>历史记录</button>{open && <div className="modal-backdrop" role="presentation" onClick={event => { if (event.target === event.currentTarget) setOpen(false) }}><section className="surface sql-history-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>SQL 历史记录</h2><p>共 {total} 条记录</p></div><button className="close-button" type="button" onClick={() => setOpen(false)}>×</button></header><div className="sql-history-modal-list">{loading ? <div className="empty-state"><b>正在加载历史记录</b></div> : items.length === 0 ? <div className="empty-state"><b>暂无执行记录</b></div> : items.map(item => <article key={item.id}><header><span className={item.status === 'success' ? 'success' : 'failed'} /> <b>{item.database || '默认数据库'}</b><time>{item.createdAt}</time></header><code>{item.statement}</code><footer><small>{item.status === 'success' ? `${item.resultRows > 0 ? `${item.resultRows} 行结果` : `影响 ${item.affected} 行`}` : item.error || '执行失败'} · {item.durationMs}ms</small><div><button className="text-button" type="button" onClick={() => onSelect(item)}>载入 SQL</button><button className="text-button" type="button" onClick={() => onCopy(item.statement)}>复制 SQL</button></div></footer></article>)}</div>{total > 0 && <UnifiedPagination total={total} page={page} totalPages={pages} onPageChange={onPageChange} ariaLabel="SQL 历史分页" />}</section></div>}</>
}

function MySQLTaskCenter({ sourceID }: { sourceID: string }) {
  const [jobs, setJobs] = useState<MySQLAsyncJob[]>([])
  const [expanded, setExpanded] = useState(false)
  const [resultJob, setResultJob] = useState<MySQLAsyncJob | null>(null)
  const root = useRef<HTMLElement>(null)
  const load = async () => { const response = await fetch(`${api}/mysql-jobs?sourceId=${encodeURIComponent(sourceID)}`); if (response.ok) { const data = await response.json(); setJobs(Array.isArray(data.items) ? data.items : []) } }
  useEffect(() => { void load(); const timer = window.setInterval(() => void load(), 2000); return () => window.clearInterval(timer) }, [sourceID])
  useEffect(() => { if (!expanded) return; const close = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setExpanded(false) }; document.addEventListener('pointerdown', close); return () => document.removeEventListener('pointerdown', close) }, [expanded])
  const cancel = async (id: string) => { await fetch(`${api}/mysql-jobs/${id}/cancel`, { method: 'POST' }); void load() }
  const viewResult = async (id: string) => { const response = await fetch(`${api}/mysql-jobs/${id}`); if (response.ok) setResultJob(await response.json()) }
  if (jobs.length === 0) return null
  const results = Array.isArray(resultJob?.result?.results) ? resultJob.result.results : []
  return <><section ref={root} className={`surface mysql-task-center ${expanded ? 'expanded' : ''}`}><header><button className="mysql-task-toggle" type="button" onClick={() => setExpanded(value => !value)} aria-expanded={expanded}><div><h3>后台任务</h3><span>最近 {jobs.length} 项</span></div></button></header>{expanded && <div className="mysql-task-list">{jobs.map(job => { const failed = job.status === 'failed'; const progress = failed ? 0 : Math.min(100, Math.max(0, job.progress)); const name = job.kind === 'query' ? 'SQL 查询' : job.kind === 'import' ? `导入任务 · ${job.fileName || '未记录上传文件名'}` : `导出任务 · ${job.fileName || '未命名文件'}`; return <article className={failed ? 'failed' : ''} key={job.id}><div><b title={name}>{name}</b><small title={job.id}>任务编号：{job.id} · {job.message} · {new Date(job.createdAt).toLocaleString('zh-CN')}</small><i className="mysql-task-progress"><em style={{ width: `${progress}%` }} /></i></div><strong>{progress}%</strong>{['queued', 'running'].includes(job.status) ? <button className="text-button danger" type="button" onClick={() => void cancel(job.id)}>取消</button> : job.kind === 'import' ? <button className="text-button" type="button" onClick={() => void viewResult(job.id)}>查看结果</button> : job.status === 'succeeded' ? <a className="text-button" href={`${api}/mysql-jobs/${job.id}/download`} download={job.fileName || 'mysql-result.csv'}>下载结果</a> : <span>失败</span>}</article>})}</div>}</section>{resultJob && <div className="modal-backdrop" role="presentation" onClick={event => { if (event.target === event.currentTarget) setResultJob(null) }}><section className="surface mysql-import-results-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>导入结果</h2><p>{resultJob.message}</p></div><button className="close-button" type="button" onClick={() => setResultJob(null)}>×</button></header><div className="mysql-import-results">{results.length ? results.map((item: any) => <article key={item.index} className={item.status}><span>#{item.index}</span><code>{item.statement}</code><em>{item.status === 'success' ? `成功${item.affected ? `，${item.affected} 行` : ''}` : item.status === 'skipped' ? '未执行' : item.error || '失败'}</em></article>) : <p>暂无可展示的逐条执行结果。</p>}</div><footer className="modal-actions"><button className="button" type="button" onClick={() => setResultJob(null)}>关闭</button></footer></section></div>}</>
}

function Ambari() {
  const location = useLocation()
  const sourceID = new URLSearchParams(location.search).get('source') || ''
  const [overview, setOverview] = useState<AmbariOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const load = async () => {
    if (!sourceID) { setOverview(null); setLoading(false); return }
    setLoading(true)
    try { const response = await fetch(`${api}/ambari/${sourceID}/overview`); const data = await response.json(); if (!response.ok) throw new Error(data.error || 'Ambari 数据加载失败'); setOverview(data.data); setMessage('') } catch (err) { setMessage(err instanceof Error ? err.message : 'Ambari 数据加载失败') } finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [sourceID])
  const badAlerts = overview?.alerts.filter(item => ['CRITICAL', 'WARNING', 'UNKNOWN'].includes(item.state.toUpperCase())) || []
  return <div className="page ambari-page"><PageHead title="Ambari 集群" description="服务、主机、告警与后台操作由 Ambari Web API 读取。" actionNode={<button className="refresh-button" type="button" onClick={() => void load()} disabled={loading}>{loading ? '刷新中...' : '刷新'}</button>} />{message ? <section className="surface empty-state"><b>读取失败</b><span>{message}</span></section> : loading ? <section className="surface empty-state"><b>正在读取 Ambari 集群</b></section> : !overview ? <section className="surface empty-state"><b>未选择 Ambari 数据源</b><span>请在数据节点中导入 Ambari 菜单后再访问。</span></section> : <><section className="overview-stat-grid"><article className="overview-stat source"><span>集群名称</span><b className="ambari-cluster-name">{overview.clusterName}</b><small>{overview.stack || overview.version || 'Ambari'}</small></article><article className="overview-stat rule"><span>服务</span><b>{overview.services.length}</b><small>已接入的服务组件</small></article><article className="overview-stat health"><span>主机</span><b>{overview.hosts.length}</b><small>由 Ambari 管理</small></article><article className={`overview-stat ${badAlerts.length ? 'alert' : 'notice'}`}><span>活跃告警</span><b>{badAlerts.length}</b><small>Critical / Warning / Unknown</small></article></section><section className="ambari-grid"><article className="surface ambari-panel"><header><h2>服务状态</h2><span>{overview.services.length} 项</span></header><div className="ambari-list">{overview.services.map(item => <div key={item.name}><b>{item.name}</b><span className={item.state === 'STARTED' ? 'success' : 'danger'}>{item.state || '-'}</span><small>{item.maintenance || '正常维护状态'}</small></div>)}{overview.services.length === 0 && <p>Ambari 未返回服务清单。</p>}</div></article><article className="surface ambari-panel"><header><h2>主机状态</h2><span>{overview.hosts.length} 台</span></header><div className="ambari-list">{overview.hosts.map(item => <div key={item.name}><b>{item.name}</b><span className={item.status === 'HEALTHY' ? 'success' : 'danger'}>{item.status || '-'}</span><small>{item.maintenance || '正常维护状态'}</small></div>)}{overview.hosts.length === 0 && <p>Ambari 未返回主机清单。</p>}</div></article></section><section className="surface ambari-panel"><header><h2>Ambari 告警</h2><span>{overview.alerts.length} 条</span></header><div className="ambari-list wide-list">{overview.alerts.map((item, index) => <div key={`${item.label}-${index}`}><b>{item.label || '未命名告警'}</b><span className={['OK', 'NONE'].includes(item.state.toUpperCase()) ? 'success' : 'danger'}>{item.state || '-'}</span><small>{[item.service, item.host, item.text].filter(Boolean).join(' · ')}</small></div>)}{overview.alerts.length === 0 && <p>暂无 Ambari 告警。</p>}</div></section><section className="surface ambari-panel"><header><h2>最近后台操作</h2><span>最多 20 条</span></header><div className="ambari-list wide-list">{overview.requests.map(item => <div key={item.id}><b>{item.context || `请求 #${item.id}`}</b><span className={item.status === 'Completed' ? 'success' : 'danger'}>{item.status || '-'}</span><small>请求 ID：{item.id}</small></div>)}{overview.requests.length === 0 && <p>暂无可读取的后台操作。</p>}</div></section></>}</div>
}

function DataSources() {
  const [allSources, setSources] = useState<Source[]>([])
  const [sourcePage, setSourcePage] = useState(1)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Source | null>(null)
  const [selectedType, setSelectedType] = useState<'Prometheus' | 'MySQL' | 'SSH' | 'Hadoop' | 'Ambari' | 'Consul'>('Prometheus')
  const [sshAuthMode, setSSHAuthMode] = useState<'password' | 'agent'>('password')
  const [importTarget, setImportTarget] = useState<{ source: Source; kind: 'dashboard' | 'hadoop' | 'ambari' | 'mysql' | 'consul' } | null>(null)
  const [importName, setImportName] = useState('')
  const [importing, setImporting] = useState(false)
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [agentToken, setAgentToken] = useState<{ token: string; host: string; port: string } | null>(null)
  const [deletingSource, setDeletingSource] = useState<Source | null>(null)
  const [, setRefreshing] = useState(false)
  const refreshInterval = useRefreshInterval()
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => ['Prometheus', 'MySQL', 'SSH', 'Agent', 'Hadoop', 'Ambari', 'Consul', 'Redis', 'ClickHouse', 'Kafka'].includes(item.type)) : [])
    } catch { setSources([]) }
  }
  useEffect(() => { void loadSources(); const timer = window.setInterval(loadSources, refreshInterval); return () => window.clearInterval(timer) }, [refreshInterval])
  const openModal = (source?: Source) => { setEditing(source || null); setSelectedType(source?.type === 'Agent' ? 'SSH' : (source?.type || 'Prometheus') as 'Prometheus' | 'MySQL' | 'SSH' | 'Hadoop' | 'Ambari'); setSSHAuthMode(source?.type === 'Agent' || source?.options?.authMode === 'agent' ? 'agent' : 'password'); setModalOpen(true); setMessage('') }
  const sourceLogo = (source: Source) => source.type === 'MySQL' ? 'M' : source.type === 'SSH' ? 'S' : source.type === 'Agent' ? 'A' : source.type === 'Hadoop' ? 'H' : source.type === 'Ambari' ? 'A' : source.type === 'Redis' ? 'R' : source.type === 'ClickHouse' ? 'C' : source.type === 'Kafka' ? 'K' : 'P'
  const sourceSubtitle = (source: Source) => (source.type === 'Hadoop' || source.type === 'Ambari') && /^https?:\/\//i.test(source.host) ? `${source.type} · ${source.host}` : `${source.type === 'Agent' ? 'SSH - Agent' : source.type} · ${source.host}:${source.port}`
  const sourcePages = Math.max(1, Math.ceil(allSources.length / 20))
  const currentSourcePage = Math.min(sourcePage, sourcePages)
  const sources = allSources.slice((currentSourcePage - 1) * 20, currentSourcePage * 20)
  useEffect(() => { setSourcePage(current => Math.min(current, sourcePages)) }, [sourcePages])
  const importDashboard = (source: Source) => {
    if (!['MySQL', 'SSH', 'Agent', 'Redis', 'ClickHouse', 'Kafka'].includes(source.type)) { showMessage('该数据源类型暂不支持导入大屏'); return }
    setImportName(`${source.name} 大屏`)
    setImportTarget({ source, kind: 'dashboard' })
  }
  const importHadoopMenu = (source: Source) => {
    if (source.type !== 'Hadoop') return
    setImportName(source.name)
    setImportTarget({ source, kind: 'hadoop' })
  }
  const importMySQLMenu = (source: Source) => { if (source.type === 'MySQL') { setImportName(`${source.name} 工作台`); setImportTarget({ source, kind: 'mysql' }) } }
  const importConsulMenu = (source: Source) => { if (source.type === 'Consul') { setImportName(`${source.name} KV 工作台`); setImportTarget({ source, kind: 'consul' }) } }
  const checkAgentStatus = async (source: Source) => {
    try {
      const response = await fetch(`${api}/data-sources/${source.id}/agent-status`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '读取状态失败')
      showMessage(`Agent 在线 · 版本 ${data.version || '未上报'} · 白名单 ${data.ipWhitelistConfigured === true ? '已配置' : data.ipWhitelistConfigured === false ? '未配置' : '未知'}`)
    } catch (err) { showMessage(err instanceof Error ? err.message : '读取 Agent 状态失败') }
  }
  const confirmImport = async () => {
    if (!importTarget) return
    const name = importName.trim()
    if (!name) { showMessage('请输入名称'); return }
    setImporting(true)
    try {
      if (importTarget.kind === 'dashboard') {
        const { source } = importTarget
        await saveDashboardItem({ id: '', name, sourceId: source.id, sourceName: source.name, sourceType: source.type, createdAt: new Date().toISOString() })
        showMessage(`${name} 已导入大屏`)
      } else if (importTarget.kind === 'hadoop') {
        await saveHadoopMenuItem(importTarget.source.id, name)
        showMessage(`${name} 已导入菜单`)
      } else if (importTarget.kind === 'mysql') {
        await saveMySQLMenuItem(importTarget.source.id, name)
        showMessage(`${name} 已导入菜单`)
      } else if (importTarget.kind === 'consul') {
        await saveConsulMenuItem(importTarget.source.id, name)
        showMessage(`${name} 已导入菜单`)
      } else {
        await saveAmbariMenuItem(importTarget.source.id, name)
        showMessage(`${name} 已导入菜单`)
      }
      setImportTarget(null)
    } catch (err) { showMessage(err instanceof Error ? err.message : '导入失败') } finally { setImporting(false) }
  }
  const buildPayload = (formElement: HTMLFormElement): Source => {
    const form = new FormData(formElement)
    const type = String(form.get('type') || selectedType) as 'Prometheus' | 'MySQL' | 'SSH' | 'Hadoop' | 'Ambari' | 'Consul'
    const needsCredentials = type === 'MySQL' || type === 'SSH' || type === 'Ambari' || type === 'Consul'
    const nodeManagerUrl = String(form.get('nodeManagerUrl') || '').trim()
    const jobHistoryUrl = String(form.get('jobHistoryUrl') || '').trim()
    const sshSourceId = String(form.get('sshSourceId') ?? editing?.options?.sshSourceId ?? '').trim()
    const options: Record<string, string> | undefined = type === 'Hadoop' ? { ...editing?.options, ...(nodeManagerUrl ? { nodeManagerUrl } : {}), ...(jobHistoryUrl ? { jobHistoryUrl } : {}), ...(sshSourceId ? { sshSourceId } : {}) } : type === 'SSH' ? { ...editing?.options, ...(sshAuthMode === 'agent' ? { authMode: 'agent' } : {}) } : undefined
    if (options && !nodeManagerUrl) delete options.nodeManagerUrl
    if (options && !jobHistoryUrl) delete options.jobHistoryUrl
    if (options && !sshSourceId) delete options.sshSourceId
    if (options && type === 'SSH' && sshAuthMode !== 'agent') delete options.authMode
    return { id: editing?.id || '', name: String(form.get('name') || ''), type, host: String(form.get('host') || ''), port: String(form.get('port') || ''), username: type === 'MySQL' || type === 'SSH' || type === 'Ambari' || type === 'Consul' ? String(form.get('username') || '') : '', password: String(form.get(needsCredentials ? 'password' : 'token') || ''), database: type === 'MySQL' ? String(form.get('database') || '') : '', options, remark: String(form.get('remark') || ''), enabled: true, status: '待测试', lastTest: '' }
  }
  const saveSource = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    const payload = buildPayload(event.currentTarget)
    try {
      const response = await fetch(editing ? `${api}/data-sources/${editing.id}` : `${api}/data-sources`, { method: editing ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存失败')
      if (payload.type === 'SSH' && sshAuthMode === 'agent' && !editing && data.password) {
        setAgentToken({ token: String(data.password), host: String(data.host || payload.host), port: String(data.port || payload.port) })
        showMessage(`${data.name} 已保存`)
      } else showMessage(`${data.name} 已保存`)
      setModalOpen(false)
      setEditing(null)
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (err) {
      showMessage(err instanceof Error ? err.message : '保存失败')
    } finally { setSaving(false) }
  }
  const testSource = async (formElement: HTMLFormElement) => {
    if (selectedType === 'SSH' && sshAuthMode === 'agent' && !editing) { showMessage('请先保存以生成 Agent 令牌，再完成 Agent 部署后测试连接'); return }
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
      setSources(Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => ['Prometheus', 'MySQL', 'SSH', 'Agent', 'Hadoop', 'Ambari', 'Redis', 'ClickHouse', 'Kafka'].includes(item.type)) : [])
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
      showMessage(`${source.name} 已删除`)
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (err) { showMessage(err instanceof Error ? err.message : '删除失败') }
  }
  const deleteSource = (source: Source) => setDeletingSource(source)
  void refreshHealth
  const pageAction = <div className="page-action-group"><button className="button" onClick={() => openModal()}><Icon name="plus" /> 新增数据源</button></div>
  return <div className="page"><PageHead title="数据节点" description="Prometheus、MySQL、SSH、Hadoop、Ambari 和 Consul 数据源统一接入。" actionNode={pageAction} />{sources.length === 0 ? <section className="surface empty-state"><b>暂无数据源</b><span>点击右上角新增数据源。</span></section> : <><section className="source-list">{sources.map(source => <article className={`surface source-row ${source.status === '健康' ? 'healthy' : 'warning'} ${!source.enabled ? 'disabled' : ''}`} key={source.id}><div className="node-main"><span className="source-logo">{sourceLogo(source)}</span><div><h3>{source.name}</h3><p>{sourceSubtitle(source)}</p>{source.remark && <small className="source-remark">{source.remark}</small>}</div></div><div className="node-status" /><div className="node-enabled"><StatusSwitch checked={source.enabled} onChange={(checked) => void toggleEnabled(source, checked)} /></div><div className="node-meta"><span>最近检测：{formatCollectedAt(source.lastTest)}</span><span>{source.status}</span></div><div className="source-actions">{['MySQL', 'SSH', 'Agent', 'Redis', 'ClickHouse', 'Kafka'].includes(source.type) && <button type="button" onClick={() => importDashboard(source)}>导入大屏</button>}{source.type === 'MySQL' && <button type="button" onClick={() => importMySQLMenu(source)}>导入工作台</button>}{source.type === 'Consul' && <button type="button" onClick={() => importConsulMenu(source)}>导入工作台</button>}{source.type === 'Hadoop' && <button type="button" onClick={() => importHadoopMenu(source)}>导入菜单</button>}{source.type === 'Ambari' && <button type="button" onClick={() => { setImportName(source.name); setImportTarget({ source, kind: 'ambari' }) }}>导入菜单</button>}{(source.type === 'Agent' || (source.type === 'SSH' && source.options?.authMode === 'agent')) && <button type="button" onClick={() => void checkAgentStatus(source)}>Agent 状态</button>}<button type="button" onClick={() => openModal(source)}>编辑</button><button className="danger" type="button" onClick={() => deleteSource(source)}>删除</button></div></article>)}</section><ListPagination total={allSources.length} page={currentSourcePage} onPageChange={setSourcePage} /></>}{agentToken && <AgentTokenDialog {...agentToken} onClose={() => setAgentToken(null)} />}{deletingSource && <DataSourceDeleteDialog sourceName={deletingSource.name} onClose={() => setDeletingSource(null)} onConfirm={() => { const source = deletingSource; setDeletingSource(null); void performDeleteSource(source) }} />}{importTarget && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget && !importing) setImportTarget(null) }}><section className="surface import-name-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>{importTarget.kind === 'dashboard' ? '导入大屏' : '导入菜单'}</h2><p>{importTarget.source.name}</p></div><button className="close-button" type="button" disabled={importing} onClick={() => setImportTarget(null)}>×</button></header><label className="import-name-field">名称 <span className="required-mark">*</span><input autoFocus value={importName} maxLength={120} onChange={(event) => setImportName(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); void confirmImport() } }} /></label><footer className="modal-actions"><button className="button secondary" type="button" disabled={importing} onClick={() => setImportTarget(null)}>取消</button><button className="button" type="button" disabled={importing} onClick={() => void confirmImport()}>{importing ? '导入中...' : '确认导入'}</button></footer></section></div>}{modalOpen && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setModalOpen(false) }}><section className="surface source-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2>{editing ? '编辑数据源' : '新增数据源'}</h2><p>{selectedType} 接入</p></div><button className="close-button" type="button" onClick={() => setModalOpen(false)}>×</button></header><form onSubmit={saveSource}><div className="modal-form"><label>类型 <span className="required-mark">*</span><AppSelect name="type" value={selectedType} onChange={(next) => setSelectedType(next as 'Prometheus' | 'MySQL' | 'SSH' | 'Hadoop')} disabled={!!editing} options={[{ value: 'Prometheus', label: 'Prometheus' }, { value: 'MySQL', label: 'MySQL' }, { value: 'SSH', label: 'SSH' }, { value: 'Hadoop', label: 'Hadoop' }, { value: 'Consul', label: 'Consul' }]} /></label><label>名称 <span className="required-mark">*</span><input key={`name-${selectedType}-${editing?.id || 'new'}`} name="name" defaultValue={editing?.name || ''} required /></label><label className={selectedType === 'Hadoop' ? 'wide' : ''}>{selectedType === 'Hadoop' ? 'Hadoop Web 地址' : '地址'} <span className="required-mark">*</span><input name="host" defaultValue={editing?.host || ''} placeholder={selectedType === 'MySQL' ? 'MySQL 主机地址' : selectedType === 'SSH' ? 'SSH 主机地址' : selectedType === 'Hadoop' ? '例如 http://hadoop-master:8088' : selectedType === 'Consul' ? '例如 http://consul-server' : '例如 prometheus.example.com'} required /></label>{selectedType !== 'Hadoop' && <label>端口 <span className="required-mark">*</span><input key={`port-${selectedType}-${editing?.id || 'new'}`} name="port" defaultValue={editing?.port || (selectedType === 'Consul' ? '8500' : '')} required /></label>}{selectedType === 'MySQL' || selectedType === 'SSH' || selectedType === 'Consul' ? <><label>用户名 <span className="optional-mark">选填</span><input name="username" defaultValue={editing?.username || ''} placeholder={selectedType === 'Consul' ? '可选；仅用于基础认证' : undefined} required={selectedType !== 'Consul'} /></label><label>密码{selectedType === 'Consul' && <span className="optional-mark">选填</span>}<input name="password" type="password" placeholder={editing ? '留空则不修改' : selectedType === 'Consul' ? '可选；账号同时填写时用于 Nginx 密码，否则作为 Consul Token' : selectedType === 'SSH' ? 'SSH 密码，可选' : 'MySQL 密码'} required={selectedType === 'MySQL' && !editing} /></label>{selectedType === 'MySQL' && <label className="wide">数据库<input name="database" defaultValue={editing?.database || ''} placeholder="可选，不填则采集实例级指标" /></label>}{selectedType === 'SSH' && <span className="form-error wide">SSH 仅采集 CPU、负载、内存和磁盘使用率。</span>}</> : selectedType === 'Hadoop' ? <><label className="wide">NodeManager 日志地址<input name="nodeManagerUrl" defaultValue={editing?.options?.nodeManagerUrl || ''} placeholder="选填，例如 http://hadoop-node-1:8042" /></label><label className="wide">JobHistory 日志地址<input name="jobHistoryUrl" defaultValue={editing?.options?.jobHistoryUrl || ''} placeholder="选填，例如 http://hadoop-history:19888" /></label><span className="form-error wide">NodeManager 与 JobHistory 地址仅用于读取 YARN 容器日志；不填时系统使用 YARN 返回的日志地址。</span></> : <label>Token<input name="token" type="password" placeholder={editing ? '留空则不修改' : '可选'} /></label>}<label className="wide">备注<textarea name="remark" defaultValue={editing?.remark || ''} placeholder="记录数据源用途或环境" /></label></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.closest('form'); if (form) void testSource(form) }}>测试连接</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
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
  const [ruleKind, setRuleKind] = useState<'data-monitor' | 'file-monitor' | 'script-monitor' | 'prometheus' | 'http' | 'https' | 'tcp' | 'udp'>('data-monitor')
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
  const sshSources = sources.filter(item => item.enabled && (item.type === 'SSH' || item.type === 'Agent'))
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
  const ruleStatusHint = (tone: string) => tone === 'success'
    ? '绿色：规则已启用，最近一次检测正常。'
    : tone === 'danger'
      ? '红色：规则已启用，当前告警中或最近一次执行失败。'
      : '黄色：规则已启用，等待首次执行或等待告警条件满足。'
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
    setRuleKind('data-monitor')
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
    if ((ruleKind === 'file-monitor' || ruleKind === 'script-monitor') && selectedSSHSources.length === 0) { showMessage('请至少选择一个主机节点'); return }
    if (ruleKind === 'prometheus' && !selectedPrometheusSource) { showMessage('请选择 Prometheus 数据源'); return }
    const payload: CollectionRule = ruleKind === 'data-monitor'
      ? { id: editingRule?.id || '', name, source: selectedMySQLSource, database: selectedDatabase, table: selectedTable, field: selectedField, condition: '当天有数据', threshold: '', timeWindow: deadline, frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
      : ruleKind === 'file-monitor'
        ? { id: editingRule?.id || '', name, source: selectedSSHSources.join(','), database: 'file-monitor', table: String(form.get('filePath') || ''), field: String(form.get('filePattern') || ''), condition: '当天生成文件', threshold: '', timeWindow: deadline, frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
        : ruleKind === 'script-monitor'
          ? { id: editingRule?.id || '', name, source: selectedSSHSources.join(','), database: 'script-monitor', table: String(form.get('script') || ''), field: String(form.get('scriptDirectory') || ''), condition: scriptCondition, threshold: String(form.get('expectedExitCode') || '0'), timeWindow: '', frequency: `${String(form.get('frequencyValue') || '1')}${frequencyUnit}`, remark: String(form.get('remark') || ''), lastRun: editingRule?.lastRun || '待执行', status: editingRule?.status || '启用' }
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
                      <b><span className="rule-name">{rule.name}</span></b>
                      <Tooltip content={ruleStatusHint(stateClass(rule.state, rule.health))}><span className={`alert-result ${stateClass(rule.state, rule.health)}`}>{stateLabel(rule.state || rule.health)}</span></Tooltip>
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
                      <b><span className="rule-title-line"><span className="rule-name">{rule.name}</span><Tooltip content="下次检测时间"><span className="rule-next-check">下检：{nextCollectionCheckTime(rule)}</span></Tooltip></span></b>
                      <Tooltip content={rule.lastRun || '待执行'}><span className={`alert-result ${customStateClass(rule.lastRun)}`}>{collectionRuleResultLabel(rule.lastRun)}</span></Tooltip>
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
                      <b><span className="rule-title-line"><span className="rule-name">{rule.name}</span><Tooltip content="下次检测时间"><span className="rule-next-check">下检：{nextCollectionCheckTime(rule)}</span></Tooltip></span></b>
                      {['file-monitor', 'script-monitor'].includes(rule.database) && rule.resultDetails && <button className="rule-result-button" type="button" onClick={() => setResultRule(rule)}>结果详情</button>}
                      <Tooltip content={rule.lastRun || '待执行'}><span className={`alert-result ${customStateClass(rule.lastRun)}`}>{collectionRuleResultLabel(rule.lastRun)}</span></Tooltip>
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
                    <label>告警判定时间<AppSelect name="deadline" value={deadline} onChange={setDeadline} options={Array.from({ length: 24 }, (_, hour) => `${String(hour).padStart(2, '0')}:00`).map(value => ({ value, label: value }))} /></label>
                    {schemaError && <span className="form-error wide">{schemaError}</span>}
                  </>
                ) : ruleKind === 'file-monitor' ? (
                  <>
                    <label>主机节点 <span className="required-mark">*</span><SSHSourceMultiSelect sources={sshSources} value={selectedSSHSources} onChange={setSelectedSSHSources} /></label>
                    <label>告警判定时间 <span className="required-mark">*</span><AppSelect name="deadline" value={deadline} onChange={setDeadline} options={Array.from({ length: 24 }, (_, hour) => `${String(hour).padStart(2, '0')}:00`).map(value => ({ value, label: value }))} /></label>
                    <label className="wide">路径 <span className="required-mark">*</span><input name="filePath" defaultValue={editingRule?.database === 'file-monitor' ? editingRule.table : ''} placeholder="/var/data/reports" required /></label>
                    <label className="wide">文件名正则 <span className="required-mark">*</span><input name="filePattern" defaultValue={editingRule?.database === 'file-monitor' ? editingRule.field : ''} placeholder="^report_\\d{8}\\.csv$" required /></label>
                  </>
                ) : ruleKind === 'script-monitor' ? (
                  <>
                    <label>主机节点 <span className="required-mark">*</span><SSHSourceMultiSelect sources={sshSources} value={selectedSSHSources} onChange={setSelectedSSHSources} /></label>
                    <label>判断逻辑 <span className="required-mark">*</span><AppSelect value={scriptCondition} onChange={setScriptCondition} options={[{ value: '退出码等于', label: '退出码等于' }]} /></label>
                    <label>预期退出码 <span className="required-mark">*</span><input name="expectedExitCode" type="number" min="0" max="255" step="1" defaultValue={editingRule?.database === 'script-monitor' ? editingRule.threshold || '0' : '0'} required /></label>
                    <label className="wide">执行目录<input name="scriptDirectory" defaultValue={editingRule?.database === 'script-monitor' ? editingRule.field : ''} placeholder="可选，例如 /opt/ops/scripts；不填则使用节点默认目录" /></label>
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
      {resultRule && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setResultRule(null) }}><section className="surface result-details-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>{resultRule.database === 'script-monitor' ? '脚本执行日志' : '结果详情'}</h2><p>{resultRule.name}</p></div><button className="close-button" type="button" onClick={() => setResultRule(null)}>×</button></header><pre className={resultRule.database === 'script-monitor' ? 'rule-execution-log' : ''}>{resultRule.database === 'file-monitor' ? normalizeFileMonitorResultDetails(resultRule.resultDetails || '') : resultRule.resultDetails}</pre></section></div>}
    </div>
  )

}

function AuditLogs() {
  const [items, setItems] = useState<Array<{ id: number; action: string; username: string; remoteAddr: string; details: string; createdAt: string }>>([])
  const [loading, setLoading] = useState(false)
  const [keyword, setKeyword] = useState('')
	const [search, setSearch] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [accountFilter, setAccountFilter] = useState('')
  const [page, setPage] = useState(1)
	const [total, setTotal] = useState(0)
	const [actions, setActions] = useState<string[]>([])
	const [usernames, setUsernames] = useState<string[]>([])
	const load = async () => { setLoading(true); try { const query = new URLSearchParams({ page: String(page), pageSize: '20', action: typeFilter, username: accountFilter, keyword: search }); const response = await fetch(`${api}/audit-logs?${query}`); const data = await response.json(); if (!response.ok) throw new Error(data.error || '读取日志失败'); setItems(data.items || []); setTotal(data.total || 0); setActions(data.actions || []); setUsernames(data.usernames || []) } finally { setLoading(false) } }
	useEffect(() => { const timer = window.setTimeout(() => setSearch(keyword), 280); return () => window.clearTimeout(timer) }, [keyword])
	useEffect(() => { void load() }, [page, typeFilter, accountFilter, search])
	const pages = Math.max(1, Math.ceil(total / 20))
	return <div className="page audit-log-page"><PageHead title="日志中心" description="操作审计、登录与安全事件" /><div className="audit-log-toolbar"><AppSelect value={typeFilter} placeholder="全部操作类型" onChange={value => { setTypeFilter(value); setPage(1) }} options={[{ value: '', label: '全部操作类型' }, ...actions.map(value => ({ value, label: value }))]} /><AppSelect value={accountFilter} placeholder="全部账号" onChange={value => { setAccountFilter(value); setPage(1) }} options={[{ value: '', label: '全部账号' }, ...usernames.map(value => ({ value, label: value }))]} /><input value={keyword} placeholder="搜索详情" onChange={event => { setKeyword(event.target.value); setPage(1) }} /><span>共 {total} 条</span></div><section className="surface audit-log-list"><header className="audit-log-head"><span>时间</span><span>操作</span><span>账号</span><span>详情</span><span>来源地址</span></header>{items.length === 0 ? <div className="empty-state"><b>{loading ? '正在读取日志' : '暂无日志'}</b></div> : items.map(item => <article key={`${item.action}-${item.id}`}><time>{item.createdAt}</time><b>{item.action}</b><span>{item.username || '-'}</span><code>{item.details || '平台操作'}</code><small>{item.remoteAddr || '-'}</small></article>)}{total > 0 && <UnifiedPagination total={total} page={page} totalPages={pages} onPageChange={setPage} ariaLabel="日志分页" />}</section></div>
}

function Notifications() {
  const location = useLocation()
  const [allItems, setItems] = useState<NotificationItem[]>([])
  const currentDate = new Date()
  const today = `${currentDate.getFullYear()}-${String(currentDate.getMonth() + 1).padStart(2, '0')}-${String(currentDate.getDate()).padStart(2, '0')}`
  const initialRange = new URLSearchParams(location.search)
  const [startDate, setStartDate] = useState(initialRange.get('start') || today)
  const [endDate, setEndDate] = useState(initialRange.get('end') || today)
  const [notificationType, setNotificationType] = useState(initialRange.get('type') || 'all')
  const [loading, setLoading] = useState(false)
  const [page, setPage] = useState(1)
  const [detailGroup, setDetailGroup] = useState<NotificationGroup | null>(null)
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
        const firstSeenAt = new Date(item.firstSeenAt).getTime()
        const lastSeenAt = new Date(item.lastSeenAt).getTime()
        const matchesType = notificationType === 'all' || (notificationType === 'alerts' && ['alert', 'active'].includes(item.status)) || item.status === notificationType
        const matchesRange = item.status === 'resolved'
          ? Number.isFinite(lastSeenAt) && lastSeenAt >= rangeStart && lastSeenAt < rangeEnd
          : Number.isFinite(firstSeenAt) && firstSeenAt < rangeEnd && (item.status === 'active' || (Number.isFinite(lastSeenAt) && lastSeenAt >= rangeStart))
        return matchesRange && matchesType
      }))
    } catch { setItems([]) } finally { setLoading(false) }
  }
  const groupedItems = useMemo(() => groupNotifications(allItems), [allItems])
  const pageSize = 20
  const totalPages = Math.max(1, Math.ceil(groupedItems.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const items = groupedItems.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  const notificationPagination = groupedItems.length > 0 && <UnifiedPagination total={groupedItems.length} page={currentPage} totalPages={totalPages} onPageChange={setPage} ariaLabel="通知分页" />
  useEffect(() => { void load() }, [startDate, endDate, notificationType])
  useEffect(() => { setPage(1) }, [startDate, endDate, notificationType])
  useEffect(() => { const refresh = () => void load(); window.addEventListener('opsguard-notifications-change', refresh); return () => window.removeEventListener('opsguard-notifications-change', refresh) }, [startDate, endDate, notificationType])
  useEffect(() => { const timer = window.setInterval(() => void load(), refreshInterval); return () => window.clearInterval(timer) }, [startDate, endDate, notificationType, refreshInterval])
  return <div className="page">
    <section className="notification-toolbar">
      <form onSubmit={(event) => { event.preventDefault(); void load() }}>
        <input type="date" value={startDate} max={endDate || today} onChange={(event) => setStartDate(event.target.value)} aria-label="开始日期" />
        <span>至</span>
        <input type="date" value={endDate} min={startDate} max={today} onChange={(event) => setEndDate(event.target.value)} aria-label="结束日期" />
        <AppSelect className="notification-kind-select" value={notificationType} onChange={setNotificationType} options={[{ value: 'all', label: '全部通知' }, { value: 'active', label: '活跃告警' }, { value: 'resolved', label: '告警恢复' }, { value: 'alert', label: '历史告警' }]} />
        <button className="button secondary" type="submit" disabled={loading}>{loading ? '加载中...' : '查询'}</button>
      </form>
      <button className="button secondary notification-read-all" type="button" disabled={!allItems.some(item => item.unread)} onClick={() => void markAllRead()}>全部已读</button>
    </section>
    <section className="surface notification-list">
      {items.length === 0 ? <div className="empty-state alert-empty-state"><b>{loading ? '正在加载通知' : '暂无通知'}</b><span>{loading ? '请稍候。' : '当前时间区间内没有匹配通知。'}</span></div> : items.map(item => {
        const statusLabel = item.status === 'active' ? '活跃告警' : item.status === 'resolved' ? '告警恢复' : '历史告警'
        const tone = item.status === 'resolved' ? 'success' : 'danger'
        return <article className={`notification-row ${item.unread ? 'unread' : ''} ${item.status === 'active' ? 'active-alert' : ''}`} key={item.key}>
          <i className={`notification-dot ${tone}`} />
          <div><header><b>{item.ruleName}</b><span className={`alert-result ${tone} ${item.status === 'active' ? 'active-alert' : ''}`}>{statusLabel}</span></header><p>{item.message}</p><div className="notification-times"><small>通知时间：{formatCollectedAt(item.lastSeenAt)}</small></div></div>
          <div className="notification-row-actions"><button className="notification-rule-toggle" type="button" onClick={() => setDetailGroup(item)}>通知详情</button>{item.status === 'active' && <label className={`notification-mute ${item.muted ? 'active' : ''}`} title="开启后，该持续告警不再计入未读通知"><input type="checkbox" checked={item.muted} onChange={(event) => void toggleMuted(item.representative, event.target.checked)} /><i /><span>隐秘告警</span></label>}</div>
        </article>
      })}
      {notificationPagination}
    </section>
    {detailGroup && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setDetailGroup(null) }}><section className="surface notification-detail-modal" role="dialog" aria-modal="true"><header className="modal-head"><div><h2>通知详情</h2><p>{detailGroup.ruleName}</p></div><button className="close-button" type="button" onClick={() => setDetailGroup(null)}>×</button></header><div className="notification-detail-grid"><span>首次触发时间<b>{formatCollectedAt(detailGroup.firstSeenAt)}</b></span><span>最近告警时间<b>{detailGroup.latestAlertAt ? formatCollectedAt(detailGroup.latestAlertAt) : '-'}</b></span><span>持续时间<b>{notificationDuration(detailGroup.firstSeenAt, detailGroup.status === 'resolved' ? detailGroup.resolvedAt : undefined)}</b></span><span>恢复时间<b>{detailGroup.resolvedAt ? formatCollectedAt(detailGroup.resolvedAt) : '-'}</b></span></div><section className="notification-detail-message"><b>告警信息</b><p>{detailGroup.alertMessage}</p></section></section></div>}
  </div>
}

function FrequencyEditor({ label, value, onChange }: { label: string; value: RefreshSetting; onChange: (value: RefreshSetting) => void }) {
  return <label>{label} <span className="required-mark">*</span><span className="frequency-input"><input value={value.value} type="number" min="1" step="1" onChange={(event) => onChange({ ...value, value: Math.max(1, Number(event.target.value) || 1) })} required /><AppSelect value={value.unit} onChange={(unit) => onChange({ ...value, unit: unit as RefreshSetting['unit'] })} options={[{ value: 's', label: '秒' }, { value: 'm', label: '分钟' }, { value: 'h', label: '小时' }]} /></span></label>
}

function OperationConfirmationPanel() {
  const [required, setRequired] = useState(true)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  useEffect(() => { void fetch(`${api}/system-settings/confirmations`).then(response => response.json()).then(data => setRequired(data.requireWriteConfirmation !== false)) }, [])
  const save = async () => { setSaving(true); try { const response = await fetch(`${api}/system-settings/confirmations`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ requireWriteConfirmation: required }) }); if (!response.ok) throw new Error('保存操作确认设置失败'); setMessage('操作确认设置已保存') } catch (err) { setMessage(err instanceof Error ? err.message : '保存失败') } finally { setSaving(false) } }
  return <div className="form-section"><h3>操作确认</h3><label className={`operation-confirmation-choice ${required ? 'checked' : ''}`}><input type="checkbox" checked={required} onChange={event => setRequired(event.target.checked)} /><span><b>写入操作前确认</b><small>执行 SQL 写操作和导入任务前要求用户确认</small></span></label><div className="settings-actions"><button className="button" type="button" disabled={saving} onClick={() => void save()}>{saving ? '保存中...' : '保存操作确认设置'}</button></div>{message && <span className="settings-inline-message">{message}</span>}</div>
}

function MySQLQueryLimitPanel() {
  const [limit, setLimit] = useState('500'); const [maxSizeMB, setMaxSizeMB] = useState('2'); const [saving, setSaving] = useState(false); const [message, setMessage] = useState('')
  useEffect(() => { void fetch(`${api}/system-settings/mysql-query-limit`).then(response => response.json()).then(data => { setLimit(String(data.limit || 500)); setMaxSizeMB(String(Math.max(1, Math.round((Number(data.maxBytes) || 2 * 1024 * 1024) / 1024 / 1024)))) }) }, [])
  const save = async (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); setSaving(true); try { const response = await fetch(`${api}/system-settings/mysql-query-limit`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ limit: Number(limit), maxBytes: Number(maxSizeMB) * 1024 * 1024 }) }); const data = await response.json(); if (!response.ok) throw new Error(data.error || '保存失败'); setLimit(String(data.limit)); setMaxSizeMB(String(Math.round(data.maxBytes / 1024 / 1024))); setMessage('SQL 查询结果上限已保存') } catch (err) { setMessage(err instanceof Error ? err.message : '保存失败') } finally { setSaving(false) } }
  return <div className="form-section"><h3>SQL 查询结果</h3><form className="settings-form" onSubmit={save}><label>最大返回行数 <span className="required-mark">*</span><input value={limit} type="number" min="1" max="10000" step="1" onChange={event => setLimit(event.target.value)} required /><small>单次 SQL 查询最多返回的记录数，范围 1–10000 行。</small></label><label>最大结果大小（MB） <span className="required-mark">*</span><input value={maxSizeMB} type="number" min="1" max="100" step="1" onChange={event => setMaxSizeMB(event.target.value)} required /><small>查询结果序列化后允许的最大大小，范围 1–100 MB。</small></label><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存查询上限'}</button></div>{message && <span className="settings-inline-message">{message}</span>}</form></div>
}

function Settings() {
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [settings, setSettings] = useState<Record<UpdateFrequencyScope, RefreshSetting>>({ general: readRefreshInterval(), datasource: updateFrequencyDefaults.datasource, hadoop: updateFrequencyDefaults.hadoop, dashboard: updateFrequencyDefaults.dashboard, consul: updateFrequencyDefaults.consul })
  useEffect(() => { void Promise.all((['general', 'datasource', 'hadoop', 'dashboard', 'consul'] as UpdateFrequencyScope[]).map(async scope => [scope, await loadScopedRefreshSettings(scope)] as const)).then(items => setSettings(current => ({ ...current, ...Object.fromEntries(items) }))) }, [])
  const saveFrequencies = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setSaving(true)
    try {
      const entries = await Promise.all((['general', 'datasource', 'hadoop', 'dashboard', 'consul'] as UpdateFrequencyScope[]).map(async scope => [scope, await saveScopedRefreshSettings(scope, settings[scope])] as const))
      setSettings(current => ({ ...current, ...Object.fromEntries(entries) })); setMessage('更新频率已保存')
    } catch (err) { setMessage(err instanceof Error ? err.message : '更新频率保存失败') } finally { setSaving(false) }
  }
  const changePassword = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); const form = new FormData(event.currentTarget); const next = String(form.get('newPassword') || '')
    if (next !== String(form.get('confirmPassword') || '')) { setMessage('两次输入的新密码不一致'); return }
    const response = await fetch(`${api}/change-password`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ oldPassword: form.get('oldPassword'), newPassword: next }) })
    const data = await response.json(); setMessage(response.ok ? '密码已修改' : data.error || '密码修改失败'); if (response.ok) event.currentTarget.reset()
  }
  const scrollToSection = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  return <div className="page"><PageHead title="系统设置" description="集中管理平台更新频率、常用平台与账户配置。" />{message && <div className="toast">{message}</div>}<section className="settings-layout"><aside className="surface settings-tabs" aria-label="配置目录"><button type="button" onClick={() => scrollToSection('settings-frequency')}>更新频率</button><button type="button" onClick={() => scrollToSection('settings-platform-name')}>平台名称</button><button type="button" onClick={() => scrollToSection('settings-platforms')}>常用平台</button><button type="button" onClick={() => scrollToSection('settings-profile')}>密码修改</button><button type="button" onClick={() => scrollToSection('settings-confirmations')}>操作确认</button><button type="button" onClick={() => scrollToSection('settings-sql-query-limit')}>SQL 查询结果</button></aside><section className="surface settings settings-all"><div id="settings-frequency" className="form-section settings-anchor"><h3>更新频率</h3><form className="settings-form frequency-settings-form" onSubmit={saveFrequencies}><FrequencyEditor label="通用页面更新频率" value={settings.general} onChange={(value) => setSettings(current => ({ ...current, general: value }))} /><FrequencyEditor label="数据源检测频率" value={settings.datasource} onChange={(value) => setSettings(current => ({ ...current, datasource: value }))} /><FrequencyEditor label="Hadoop 任务列表更新频率" value={settings.hadoop} onChange={(value) => setSettings(current => ({ ...current, hadoop: value }))} /><FrequencyEditor label="监控大屏更新频率" value={settings.dashboard} onChange={(value) => setSettings(current => ({ ...current, dashboard: value }))} /><FrequencyEditor label="Consul 当前目录自动刷新频率" value={settings.consul} onChange={(value) => setSettings(current => ({ ...current, consul: value }))} /><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存更新频率'}</button></div></form></div><div id="settings-platform-name" className="settings-anchor"><PlatformNameSettings /></div><div id="settings-platforms" className="settings-anchor"><PlatformLinkSettings /></div><div id="settings-profile" className="form-section settings-anchor"><h3>密码修改</h3><form className="settings-form" onSubmit={changePassword}><label>原密码 <span className="required-mark">*</span><input name="oldPassword" type="password" autoComplete="current-password" required /></label><label>新密码 <span className="required-mark">*</span><input name="newPassword" type="password" autoComplete="new-password" required /></label><label>确认新密码 <span className="required-mark">*</span><input name="confirmPassword" type="password" autoComplete="new-password" required /></label><div className="settings-actions"><button className="button" type="submit">保存密码</button></div></form></div><div id="settings-confirmations" className="settings-anchor"><OperationConfirmationPanel /></div><div id="settings-sql-query-limit" className="settings-anchor"><MySQLQueryLimitPanel /></div></section></section></div>
}

function formatCollectedAt(value?: string) {
  if (!value) return '待检测'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export default App
