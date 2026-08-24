import React, { useState, useEffect } from 'react'
import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import './App.css'

type MenuItem = {
  label: string
  path: string
  icon: string
}

type Metric = {
  label: string
  value: string
  trend: string
  unit: string
}


type InspectionTask = {
  id: string
  title: string
  owner: string
  status: string
  progress: number
  lastUpdated: string
}

type DataSource = {
  id: string
  name: string
  type: string
  host: string
  port: string
  username: string
  database: string
  description: string
  status: string
  lastTest: string
}

type Rule = {
  id: string
  name: string
  source: string
  database: string
  table: string
  field: string
  condition: string
  threshold: string
  timeWindow: string
  lastRun: string
  status: string
}

type SystemConfigState = {
  platformName: string
  environment: string
  responsible: string
  notificationMail: string
  smsReceiver: string
  smtpHost: string
  smtpPort: string
  openTelemetry: string
  alertWebhook: string
}

const menuItems: MenuItem[] = [
  { label: '监控大屏', path: '/', icon: '◫' },
  { label: '巡检', path: '/inspection', icon: '◌' },
  { label: '数据源', path: '/datasources', icon: '◍' },
  { label: '系统配置', path: '/config', icon: '◎' },
  { label: '平台告警', path: '/alerts', icon: '⚠' },
]

const baseMetrics: Metric[] = [
  { label: '全网请求数', value: '812.4K', trend: '+12.8%', unit: '次/分钟' },
  { label: '应用可用率', value: '99.97%', trend: '+0.03%', unit: 'SLA' },
  { label: '平均响应时间', value: '184ms', trend: '-9.4%', unit: 'ms' },
  { label: '异常告警', value: '7', trend: '-2', unit: '条' },
]


const patrolTasks: InspectionTask[] = [
  { id: 'insp-101', title: '订单系统巡检', owner: '刘旭', status: '运行中', progress: 84, lastUpdated: '10分钟前' },
  { id: 'insp-102', title: '支付链路巡检', owner: '周琳', status: '待执行', progress: 24, lastUpdated: '32分钟前' },
  { id: 'insp-103', title: '日志采集健康检查', owner: '许凯', status: '已完成', progress: 100, lastUpdated: '2小时前' },
]

const initialRules: Rule[] = [
  { id: 'rule-001', name: '订单支付慢查询', source: 'MySQL', database: 'order_center', table: 'payment_orders', field: 'paid_at', condition: '今天有数据', threshold: '', timeWindow: '24h', lastRun: '刚刚', status: '启用' },
  { id: 'rule-002', name: '库存预警值为 0', source: 'Redis', database: 'inventory', table: 'stock_info', field: 'available_qty', condition: '数值为 0', threshold: '0', timeWindow: '1h', lastRun: '12分钟前', status: '启用' },
  { id: 'rule-003', name: '订单状态为空', source: 'MySQL', database: 'order_center', table: 'orders', field: 'status', condition: '为空', threshold: '', timeWindow: '6h', lastRun: '31分钟前', status: '待确认' },
]

const initialDataSources: DataSource[] = [
  { id: 'mysql-01', name: '订单主库', type: 'MySQL', host: '10.10.20.18', port: '3306', username: 'monitor', database: 'order_center', description: '订单、支付大表实时采集', status: '健康', lastTest: '2分钟前' },
  { id: 'kafka-01', name: '日志消息总线', type: 'Kafka', host: '10.10.20.31', port: '9092', username: 'producer', database: 'platform_log', description: '业务日志、告警事件流', status: '健康', lastTest: '1分钟前' },
  { id: 'redis-01', name: '缓存集群', type: 'Redis', host: '10.10.20.45', port: '6379', username: 'cache-admin', database: '0', description: '热点缓存、限流、会话存储', status: '健康', lastTest: '5分钟前' },
]

const initialSystemConfig: SystemConfigState = {
  platformName: '数据平台监控中台',
  environment: '生产环境',
  responsible: '平台运维部',
  notificationMail: 'ops@company.com',
  smsReceiver: '13800000001',
  smtpHost: 'smtp.company.com',
  smtpPort: '465',
  openTelemetry: 'http://10.10.20.4:4318',
  alertWebhook: 'https://hooks.company.com/ops-monitor',
}

// Backend API base (使用你提供的公网 IP 和后端端口)
const API_BASE = 'http://47.80.27.198:8030'

