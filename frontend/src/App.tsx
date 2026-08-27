import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { BrowserRouter, NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import './App.css'

type Source = { id: string; name: string; type: string; host: string; port: string; enabled: boolean; status: string; lastTest: string; username?: string; password?: string; database?: string; remark?: string; options?: Record<string, string> }
type PrometheusRule = { name: string; type: string; query: string; duration: number; health: string; state?: string; severity?: string; summary?: string; description?: string; group: string; file?: string }
type NotificationItem = { id: string; ruleName: string; status: string; message: string; unread: boolean; firstSeenAt: string; lastSeenAt: string }
type CollectionRule = { id: string; name: string; source: string; database: string; table: string; field: string; condition: string; threshold?: string; timeWindow: string; lastRun: string; status: string }
type DashboardItem = { id: string; name: string; sourceId: string; sourceName: string; sourceType: string; createdAt: string }

const api = '/api'
const icons: Record<string, string> = { query: '◎', dashboard: '▦', data: '◫', alert: '◇', notify: '◉', settings: '⚙', plus: '+', arrow: '→', bell: '●' }
function Icon({ name }: { name: string }) { return <span className={`icon icon-${name}`} aria-hidden="true">{icons[name]}</span> }

function App() {
  const [authed, setAuthed] = useState(() => localStorage.getItem('opsguard_token') === 'opsguard-admin')
  const logout = async () => {
    await fetch(`${api}/logout`, { method: 'POST' }).catch(() => {})
    localStorage.removeItem('opsguard_token')
    setAuthed(false)
  }
  if (!authed) return <Login onLogin={() => setAuthed(true)} />
  return <BrowserRouter><div className="app-shell"><Sidebar /><main className="workspace"><TopNav onLogout={logout} /><Routes><Route path="/" element={<Dashboards />} /><Route path="/metrics" element={<MetricQuery />} /><Route path="/datasources" element={<DataSources />} /><Route path="/alerts" element={<Alerts />} /><Route path="/notifications" element={<Notifications />} /><Route path="/config" element={<Settings />} /></Routes></main></div></BrowserRouter>
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
  const titles: Record<string, string> = { '/': '数据展示', '/metrics': '指标查询', '/datasources': '数据节点', '/alerts': '告警规则', '/notifications': '通知中心', '/config': '系统设置' }
  const [unread, setUnread] = useState(0)
  const loadUnread = async () => {
    try {
      const response = await fetch(`${api}/notifications?unread=1&limit=1`)
      const data = await response.json()
      setUnread(Number(data.unread || 0))
    } catch { setUnread(0) }
  }
  useEffect(() => { void loadUnread(); const timer = window.setInterval(loadUnread, 30000); return () => window.clearInterval(timer) }, [])
  return <header className="page-nav"><div className="nav-path"><span>Ops</span><i>/</i><b>{titles[location.pathname] || '指标查询'}</b></div><div className="nav-tools"><button className="bell-button" type="button" aria-label="通知中心" onClick={() => navigate('/notifications')}><Icon name="bell" />{unread > 0 && <i>{unread > 99 ? '99+' : unread}</i>}</button><AccountMenu onLogout={onLogout} /></div></header>
}

function AccountMenu({ onLogout }: { onLogout: () => void }) {
  const [open, setOpen] = useState(false)
  return <div className="account-menu"><button className="user-avatar" type="button" onClick={() => setOpen(current => !current)}><span>管</span></button>{open && <div className="surface account-popover"><button type="button" onClick={() => setOpen(false)}>平台管理员</button><button className="danger" type="button" onClick={onLogout}>退出登录</button></div>}</div>
}

function Sidebar() {
  const [sources, setSources] = useState<Source[]>([])
  const load = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources : [])
    } catch { setSources([]) }
  }
  useEffect(() => { void load(); const timer = window.setInterval(load, 15000); window.addEventListener('opsguard-data-sources-change', load); return () => { window.clearInterval(timer); window.removeEventListener('opsguard-data-sources-change', load) } }, [])
  const online = sources.filter(item => item.enabled && item.status === '健康').length
  const items = [['dashboard', '数据展示', '/'], ['alert', '告警规则', '/alerts'], ['notify', '通知中心', '/notifications'], ['data', '数据节点', '/datasources'], ['settings', '系统设置', '/config']]
  return <aside className="sidebar"><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><small>巡检平台</small></div></div><nav>{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name={icon} /><span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><span className="online-dot" /><span>{online} / {sources.length} 数据源在线</span></div></aside>
}

