import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { BrowserRouter, NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import './App.css'

type Source = { id: string; name: string; type: string; host: string; port: string; enabled: boolean; status: string; lastTest: string; username?: string; password?: string; database?: string; remark?: string; options?: Record<string, string> }
type PrometheusMetric = { name: string }
type PrometheusAlert = { name: string; state: string; severity?: string; summary?: string; description?: string; activeAt?: string; value?: string }
type NotificationItem = { id: string; ruleName: string; status: string; message: string; unread: boolean; firstSeenAt: string; lastSeenAt: string }

const api = '/api'
const icons: Record<string, string> = { query: '◎', data: '◫', alert: '◇', notify: '◉', settings: '⚙', plus: '+', arrow: '→', bell: '●' }
function Icon({ name }: { name: string }) { return <span className={`icon icon-${name}`} aria-hidden="true">{icons[name]}</span> }

function App() {
  const [authed, setAuthed] = useState(() => localStorage.getItem('opsguard_token') === 'opsguard-admin')
  const logout = async () => {
    await fetch(`${api}/logout`, { method: 'POST' }).catch(() => {})
    localStorage.removeItem('opsguard_token')
    setAuthed(false)
  }
  if (!authed) return <Login onLogin={() => setAuthed(true)} />
  return <BrowserRouter><div className="app-shell"><Sidebar /><main className="workspace"><TopNav onLogout={logout} /><Routes><Route path="/" element={<MetricQuery />} /><Route path="/datasources" element={<DataSources />} /><Route path="/alerts" element={<Alerts />} /><Route path="/notifications" element={<Notifications />} /><Route path="/config" element={<Settings />} /></Routes></main></div></BrowserRouter>
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
  const titles: Record<string, string> = { '/': '指标查询', '/datasources': '数据节点', '/alerts': '告警规则', '/notifications': '通知中心', '/config': '系统设置' }
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
  const items = [['query', '指标查询', '/'], ['data', '数据节点', '/datasources'], ['alert', '告警规则', '/alerts'], ['notify', '通知中心', '/notifications'], ['settings', '系统设置', '/config']]
  return <aside className="sidebar"><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><small>Prometheus</small></div></div><nav>{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name={icon} /><span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><span className="online-dot" /><span>{online} / {sources.length} 节点在线</span><small>Prometheus 数据源</small></div></aside>
}

function PageHead({ title, description, action, onAction }: { title: string; description: string; action?: string; onAction?: () => void }) {
  return <header className="page-head"><div><h2>{title}</h2><span>{description}</span></div>{action && <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header>
}

function MetricQuery() {
  const [sources, setSources] = useState<Source[]>([])
  const [sourceId, setSourceId] = useState('')
  const [metrics, setMetrics] = useState<PrometheusMetric[]>([])
  const [alerts, setAlerts] = useState<PrometheusAlert[]>([])
  const [metricFilter, setMetricFilter] = useState('')
  const [query, setQuery] = useState('mysql_up')
  const [result, setResult] = useState<any>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const enabledSources = sources.filter(item => item.enabled && item.type === 'Prometheus')
  const selectedSourceId = sourceId || enabledSources[0]?.id || ''
  const filteredMetrics = useMemo(() => metrics.filter(item => item.name.toLowerCase().includes(metricFilter.toLowerCase())).slice(0, 300), [metrics, metricFilter])
  const loadSources = async () => {
    const response = await fetch(`${api}/data-sources`)
    const data = await response.json()
    const next = Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => item.type === 'Prometheus') : []
    setSources(next)
    if (!sourceId && next[0]) setSourceId(next[0].id)
  }
  const loadPrometheus = async (id = selectedSourceId) => {
    if (!id) return
    setLoading(true)
    setMessage('')
    try {
      const [metricResponse, alertResponse] = await Promise.all([fetch(`${api}/prometheus/${id}/metrics?limit=500`), fetch(`${api}/prometheus/${id}/alerts`)])
      const metricData = await metricResponse.json()
      const alertData = await alertResponse.json()
      if (!metricResponse.ok) throw new Error(metricData.error || '指标获取失败')
      if (!alertResponse.ok) throw new Error(alertData.error || '告警获取失败')
      setMetrics(Array.isArray(metricData.metrics) ? metricData.metrics : [])
      setAlerts(Array.isArray(alertData.alerts) ? alertData.alerts : [])
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Prometheus 数据获取失败')
      setMetrics([])
      setAlerts([])
    } finally { setLoading(false) }
  }
  useEffect(() => { void loadSources() }, [])
  useEffect(() => { if (selectedSourceId) void loadPrometheus(selectedSourceId) }, [selectedSourceId])
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
      setMessage(err instanceof Error ? err.message : 'PromQL 查询失败')
    } finally { setLoading(false) }
  }
  const vectorRows = Array.isArray(result?.result) ? result.result : []
  return <div className="page"><PageHead title="Prometheus 指标查询" description="选择 Prometheus 数据源，查询已存储指标、当前告警和 PromQL 结果。" /><section className="external-toolbar surface"><div><b>数据源</b><span>{enabledSources.length > 0 ? 'Prometheus 数据源在线读取' : '暂无启用的 Prometheus 数据源'}</span></div><select className="filter" value={selectedSourceId} onChange={(event) => setSourceId(event.target.value)}>{enabledSources.map(source => <option key={source.id} value={source.id}>{source.name} · {source.host}:{source.port}</option>)}</select><button className="button secondary" type="button" onClick={() => void loadPrometheus()} disabled={loading || !selectedSourceId}>{loading ? '同步中...' : '刷新'}</button></section>{message && <div className="toast">{message}</div>}{enabledSources.length === 0 ? <section className="surface empty-state"><b>暂无 Prometheus 数据源</b><span>请先到数据节点新增 Prometheus 数据源。</span></section> : <><section className="external-grid"><div className="surface external-panel"><SectionTitle title="Prometheus 告警" action={`${alerts.length} 条`} />{alerts.length === 0 ? <div className="empty-state"><b>暂无 firing 告警</b><span>Prometheus 当前没有返回告警。</span></div> : <div className="external-list">{alerts.map((alert, index) => <article key={`${alert.name}-${index}`}><header><b>{alert.name}</b><span className={`alert-result ${alert.state === 'firing' ? 'danger' : 'success'}`}>{alert.state}</span></header><p>{alert.summary || alert.description || '-'}</p><small>{alert.severity || 'unknown'} · {alert.activeAt ? formatCollectedAt(alert.activeAt) : '-'}</small></article>)}</div>}</div><div className="surface external-panel"><SectionTitle title="已存储指标" action={`${metrics.length} 项`} /><input className="metric-filter" value={metricFilter} onChange={(event) => setMetricFilter(event.target.value)} placeholder="搜索指标名" />{filteredMetrics.length === 0 ? <div className="empty-state"><b>暂无指标</b><span>Prometheus 暂未返回指标名。</span></div> : <div className="metric-name-grid">{filteredMetrics.map(metric => <button type="button" key={metric.name} onClick={() => setQuery(metric.name)}>{metric.name}</button>)}</div>}</div></section><section className="surface external-panel promql-panel"><SectionTitle title="PromQL 查询" /><form className="promql-form" onSubmit={runQuery}><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="例如 mysql_up 或 rate(mysql_global_status_questions[5m])" /><button className="button" type="submit" disabled={loading}>查询</button></form>{vectorRows.length > 0 ? <div className="query-table"><div className="query-row query-head"><span>指标标签</span><span>值</span></div>{vectorRows.map((row: any, index: number) => <div className="query-row" key={index}><code>{JSON.stringify(row.metric)}</code><b>{Array.isArray(row.value) ? row.value[1] : '-'}</b></div>)}</div> : result && <pre className="query-result">{JSON.stringify(result, null, 2)}</pre>}</section></>}</div>
}

