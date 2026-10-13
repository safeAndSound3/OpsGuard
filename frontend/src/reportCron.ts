export function validateCronField(value: string, min: number, max: number, allowQuestion = false): string {
  const input = value.trim()
  if (!input) return '不能为空'
  if (input === '?') return allowQuestion ? '' : '仅日和周允许使用 ?'
  for (const part of input.split(',')) {
    const match = /^(\*|\d+(?:-\d+)?)(?:\/(\d+))?$/.exec(part)
    if (!match) return '格式错误，示例：*、*/30、1-5、1,3,5'
    if (match[2] !== undefined && (Number(match[2]) < 1 || Number(match[2]) > max - min + 1)) return `步长范围 1-${max - min + 1}`
    if (match[1] === '*') continue
    const [start, end = start] = match[1].split('-').map(Number)
    if (start < min || end > max || start > max || end < min) return `范围 ${min}-${max}`
    if (start > end) return '范围起点不能大于终点'
  }
  return ''
}

export function describeCronField(value: string, label: string, min: number): string {
  const input = value.trim()
  if (input === '?') return `${label}：不限定`
  if (input === '*') return `${label}：每${label}`
  return `${label}：${input.split(',').map(part => {
    const [base, step] = part.split('/')
    if (step) return `${base === '*' ? `从 ${min} 起` : base.includes('-') ? `在 ${base} 范围内` : `从 ${base} 起`}，每隔 ${Number(step)} ${label}${label === '日' ? '（每月重新计算）' : ''}`
    return base.includes('-') ? `${base.replace('-', ' 至 ')}` : base
  }).join('、')}`
}

// Extract the primary referenced table (db.table) from a read-only report SQL.
// Returns '' when it cannot be confidently identified; callers then fall back to the raw SQL.
export function extractMainTable(sql: string): string {
  if (!sql) return ''
  const cleaned = sql.replace(/\s+/g, ' ')
  const re = /\b(?:from|join)\s+`?([\w$]+(?:\.`?[\w$]+`?)?)`?/gi
  let match: RegExpExecArray | null
  while ((match = re.exec(cleaned))) {
    const table = match[1].replace(/`/g, '')
    if (/^(select|where|group|order|limit|on|using|as)$/i.test(table)) continue
    return table
  }
  return ''
}