function PageHead({ action, onAction, actionNode }: { title: string; description: string; action?: string; onAction?: () => void; actionNode?: any }) {
  if (!action && !actionNode) return null
  return <header className="page-head compact"><span />{actionNode || <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header>
}

function SelectShell({ children, className = '' }: { children: any; className?: string }) {
  return <span className={`select-shell ${className}`}>{children}<i>⌄</i></span>
}

function RefreshButton({ loading, disabled, onClick }: { loading?: boolean; disabled?: boolean; onClick: () => void }) {
  const [clicked, setClicked] = useState(false)
  const handleClick = () => {
    setClicked(true)
    window.setTimeout(() => setClicked(false), 650)
    onClick()
  }
  return <button className={`refresh-button ${loading || clicked ? 'spinning' : ''}`} type="button" onClick={handleClick} disabled={disabled} aria-label="刷新"><span>↻</span></button>
}

function PlatformSelect({ value, options, onChange, className = '' }: { value: string; options: { value: string; label: string }[]; onChange: (next: string) => void; className?: string }) {
  const [open, setOpen] = useState(false)
  const selected = options.find(item => item.value === value) || options[0]
  return <span className={`platform-select ${className}`}><button type="button" onClick={() => setOpen(current => !current)}><span>{selected?.label || '-'}</span></button>{open && <span className="platform-select-menu">{options.map(option => <button className={option.value === value ? 'active' : ''} type="button" key={option.value} onClick={() => { onChange(option.value); setOpen(false) }}>{option.label}</button>)}</span>}</span>
}

function dashboardsFromStorage(): DashboardItem[] {
  try { return JSON.parse(localStorage.getItem('opsguard_dashboards') || '[]') } catch { return [] }
}

function saveDashboards(items: DashboardItem[]) {
  localStorage.setItem('opsguard_dashboards', JSON.stringify(items))
  window.dispatchEvent(new Event('opsguard-dashboards-change'))
}

function Dashboards() {
  const [items, setItems] = useState<DashboardItem[]>(dashboardsFromStorage)
  const [prometheusId, setPrometheusId] = useState('')
  const [values, setValues] = useState<Record<string, Record<string, string>>>({})
  const mysqlDashboards = items.filter(item => item.sourceType === 'MySQL')
  const loadPrometheus = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      const prometheus = (Array.isArray(data.dataSources) ? data.dataSources : []).find((item: Source) => item.enabled && item.type === 'Prometheus')
      setPrometheusId(prometheus?.id || '')
    } catch { setPrometheusId('') }
  }
  const queryValue = async (promql: string) => {
    if (!prometheusId) return '-'
    const response = await fetch(`${api}/prometheus/${prometheusId}/query?query=${encodeURIComponent(promql)}`)
    const data = await response.json()
    const row = data?.data?.result?.[0]
    return Array.isArray(row?.value) ? String(row.value[1]) : '-'
  }
  const refresh = async () => {
    if (!prometheusId || mysqlDashboards.length === 0) return
    const next: Record<string, Record<string, string>> = {}
    for (const item of mysqlDashboards) {
      const sid = item.sourceId
      next[sid] = {
        up: await queryValue(`opsguard_mysql_up{source_id="${sid}"}`),
        threads: await queryValue(`opsguard_mysql_global_status{source_id="${sid}",variable="Threads_connected"}`),
        running: await queryValue(`opsguard_mysql_global_status{source_id="${sid}",variable="Threads_running"}`),
        slow: await queryValue(`opsguard_mysql_global_status{source_id="${sid}",variable="Slow_queries"}`),
        questions: await queryValue(`opsguard_mysql_global_status{source_id="${sid}",variable="Questions"}`),
        hit: await queryValue(`opsguard_mysql_innodb_buffer_pool_hit_ratio{source_id="${sid}"}`),
      }
    }
    setValues(next)
  }
  useEffect(() => { void loadPrometheus(); const onChange = () => setItems(dashboardsFromStorage()); window.addEventListener('opsguard-dashboards-change', onChange); return () => window.removeEventListener('opsguard-dashboards-change', onChange) }, [])
  useEffect(() => { void refresh(); const timer = window.setInterval(refresh, 30000); return () => window.clearInterval(timer) }, [prometheusId, items.length])
  const remove = (id: string) => saveDashboards(items.filter(item => item.id !== id))
  return <div className="page"><PageHead title="数据展示" description="从数据节点导入 MySQL 大屏。" />{mysqlDashboards.length === 0 ? <section className="surface empty-state"><b>暂无数据展示</b><span>到数据节点点击 MySQL 数据源的导入大屏按钮。</span></section> : <section className="dashboard-list">{mysqlDashboards.map(item => { const v = values[item.sourceId] || {}; return <article className="surface mysql-dashboard-card" key={item.id}><header><div><h3>{item.name}</h3><span>{item.sourceName}</span></div><button className="text-button danger" type="button" onClick={() => remove(item.id)}>删除</button></header><div className="dashboard-metrics"><div><span>状态</span><b>{v.up === '1' ? '正常' : '-'}</b></div><div><span>连接数</span><b>{v.threads || '-'}</b></div><div><span>运行线程</span><b>{v.running || '-'}</b></div><div><span>慢查询</span><b>{v.slow || '-'}</b></div><div><span>查询总数</span><b>{v.questions || '-'}</b></div><div><span>Buffer 命中率</span><b>{formatPercent(v.hit)}</b></div></div></article> })}</section>}</div>
}