function SectionTitle({ title, action }: { title: string; action?: string }) { return <div className="section-title"><div><h2>{title}</h2></div>{action && <span className="section-badge">{action}</span>}</div> }

function DataSources() {
  const [sources, setSources] = useState<Source[]>([])
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Source | null>(null)
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const loadSources = async () => {
    try {
      const response = await fetch(`${api}/data-sources`)
      const data = await response.json()
      setSources(Array.isArray(data.dataSources) ? data.dataSources.filter((item: Source) => item.type === 'Prometheus') : [])
    } catch { setSources([]) }
  }
  useEffect(() => { void loadSources() }, [])
  const openModal = (source?: Source) => { setEditing(source || null); setModalOpen(true) }
  const saveSource = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    const form = new FormData(event.currentTarget)
    const payload: Source = { id: editing?.id || '', name: String(form.get('name') || ''), type: 'Prometheus', host: String(form.get('host') || ''), port: String(form.get('port') || '9090'), username: '', password: String(form.get('token') || ''), database: '', remark: String(form.get('remark') || ''), enabled: true, status: '待测试', lastTest: '' }
    try {
      const response = await fetch(editing ? `${api}/data-sources/${editing.id}` : `${api}/data-sources`, { method: editing ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存失败')
      setMessage(`${data.name} 已保存`)
      setModalOpen(false)
      setEditing(null)
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
      void loadSources()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '保存失败')
    } finally { setSaving(false) }
  }
  const testSource = async (formElement: HTMLFormElement) => {
    const form = new FormData(formElement)
    const payload = { id: editing?.id || '', name: String(form.get('name') || ''), type: 'Prometheus', host: String(form.get('host') || ''), port: String(form.get('port') || '9090'), password: String(form.get('token') || '') }
    try {
      const response = await fetch(`${api}/data-sources/test`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
      const data = await response.json()
      setMessage(data.message || (data.success ? '测试成功' : '测试失败'))
    } catch { setMessage('测试失败') }
  }
  const toggleEnabled = async (source: Source, enabled: boolean) => {
    try {
      const response = await fetch(`${api}/data-sources/${source.id}/enabled`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '状态更新失败')
      setSources(current => current.map(item => item.id === source.id ? data : item))
      window.dispatchEvent(new Event('opsguard-data-sources-change'))
    } catch (err) { setMessage(err instanceof Error ? err.message : '状态更新失败') }
  }
  return <div className="page"><PageHead title="数据节点" description="Prometheus 作为唯一数据源接入，平台不再自行采集 MySQL/Redis/SSH。" action="添加 Prometheus" onAction={() => openModal()} />{sources.length === 0 ? <section className="surface empty-state"><b>暂无 Prometheus 数据源</b><span>点击右上角添加 Prometheus 后即可查询指标。</span></section> : <section className="source-list">{sources.map(source => <article className={`surface source-row ${source.status === '健康' ? 'healthy' : 'warning'} ${!source.enabled ? 'disabled' : ''}`} key={source.id}><div className="node-main"><span className="source-logo">P</span><div><h3>{source.name}</h3><p>Prometheus · {source.host}:{source.port}</p>{source.remark && <small className="source-remark">{source.remark}</small>}</div></div><div className="node-status" /><div className="node-enabled"><StatusSwitch checked={source.enabled} onChange={(checked) => void toggleEnabled(source, checked)} /></div><div className="node-meta"><span>最近检测：{formatCollectedAt(source.lastTest)}</span><span>{source.status}</span></div><div className="source-actions"><button type="button" onClick={() => openModal(source)}>编辑</button></div></article>)}</section>}{modalOpen && <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setModalOpen(false) }}><section className="surface source-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}><header className="modal-head"><div><h2>{editing ? '编辑 Prometheus' : '添加 Prometheus'}</h2><p>数据源接入</p></div><button className="close-button" type="button" onClick={() => setModalOpen(false)}>×</button></header><form onSubmit={saveSource}><div className="modal-form"><label>名称 <span className="required-mark">*</span><input name="name" defaultValue={editing?.name || '本机 Prometheus'} required /></label><label>地址 <span className="required-mark">*</span><input name="host" defaultValue={editing?.host || '127.0.0.1'} placeholder="127.0.0.1 或 http://prometheus:9090" required /></label><label>端口 <span className="required-mark">*</span><input name="port" defaultValue={editing?.port || '9090'} required /></label><label>Token<input name="token" type="password" placeholder={editing ? '留空则不修改' : '可选'} /></label><label className="wide">备注<textarea name="remark" defaultValue={editing?.remark || ''} placeholder="记录 Prometheus 用途或环境" /></label></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.closest('form'); if (form) void testSource(form) }}>测试连接</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
}