function App() {
  return (
    <BrowserRouter>
      <div className="app-shell">
        <Sidebar />
        <main className="main-panel">
          <header className="topbar">
            <div>
              <p className="eyebrow">智能运维平台</p>
              <h1>监控与采集管理</h1>
            </div>
            <div className="topbar-badge">在线 · 7个告警</div>
          </header>
          <Routes>
            <Route path="/" element={<MonitorDashboard />} />
            <Route path="/inspection" element={<InspectionPage />} />
            <Route path="/datasources" element={<DataSourcePage />} />
            <Route path="/config" element={<SystemConfigPage />} />
            <Route path="/alerts" element={<AlertsPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}

function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="brand-block">
        <div className="brand-mark">P</div>
        <div>
          <div className="brand-name">Platform</div>
          <div className="brand-subtitle">Monitor Center</div>
        </div>
      </div>

      <nav className="nav-list">
        {menuItems.map((item) => (
          <NavLink
            key={item.path}
            to={item.path}
            end={item.path === '/'}
            className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}
          >
            <span className="nav-icon">{item.icon}</span>
            <span>{item.label}</span>
          </NavLink>
        ))}
      </nav>

      <div className="sidebar-card">
        <div className="sidebar-title">Agent 状态</div>
        <div className="status-row">
          <span className="dot green" /> 23/26 节点在线
        </div>
        <div className="status-row">
          <span className="dot yellow" /> 3 个采集任务待执行
        </div>
      </div>
    </aside>
  )
}

function MonitorDashboard() {
  const [metrics] = useState<Metric[]>(baseMetrics)

  return (
    <div className="page-grid">
      <section className="overview-panel panel">
        <div className="panel-header">
          <div>
            <p className="panel-label">全局指标</p>
            <h2>监控大屏</h2>
          </div>
          <button className="chip-btn">刷新数据</button>
        </div>
        <div className="metric-grid">
          {metrics.map((metric) => (
            <article key={metric.label} className="metric-card">
              <div className="metric-label">{metric.label}</div>
              <div className="metric-row">
                <strong>{metric.value}</strong>
                <span className="trend positive">{metric.trend}</span>
              </div>
              <div className="metric-unit">{metric.unit}</div>
            </article>
          ))}
        </div>
      </section>

      <section className="panel chart-panel">
        <div className="panel-header">
          <div>
            <p className="panel-label">请求曲线</p>
            <h2>平台吞吐量</h2>
          </div>
        </div>
        <div className="chart-wrap" aria-label="吞吐量柱状图">
          {[52, 65, 47, 83, 70, 98, 90, 110, 86, 72, 96, 118].map((height, index) => (
            <div key={index} className="bar-group">
              <span className="bar" style={{ height: `${height}%` }} />
            </div>
          ))}
        </div>
      </section>

      {/* 监控大屏不展示告警，按需在平台告警里管理 */}
    </div>
  )
}

function InspectionPage() {
  return (
    <section className="panel page-panel">
      <div className="panel-header">
        <div>
          <p className="panel-label">巡检管理</p>
          <h2>平台巡检任务</h2>
        </div>
        <button className="chip-btn">新建巡检</button>
      </div>

      <div className="task-list">
        {patrolTasks.map((task) => (
          <article key={task.id} className="task-card">
            <div className="task-topline">
              <span className="task-title">{task.title}</span>
              <span className="pill status-soft">{task.status}</span>
            </div>
            <div className="task-meta">
              <span>负责人：{task.owner}</span>
              <span>最后更新：{task.lastUpdated}</span>
            </div>
            <div className="progress-wrap">
              <div className="progress-bar">
                <span style={{ width: `${task.progress}%` }} />
              </div>
              <strong>{task.progress}%</strong>
            </div>
          </article>
        ))}
      </div>
    </section>
  )
}

