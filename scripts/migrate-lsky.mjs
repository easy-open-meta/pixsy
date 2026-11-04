#!/usr/bin/env node
// 从 Lsky Pro(开源版 lsky-org/lsky-pro)迁移图片到 pixsy。
// 走 Lsky API /api/v1/images 拉清单 → 逐张下载 → pixsy API /api/v1/images 上传,
// 输出 旧URL→新URL 映射 CSV,用于替换文章里已引用的旧链接。
//
// 用法:
//   node scripts/migrate-lsky.mjs --lsky https://lsky.example.com \
//       --lsky-email 邮箱 --lsky-password 密码 \
//       --pixsy http://localhost:8090 --pixsy-user admin --pixsy-pass 密码 \
//       [--run] [--csv 路径] [--concurrency 3]
//
// 默认 dry-run:只登录并拉取清单做统计,确认无误后加 --run 真正迁移。
// 迁移中断后重跑即可:已成功的条目按 CSV 记录自动跳过。

import { open, readFile } from 'node:fs/promises';
import { statSync } from 'node:fs';

// ---- CLI 参数 ----
const args = {};
for (let i = 2; i < process.argv.length; i++) {
  const a = process.argv[i];
  if (a === '--run') { args.run = true; continue; }
  const key = a.replace(/^--/, '').replace(/-([a-z])/g, (_, c) => c.toUpperCase());
  args[key] = process.argv[++i];
}
const lskyBase = (args.lsky ?? '').replace(/\/+$/, '');
const lskyEmail = args.lskyEmail ?? process.env.LSKY_EMAIL;
const lskyPass = args.lskyPassword ?? process.env.LSKY_PASSWORD;
const pixsyBase = (args.pixsy ?? 'http://localhost:8090').replace(/\/+$/, '');
const pixsyUser = args.pixsyUser;
const pixsyPass = args.pixsyPass;
const pixsyToken = args.pixsyToken;
const csvPath = args.csv ?? 'lsky-pixsy-mapping.csv';
const concurrency = Math.max(1, Number(args.concurrency ?? 3));

const need = (v, name) => { if (!v) { console.error(`缺少参数 --${name}`); process.exit(1); } };
need(lskyBase, 'lsky');
need(lskyEmail, 'lsky-email');
need(lskyPass, 'lsky-password');
if (!pixsyToken) { need(pixsyUser, 'pixsy-user'); need(pixsyPass, 'pixsy-pass'); }

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const KB = 1024, MB = 1024 * KB;
const fmtSize = (n) => n >= MB ? `${(n / MB).toFixed(1)}MB` : `${Math.ceil(n / KB)}KB`;

// pixsy 只认这四种格式(按内容魔数判定;扩展名仅决定 URL 后缀)。
const SUPPORTED = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp']);

function csvRow(cells) {
  return cells.map((v) => `"${String(v ?? '').replaceAll('"', '""')}"`).join(',');
}

async function api(url, opts = {}, timeoutMs = 60_000) {
  return fetch(url, { ...opts, signal: AbortSignal.timeout(timeoutMs) });
}

// Lsky 响应包:{status, message, data};pixsy 响应包:{data} / {error:{message}}。
async function bodyOf(res) {
  try { return await res.json(); } catch { return {}; }
}

async function lskyToken() {
  let res;
  for (let attempt = 1; ; attempt++) {
    res = await api(`${lskyBase}/api/v1/tokens`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: lskyEmail, password: lskyPass }),
    });
    if (res.status === 429 && attempt <= 3) {
      console.log(`Lsky 接口限流,20s 后重试(${attempt}/3)…`);
      await sleep(20_000);
      continue;
    }
    break;
  }
  const j = await bodyOf(res);
  if (!res.ok || j.status === false || !j.data?.token) {
    throw new Error(`Lsky 登录失败(HTTP ${res.status}):${j.message ?? JSON.stringify(j).slice(0, 200)}`);
  }
  return j.data.token;
}

async function lskyPage(token, page) {
  const res = await api(`${lskyBase}/api/v1/images?page=${page}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  const j = await bodyOf(res);
  if (!res.ok || j.status === false) {
    throw new Error(`Lsky 图片清单第 ${page} 页拉取失败(HTTP ${res.status}):${j.message ?? ''}`);
  }
  // Laravel 分页器:fixed 40/页,data.data 为条目,last_page/total 在 data 上。
  const p = j.data;
  if (!Array.isArray(p?.data)) {
    throw new Error(`Lsky 响应结构与预期不符(顶层键:${Object.keys(j).join(',')})。` +
      `若你的兰空版本不同,请把本条报错反馈给维护者调整解析。`);
  }
  return { items: p.data, lastPage: Number(p.last_page ?? 1), total: Number(p.total ?? p.data.length) };
}

async function getPixsyToken() {
  if (pixsyToken) return pixsyToken;
  const res = await api(`${pixsyBase}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: pixsyUser, password: pixsyPass }),
  });
  const j = await bodyOf(res);
  if (!res.ok || !j.data?.token) {
    throw new Error(`pixsy 登录失败(HTTP ${res.status}):${j.error?.message ?? ''}`);
  }
  return j.data.token;
}

