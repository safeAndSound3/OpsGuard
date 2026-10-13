// 本地联调：使用环境变量提供登录信息，仅创建并清理本脚本的测试任务。
const https = require('node:https');
const assert = require('node:assert/strict');
let cookie = '';
const base = process.env.OPSGUARD_TEST_URL || 'https://127.0.0.1:8030';
async function request(path, method = 'GET', body) {
  return new Promise((resolve, reject) => {
    const req = https.request(base + path, { method, rejectUnauthorized: false, headers: { Cookie: cookie, 'Content-Type': 'application/json', Origin: base } }, res => {
      if (res.headers['set-cookie']) cookie = res.headers['set-cookie'].map(v => v.split(';')[0]).join('; ');
      let raw = ''; res.on('data', chunk => raw += chunk);
      res.on('end', () => { try { resolve({ status: res.statusCode, data: JSON.parse(raw) }); } catch { reject(new Error('Invalid response: ' + res.statusCode)); } });
    });
    req.on('error', reject); req.setTimeout(15000, () => req.destroy(new Error('timeout')));
    req.end(body === undefined ? undefined : JSON.stringify(body));
  });
}
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
async function main() {
  assert(process.env.OPSGUARD_TEST_PASSWORD, 'Set OPSGUARD_TEST_PASSWORD');
  assert.equal((await request('/api/login', 'POST', { username: 'admin', password: process.env.OPSGUARD_TEST_PASSWORD })).status, 200, 'login');
  const sources = (await request('/api/data-sources')).data;
  const source = (sources.dataSources || []).find(s => s.type.toLowerCase() === 'mysql' && s.enabled);
  assert(source, 'enabled MySQL source');
  const ids = [];
  async function create(sql, schedule) {
    const response = await request('/api/mysql-report-tasks', 'POST', { name: '调度联调-' + Date.now(), sourceId: source.id, sql, schedule, enabled: true });
    assert.equal(response.status, 200, JSON.stringify(response.data));
    ids.push(response.data.id); return response.data.id;
  }
  async function result(id) {
    for (let i = 0; i < 20; i++) {
      const r = await request('/api/mysql-report-tasks/' + id + '/result');
      if (r.status === 200 && r.data.status !== '执行中') return r.data;
      await wait(1000);
    }
    throw new Error('Task did not finish');
  }
  try {
    const id = await create('SELECT 1 AS scheduler_check', '*/5 * * * * ?');
    const first = await result(id);
    assert.equal(first.status, '成功', first.message);
    assert.equal(String(first.result.rows[0][0]), '1');
    assert.equal((await request('/api/mysql-report-tasks/' + id + '/enabled', 'POST', { enabled: false })).status, 200);
    await wait(1500);
    const stopped = await result(id); await wait(6000);
    assert.equal((await result(id)).id, stopped.id, 'disabled task must not run');
    console.log('PASS automatic execution, persisted row result, disabled schedule');
    assert.equal((await request('/api/mysql-report-tasks/' + id + '/run', 'POST')).status, 202);
    await wait(1500);
    assert.notEqual((await result(id)).id, stopped.id);
    console.log('PASS manual execution');
    const failed = await create('SELECT * FROM opsguard.__missing_report_test_table__', '手动');
    assert.equal((await request('/api/mysql-report-tasks/' + failed + '/run', 'POST')).status, 202);
    assert.equal((await result(failed)).status, '失败');
    console.log('PASS SQL failure persisted');
    const invalid = await request('/api/mysql-report-tasks', 'POST', { name: 'invalid', sourceId: source.id, sql: 'SELECT 1', schedule: '*/0 * * * * ?', enabled: true });
    assert.equal(invalid.status, 400);
    console.log('PASS invalid Cron rejected');
  } finally {
    for (const id of ids) assert.equal((await request('/api/mysql-report-tasks/' + id, 'DELETE')).status, 200);
    console.log('Test tasks removed');
  }
}
main().catch(e => { console.error(e.message); process.exitCode = 1; });