function DataSourcePage() {
  const [sources, setSources] = useState<DataSource[]>(initialDataSources)
  const [activeTab, setActiveTab] = useState<'list'|'collector'>('list')
  const [showAddModal, setShowAddModal] = useState(false)
  const [testResult, setTestResult] = useState('')

  // load real sources from backend on mount
  React.useEffect(() => {
    fetch(`${API_BASE}/api/data-sources`).then((r) => r.json()).then((d) => {
      if (d && d.dataSources) setSources(d.dataSources)
    }).catch(() => {})
  }, [])

  const handleTestConnection = async (payload: any) => {
    try {
      const response = await fetch(`${API_BASE}/api/data-sources/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      const data = await response.json()
      setTestResult(data.message || '连接测试成功')
    } catch (error) {
      setTestResult('测试失败：后端未响应')
    }
  }

  const handleAddSource = async (payload: any) => {
    try {
      const resp = await fetch(`${API_BASE}/api/data-sources`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      const created = await resp.json()
      setSources((s) => [created, ...s])
      setShowAddModal(false)
      setTestResult('数据源已保存，状态：待测试')
    } catch (err) {
      setTestResult('保存失败')
    }
  }

  return (
    <div className="page-grid form-grid">
      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="panel-label">数据源</p>
            <h2>数据源管理</h2>
          </div>
          <div>
            <button className={`chip-btn ${activeTab==='list'?'':'muted'}`} onClick={() => setActiveTab('list')}>已接入数据源</button>
            <button className={`chip-btn ${activeTab==='collector'?'':'muted'}`} onClick={() => setActiveTab('collector')}>自定义采集</button>
            <button className="btn primary" style={{marginLeft:12}} onClick={() => setShowAddModal(true)}>添加数据源</button>
          </div>
        </div>

        {activeTab === 'list' && (
          <div>
            <div className="source-list">
              {sources.map((source) => (
                <div key={source.id} className="source-item">
                  <div>
                    <div className="source-name">{source.name}</div>
                    <div className="source-meta">{source.type} · {source.host}:{source.port}</div>
                  </div>
                  <div className="source-right">
                    <span className="pill status-soft">{source.status}</span>
                    <button className="btn small" onClick={() => handleTestConnection(source)}>测试连接</button>
                  </div>
                </div>
              ))}
            </div>

            {testResult && <div className="result-box">{testResult}</div>}
          </div>
        )}

        {activeTab === 'collector' && (
          <CollectorPanel sources={sources} />
        )}
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="panel-label">采集规则</p>
            <h2>自定义采集预览</h2>
          </div>
        </div>

        <div className="rule-list">
          {initialRules.map((rule) => (
            <div key={rule.id} className="rule-card">
              <div className="rule-head">
                <strong>{rule.name}</strong>
                <span className="pill status-soft">{rule.status}</span>
              </div>
              <div className="rule-body">
                <span>{rule.source}</span>
                <span>{rule.database}.{rule.table}.{rule.field}</span>
                <span>{rule.condition}</span>
              </div>
            </div>
          ))}
        </div>
      </section>

      {showAddModal && <AddDataSourceModal onClose={() => setShowAddModal(false)} onAdd={handleAddSource} onTest={handleTestConnection} />}
    </div>
  )
}

// CollectorPanel component - choose data source -> load schema -> pick table/field
function CollectorPanel({ sources }: { sources: DataSource[] }) {
  const [selectedId, setSelectedId] = useState<string>(sources[0]?.id || '')
  const [schema, setSchema] = useState<Record<string,string[]>>({})
  const [selectedTable, setSelectedTable] = useState('')
  const [selectedField, setSelectedField] = useState('')
  const [condition, setCondition] = useState('今天有数据')
  const [threshold, setThreshold] = useState('')

  React.useEffect(() => {
    if (!selectedId) return
    fetch(`${API_BASE}/api/data-sources/${selectedId}/schema`).then(r=>r.json()).then(d=>{
      if (d && d.schema) {
        setSchema(d.schema)
        const first = Object.keys(d.schema)[0]
        setSelectedTable(first || '')
        setSelectedField((d.schema[first] && d.schema[first][0]) || '')
      }
    }).catch(()=>{})
  }, [selectedId])

  return (
    <div className="collector-form">
      <div className="form-grid-two compact">
        <label>
          <span>数据源</span>
          <select value={selectedId} onChange={(e) => setSelectedId(e.target.value)}>
            {sources.map(s => <option key={s.id} value={s.id}>{s.name} ({s.type})</option>)}
          </select>
        </label>
        <label>
          <span>表</span>
          <select value={selectedTable} onChange={(e)=>setSelectedTable(e.target.value)}>
            {Object.keys(schema).map(t=> <option key={t} value={t}>{t}</option>)}
          </select>
        </label>
        <label>
          <span>字段</span>
          <select value={selectedField} onChange={(e)=>setSelectedField(e.target.value)}>
            {(schema[selectedTable]||[]).map(f=> <option key={f} value={f}>{f}</option>)}
          </select>
        </label>
        <label>
          <span>采集条件</span>
          <select value={condition} onChange={(e)=>setCondition(e.target.value)}>
            <option>今天有数据</option>
            <option>数值为 0</option>
            <option>为空</option>
            <option>大于阈值</option>
          </select>
        </label>
        <label>
          <span>阈值</span>
          <input value={threshold} onChange={(e)=>setThreshold(e.target.value)} placeholder="如：0" />
        </label>
      </div>

      <div className="rule-preview">
        <div className="rule-label">采集任务预览</div>
        <div className="rule-line">数据源：{selectedId}</div>
        <div className="rule-line">库/表/字段：{selectedTable}.{selectedField}</div>
        <div className="rule-line">触发条件：{condition} {threshold?`且值 ${threshold}`:''}</div>
      </div>
    </div>
  )
}

// AddDataSourceModal component
function AddDataSourceModal({ onClose, onAdd, onTest }: { onClose: ()=>void, onAdd: (p:any)=>void, onTest: (p:any)=>void }) {
  const [payload, setPayload] = useState<any>({ name:'', type:'MySQL', host:'', port:'3306', username:'', password:'', database:'', description:'' })
  const [err, setErr] = useState('')
  const handle = (k:string, v:string)=> setPayload((p:any)=>({...p,[k]:v}))
  const submit = ()=>{
    // validate required: name,type,host,port
    if(!payload.name || !payload.type || !payload.host || !payload.port) { setErr('请填写所有必填项（带 *）'); return }
    onAdd(payload)
  }
  return (
    <div className="modal-backdrop">
      <div className="modal-card">
        <h3>添加数据源</h3>
        <div className="form-grid-two compact">
          <label><span>数据源名称 <em>*</em></span><input value={payload.name} onChange={(e)=>handle('name',e.target.value)} /></label>
          <label><span>类型 <em>*</em></span><select value={payload.type} onChange={(e)=>handle('type',e.target.value)}><option>MySQL</option><option>Kafka</option><option>Redis</option><option>Elasticsearch</option></select></label>
          <label><span>结构地址 <em>*</em></span><input value={payload.host} onChange={(e)=>handle('host',e.target.value)} /></label>
          <label><span>端口 <em>*</em></span><input value={payload.port} onChange={(e)=>handle('port',e.target.value)} /></label>
          <label><span>用户名</span><input value={payload.username} onChange={(e)=>handle('username',e.target.value)} /></label>
          <label><span>密码</span><input type="password" value={payload.password} onChange={(e)=>handle('password',e.target.value)} /></label>
          <label><span>数据库/主题</span><input value={payload.database} onChange={(e)=>handle('database',e.target.value)} /></label>
          <label className="full-span"><span>描述</span><textarea value={payload.description} onChange={(e)=>handle('description',e.target.value)} /></label>
        </div>
        <div style={{display:'flex',gap:12,marginTop:12}}>
          <button className="btn primary" onClick={submit}>保存</button>
          <button className="btn muted" onClick={()=>onTest(payload)}>测试连接</button>
          <button className="btn" onClick={onClose}>取消</button>
        </div>
        {err && <div style={{color:'salmon',marginTop:8}}>{err}</div>}
      </div>
    </div>
  )
}


function SystemConfigPage() {
  const [config, setConfig] = useState<SystemConfigState>(initialSystemConfig)
  const [savedState, setSavedState] = useState('')

  const updateField = (field: keyof SystemConfigState, value: string) => {
    setConfig((current) => ({ ...current, [field]: value }))
  }

  const handleSave = () => {
    setSavedState('配置已保存，下一次平台采集会自动加载最新设置。')
  }

  const handleTestAlert = async () => {
    try {
      const response = await fetch('http://localhost:8080/api/system-config/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
      })
      const data = await response.json()
      setSavedState(data.message || '告警通道测试成功')
    } catch (error) {
      setSavedState('告警通道测试失败：后端未启动或未在 localhost:8080 提供服务')
    }
  }

  return (
    <section className="panel page-panel">
      <div className="panel-header">
        <div>
          <p className="panel-label">系统配置</p>
          <h2>平台通用设置</h2>
        </div>
      </div>

      <div className="config-layout">
        <div className="form-panel">
          <div className="form-grid-two compact">
            <label>
              <span>平台名称 <em>*</em></span>
              <input value={config.platformName} onChange={(e) => updateField('platformName', e.target.value)} />
            </label>
            <label>
              <span>部署环境 <em>*</em></span>
              <select value={config.environment} onChange={(e) => updateField('environment', e.target.value)}>
                <option>生产环境</option>
                <option>测试环境</option>
                <option>预发环境</option>
              </select>
            </label>
            <label>
              <span>责任人 <em>*</em></span>
              <input value={config.responsible} onChange={(e) => updateField('responsible', e.target.value)} />
            </label>
            <label>
              <span>告警邮箱 <em>*</em></span>
              <input value={config.notificationMail} onChange={(e) => updateField('notificationMail', e.target.value)} />
            </label>
            <label>
              <span>短信接收人</span>
              <input value={config.smsReceiver} onChange={(e) => updateField('smsReceiver', e.target.value)} />
            </label>
            <label>
              <span>SMTP 地址</span>
              <input value={config.smtpHost} onChange={(e) => updateField('smtpHost', e.target.value)} />
            </label>
            <label>
              <span>SMTP 端口</span>
              <input value={config.smtpPort} onChange={(e) => updateField('smtpPort', e.target.value)} />
            </label>
            <label>
              <span>OpenTelemetry 地址</span>
              <input value={config.openTelemetry} onChange={(e) => updateField('openTelemetry', e.target.value)} />
            </label>
            <label className="full-span">
              <span>告警 Webhook</span>
              <input value={config.alertWebhook} onChange={(e) => updateField('alertWebhook', e.target.value)} />
            </label>
          </div>

          <div className="action-row">
            <button className="btn primary" onClick={handleSave}>保存配置</button>
            <button className="btn muted" onClick={handleTestAlert}>测试告警通道</button>
          </div>
          {savedState && <div className="result-box">{savedState}</div>}
        </div>

        <aside className="info-panel">
          <div className="info-box">
            <div className="info-title">必填项</div>
            <ul>
              {['平台名称', '部署环境', '责任人', '告警邮箱'].map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          </div>
          <div className="info-box">
            <div className="info-title">选填项</div>
            <ul>
              {['短信接收人', 'SMTP 地址', 'OpenTelemetry 地址', '告警 Webhook'].map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          </div>
        </aside>
      </div>
    </section>
  )
}

// Alerts rule page
function AlertsPage() {
  const [rules, setRules] = useState<Rule[]>(initialRules)
  const [name, setName] = useState('')
  const [source, setSource] = useState('MySQL')

  useEffect(()=>{
    fetch(`${API_BASE}/api/collection-rules`).then(r=>r.json()).then(d=>{
      if(d && d.rules) setRules(d.rules)
    }).catch(()=>{})
  },[])

  const addRule = async ()=>{
    const rule: Rule = { id:`rule-${Date.now()}`, name, source, database:'', table:'', field:'', condition:'今天有数据', threshold:'', timeWindow:'24h', lastRun:'', status:'启用' }
    try{
      const resp = await fetch(`${API_BASE}/api/collection-rules`, { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(rule) })
      const created = await resp.json()
      setRules(r=>[created, ...r])
      setName('')
    }catch(e){}
  }

  return (
    <section className="panel page-panel">
      <div className="panel-header">
        <div>
          <p className="panel-label">平台告警</p>
          <h2>告警规则</h2>
        </div>
        <div>
          <input placeholder="规则名称" value={name} onChange={(e)=>setName(e.target.value)} />
          <select value={source} onChange={(e)=>setSource(e.target.value)} style={{marginLeft:8}}>
            <option>MySQL</option>
            <option>Redis</option>
            <option>Kafka</option>
          </select>
          <button className="btn primary" onClick={addRule} style={{marginLeft:8}}>新增规则</button>
        </div>
      </div>

      <div className="rule-list">
        {rules.map(r=> (
          <div key={r.id} className="rule-card">
            <div className="rule-head"><strong>{r.name}</strong><span className="pill status-soft">{r.status}</span></div>
            <div className="rule-body"><span>{r.source}</span><span>{r.database}.{r.table}.{r.field}</span><span>{r.condition}</span></div>
          </div>
        ))}
      </div>
    </section>
  )
}

export default App