function formatPercent(value?: string) {
  const n = Number(value)
  if (!Number.isFinite(n)) return '-'
  return `${(n * 100).toFixed(2)}%`
}

function MetricQuery() {
  const [sources, setSources] = useState<Source[]>([])
  const [sourceId, setSourceId] = useState('')
  const [query, setQuery] = useState('mysql_up')
  const [result, setResult] = useState<any>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const enabledSources = sources.filter(item => item.enabled && item.type === 'Prometheus')
  const selectedSourceId = sourceId || enabledSources[0]?.id || ''
  const loadSources = async () => {
    const response = await fetch(`${api}/data-sources`)
    const data = await response.json()
    const next = Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => item.type === 'Prometheus') : []
    setSources(next)
    if (!sourceId && next[0]) setSourceId(next[0].id)
  }
  useEffect(() => { void loadSources() }, [])
  const runQuery = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selectedSourceId) return
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
  const vectorRows = Array.isArray(result?.result) ? result.result : []
  const sourceSelect = <SelectShell className="metric-source-select"><select value={selectedSourceId} onChange={(event) => setSourceId(event.target.value)}>{enabledSources.map(source => <option key={source.id} value={source.id}>{source.name} · {source.host}:{source.port}</option>)}</select></SelectShell>
  return <div className="page"><PageHead title="指标查询" description="输入 PromQL 查询存储指标。" actionNode={sourceSelect} />{message && <div className="toast">{message}</div>}{enabledSources.length === 0 ? <section className="surface empty-state"><b>暂无 Prometheus 数据源</b><span>请先到数据节点新增 Prometheus 数据源。</span></section> : <section className="surface external-panel promql-panel"><form className="promql-form" onSubmit={runQuery}><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="例如 mysql_up 或 opsguard_mysql_global_status{variable=Threads_connected}" /><button className="button" type="submit" disabled={loading}>{loading ? '查询中...' : '查询'}</button></form>{vectorRows.length > 0 ? <div className="query-table"><div className="query-row query-head"><span>指标标签</span><span>值</span></div>{vectorRows.map((row: any, index: number) => <div className="query-row" key={index}><code>{JSON.stringify(row.metric)}</code><b>{Array.isArray(row.value) ? row.value[1] : '-'}</b></div>)}</div> : result && <pre className="query-result">{JSON.stringify(result, null, 2)}</pre>}</section>}</div>
}


