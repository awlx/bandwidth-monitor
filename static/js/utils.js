/* Shared formatting utilities and chart helpers.
 * Exports via window.BM for use by other modules. */
(function() {
    'use strict';
    var BM = window.BM = window.BM || {};

    BM.formatBytes = function(bytes, dec) {
        if (dec === undefined) dec = 1;
        if (bytes === 0) return '0 B';
        var k = 1024, s = ['B','KB','MB','GB','TB'];
        var i = Math.floor(Math.log(bytes) / Math.log(k));
        return (bytes / Math.pow(k, i)).toFixed(dec) + ' ' + s[i];
    };

    BM.formatRate = function(bps) {
        var mbps = (bps * 8) / 1e6;
        if (mbps < 0.01 && mbps > 0) return mbps.toFixed(4) + ' Mbit/s';
        if (mbps < 1) return mbps.toFixed(2) + ' Mbit/s';
        if (mbps < 100) return mbps.toFixed(1) + ' Mbit/s';
        return mbps.toFixed(0) + ' Mbit/s';
    };

    BM.formatPPS = function(pps) {
        if (pps === 0) return '0 pps';
        if (pps < 1000) return pps.toFixed(0) + ' pps';
        if (pps < 1e6) return (pps / 1000).toFixed(1) + ' Kpps';
        return (pps / 1e6).toFixed(1) + ' Mpps';
    };

    BM.formatUptime = function(secs) {
        if (!secs || secs <= 0) return '—';
        var d = Math.floor(secs / 86400);
        var h = Math.floor((secs % 86400) / 3600);
        var m = Math.floor((secs % 3600) / 60);
        if (d > 0) return d + 'd ' + h + 'h';
        if (h > 0) return h + 'h ' + m + 'm';
        return m + 'm';
    };

    BM.countryFlag = function(cc) {
        if (!cc || cc.length !== 2) return '';
        var a = cc.toUpperCase();
        return String.fromCodePoint(0x1F1E6 + a.charCodeAt(0) - 65, 0x1F1E6 + a.charCodeAt(1) - 65);
    };

    BM.rankClass = function(i) { return i === 0 ? 'rank rank-1' : 'rank'; };

    BM.escSvg = function(s) {
        return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    };

    /* Escapes a string for safe embedding as a single-quoted JS string
     * literal inside an inline onclick="..." HTML attribute, e.g.
     * onclick="fn('" + BM.escJs(name) + "')" . Escapes backslash/quote for
     * the JS string context, then HTML-escapes so the value can't break out
     * of the (double-quoted) attribute itself. */
    BM.escJs = function(s) {
        return String(s == null ? '' : s)
            .replace(/\\/g, '\\\\')
            .replace(/'/g, "\\'")
            .replace(/\n/g, '\\n')
            .replace(/\r/g, '')
            .replace(/&/g, '&amp;')
            .replace(/"/g, '&quot;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;');
    };

    /* Reusable SSE-style stream reader for POST endpoints that return
     * Server-Sent Events (e.g. speed test, traceroute, MTU discovery).
     * onDone is called only for a clean EOF; HTTP, fetch, and read failures
     * are passed to onError.
     * opts: { url, method, onMessage(parsed), onDone(), onError(err) } */
    BM.streamSSE = function(opts) {
        var method = opts.method || 'POST';
        return fetch(opts.url, { method: method }).then(function(resp) {
            if (!resp.ok) {
                var httpError = new Error(resp.status === 409 ? 'Already running' : 'HTTP ' + resp.status);
                httpError.status = resp.status;
                throw httpError;
            }
            if (!resp.body) throw new Error('Response body is not readable');

            var reader = resp.body.getReader();
            var decoder = new TextDecoder();
            var buffer = '';

            function processLines(lines) {
                for (var i = 0; i < lines.length; i++) {
                    var line = lines[i].trim();
                    if (!line.startsWith('data:')) continue;
                    try {
                        var p = JSON.parse(line.substring(5).trim());
                        if (opts.onMessage) opts.onMessage(p);
                    } catch(e) {}
                }
            }

            function processChunk() {
                return reader.read().then(function(result) {
                    if (result.done) {
                        buffer += decoder.decode();
                        if (buffer) processLines([buffer]);
                        return;
                    }
                    buffer += decoder.decode(result.value, { stream: true });
                    var lines = buffer.split('\n');
                    buffer = lines.pop();
                    processLines(lines);
                    return processChunk();
                });
            }

            return processChunk();
        }).then(function() {
            if (opts.onDone) opts.onDone();
        }).catch(function(e) {
            if (opts.onError) opts.onError(e);
        });
    };

    BM.describeStreamError = function(err) {
        if (err && err.message) return err.message;
        return String(err || 'Connection error');
    };

    // Doughnut chart helpers

    BM.doughnutColors = [
        '#22d3ee','#a78bfa','#34d399','#fb923c','#f472b6',
        '#60a5fa','#fbbf24','#e879f9','#4ade80','#f87171'
    ];

    BM.makeDoughnut = function(id) {
        var el = document.getElementById(id);
        if (!el) return null;
        return new Chart(el.getContext('2d'), {
            type: 'doughnut',
            data: { labels: [], datasets: [{ data: [], backgroundColor: BM.doughnutColors, borderWidth: 0 }] },
            options: {
                responsive: true, maintainAspectRatio: false, cutout: '65%',
                animation: { duration: 300 },
                plugins: {
                    legend: { display: false },
                    tooltip: {
                        backgroundColor: '#18181b', titleColor: '#fafafa', bodyColor: '#a1a1aa',
                        borderColor: '#27272a', borderWidth: 1, padding: 10, cornerRadius: 6
                    }
                }
            }
        });
    };

    BM.fillDoughnut = function(chart, legendEl, items, labelFn, valueFn, fmtFn) {
        if (!chart) return;
        if (!items || items.length === 0) {
            chart.data.labels = []; chart.data.datasets[0].data = []; chart.update('none');
            if (legendEl) legendEl.innerHTML = '<div class="empty-state">No data</div>';
            return;
        }
        var labels = [], vals = [];
        for (var i = 0; i < items.length; i++) {
            labels.push(labelFn(items[i]));
            vals.push(valueFn(items[i]));
        }
        chart.data.labels = labels;
        chart.data.datasets[0].data = vals;
        chart.update('none');
        if (!legendEl) return;
        var total = vals.reduce(function(s,v) { return s + v; }, 0) || 1;
        var lh = '';
        for (var i = 0; i < items.length; i++) {
            var pct = ((vals[i] / total) * 100).toFixed(1);
            lh += '<div class="breakdown-legend-item"><span class="breakdown-legend-swatch" style="background:' + BM.doughnutColors[i % BM.doughnutColors.length] + '"></span>';
            lh += '<div style="flex:1;min-width:0"><div style="font-size:12px;font-weight:500;color:var(--text-0);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' + labels[i] + '</div>';
            lh += '<div style="font-size:11px;color:var(--text-2)">' + fmtFn(vals[i]) + ' · ' + pct + '%</div></div></div>';
        }
        legendEl.innerHTML = lh;
    };

    BM.fillDetailTable = function(tbId, items, labelFn, valueFn, fmtFn, cls) {
        var tb = document.getElementById(tbId);
        if (!tb) return;
        if (!items || items.length === 0) { tb.innerHTML = '<tr><td colspan="4" class="empty-state">No data</td></tr>'; return; }
        var total = 0;
        for (var i = 0; i < items.length; i++) total += valueFn(items[i]);
        var maxVal = items.length > 0 ? valueFn(items[0]) : 1;
        var h = '';
        for (var i = 0; i < items.length; i++) {
            var v = valueFn(items[i]);
            var pct = maxVal > 0 ? ((v / maxVal) * 100).toFixed(1) : '0';
            h += '<tr><td>' + BM.rankClass(i) + '</td><td>' + labelFn(items[i]) + '</td>';
            h += '<td>' + fmtFn(v) + '</td>';
            h += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill ' + cls + '" style="width:' + pct + '%"></div></td></tr>';
        }
        tb.innerHTML = h;
    };

    BM.fillTrafficDetailTable = function(tbId, items, labelFn) {
        var tb = document.getElementById(tbId);
        if (!tb) return;
        if (!items || items.length === 0) { tb.innerHTML = '<tr><td colspan="5" class="empty-state">No data</td></tr>'; return; }
        var maxTotal = 1;
        for (var i = 0; i < items.length; i++) {
            var t = (items[i].tx_bytes || 0) + (items[i].rx_bytes || 0);
            if (t > maxTotal) maxTotal = t;
        }
        var h = '';
        for (var i = 0; i < items.length; i++) {
            var it = items[i]; var total = (it.tx_bytes||0) + (it.rx_bytes||0);
            var pct = maxTotal > 0 ? ((total / maxTotal) * 100).toFixed(1) : '0';
            h += '<tr><td>' + labelFn(it) + '</td><td>' + BM.formatBytes(it.rx_bytes||0) + '</td><td>' + BM.formatBytes(it.tx_bytes||0) + '</td>';
            h += '<td>' + BM.formatBytes(total) + '</td>';
            h += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill bw" style="width:' + pct + '%"></div></td></tr>';
        }
        tb.innerHTML = h;
    };

    BM.makeBarChart = function(canvasId, color) {
        var el = document.getElementById(canvasId);
        if (!el) return null;
        return new Chart(el.getContext('2d'), {
            type: 'bar',
            data: { labels: [], datasets: [{ data: [], backgroundColor: color, borderRadius: 2 }] },
            options: {
                responsive: true, maintainAspectRatio: false,
                animation: { duration: 300 },
                scales: { x: { display: false }, y: { display: false } },
                plugins: { legend: { display: false }, tooltip: { backgroundColor: '#18181b', titleColor: '#fafafa', bodyColor: '#a1a1aa', borderColor: '#27272a', borderWidth: 1 } }
            }
        });
    };

    /* niceScale — compute human-friendly axis tick values. */
    BM.niceScale = function(lo, hi, maxTicks) {
        if (hi <= lo) hi = lo + 1;
        var range = hi - lo;
        var rough = range / (maxTicks || 5);
        var mag = Math.pow(10, Math.floor(Math.log10(rough)));
        var residual = rough / mag;
        var step;
        if (residual <= 1.5) step = mag;
        else if (residual <= 3) step = 2 * mag;
        else if (residual <= 7) step = 5 * mag;
        else step = 10 * mag;
        var niceMin = Math.floor(lo / step) * step;
        var niceMax = Math.ceil(hi / step) * step;
        var ticks = [];
        for (var v = niceMin; v <= niceMax + step * 0.5; v += step) ticks.push(Math.round(v * 1e6) / 1e6);
        return { min: niceMin, max: niceMax, step: step, ticks: ticks };
    };

    // ── URL hash state ──
    // The hash carries the active tab and, on tabs with per-view state,
    // query-style params after a "?": e.g. "#traffic?iface=eth0&hist=eth0".
    // Splitting on "?" keeps tab routing working while letting a tab persist
    // its selected interface across reloads.
    //
    // These live in utils.js (not core.js) because scripts load with `defer`
    // and so execute in document order — utils.js first, tab modules next,
    // core.js last. A tab module that reads its saved selection at parse time
    // (e.g. traffic.js initialising selectedIface) needs these defined already,
    // which they wouldn't be if they lived in core.js.
    BM._hashTab = function() {
        return location.hash.replace(/^#/, '').split('?')[0];
    };
    BM._hashParams = function() {
        return new URLSearchParams(location.hash.split('?')[1] || '');
    };
    // _setHashParam updates one param without disturbing the tab or other
    // params. Pass null/'' to remove it. Uses replaceState so it doesn't spam
    // history or fire hashchange.
    BM._setHashParam = function(key, value) {
        var params = BM._hashParams();
        if (value === null || value === undefined || value === '') {
            params.delete(key);
        } else {
            params.set(key, value);
        }
        var tab = BM._hashTab() || BM._activeTab || 'traffic';
        var qs = params.toString();
        var newHash = '#' + tab + (qs ? '?' + qs : '');
        if (history.replaceState) {
            history.replaceState(null, '', newHash);
        } else {
            location.hash = newHash;
        }
    };
})();
