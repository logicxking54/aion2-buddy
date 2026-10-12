const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const { parse, compileScript, compileTemplate } = require('@vue/compiler-sfc');
const root = path.resolve(__dirname, '..');
const read = file => fs.readFileSync(path.join(root, file), 'utf8');

test('app contains only Ping Maker and its shared capture controls', () => {
  const app = read('src/App.vue');
  assert.match(app, /<PingMaker\s*\/>/);
  assert.match(app, /<CaptureBar\s*\/>/);
  assert.doesNotMatch(app, /Sidebar|ChangelogModal|projects\/registry|activeId|devMode/);
  const screen = read('src/projects/pingmaker/PingMaker.vue');
  assert.doesNotMatch(screen, /LatencyLab|RotationLab|activeView|role="tab"/);
  assert.doesNotMatch(read('src/components/CaptureBar.vue'), /setAnimMask|setCasterMask|devMode/);
  assert.doesNotMatch(read('src/captureController.ts'), /mod:log/);
});

test('removed feature screens and bridge wrappers are absent', () => {
  for (const file of ['projects/mod/Mod.vue', 'projects/inspector/Inspector.vue', 'projects/settings/Settings.vue', 'projects/pingmaker/LatencyLab.vue', 'projects/pingmaker/RotationLab.vue']) {
    assert.equal(fs.existsSync(path.join(root, 'src', file)), false, file);
  }
  assert.doesNotMatch(read('src/projects/pingmaker/capture.ts'), /Latency|Rotation|Optimizer|SetInspect|SetDecode|SetSessionRecord|SetCasterMask|SetAnimMask|SetStatSpeed/);
});

test('remaining Vue screens compile', () => {
  for (const file of ['src/App.vue', 'src/projects/pingmaker/PingMaker.vue', 'src/components/CaptureBar.vue']) {
    const { descriptor, errors } = parse(read(file), { filename: file });
    assert.deepEqual(errors, []);
    const script = compileScript(descriptor, { id: file });
    const template = compileTemplate({ source: descriptor.template.content, filename: file, id: file, compilerOptions: { bindingMetadata: script.bindings } });
    assert.deepEqual(template.errors, []);
  }
});

test('legacy config keeps skill values without reviving removed features', async () => {
  const saved = {
    language: 'en', character: 'Maliwan', defaultSpeed: 220, panelHeight: 245,
    casterRecord: 999, devMode: true, animMask: true, casterMask: true,
    rows: [{ id: 'hellfire-max', speedPct: 145, overridden: true, brk: false }],
    tierMode: true, tierTargetPct: 85, tierThresholds: [51.2, 85],
    latencyRuns: [{ id: 'old-run' }], optimizerConfigPath: 'F:/example/config.txt',
  };
  // Tier Mode settings from an older version must stay inert after removal.
  let written;
  let pendingSave;
  const exports = {};
  const context = {
    exports, require, console,
    localStorage: { getItem: () => null },
    window: { go: { main: { App: { LoadConfig: async () => JSON.stringify(saved), SaveConfig: async value => { written = JSON.parse(value); } } } } },
    setTimeout: callback => { pendingSave = callback; return 1; },
    clearTimeout: () => {},
  };
  const js = ts.transpileModule(read('src/config.ts'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
  vm.runInNewContext(js, context, { filename: 'config.ts' });
  await exports.loadConfig();
  const state = JSON.parse(JSON.stringify(exports.config));
  assert.deepEqual(state.rows, saved.rows);
  assert.equal(state.character, saved.character);
  assert.equal(state.defaultSpeed, saved.defaultSpeed);
  assert.equal(state.language, saved.language);
  assert.equal(state.panelHeight, saved.panelHeight);
  assert.equal(state.casterRecord, 0);
  for (const key of ['devMode', 'animMask', 'casterMask', 'tierMode', 'tierTargetPct', 'tierThresholds', 'latencyRuns', 'optimizerConfigPath']) {
    assert.equal(key in state, false, `${key} must not enter live config`);
  }
  exports.config.rows[0].speedPct = 150;
  exports.saveConfig();
  pendingSave();
  assert.equal(written.rows[0].speedPct, 150);
  assert.equal(written.tierMode, true, 'preserve removed tier settings as inert data for rollback');
  assert.deepEqual(written.tierThresholds, [51.2, 85]);
  assert.equal('casterRecord' in written, false);
  assert.deepEqual(written.latencyRuns, saved.latencyRuns, 'preserve legacy results as inert data for rollback');
});