function DataSources() {
  const [sources, setSources] = useState<Source[]>([])
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Source | null>(null)
  const [selectedType, setSelectedType] = useState<'Prometheus' | 'MySQL'>('Prometheus')
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => ['Prometheus', 'MySQL'].includes(item.type)) : [])
    } catch { setSources([]) }
  }
  useEffect(() => { void loadSources(); const timer = window.setInterval(loadSources, 60000); return () => window.clearInterval(timer) }, [])
  const openModal = (source?: Source) => { setEditing(source || null); setSelectedType(source?.type === 'MySQL' ? 'MySQL' : 'Prometheus'); setModalOpen(true); setMessage('') }
  const defaultPort = selectedType === 'MySQL' ? '3306' : '9090'
  const sourceLogo = (source: Source) => source.type === 'MySQL' ? 'M' : 'P'
  const sourceSubtitle = (source: Source) => `${source.type} · ${source.host}:${source.port}`
  const importDashboard = (source: Source) => {
    if (source.type !== 'MySQL') { showMessage('只有 MySQL 数据源支持导入大屏'); return }
    const current = dashboardsFromStorage()
    if (current.some(item => item.sourceId === source.id)) { showMessage('该数据源已导入大屏'); return }
    saveDashboards([{ id: `dash-${Date.now()}`, name: `${source.name} 大屏`, sourceId: source.id, sourceName: source.name, sourceType: source.type, createdAt: new Date().toISOString() }, ...current])
    showMessage(`${source.name} 已导入大屏`)
  }
  const buildPayload = (formElement: HTMLFormElement): Source => {
    const form = new FormData(formElement)
    const type = String(form.get('type') || selectedType) as 'Prometheus' | 'MySQL'
    return { id: editing?.id || '', name: String(form.get('name') || ''), type, host: String(form.get('host') || ''), port: String(form.get('port') || (type === 'MySQL' ? '3306' : '9090')), username: type === 'MySQL' ? String(form.get('username') || '') : '', password: String(form.get(type === 'MySQL' ? 'password' : 'token') || ''), database: type === 'MySQL' ? String(form.get('database') || '') : '', remark: String(form.get('remark') || ''), enabled: true, status: '待测试', lastTest: '' }
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
      setSources(Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => ['Prometheus', 'MySQL'].includes(item.type)) : [])
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
  const deleteSource = async (source: Source) => {
    if (!window.confirm(`确认删除 ${source.name}？`)) return
    try {
      const response = await fetch(`${api}/data-sources/${source.id}`, { method: 'DELETE' })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '删除失败')
      if (source.type !== 'Prometheus') saveDashboards(dashboardsFromStorage().filter(item => item.sourceId !== source.id))
      showMessage(`${source.name} 已删除`)
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (err) { showMessage(err instanceof Error ? err.message : '删除失败') }
  }
  const pageAction = <div className="page-action-group"><RefreshButton loading={refreshing} onClick={() => void refreshHealth()} /><button className="button" onClick={() => openModal()}><Icon name="plus" /> 新增数据源</button></div>
  return <div className="page"><PageHead title="数据节点" description="Prometheus 作为查询入口，MySQL 数据源由平台转换成 Prometheus 指标供采集。" actionNode={pageAction} />{sources.length === 0 ? <section className="surface empty-state"><b>暂无数据源</b><span>点击右上角新增 Prometheus 或 MySQL 数据源。</span></section> : <section className="source-list">{sources.map(source => <article className={`surface source-row ${source.status === '健康' ? 'healthy' : 'warning'} ${!source.enabled ? 'disabled' : ''}`} key={source.id}><div className="node-main"><span className="source-logo">{sourceLogo(source)}</span><div><h3>{source.name}</h3><p>{sourceSubtitle(source)}</p>{source.remark && <small className="source-remark">{source.remark}</small>}</div></div><div className="node-status" /><div className="node-enabled"><StatusSwitch checked={source.enabled} onChange={(checked) => void toggleEnabled(source, checked)} /></div><div className="node-meta"><span>最近检测：{formatCollectedAt(source.lastTest)}</span><span>{source.status}</span></div><div className="source-actions">{source.type === 'MySQL' && <button type="button" onClick={() => importDashboard(source)}>导入大屏</button>}<button type="button" onClick={() => openModal(source)}>编辑</button><button className="danger" type="button" onClick={() => deleteSource(source)}>删除</button></div></article>)}</section>}{modalOpen && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setModalOpen(false) }}><section className="surface source-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2>{editing ? '编辑数据源' : '新增数据源'}</h2><p>{selectedType} 接入</p></div><button className="close-button" type="button" onClick={() => setModalOpen(false)}>×</button></header><form onSubmit={saveSource}><div className="modal-form"><label>类型 <span className="required-mark">*</span><select name="type" value={selectedType} onChange={(event) => setSelectedType(event.target.value as 'Prometheus' | 'MySQL')} disabled={!!editing}><option value="Prometheus">Prometheus</option><option value="MySQL">MySQL</option></select></label><label>名称 <span className="required-mark">*</span><input key={`name-${selectedType}-${editing?.id || 'new'}`} name="name" defaultValue={editing?.name || (selectedType === 'MySQL' ? 'MySQL 数据源' : '本机 Prometheus')} required /></label><label>地址 <span className="required-mark">*</span><input name="host" defaultValue={editing?.host || '127.0.0.1'} placeholder={selectedType === 'MySQL' ? 'MySQL 主机地址' : '127.0.0.1 或 http://prometheus:9090'} required /></label><label>端口 <span className="required-mark">*</span><input key={`port-${selectedType}-${editing?.id || 'new'}`} name="port" defaultValue={editing?.port || defaultPort} required /></label>{selectedType === 'MySQL' ? <><label>用户名 <span className="required-mark">*</span><input name="username" defaultValue={editing?.username || 'root'} required /></label><label>密码 <span className="required-mark">*</span><input name="password" type="password" placeholder={editing ? '留空则不修改' : 'MySQL 密码'} required={!editing} /></label><label className="wide">数据库<input name="database" defaultValue={editing?.database || ''} placeholder="可选，不填则采集实例级指标" /></label></> : <label>Token<input name="token" type="password" placeholder={editing ? '留空则不修改' : '可选'} /></label>}<label className="wide">备注<textarea name="remark" defaultValue={editing?.remark || ''} placeholder="记录数据源用途或环境" /></label></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.closest('form'); if (form) void testSource(form) }}>测试连接</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
}