function StatusSwitch({ checked, disabled, onChange }: { checked: boolean; disabled?: boolean; onChange: (next: boolean) => void }) { return <button type="button" className={`status-switch ${checked ? 'checked' : ''}`} aria-pressed={checked} disabled={disabled} onClick={() => onChange(!checked)}><span className="status-switch-track"><span className="status-switch-thumb" /></span><span>{checked ? '启用' : '停用'}</span></button> }

function Alerts() { return <div className="page"><PageHead title="告警规则" description="旧告警规则已清空；后续告警统一从 Prometheus 规则读取或另行接入 Alertmanager。" /><section className="surface rules"><div className="empty-state alert-empty-state"><b>暂无告警规则</b><span>平台当前不再维护自定义告警规则。</span></div></section></div> }

function Notifications() {
  const [items, setItems] = useState<NotificationItem[]>([])
  useEffect(() => { fetch(`${api}/notifications?limit=100`).then(r => r.json()).then(data => setItems(Array.isArray(data.notifications) ? data.notifications : [])).catch(() => setItems([])) }, [])
  return <div className="page"><PageHead title="通知中心" description="旧平台告警通知已清空，后续可接 Alertmanager 通知流。" /><section className="surface notification-list">{items.length === 0 ? <div className="empty-state alert-empty-state"><b>暂无通知</b><span>当前没有平台通知。</span></div> : items.map(item => <article className={`notification-row ${item.unread ? 'unread' : ''}`} key={item.id}><i className={`notification-dot ${item.status === 'active' ? 'danger' : 'success'}`} /><div><header><b>{item.ruleName}</b><span className={`alert-result ${item.status === 'active' ? 'danger' : 'success'}`}>{item.status}</span></header><p>{item.message}</p><small>{formatCollectedAt(item.lastSeenAt)}</small></div></article>)}</section></div>
}

