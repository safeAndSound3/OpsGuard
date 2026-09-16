import { useCallback, useEffect, useMemo, useState } from 'react'
import type { KeyboardEvent } from 'react'
import { useLocation } from 'react-router-dom'
import './ConsulWorkspace.css'

const api = '/api'

type KVValue = { key: string; value: string; modifyIndex: number }
type ConfigLanguage = 'json' | 'hcl' | 'yaml'

function validateConfig(value: string, language: ConfigLanguage): string | null {
  if (!value.trim()) return null
  if (language === 'json') {
    try { JSON.parse(value); return null } catch (error) { return `JSON 格式错误：${error instanceof Error ? error.message : '无法解析'}` }
  }
  let quote = ''
  let escaped = false
  let braces = 0
  for (const char of value) {
    if (quote) {
      if (escaped) { escaped = false; continue }
      if (char === '\\') { escaped = true; continue }
      if (char === quote) quote = ''
      continue
    }
    if (char === '"' || char === "'") quote = char
    else if (char === '{' || char === '[' || char === '(') braces++
    else if (char === '}' || char === ']' || char === ')') braces--
    if (braces < 0) return `${language.toUpperCase()} 格式错误：存在未匹配的结束括号`
  }
  if (quote) return `${language.toUpperCase()} 格式错误：存在未闭合的引号`
  if (braces !== 0) return `${language.toUpperCase()} 格式错误：存在未闭合的括号`
  const lines = value.split(/\r?\n/).map(line => line.trim()).filter(line => line && !line.startsWith('#') && !line.startsWith('//'))
  if (language === 'yaml' && lines.some(line => !line.startsWith('- ') && !line.includes(':') && !line.startsWith('---'))) return 'YAML 格式错误：配置项应使用“键: 值”或列表项语法'
  if (language === 'hcl' && lines.some(line => !line.endsWith('{') && !line.startsWith('}') && !line.includes('=') && !line.includes('{'))) return 'HCL 格式错误：配置项应使用“键 = 值”或块语法'
  return null
}

function currentEntries(keys: string[], prefix: string) {
  const normalized = prefix.replace(/^\/+/, '').replace(/([^/])$/, '$1/')
  const entries = new Map<string, { path: string; name: string; directory: boolean }>()
  for (const key of keys) {
    const rest = normalized && key.startsWith(normalized) ? key.slice(normalized.length) : normalized ? '' : key
    if (!rest) continue
    const [name, ...nested] = rest.split('/')
    if (!name) continue
    const directory = nested.length > 0
    const path = directory ? `${normalized}${name}/` : `${normalized}${name}`
    entries.set(path, { path, name, directory })
  }
  return [...entries.values()].sort((left, right) => Number(right.directory) - Number(left.directory) || left.name.localeCompare(right.name))
}

function matchingEntries(keys: string[], keyword: string) {
  const normalized = keyword.trim().toLocaleLowerCase()
  return keys.filter(key => key.toLocaleLowerCase().includes(normalized)).map(path => {
    const directory = path.endsWith('/') || keys.some(candidate => candidate !== path && candidate.startsWith(`${path.replace(/\/+$/, '')}/`))
    return { path, name: path.replace(/\/$/, ''), directory }
  }).sort((left, right) => Number(right.directory) - Number(left.directory) || left.name.localeCompare(right.name))
}

function ConsulCreateDialog({ prefix, onClose, onSave }: { prefix: string; onClose: () => void; onSave: (key: string, value: string) => void }) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  return <div className="modal-backdrop" role="presentation" onClick={onClose}><section className="surface consul-create-dialog" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}><header className="modal-head"><div><h2>新增 KV</h2><p>当前目录：{prefix || '/'}</p></div><button className="close-button" type="button" onClick={onClose}>×</button></header><label>Key 名称<input autoFocus value={key} placeholder="例如 database/config" onChange={event => setKey(event.target.value)} /></label><label>初始值<textarea value={value} placeholder="可选" onChange={event => setValue(event.target.value)} /></label><footer className="modal-actions"><button className="button secondary" type="button" onClick={onClose}>取消</button><button className="button" type="button" disabled={!key.trim()} onClick={() => onSave(`${prefix.replace(/([^/])$/, '$1/')}${key.replace(/^\/+/, '')}`, value)}>创建并编辑</button></footer></section></div>
}

