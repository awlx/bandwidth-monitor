#!/usr/bin/env node
'use strict';

var assert = require('assert');
var fs = require('fs');
var path = require('path');
var vm = require('vm');

var elements = {};
var context = {
    window: {},
    document: {
        getElementById: function(id) { return elements[id] || null; }
    },
    location: { hash: '' },
    history: { replaceState: function() {} },
    URLSearchParams: URLSearchParams,
    TextDecoder: TextDecoder,
    fetch: function() { throw new Error('unexpected fetch'); },
    Chart: function() {}
};
context.window.window = context.window;
context.window.location = context.location;
context.window.history = context.history;

var source = fs.readFileSync(path.join(__dirname, 'utils.js'), 'utf8');
vm.runInNewContext(source, context, { filename: 'utils.js' });
var BM = context.window.BM;

var hostile = '<img src=x onerror="boom()">\'&';
assert.strictEqual(
    BM.escHtml(hostile),
    '&lt;img src=x onerror=&quot;boom()&quot;&gt;&#39;&amp;'
);
assert.strictEqual(BM.escAttr(hostile), BM.escHtml(hostile));
assert.strictEqual(BM.escSvg(hostile), BM.escHtml(hostile));
assert.strictEqual(BM.escHtml(null), '');
assert.strictEqual(BM.integer(hostile, 7), 7);
assert.strictEqual(BM.formatCount(hostile), '0');
assert.ok(BM.escJs("x');boom();//\u2028").includes("\\'"));
assert.ok(BM.escJs("x');boom();//\u2028").includes('\\u2028'));

var legend = { innerHTML: '' };
var chart = {
    data: { labels: [], datasets: [{ data: [] }] },
    update: function() {}
};
BM.fillDoughnut(chart, legend, [{ label: hostile, value: 1 }],
    function(item) { return item.label; },
    function(item) { return item.value; },
    function(value) { return String(value); }
);
assert.ok(legend.innerHTML.includes('&lt;img'));
assert.ok(!legend.innerHTML.includes('<img'));

elements.hostileTable = { innerHTML: '' };
BM.fillDetailTable('hostileTable', [{ label: hostile, value: 1 }],
    function(item) { return item.label; },
    function(item) { return item.value; },
    function(value) { return String(value); },
    'bw'
);
assert.ok(elements.hostileTable.innerHTML.includes('&lt;img'));
assert.ok(!elements.hostileTable.innerHTML.includes('<img'));

console.log('frontend escaping tests passed');
