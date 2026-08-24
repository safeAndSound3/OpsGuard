import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import './App.css'

type Metric = { label: string; value: string; detail: string; change: string; tone: 'blue' | 'green' | 'amber' | 'violet' }
type Task = { id: string; title: string; owner: string; status: string; progress: number; updated: string }
type Source = { id: string; name: string; type: string; host: string; port: string; status: string; lastTest: string; remark?: string; options?: Record<string, string> }
type Rule = { id: string; name: string; source: string; database: string; table: string; field: string; condition: string; status: string }
type SourceType = 'MySQL' | 'Kafka' | 'Redis' | 'PostgreSQL' | 'Elasticsearch'

const api = '/api'
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

function App() { return <BrowserRouter><div className="app-shell"><Sidebar /><main className="workspace"><Topbar /><Routes><Route path="/" element={<Dashboard />} /><Route path="/inspection" element={<Inspection />} /><Route path="/datasources" element={<DataSources />} /><Route path="/alerts" element={<Alerts />} /><Route path="/config" element={<Settings />} /></Routes></main></div></BrowserRouter> }
function Sidebar() { const items = [['overview', '监控总览', '/'], ['inspection', '巡检任务', '/inspection'], ['data', '数据源', '/datasources'], ['alert', '平台告警', '/alerts'], ['settings', '系统配置', '/config']]; return <aside className="sidebar"><div className="brand"><img className="brand-logo" src="/favicon.svg" alt="" /><div><b>OpsGuard</b><small>巡检平台</small></div></div><nav>{items.map(([icon, label, path]) => <NavLink key={path} end={path === '/'} to={path} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><Icon name={icon} /><span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><span className="online-dot" /><span>23 / 26 节点在线</span><small>采集服务运行正常</small></div></aside> }
function Topbar() { return <header className="topbar"><div><p className="breadcrumb">运维平台 / 平台总览</p><h1>监控与采集管理</h1></div><div className="top-actions"><button className="icon-button" aria-label="通知"><Icon name="bell" /><i>7</i></button><div className="avatar">OP</div><div className="account"><b>平台运维部</b><span>生产环境</span></div></div></header> }
function Dashboard() { const [metrics, setMetrics] = useState(fallbackMetrics); const [loading, setLoading] = useState(false); const refresh = async () => { setLoading(true); try { const r = await fetch(`${api}/overview`); const d = await r.json(); if (Array.isArray(d.metrics)) setMetrics(d.metrics.map((m: any, i: number) => ({ ...fallbackMetrics[i % 4], label: m.label ?? m.name ?? fallbackMetrics[i % 4].label, value: m.value ?? fallbackMetrics[i % 4].value }))) } catch { /* mock snapshot stays visible */ } finally { setLoading(false) } }; useEffect(() => { void refresh() }, []); return <div className="page dashboard"><section className="hero"><div><span className="live"><i />实时运行中</span><h2>平台运行平稳，<em>服务健康。</em></h2><p>系统已连续稳定运行 32 天，关键业务链路处于预期区间。</p></div><button className="button secondary" onClick={refresh} disabled={loading}>{loading ? '同步中…' : '刷新数据'} <Icon name="arrow" /></button></section><section className="metric-grid">{metrics.map((m) => <article className={`metric-card ${m.tone}`} key={m.label}><div className="metric-top"><span>{m.label}</span><span className="metric-symbol">⌁</span></div><strong>{m.value}</strong><div className="metric-foot"><span>{m.detail}</span><b>{m.change}</b></div></article>)}</section><section className="content-grid"><article className="surface chart-card"><SectionTitle eyebrow="趋势分析" title="平台吞吐量" action="近 12 小时" /><div className="chart"><div className="chart-labels"><span>120k</span><span>80k</span><span>40k</span><span>0</span></div><div className="chart-bars">{[34, 48, 42, 61, 58, 82, 70, 91, 65, 73, 86, 96].map((n, i) => <div className="bar-col" key={i}><i style={{ height: `${n}%` }} /></div>)}</div></div><div className="chart-axis"><span>00:00</span><span>04:00</span><span>08:00</span><span>12:00</span></div></article><article className="surface health-card"><SectionTitle eyebrow="资源状态" title="服务健康度" /><div className="health-ring"><span>98<small>健康评分</small></span></div><div className="health-stats"><p><span>●</span>在线服务 <b>23 / 26</b></p><p><span>●</span>采集任务 <b>18 / 21</b></p><p><span>●</span>数据源健康 <b>12 / 13</b></p></div></article></section><section className="surface activity"><SectionTitle eyebrow="运行动态" title="近期告警" action="查看全部" /><div className="activity-list"><Activity tone="danger" title="支付服务响应时间超过阈值" meta="支付链路 · 4 分钟前" /><Activity tone="warning" title="缓存集群容量已使用 82%" meta="Redis 集群 · 18 分钟前" /><Activity tone="info" title="订单库巡检任务已完成" meta="自动巡检 · 32 分钟前" /></div></section></div> }
function SectionTitle({ eyebrow, title, action }: { eyebrow: string; title: string; action?: string }) { return <div className="section-title"><div><p>{eyebrow}</p><h2>{title}</h2></div>{action && <button className="text-button">{action} <Icon name="arrow" /></button>}</div> }
function Activity({ tone, title, meta }: { tone: string; title: string; meta: string }) { return <div className="activity-row"><i className={`status-dot ${tone}`} /><div><b>{title}</b><span>{meta}</span></div><button className="more">•••</button></div> }
function PageHead({ eyebrow, title, description, action, onAction }: { eyebrow: string; title: string; description: string; action?: string; onAction?: () => void }) { return <header className="page-head"><div><p>{eyebrow}</p><h2>{title}</h2><span>{description}</span></div>{action && <button className="button" onClick={onAction}><Icon name="plus" /> {action}</button>}</header> }
function Inspection() { return <div className="page"><PageHead eyebrow="任务编排" title="巡检任务" description="统一查看任务执行状态与最近的健康检查结果。" action="新建巡检" /><section className="surface table-card"><div className="table-toolbar"><b>全部任务 <small>{fallbackTasks.length}</small></b><div><button className="filter">状态：全部⌄</button><button className="filter">最近更新⌄</button></div></div><div className="task-table">{fallbackTasks.map(t => <div className="task-row" key={t.id}><div><b>{t.title}</b><span>{t.id} · 负责人：{t.owner}</span></div><span className={`tag ${t.status === '已完成' ? 'success' : t.status === '运行中' ? 'running' : 'pending'}`}>{t.status}</span><div className="progress"><i><b style={{ width: `${t.progress}%` }} /></i><span>{t.progress}%</span></div><time>{t.updated}</time><button className="more">•••</button></div>)}</div></section></div> }
function DataSources() {
  const [sources, setSources] = useState(fallbackSources)
  const [message, setMessage] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [sourceType, setSourceType] = useState<SourceType>('MySQL')
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [optionRows, setOptionRows] = useState([{ key: '', value: '' }, { key: '', value: '' }])

  useEffect(() => {
    fetch(`${api}/data-sources`).then(r => r.json()).then(d => setSources(Array.isArray(d.dataSources) ? d.dataSources : [])).catch(() => setSources([]))
  }, [])

  const closeModal = () => {
    setModalOpen(false)
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
      const response = await fetch(`${api}/data-sources`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(buildPayload(event.currentTarget)) })
      const added = await response.json()
      if (!response.ok) throw new Error(added.error || '保存失败')
      setSources(current => [added, ...current])
      setMessage(`${added.name} 已保存`)
      closeModal()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '保存失败')
      setSaving(false)
    }
  }

  return <div className="page"><PageHead eyebrow="接入管理" title="数据源" description="管理平台采集的数据连接，并及时处理异常源。" action="添加数据源" onAction={() => setModalOpen(true)} />{sources.length === 0 ? <section className="surface empty-state"><b>暂无数据源</b><span>点击右上角添加数据源，完成连接测试后即可保存。</span></section> : <section className="source-grid">{sources.map(s => <article className="surface source-card" key={s.id}><div className="source-card-top"><span className="source-logo">{s.type.slice(0, 1)}</span><span className={`tag ${s.status === '健康' ? 'success' : 'pending'}`}>{s.status}</span></div><h3>{s.name}</h3><p>{s.type} · {s.host}:{s.port}</p>{s.remark && <small className="source-remark">{s.remark}</small>}<footer><span>上次检测：{s.lastTest}</span></footer></article>)}</section>}{modalOpen && <div className="modal-backdrop" role="presentation" onMouseDown={closeModal}><section className="surface source-modal" role="dialog" aria-modal="true" aria-labelledby="source-modal-title" onMouseDown={(event) => event.stopPropagation()}><header className="modal-head"><div><p>新增接入</p><h2 id="source-modal-title">添加数据源</h2></div><button className="close-button" type="button" aria-label="关闭" onClick={closeModal}>×</button></header><form key={sourceType} onSubmit={saveSource}><div className="type-picker" role="group" aria-label="数据类型">{sourceTypes.map(type => <button key={type} type="button" className={sourceType === type ? 'active' : ''} onClick={() => { setSourceType(type); setOptionRows([{ key: '', value: '' }, { key: '', value: '' }]) }}>{type}</button>)}</div><div className="modal-form"><Field label="数据源名称" name="name" value={`${sourceType} 生产数据源`} /><label>主机地址<input name="host" placeholder="例如 127.0.0.1 或 broker.internal" required /></label><label>端口<input name="port" defaultValue={defaultPorts[sourceType]} required /></label>{sourceType === 'Kafka' ? <label>Topic / Consumer Group<input name="topic" placeholder="例如 ops-events / ops-monitor" /></label> : <label>数据库 / 命名空间<input name="database" placeholder={sourceType === 'Redis' ? '例如 0' : '例如 opsguard_lab'} /></label>}<label>用户名<input name="username" placeholder={sourceType === 'Redis' ? '可选' : '请输入用户名'} /></label><label>密码<input name="password" type="password" placeholder="请输入密码" /></label>{sourceType === 'Elasticsearch' && <label>索引前缀<input name="indexPrefix" placeholder="例如 logs-*" /></label>}<label className="wide">备注<textarea name="remark" placeholder="记录用途、负责人、环境或注意事项" /></label><div className="wide option-editor"><div><b>连接参数</b><span>示例：ssl true、timeout 10s、brokers host1:9092,host2:9092</span></div>{optionRows.map((row, index) => <div className="option-row" key={index}><input aria-label="参数名" placeholder="key" value={row.key} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, key: event.target.value } : item))} /><input aria-label="参数值" placeholder="value" value={row.value} onChange={(event) => setOptionRows(rows => rows.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} /></div>)}<button className="text-button" type="button" onClick={() => setOptionRows(rows => [...rows, { key: '', value: '' }])}>添加参数 <Icon name="plus" /></button></div></div><footer className="modal-actions"><button className="button secondary" type="button" onClick={(event) => { const form = event.currentTarget.form; if (form) void testConnection(form) }} disabled={testing}>{testing ? '测试中...' : '测试连接'}</button><button className="button" type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</button></footer></form></section></div>}{message && <div className="toast">{message}</div>}</div>
}
function Alerts() { return <div className="page"><PageHead eyebrow="风险中心" title="告警规则" description="以业务优先级管理告警规则和处置状态。" action="新建规则" /><section className="surface rules">{fallbackRules.map(r => <div className="rule-row" key={r.id}><i className="rule-icon">⌁</i><div><b>{r.name}</b><span>{r.source} · {r.database}.{r.table}.{r.field} · {r.condition}</span></div><span className={`tag ${r.status === '启用' ? 'success' : 'pending'}`}>{r.status}</span><button className="text-button">编辑 <Icon name="arrow" /></button></div>)}</section></div> }
function Settings() { const [saved, setSaved] = useState(false); return <div className="page"><PageHead eyebrow="平台设置" title="系统配置" description="维护平台基础信息和告警通知渠道。" /><section className="surface settings"><div className="form-section"><h3>基础信息</h3><div className="form-grid"><Field label="平台名称" value="数据平台监控中台" /><Field label="部署环境" value="生产环境" /><Field label="责任团队" value="平台运维部" /><Field label="告警邮箱" value="ops@company.com" /></div></div><div className="form-section"><h3>可观测性</h3><div className="form-grid"><Field label="OpenTelemetry 地址" value="http://10.10.20.4:4318" /><Field label="告警 Webhook" value="https://hooks.company.com/ops-monitor" /></div></div><button className="button" onClick={() => setSaved(true)}>保存配置</button>{saved && <span className="saved">配置已保存</span>}</section></div> }
function Field({ label, value, name }: { label: string; value: string; name?: string }) { return <label>{label}<input name={name} defaultValue={value} /></label> }
export default App