function Settings() {
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const changePassword = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setMessage('')
    const form = new FormData(event.currentTarget)
    const newPassword = String(form.get('newPassword') || '')
    if (newPassword !== String(form.get('confirmPassword') || '')) { setMessage('两次新密码不一致'); return }
    setSaving(true)
    try {
      const response = await fetch(`${api}/change-password`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ oldPassword: form.get('oldPassword'), newPassword }) })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || '修改失败')
      setMessage('密码已修改')
      event.currentTarget.reset()
    } catch (err) { setMessage(err instanceof Error ? err.message : '修改失败') } finally { setSaving(false) }
  }
  return <div className="page"><PageHead title="系统设置" description="Prometheus 已改为数据源接入；系统设置仅保留个人信息。" /><section className="surface settings"><div className="form-section"><h3>个人信息</h3><form className="settings-form" onSubmit={changePassword}><label>原密码 <span className="required-mark">*</span><input name="oldPassword" type="password" autoComplete="current-password" required /></label><label>新密码 <span className="required-mark">*</span><input name="newPassword" type="password" autoComplete="new-password" required /></label><label>确认新密码 <span className="required-mark">*</span><input name="confirmPassword" type="password" autoComplete="new-password" required /></label><div className="settings-actions"><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存密码'}</button>{message && <span>{message}</span>}</div></form></div></section></div>
}

function formatCollectedAt(value?: string) {
  if (!value) return '待检测'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export default App
