// Static UI bundle regressions: no browser and no network required.
// The management UI is a multi-file bundle (index.html + css/js under
// internal/monitor/assets) embedded with go:embed, so the cheapest useful gate
// is structural: every referenced file exists, every page the router knows has
// a renderer, and every inline handler points at a function the bundle defines.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';

const root = 'internal/monitor/assets';
const html = readFileSync(join(root, 'index.html'), 'utf8');

function listAssets(dir, base = '') {
  const out = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const rel = base ? `${base}/${entry.name}` : entry.name;
    if (entry.isDirectory()) out.push(...listAssets(join(dir, entry.name), rel));
    else out.push(rel);
  }
  return out;
}

const assets = listAssets(root);
const jsFiles = assets.filter(name => name.endsWith('.js'));
const jsSource = jsFiles.map(name => readFileSync(join(root, name), 'utf8')).join('\n');
const cssFiles = assets.filter(name => name.endsWith('.css'));

test('every asset referenced by index.html exists in the embedded bundle', () => {
  const refs = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+)"/g)].map(m => m[1]);
  assert.ok(refs.length >= 8, `expected the bundled css/js, found ${refs.length}`);
  for (const ref of refs) {
    const rel = ref.slice('assets/'.length);
    assert.ok(assets.includes(rel), `missing embedded asset: ${ref}`);
  }
  assert.ok(assets.includes('index.html'));
  assert.ok(assets.includes('favicon.svg'));
  assert.ok(cssFiles.length >= 1 && jsFiles.length >= 1);
});

test('every page in the router has a renderer defined', () => {
  const navBlock = jsSource.match(/const pages=\[([\s\S]*?)\];/);
  assert.ok(navBlock, 'navigation table not found');
  const ids = [...navBlock[1].matchAll(/\['([a-z]+)'/g)].map(m => m[1]);
  assert.ok(ids.includes('dashboard') && ids.includes('settings'));
  assert.ok(ids.includes('sessions'), 'session routing page must stay in the navigation');
  for (const id of ids) {
    const renderer = id === 'pool' || id === 'nodes' || id === 'failed' ? 'renderManaged' : `render${id[0].toUpperCase()}${id.slice(1)}`;
    assert.ok(new RegExp(`function ${renderer}\\(`).test(jsSource), `missing ${renderer} for page "${id}"`);
  }
  for (const loader of ['loadSessions', 'loadSettings', 'loadManaged', 'loadPorts']) {
    assert.ok(new RegExp(`(async )?function ${loader}\\(`).test(jsSource), `missing ${loader}`);
  }
});

test('inline handlers only reference functions the bundle defines', () => {
  const named = new Set([...jsSource.matchAll(/function\s+([A-Za-z_$][\w$]*)\s*\(/g)].map(m => m[1]));
  const assigned = new Set([
    ...jsSource.matchAll(/(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\(|function)/g),
    ...jsSource.matchAll(/(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)?\s*=>/g),
  ].map(m => m[1]));
  const handlers = [...html.matchAll(/on(?:click|change|input|submit)="([A-Za-z_$][\w$]*)\(/g)].map(m => m[1]);
  const inline = [...jsSource.matchAll(/on(?:click|change|input)="([A-Za-z_$][\w$]*)\(/g)].map(m => m[1]);
  // Inline attributes may also carry plain JS expressions (onchange="if(...)").
  const globals = new Set(['confirm', 'alert', 'prompt', 'if', 'for', 'while', 'return', 'typeof', 'new', 'void', 'delete']);
  for (const name of [...handlers, ...inline]) {
    if (globals.has(name)) continue;
    assert.ok(named.has(name) || assigned.has(name), `undefined handler: ${name}()`);
  }
});

test('session page keeps the management API contract', () => {
  const sessions = readFileSync(join(root, 'js/pages/sessions.js'), 'utf8');
  assert.match(sessions, /\/api\/proxy-sessions'/);
  assert.match(sessions, /\/api\/proxy-sessions\/links/);
  assert.match(sessions, /method:\s*'DELETE'/);
});

test('pool mode matches backend sequential spelling', () => {
  assert.match(jsSource, /'sequential'|"sequential"/);
  assert.doesNotMatch(jsSource, /round-robin/);
});
