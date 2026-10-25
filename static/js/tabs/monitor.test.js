#!/usr/bin/env node
'use strict';

var assert = require('assert');
var fs = require('fs');
var path = require('path');
var vm = require('vm');

function el() {
    return { style: {}, innerHTML: '', addEventListener: function() {}, querySelector: function() { return null; } };
}
var elements = {};
['latencySection', 'latencyTargets', 'latencyWANCompare', 'captureHealthSection', 'captureHealth'].forEach(function(id) {
    elements[id] = el();
});
var context = {
    window: {},
    document: {
        getElementById: function(id) { return elements[id] || null; },
        addEventListener: function() {},
        querySelector: function() { return null; },
        querySelectorAll: function() { return []; }
    },
    location: { hash: '' },
    history: { replaceState: function() {} },
    URLSearchParams: URLSearchParams,
    TextDecoder: TextDecoder,
    setTimeout: setTimeout,
    fetch: function() { throw new Error('unexpected fetch'); },
    Chart: function() {}
};
context.window.window = context.window;
context.window.document = context.document;
vm.createContext(context);
['../utils.js', 'monitor.js'].forEach(function(f) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, f), 'utf8'), context, { filename: f });
});
var BM = context.window.BM;

// Capture health: hidden when unsupported, statuses rendered, names escaped.
BM.updateCaptureHealth(undefined);
assert.strictEqual(elements.captureHealthSection.style.display, 'none');
BM.updateCaptureHealth([]);
assert.strictEqual(elements.captureHealthSection.style.display, 'none');
BM.updateCaptureHealth([
    { device: 'eth0', packets_per_sec: 1000, drops_per_sec: 0, drop_ratio: 0, status: 'ok' },
    { device: 'eth1', packets_per_sec: 998, drops_per_sec: 2, drop_ratio: 0.002, status: 'warn' },
    { device: '<b>x', packets_per_sec: 980, drops_per_sec: 20, drop_ratio: 0.02, status: 'alert' }
]);
var ch = elements.captureHealth.innerHTML;
assert.strictEqual(elements.captureHealthSection.style.display, '');
assert.ok(ch.indexOf('0.200%') >= 0 && ch.indexOf('2.000%') >= 0, ch);
assert.ok(ch.indexOf('WARN') >= 0 && ch.indexOf('ALERT') >= 0 && ch.indexOf('OK') >= 0, ch);
assert.ok(ch.indexOf('<b>x') < 0, 'device name must be escaped');

function target(name, wan, avg, loss) {
    return { name: name, wan: wan, alive: true, ipv4: '192.0.2.1',
        icmp_stats: { rtt_ms: avg, avg_rtt_ms: avg, p95_rtt_ms: avg, p99_rtt_ms: avg, jitter_ms: 0.1, loss_pct: loss } };
}

// Single WAN: no comparison table and no path badges (unchanged output).
BM.updateLatency([target('a.example', undefined, 10, 0)]);
assert.strictEqual(elements.latencyWANCompare.style.display, 'none');
assert.ok(elements.latencyTargets.innerHTML.indexOf('default route') < 0);

// Multi-WAN: target × path table and per-card path badges.
BM.updateLatency([
    target('a.example', undefined, 10, 0),
    target('a.example', 'wanA', 10, 0),
    target('a.example', 'wanB', 25, 2.5)
]);
var cmp = elements.latencyWANCompare.innerHTML;
assert.strictEqual(elements.latencyWANCompare.style.display, '');
assert.ok(cmp.indexOf('wanA') >= 0 && cmp.indexOf('wanB') >= 0 && cmp.indexOf('default route') >= 0, cmp);
assert.ok(cmp.indexOf('25.0 ms') >= 0 && cmp.indexOf('2.5%') >= 0, cmp);
assert.strictEqual((cmp.match(/<tr/g) || []).length, 2, 'one header + one row per target');
assert.ok(elements.latencyTargets.innerHTML.indexOf('via wanB') >= 0);

// A WAN not routable for host traffic is not rendered at all.
var unr = { name: 'a.example', wan: 'wanC', alive: false, ipv4: '192.0.2.1', unroutable: ['ipv4'] };
BM.updateLatency([target('a.example', undefined, 10, 0), target('a.example', 'wanA', 10, 0), unr]);
cmp = elements.latencyWANCompare.innerHTML;
assert.ok(cmp.indexOf('wanC') < 0 && cmp.indexOf('routable') < 0, cmp);
assert.strictEqual((cmp.match(/<th/g) || []).length, 3, 'label column + two routable paths: ' + cmp);
var cards = elements.latencyTargets.innerHTML;
assert.ok(cards.indexOf('wanC') < 0 && cards.indexOf('routable') < 0, cards);
assert.strictEqual((cards.match(/DOWN/g) || []).length, 0, 'unroutable WAN must not show DOWN');

// Only an unroutable WAN beside one routable path: single-path view, no comparison table.
var dw = target('a.example', 'wanA', 10, 0); dw.default = true;
BM.updateLatency([dw, unr]);
assert.strictEqual(elements.latencyWANCompare.style.display, '');
BM.updateLatency([unr]);
assert.strictEqual(elements.latencySection.style.display, 'none');

// Default route carried by a WAN: one column/card labelled "default route (<wan>)".
var dflt = target('a.example', 'wanA', 10, 0); dflt.default = true;
BM.updateLatency([dflt, target('a.example', 'wanB', 25, 0)]);
cmp = elements.latencyWANCompare.innerHTML;
assert.ok(cmp.indexOf('default route (wanA)') >= 0, cmp);
assert.strictEqual((cmp.match(/<th/g) || []).length, 3, 'label column + two paths: ' + cmp);
cards = elements.latencyTargets.innerHTML;
assert.ok(cards.indexOf('default route (wanA)') >= 0 && cards.indexOf('via wanB') >= 0, cards);

console.log('monitor.test.js: ok');