export default function ConsulWorkspace() {
  const location = useLocation()
  const sourceID = new URLSearchParams(location.search).get('source') || ''
  const [keys, setKeys] = useState<string[]>([])
  const [prefix, setPrefix] = useState('')
  const [search, setSearch] = useState('')
  const [selectedKey, setSelectedKey] = useState('')
  const [value, setValue] = useState('')
  const [originalValue, setOriginalValue] = useState('')
  const [modifyIndex, setModifyIndex] = useState(0)
  const [language, setLanguage] = useState<ConfigLanguage>('json')
  const [editorScrollTop, setEditorScrollTop] = useState(0)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [message, setMessage] = useState('')
  const entries = useMemo(() => search.trim() ? matchingEntries(keys, search) : currentEntries(keys, prefix), [keys, prefix, search])
  const crumbs = useMemo(() => prefix.split('/').filter(Boolean), [prefix])
  const editorCrumbs = useMemo(() => selectedKey.split('/').filter(Boolean).slice(0, -1), [selectedKey])
  const lineNumbers = useMemo(() => Array.from({ length: Math.max(1, value.split(/\r?\n/).length) }, (_, index) => index + 1), [value])

  const showMessage = (text: string) => { setMessage(text); window.setTimeout(() => setMessage(''), 3000) }
  const loadKeys = useCallback(async (targetPrefix = prefix) => {
    if (!sourceID) return
    setLoading(true)
    try {
      const response = await fetch(`${api}/consul/${encodeURIComponent(sourceID)}/kv?prefix=${encodeURIComponent(targetPrefix)}`)
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '读取 KV 列表失败')
      setKeys(Array.isArray(data.keys) ? data.keys : [])
    } catch (error) { showMessage(error instanceof Error ? error.message : '读取 KV 列表失败') } finally { setLoading(false) }
  }, [prefix, sourceID])
  const openDirectory = (nextPrefix: string) => { setSelectedKey(''); setValue(''); setModifyIndex(0); setSearch(''); setPrefix(nextPrefix); void loadKeys(nextPrefix) }
  const searchKeys = () => { void loadKeys(search.trim() ? '' : prefix) }
  const selectKey = async (key: string) => {
    setSelectedKey(key); setValue(''); setOriginalValue(''); setModifyIndex(0); setLoading(true)
    try {
      const response = await fetch(`${api}/consul/${encodeURIComponent(sourceID)}/kv?key=${encodeURIComponent(key)}`)
      const data = await response.json() as KVValue & { error?: string }
      if (!response.ok) throw new Error(data.error || '读取 KV 值失败')
      setValue(data.value || ''); setOriginalValue(data.value || ''); setModifyIndex(Number(data.modifyIndex) || 0)
    } catch (error) { showMessage(error instanceof Error ? error.message : '读取 KV 值失败') } finally { setLoading(false) }
  }
  const save = async () => {
    if (!selectedKey) { showMessage('请先选择 KV 键'); return }
    const formatError = value === originalValue ? null : validateConfig(value, language)
    if (formatError) { showMessage(formatError); return }
    setSaving(true)
    try {
      const response = await fetch(`${api}/consul/${encodeURIComponent(sourceID)}/kv`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ key: selectedKey, value, modifyIndex, format: value === originalValue ? '' : language }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '保存 KV 失败')
      showMessage('KV 已保存到 Consul'); void selectKey(selectedKey)
    } catch (error) { showMessage(error instanceof Error ? error.message : '保存 KV 失败') } finally { setSaving(false) }
  }
  const createKey = async (key: string, initialValue: string) => {
    setSaving(true)
    try {
      const response = await fetch(`${api}/consul/${encodeURIComponent(sourceID)}/kv`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ key, value: initialValue, create: true }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || '新增 KV 失败')
      setCreateOpen(false); await loadKeys(); await selectKey(key); showMessage('KV 已创建')
    } catch (error) { showMessage(error instanceof Error ? error.message : '新增 KV 失败') } finally { setSaving(false) }
  }
  const handleEditorKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== 'Tab') return
    event.preventDefault()
    const input = event.currentTarget
    const start = input.selectionStart
    const lineStart = value.lastIndexOf('\n', Math.max(0, start - 1)) + 1
    if (event.shiftKey) {
      const indentation = value.slice(lineStart).match(/^( {1,2}|\t)/)?.[0] || ''
      if (!indentation) return
      setValue(`${value.slice(0, lineStart)}${value.slice(lineStart + indentation.length)}`)
      window.requestAnimationFrame(() => input.setSelectionRange(Math.max(lineStart, start - indentation.length), Math.max(lineStart, start - indentation.length)))
      return
    }
    const indentation = '  '
    setValue(`${value.slice(0, start)}${indentation}${value.slice(start)}`)
    window.requestAnimationFrame(() => input.setSelectionRange(start + indentation.length, start + indentation.length))
  }
  useEffect(() => { setSelectedKey(''); setValue(''); void loadKeys('') }, [loadKeys, sourceID])
  useEffect(() => {
    let active = true
    let timer = 0
    const configure = async () => {
      const response = await fetch(`${api}/settings/refresh?scope=consul`).catch(() => null)
      const setting = response?.ok ? await response.json() : { value: 1, unit: 'm' }
      const value = Math.max(1, Number(setting.value) || 1)
      const multiplier = setting.unit === 'h' ? 3600000 : setting.unit === 's' ? 1000 : 60000
      const milliseconds = value * multiplier
      if (!active) return
      window.clearInterval(timer)
      timer = window.setInterval(() => void loadKeys(), milliseconds)
    }
    void configure()
    const reload = () => { void configure() }
    window.addEventListener('opsguard-update-frequency-change', reload)
    return () => { active = false; window.clearInterval(timer); window.removeEventListener('opsguard-update-frequency-change', reload) }
  }, [loadKeys, sourceID])
  if (!sourceID) return <section className="surface consul-empty"><b>请选择已导入的 Consul 工作台</b></section>
  return <div className="page consul-workspace">{selectedKey ? <section className="surface consul-value-panel consul-value-replace"><header className="consul-browser-head"><div className="consul-breadcrumb"><button type="button" onClick={() => { setSelectedKey(''); setValue(''); setModifyIndex(0) }}>KV</button>{editorCrumbs.map((crumb, index) => { const path = `${editorCrumbs.slice(0, index + 1).join('/')}/`; return <span key={path}><i>/</i><button type="button" onClick={() => openDirectory(path)}>{crumb}</button></span> })}<span><i>/</i><b>{selectedKey.split('/').filter(Boolean).at(-1)}</b></span></div><div className="consul-editor-actions"><button className="button secondary" type="button" onClick={() => { setSelectedKey(''); setValue(''); setModifyIndex(0) }}>返回列表</button><button className="button" type="button" disabled={saving} onClick={() => void save()}>{saving ? '保存中...' : '保存'}</button></div></header><header className="consul-value-meta"><span>{selectedKey}</span>{modifyIndex > 0 && <small>修改索引：{modifyIndex}</small>}</header><div className="consul-code-editor"><div className="consul-line-numbers" aria-hidden="true"><div style={{ transform: `translateY(-${editorScrollTop}px)` }}>{lineNumbers.map(line => <span key={line}>{line}</span>)}</div></div><textarea value={value} disabled={loading} placeholder="KV 值" onChange={event => setValue(event.target.value)} onScroll={event => setEditorScrollTop(event.currentTarget.scrollTop)} onKeyDown={handleEditorKeyDown} spellCheck={false} /><label className="consul-language"><span>格式</span><select value={language} onChange={event => setLanguage(event.target.value as ConfigLanguage)}><option value="json">JSON</option><option value="hcl">HCL</option><option value="yaml">YAML</option></select></label></div></section> : <section className="surface consul-browser"><header className="consul-browser-head"><div className="consul-breadcrumb"><button type="button" className={!prefix ? 'active' : ''} onClick={() => openDirectory('')}>KV</button>{crumbs.map((crumb, index) => { const path = `${crumbs.slice(0, index + 1).join('/')}/`; return <span key={path}><i>/</i><button type="button" className={index === crumbs.length - 1 ? 'active' : ''} onClick={() => openDirectory(path)}>{crumb}</button></span> })}</div><div className="consul-browser-tools"><input value={search} placeholder="搜索完整 Key 路径" onChange={event => setSearch(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') searchKeys() }} /><button className="button" type="button" disabled={loading} onClick={searchKeys}>{loading ? '查询中...' : '查询'}</button><button className="button secondary" type="button" onClick={() => setCreateOpen(true)}>新增 Key</button></div></header><div className="consul-list-head"><span>{search.trim() ? `匹配 Key：${search.trim()}` : '名称'}</span><span>类型</span></div><div className="consul-entry-list">{loading && entries.length === 0 ? <span className="consul-list-empty">正在读取 KV 列表...</span> : entries.length === 0 ? <span className="consul-list-empty">{search.trim() ? '没有匹配的 Key。' : '当前路径下没有 KV。'}</span> : entries.map(entry => <button type="button" className="consul-entry" key={entry.path} onClick={() => entry.directory ? openDirectory(entry.path) : void selectKey(entry.path)}><span className={entry.directory ? 'consul-folder' : 'consul-key'}>{entry.directory ? '目录' : 'Key'}</span><b title={entry.name}>{entry.name}</b><small>{entry.directory ? '进入目录' : '查看与编辑值'}</small></button>)}</div></section>}{createOpen && <ConsulCreateDialog prefix={prefix} onClose={() => setCreateOpen(false)} onSave={(key, initialValue) => void createKey(key, initialValue)} />}{message && <div className="toast">{message}</div>}</div>
}