function StatusSwitch({ checked, disabled, onChange }: { checked: boolean; disabled?: boolean; onChange: (next: boolean) => void }) { return <button type="button" className={`status-switch ${checked ? 'checked' : ''}`} aria-pressed={checked} disabled={disabled} onClick={() => onChange(!checked)}><span className="status-switch-track"><span className="status-switch-thumb" /></span><span>{checked ? '启用' : '停用'}</span></button> }

function Alerts() {
  const [sources, setSources] = useState<Source[]>([])
  const [sourceId, setSourceId] = useState('')
  const [promRules, setPromRules] = useState<PrometheusRule[]>([])
  const [customRules, setCustomRules] = useState<CollectionRule[]>([])
  const [schema, setSchema] = useState<Record<string, Record<string, string[]>>>({})
  const [selectedCustomSource, setSelectedCustomSource] = useState('')
  const [selectedDatabase, setSelectedDatabase] = useState('')
  const [selectedTable, setSelectedTable] = useState('')
  const [ruleKind, setRuleKind] = useState<'prometheus' | 'today' | 'http' | 'tcp'>('today')
  const [selectedRuleSource, setSelectedRuleSource] = useState('')
  const [alertMode, setAlertMode] = useState<'http' | 'tcp' | 'datasource'>('datasource')
  const [modalOpen, setModalOpen] = useState(false)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const [category, setCategory] = useState<'prometheus' | 'custom'>('prometheus')
  const prometheusSources = sources.filter(item => item.enabled && item.type === 'Prometheus')
  const customSources = sources.filter(item => item.enabled && item.type !== 'Prometheus')
  const selectedSourceId = sourceId || prometheusSources[0]?.id || ''
  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      const next = Array.isArray(data.dataSources) ? data.dataSources : []
      setSources(next)
      if (!sourceId && next.find((item: Source) => item.type === 'Prometheus')) setSourceId(next.find((item: Source) => item.type === 'Prometheus')!.id)
      if (!selectedCustomSource && next.find((item: Source) => item.enabled && item.type !== 'Prometheus')) setSelectedCustomSource(next.find((item: Source) => item.enabled && item.type !== 'Prometheus')!.id)
      if (!selectedRuleSource && next.find((item: Source) => item.type === 'Prometheus')) setSelectedRuleSource(next.find((item: Source) => item.type === 'Prometheus')!.id)
    } catch { setSources([]) }
  }
  const loadPromRules = async (id = selectedSourceId) => {
    if (!id) { setPromRules([]); return }
    setLoading(true)
    setMessage('')
    try {
      const response = await fetch(`${api}/prometheus/${id}/rules`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || 'Prometheus 告警规则获取失败')
      setPromRules(Array.isArray(data.rules) ? data.rules : [])
    } catch (err) {
      setPromRules([])
      showMessage(err instanceof Error ? err.message : 'Prometheus 告警规则获取失败')
    } finally { setLoading(false) }
  }
  const loadCustomRules = async () => {
    try {
      const response = await fetch(`${api}/collection-rules`)
      const data = await response.json()
      setCustomRules(Array.isArray(data.rules) ? data.rules : [])
    } catch { setCustomRules([]) }
  }
  const loadSchema = async (source = selectedCustomSource, database = selectedDatabase, table = selectedTable) => {
    if (!source) { setSchema({}); return }
    const params = new URLSearchParams()
    if (database) params.set('database', database)
    if (table) params.set('table', table)
    const response = await fetch(`${api}/data-sources/${source}/schema?${params.toString()}`)
    const data = await response.json()
    setSchema(data.schema || {})
  }
  useEffect(() => { void loadSources(); void loadCustomRules() }, [])
  useEffect(() => { if (selectedSourceId) void loadPromRules(selectedSourceId) }, [selectedSourceId])
  useEffect(() => { if (modalOpen && ruleKind === 'today') void loadSchema(selectedCustomSource, selectedDatabase, selectedTable) }, [modalOpen, ruleKind, selectedCustomSource, selectedDatabase, selectedTable])
  const databases = Object.keys(schema)
  const tables = selectedDatabase ? Object.keys(schema[selectedDatabase] || {}) : []
  const fields = selectedDatabase && selectedTable ? (schema[selectedDatabase]?.[selectedTable] || []) : []
  const stateClass = (state?: string, health?: string) => state === 'firing' ? 'danger' : state === 'pending' || health !== 'ok' ? 'pending' : 'success'
  const stateLabel = (value?: string) => value === 'inactive' ? '未触发' : value === 'firing' ? '告警中' : value === 'pending' ? '待触发' : value === 'ok' ? '正常' : (value || '-')
  const customStateClass = (lastRun: string) => lastRun.startsWith('告警') || lastRun.startsWith('执行失败') ? 'danger' : lastRun.startsWith('正常') ? 'success' : 'pending'
  const openRuleModal = () => {
    setMessage('')
    if (category === 'prometheus') {
      setRuleKind('prometheus')
      setSelectedRuleSource(selectedSourceId || prometheusSources[0]?.id || '')
    } else {
      setAlertMode('datasource')
      setRuleKind('today')
      setSelectedCustomSource(selectedCustomSource || customSources[0]?.id || '')
      setSelectedDatabase('')
      setSelectedTable('')
      setSchema({})
    }
    setModalOpen(true)
  }
  const saveRule = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const payload: CollectionRule = ruleKind === 'prometheus' ? { id: '', name: String(form.get('name') || ''), source: selectedRuleSource, database: 'prometheus', table: String(form.get('promql') || ''), field: '', condition: String(form.get('condition') || '大于'), threshold: String(form.get('threshold') || ''), timeWindow: '1分钟', lastRun: '待执行', status: '启用' } : ruleKind === 'today' ? { id: '', name: String(form.get('name') || ''), source: selectedCustomSource, database: selectedDatabase, table: selectedTable, field: String(form.get('field') || ''), condition: '当天有数据', threshold: '', timeWindow: String(form.get('deadline') || '03:00'), lastRun: '待执行', status: '启用' } : { id: '', name: String(form.get('name') || ''), source: 'custom-probe', database: ruleKind, table: String(form.get('target') || ''), field: '', condition: ruleKind === 'http' ? String(form.get('condition') || '状态码小于400') : 'TCP端口可连接', threshold: String(form.get('threshold') || ''), timeWindow: String(form.get('timeout') || '5s'), lastRun: '待执行', status: '启用' }
    try {
      const response = await fetch(`${api}/collection-rules`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存失败')
      setModalOpen(false)
      showMessage('告警规则已添加')
      void loadCustomRules()
    } catch (err) { showMessage(err instanceof Error ? err.message : '保存失败') }
  }
  const alertAction = <div className="alert-section-actions"><PlatformSelect className="alert-category-select" value={category} onChange={(next) => setCategory(next as 'prometheus' | 'custom')} options={[{ value: 'prometheus', label: 'Prometheus' }, { value: 'custom', label: '自定义' }]} /><RefreshButton loading={loading} disabled={category === 'prometheus' ? !selectedSourceId : false} onClick={() => { if (category === 'prometheus') void loadPromRules(); else void loadCustomRules() }} /><button className="button" type="button" onClick={openRuleModal}>新增规则</button></div>
  return (
    <div className="page">
      <PageHead title="告警规则" description="" actionNode={alertAction} />
      {message && <div className="toast">{message}</div>}
      {category === 'prometheus' ? (
        <section className="surface rules prometheus-rules">
          {prometheusSources.length === 0 ? (
            <div className="empty-state alert-empty-state"><b>暂无 Prometheus 数据源</b><span>请先到数据节点新增并启用 Prometheus。</span></div>
          ) : promRules.length === 0 ? (
            <div className="empty-state alert-empty-state"><b>暂无 Prometheus 告警规则</b><span>当前 Prometheus 没有返回 alerting 规则。</span></div>
          ) : (
            <div className="prometheus-rule-list">
              {promRules.map((rule, index) => (
                <article className="prometheus-rule-row compact" key={`${rule.group}-${rule.name}-${index}`}>
                  <i className="rule-icon">P</i>
                  <div>
                    <header>
                      <div className="rule-head">
                        <b>{rule.name}</b>
                        <small className="rule-inline-note">{rule.summary || rule.description || '未配置中文说明'}</small>
                      </div>
                      <span className={`alert-result ${stateClass(rule.state, rule.health)}`}>{stateLabel(rule.state || rule.health)}</span>
                    </header>
                    <code>{rule.query}</code>
                    <small className="rule-frequency">{rule.duration ? `${Math.round(rule.duration)}s` : '-'}</small>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      ) : (
        <section className="surface rules prometheus-rules">
          {customRules.length === 0 ? (
            <div className="empty-state alert-empty-state"><b>暂无自定义告警规则</b><span>点击右上角新增规则。</span></div>
          ) : (
            <div className="prometheus-rule-list">
              {customRules.map(rule => (
                <article className="prometheus-rule-row compact" key={rule.id}>
                  <i className="rule-icon">C</i>
                  <div>
                    <header>
                      <div className="rule-head">
                        <b>{rule.name}</b>
                        <small className="rule-inline-note">{rule.lastRun || '待执行'}</small>
                      </div>
                      <span className={`alert-result ${customStateClass(rule.lastRun)}`}>{rule.status}</span>
                    </header>
                    <code>{rule.source === 'custom-probe' ? `${rule.database} · ${rule.table}` : `${rule.database}.${rule.table}${rule.field ? ` · ${rule.field}` : ''}`}</code>
                    <small className="rule-frequency">{rule.timeWindow || '-'}</small>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      )}
      {modalOpen && (
        <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setModalOpen(false) }}>
          <section className="surface source-modal alert-rule-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}>
            <header className="modal-head">
              <div><h2>新增规则</h2><p>Alert rule</p></div>
              <button className="close-button" type="button" onClick={() => setModalOpen(false)}>×</button>
            </header>
            <form onSubmit={saveRule}>
              <div className="modal-form">
                {ruleKind === 'prometheus' ? (
                  <>
                    <label>数据源 <span className="required-mark">*</span><SelectShell><select value={selectedRuleSource} onChange={(event) => setSelectedRuleSource(event.target.value)} required>{prometheusSources.map(source => <option key={source.id} value={source.id}>{source.name} · {source.type}</option>)}</select></SelectShell></label>
                    <label>规则名称 <span className="required-mark">*</span><input name="name" defaultValue="Prometheus 指标规则" required /></label>
                    <label className="wide">PromQL <span className="required-mark">*</span><input name="promql" placeholder="例如 up == 0 或 opsguard_mysql_up == 0" required /></label>
                    <label>判断方式<SelectShell><select name="condition"><option value="大于">大于</option><option value="小于">小于</option><option value="等于">等于</option></select></SelectShell></label>
                    <label>阈值 <span className="required-mark">*</span><input name="threshold" placeholder="0" required /></label>
                  </>
                ) : (
                  <>
                    <label>规则类型 <span className="required-mark">*</span><SelectShell><select value={alertMode} onChange={(event) => { const mode = event.target.value as 'http' | 'tcp' | 'datasource'; setAlertMode(mode); setRuleKind(mode === 'datasource' ? 'today' : mode) }}><option value="datasource">其他数据源</option><option value="http">HTTP 探针</option><option value="tcp">TCP 探针</option></select></SelectShell></label>
                    <label>规则名称 <span className="required-mark">*</span><input name="name" defaultValue={ruleKind === 'today' ? '当天有数据检查' : '自定义探测'} required /></label>
                    {alertMode === 'datasource' ? (
                      <>
                        <label>数据源 <span className="required-mark">*</span><SelectShell><select value={selectedCustomSource} onChange={(event) => { setSelectedCustomSource(event.target.value); setSelectedDatabase(''); setSelectedTable(''); setSchema({}) }} required>{customSources.map(source => <option key={source.id} value={source.id}>{source.name} · {source.type}</option>)}</select></SelectShell></label>
                        <label>数据库 <span className="required-mark">*</span><SelectShell><select value={selectedDatabase} onChange={(event) => { setSelectedDatabase(event.target.value); setSelectedTable('') }} required><option value="">请选择数据库</option>{databases.map(name => <option key={name} value={name}>{name}</option>)}</select></SelectShell></label>
                        <label>表 <span className="required-mark">*</span><SelectShell><select value={selectedTable} onChange={(event) => setSelectedTable(event.target.value)} required><option value="">请选择表</option>{tables.map(name => <option key={name} value={name}>{name}</option>)}</select></SelectShell></label>
                        <label>时间字段<SelectShell><select name="field"><option value="">不按时间字段过滤</option>{fields.map(name => <option key={name} value={name}>{name}</option>)}</select></SelectShell></label>
                        <label>规则配置<SelectShell><select disabled><option>表存在数据</option></select></SelectShell></label>
                        <label>告警截止时间<input name="deadline" defaultValue="03:00" placeholder="03:00" /></label>
                      </>
                    ) : (
                      <>
                        <label className="wide">目标 <span className="required-mark">*</span><input name="target" placeholder={alertMode === 'http' ? 'https://example.com/health' : '127.0.0.1:3306'} required /></label>
                        {alertMode === 'http' && (
                          <>
                            <label>判断方式<SelectShell><select name="condition"><option value="状态码等于">状态码等于</option><option value="页面包含">页面包含</option><option value="状态码小于400">状态码小于400</option></select></SelectShell></label>
                            <label>期望值<input name="threshold" placeholder="200 或页面关键字" /></label>
                          </>
                        )}
                        <label>超时时间<input name="timeout" defaultValue="5s" /></label>
                      </>
                    )}
                  </>
                )}
              </div>
              <footer className="modal-actions">
                <button className="button secondary" type="button" onClick={() => setModalOpen(false)}>取消</button>
                <button className="button" type="submit">保存</button>
              </footer>
            </form>
          </section>
        </div>
      )}
    </div>
  )

}

function Notifications() {
  const [items, setItems] = useState<NotificationItem[]>([])
  useEffect(() => { fetch(`${api}/notifications?limit=100`).then(r => r.json()).then(data => setItems(Array.isArray(data.notifications) ? data.notifications : [])).catch(() => setItems([])) }, [])
  return <div className="page"><PageHead title="通知中心" description="旧平台告警通知已清空，后续可接 Alertmanager 通知流。" /><section className="surface notification-list">{items.length === 0 ? <div className="empty-state alert-empty-state"><b>暂无通知</b><span>当前没有平台通知。</span></div> : items.map(item => <article className={`notification-row ${item.unread ? 'unread' : ''}`} key={item.id}><i className={`notification-dot ${item.status === 'active' ? 'danger' : 'success'}`} /><div><header><b>{item.ruleName}</b><span className={`alert-result ${item.status === 'active' ? 'danger' : 'success'}`}>{item.status}</span></header><p>{item.message}</p><small>{formatCollectedAt(item.lastSeenAt)}</small></div></article>)}</section></div>
}

function Settings() {
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
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
  return <div className="page"><PageHead title="系统设置" description="Prometheus 已改为数据源接入；系统设置仅保留个人信息。" />{message && <div className="toast">{message}</div>}<section className="surface settings"><div className="form-section"><h3>个人信息</h3><form className="settings-form" onSubmit={changePassword}><label>原密码 <span className="required-mark">*</span><input name="oldPassword" type="password" autoComplete="current-password" required /></label><label>新密码 <span className="required-mark">*</span><input name="newPassword" type="password" autoComplete="new-password" required /></label><label>确认新密码 <span className="required-mark">*</span><input name="confirmPassword" type="password" autoComplete="new-password" required /></label><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存密码'}</button></div></form></div></section></div>
}

function formatCollectedAt(value?: string) {
  if (!value) return '待检测'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export default App