async function uploadOne(pixsyAuth, item) {
  const url = new URL(item.links?.url ?? item.url, lskyBase).href;
  const name = item.origin_name || item.name || item.pathname || `lsky-${item.key ?? item.id}`;
  const ext = String(item.extension ?? '').toLowerCase();

  const dl = await api(url);
  if (!dl.ok) throw new Error(`下载失败 HTTP ${dl.status}`);
  const buf = await dl.arrayBuffer();

  const fd = new FormData();
  fd.append('file', new Blob([buf], { type: item.mimetype || 'application/octet-stream' }), name);
  const up = await api(`${pixsyBase}/api/v1/images`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${pixsyAuth}` },
    body: fd,
  });
  const j = await bodyOf(up);
  if (!up.ok || !j.data?.images?.[0]) {
    throw new Error(`上传失败 HTTP ${up.status}:${j.error?.message ?? JSON.stringify(j).slice(0, 200)}`);
  }
  const img = j.data.images[0];
  return { url, name, ext, id: img.id, pixsyUrl: img.url, markdown: img.markdown };
}

async function loadDoneUrls() {
  try {
    const text = await readFile(csvPath, 'utf8');
    return new Set(
      text.split('\n').filter((l) => l.includes(',"ok",'))
          .map((l) => l.match(/^"[^"]*","([^"]*)"/)?.[1])
          .filter(Boolean),
    );
  } catch { return new Set(); }
}

async function appendCsv(rows) {
  let header = false;
  try { statSync(csvPath); } catch { header = true; }
  const f = await open(csvPath, 'a');
  if (header) await f.write('\uFEFF' + csvRow(['lsky_name', 'lsky_url', 'pixsy_id', 'pixsy_url', 'pixsy_markdown', 'status', 'error']) + '\n');
  for (const r of rows) await f.write(csvRow(r) + '\n');
  await f.close();
}

// ---- 主流程 ----
const lskyAuth = await lskyToken();
console.log('Lsky 登录成功');

const first = await lskyPage(lskyAuth, 1);
const items = [...first.items];
for (let p = 2; p <= first.lastPage; p++) items.push(...(await lskyPage(lskyAuth, p)).items);
console.log(`共 ${items.length} 张图片(${first.total} 计入,Lsky 每页 40,${first.lastPage} 页)`);

const byExt = {};
for (const it of items) {
  const e = String(it.extension ?? '未知').toLowerCase();
  byExt[e] = (byExt[e] ?? 0) + 1;
}
const unsupported = Object.entries(byExt).filter(([e]) => !SUPPORTED.has(e));
console.log('按格式:', Object.entries(byExt).map(([e, n]) => `${e}×${n}`).join(', '));
if (unsupported.length) {
  console.log(`注意:pixsy 仅支持 ${[...SUPPORTED].join('/')};${unsupported.map(([e, n]) => `${e}×${n}`).join(', ')} 会迁移失败,需转码后再单独补传。`);
}
if (!args.run) {
  console.log('\n— dry-run 样例(前 3 张)—');
  for (const it of items.slice(0, 3)) {
    console.log(`  ${it.origin_name || it.name}  ${fmtSize(Number(it.size ?? 0))}  ${new URL(it.links?.url ?? it.url, lskyBase).href}`);
  }
  console.log(`\n确认无误后,加 --run 开始迁移(结果写入 ${csvPath})。`);
  process.exit(0);
}

const pixsyAuth = await getPixsyToken();
console.log('pixsy 登录成功');

const done = await loadDoneUrls();
const pending = items.filter((it) => {
  const u = new URL(it.links?.url ?? it.url, lskyBase).href;
  return !done.has(u);
});
console.log(`已完成 ${items.length - pending.length} 张(按 CSV 跳过),本次待迁移 ${pending.length} 张\n`);

let ok = 0, failed = 0, cursor = 0;
const failLog = [];

async function worker() {
  while (cursor < pending.length) {
    const item = pending[cursor++];
    try {
      const r = await uploadOne(pixsyAuth, item);
      ok++;
      await appendCsv([[r.name, r.url, r.id, r.pixsyUrl, r.markdown, 'ok', '']]);
      console.log(`[${ok + failed}/${pending.length}] ✓ ${r.name} → ${r.pixsyUrl}`);
    } catch (err) {
      failed++;
      const url = new URL(item.links?.url ?? item.url, lskyBase).href;
      const name = item.origin_name || item.name || url;
      await appendCsv([[name, url, '', '', '', 'fail', err.message]]);
      failLog.push(`${name}: ${err.message}`);
      console.error(`[${ok + failed}/${pending.length}] ✗ ${name} — ${err.message}`);
    }
  }
}
await Promise.all(Array.from({ length: concurrency }, worker));

console.log(`\n迁移结束:成功 ${ok},失败 ${failed}。映射已写入 ${csvPath}`);
if (failLog.length) {
  console.log('\n失败明细(修正问题后重跑脚本,会自动只补这些):');
  for (const l of failLog) console.log('  - ' + l);
}
