(function() {
    'use strict';
    var BM = window.BM;

    var chartColors = [
        { rx: '#22d3ee', tx: '#a78bfa', rxBg: 'rgba(34,211,238,0.08)', txBg: 'rgba(167,139,250,0.08)' },
        { rx: '#34d399', tx: '#fb923c', rxBg: 'rgba(52,211,153,0.08)', txBg: 'rgba(251,146,60,0.08)' },
        { rx: '#60a5fa', tx: '#f472b6', rxBg: 'rgba(96,165,250,0.08)', txBg: 'rgba(244,114,182,0.08)' },
        { rx: '#fbbf24', tx: '#e879f9', rxBg: 'rgba(251,191,36,0.08)', txBg: 'rgba(232,121,249,0.08)' },
    ];

    var protoColors = { 'TCP': '#3b82f6', 'UDP': '#22d3ee', 'ICMP': '#f59e0b', 'Other': '#71717a' };
    var geoChartPalette = ['#3b82f6','#22d3ee','#a78bfa','#34d399','#f59e0b','#f472b6','#60a5fa','#e879f9','#fb923c','#4ade80','#818cf8','#fbbf24','#c084fc','#2dd4bf','#f87171','#71717a'];

    // ── Live Traffic Chart ──
    var ctx = document.getElementById('trafficChart').getContext('2d');
    var zeroLinePlugin = {
        id: 'zeroLine',
        afterDraw: function(chart) {
            if (chart.canvas.id !== 'trafficChart') return;
            var yScale = chart.scales.y;
            if (!yScale) return;
            var y = yScale.getPixelForValue(0);
            if (y < yScale.top || y > yScale.bottom) return;
            var ctx = chart.ctx;
            ctx.save();
            ctx.beginPath();
            ctx.moveTo(chart.chartArea.left, y);
            ctx.lineTo(chart.chartArea.right, y);
            ctx.lineWidth = 1;
            ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue('--text-2').trim() || '#71717a';
            ctx.setLineDash([4, 3]);
            ctx.stroke();
            ctx.restore();
        }
    };

    var trafficChart = new Chart(ctx, {
        type: 'line',
        data: { datasets: [] },
        plugins: [zeroLinePlugin],
        options: {
            responsive: true,
            maintainAspectRatio: false,
            animation: { duration: 300 },
            interaction: { mode: 'index', intersect: false },
            plugins: {
                legend: {
                    labels: { color: '#71717a', font: { size: 11, family: 'Inter' }, boxWidth: 12, padding: 16 }
                },
                tooltip: {
                    backgroundColor: '#18181b',
                    titleColor: '#fafafa',
                    bodyColor: '#a1a1aa',
                    borderColor: '#27272a',
                    borderWidth: 1,
                    padding: 10,
                    cornerRadius: 6,
                    titleFont: { size: 12 },
                    bodyFont: { size: 12, family: 'JetBrains Mono' },
                    callbacks: {
                        label: function(c) { return c.dataset.label + ': ' + BM.formatRate(Math.abs(c.raw.y)); }
                    }
                }
            },
            scales: {
                x: {
                    type: 'time',
                    time: { unit: 'minute', displayFormats: { minute: 'HH:mm' } },
                    min: function() { return Date.now() - 3600000; },
                    max: function() { return Date.now(); },
                    grid: { color: '#1f1f23' },
                    ticks: { color: '#52525b', font: { size: 10 }, maxTicksLimit: 12, source: 'auto' },
                    border: { color: '#27272a' }
                },
                y: {
                    grid: { color: '#1f1f23' },
                    ticks: { color: '#52525b', font: { size: 10 }, callback: function(v) { return BM.formatRate(Math.abs(v)); } },
                    border: { color: '#27272a' },
                    grace: '15%'
                }
            }
        }
    });

    // ── 24h History Chart ──
    var historyCtx = document.getElementById('historyChart').getContext('2d');
    var historyChart = new Chart(historyCtx, {
        type: 'line',
        data: { datasets: [] },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            animation: false,
            interaction: { mode: 'index', intersect: false },
            plugins: {
                legend: {
                    labels: { color: '#71717a', font: { size: 11, family: 'Inter' }, boxWidth: 12, padding: 16 }
                },
                tooltip: {
                    backgroundColor: '#18181b', titleColor: '#fafafa', bodyColor: '#a1a1aa',
                    borderColor: '#27272a', borderWidth: 1, padding: 10, cornerRadius: 6,
                    callbacks: {
                        label: function(c) { return c.dataset.label + ': ' + BM.formatRate(Math.abs(c.raw.y)); }
                    }
                }
            },
            scales: {
                x: {
                    type: 'time',
                    time: { unit: 'hour', displayFormats: { hour: 'HH:mm' }, tooltipFormat: 'HH:mm:ss' },
                    min: function() { return Date.now() - 86400000; },
                    max: function() { return Date.now(); },
                    grid: { color: '#1f1f23' },
                    ticks: { color: '#52525b', font: { size: 10 }, maxTicksLimit: 12, source: 'auto' },
                    border: { color: '#27272a' }
                },
                y: {
                    grid: { color: '#1f1f23' },
                    ticks: { color: '#52525b', font: { size: 10 }, callback: function(v) { return BM.formatRate(Math.abs(v)); } },
                    border: { color: '#27272a' },
                    grace: '10%'
                }
            }
        }
    });

    BM._historyLoaded = false;
    var _historyRefreshInterval = null;
    var _historyData = {};            // accumulated /api/interfaces/history points, pruned to 24h
    var _lastHistoryTs = 0;           // newest point seen; refreshes fetch ?since= from here
    // null = All. Restored from the URL hash so a reload keeps the selection.
    var selectedHistoryIface = (BM._hashParams && BM._hashParams().get('hist')) || null;

    // Build the 24h chart datasets from _historyData, honouring the interface
    // toggle. Mirrors BM.updateChart for the Live chart: a single selected
    // interface is drawn alone (and recolours to the first palette slot), while
    // the per-series RX/TX legend toggles keep working on top.
    function _renderHistoryChart() {
        var names = Object.keys(_historyData).sort();
        // Only clear a hash-restored selection once data has actually loaded and
        // the interface genuinely isn't present — not during the initial empty
        // window, which would wipe a valid selection before its points arrive.
        if (selectedHistoryIface && names.length && names.indexOf(selectedHistoryIface) === -1) {
            selectedHistoryIface = null;
            if (BM._setHashParam) BM._setHashParam('hist', null);
        }
        var list = selectedHistoryIface ? [selectedHistoryIface] : names;
        var ds = [], ci = 0;
        for (var ni = 0; ni < list.length; ni++) {
            var name = list[ni];
            var pts = _historyData[name];
            if (!pts || !pts.length) continue;
            var c = chartColors[ci % chartColors.length];
            var rxData = [], txData = [];
            for (var pi = 0; pi < pts.length; pi++) {
                var t = new Date(pts[pi].t);
                rxData.push({ x: t, y: pts[pi].rx || 0 });
                txData.push({ x: t, y: -(pts[pi].tx || 0) });
            }
            ds.push({ label: name + ' RX', data: rxData, borderColor: c.rx, backgroundColor: 'transparent', fill: false, tension: 0.3, cubicInterpolationMode: 'monotone', pointRadius: 0, borderWidth: 1.5 });
            ds.push({ label: name + ' TX', data: txData, borderColor: c.tx, backgroundColor: 'transparent', fill: false, tension: 0.3, cubicInterpolationMode: 'monotone', pointRadius: 0, borderWidth: 1.5 });
            ci++;
        }
        historyChart.data.datasets = ds;
        historyChart.update('none');
    }

    // Interface toggle row for the 24h chart, mirroring BM.renderIfaceTabs.
    function _renderHistoryIfaceTabs() {
        var el = document.getElementById('historyIfaceTabs');
        if (!el) return;
        var names = Object.keys(_historyData).sort();
        var h = '<div class="iface-tab' + (selectedHistoryIface === null ? ' active' : '') + '" onclick="window._shi(null)">All</div>';
        for (var i = 0; i < names.length; i++) {
            var n = names[i];
            h += '<div class="iface-tab' + (selectedHistoryIface === n ? ' active' : '') + '" onclick="window._shi(\'' + n + '\')">' + n + '</div>';
        }
        el.innerHTML = h;
    }

    window._shi = function(n) { selectedHistoryIface = n; if (BM._setHashParam) BM._setHashParam('hist', n); _renderHistoryIfaceTabs(); _renderHistoryChart(); };

    // First call fetches the full 24h history; refreshes fetch only ?since= the newest point
    // already held and merge, so the periodic refresh parses a few KB instead of the multi-MB
    // full payload. The t > _lastHistoryTs guard both dedupes and absorbs servers that predate
    // the since param (they ignore it and return everything).
    function _fetchInterfaceHistory() {
        var url = '/api/interfaces/history' + (_lastHistoryTs ? '?since=' + (_lastHistoryTs + 1) : '');
        fetch(url).then(function(r) { return r.json(); }).then(function(data) {
            data = data || {};
            var cutoff = Date.now() - 24 * 3600 * 1000;
            var newest = _lastHistoryTs;
            var dataNames = Object.keys(data);
            for (var di = 0; di < dataNames.length; di++) {
                var dn = dataNames[di];
                var incoming = data[dn];
                if (!incoming || !incoming.length) continue;
                var cur = _historyData[dn] || (_historyData[dn] = []);
                for (var ii = 0; ii < incoming.length; ii++) {
                    var p = incoming[ii];
                    if (p.t > _lastHistoryTs) cur.push(p);
                    if (p.t > newest) newest = p.t;
                }
            }
            _lastHistoryTs = newest;
            var histNames = Object.keys(_historyData);
            for (var hi = 0; hi < histNames.length; hi++) {
                var pts = _historyData[histNames[hi]];
                var drop = 0;
                while (drop < pts.length && pts[drop].t < cutoff) drop++;
                if (drop) _historyData[histNames[hi]] = pts.slice(drop);
            }
            _renderHistoryIfaceTabs();
            _renderHistoryChart();
            // Subtitle reflects overall collection age, independent of the toggle.
            var earliestTs = Infinity, names = Object.keys(_historyData);
            for (var i = 0; i < names.length; i++) {
                var pts = _historyData[names[i]];
                if (pts && pts.length && pts[0].t < earliestTs) earliestTs = pts[0].t;
            }
            var subEl = document.querySelector('#historyChart').closest('.card').querySelector('.card-subtitle');
            if (subEl && earliestTs < Infinity) {
                var ageMs = Date.now() - earliestTs;
                var ageH = Math.floor(ageMs / 3600000);
                var ageM = Math.floor((ageMs % 3600000) / 60000);
                var ageStr = ageH > 0 ? ageH + 'h ' + ageM + 'm' : ageM + 'm';
                subEl.textContent = ageH >= 24 ? 'Per-interface bandwidth over the last 24 hours' : 'Collecting data \u2014 ' + ageStr + ' of 24h available';
            }
        }).catch(function(e) { console.error('history load:', e); });
    }

    BM._loadInterfaceHistory = function() {
        if (BM._historyLoaded) return;
        BM._historyLoaded = true;
        _fetchInterfaceHistory();
        _historyRefreshInterval = setInterval(_fetchInterfaceHistory, 60000);
    };

    // Seed the Live Traffic chart's rolling buffer from server-stored history so
    // a tab reload, disconnect, or hibernate doesn't blank it out — the backend
    // keeps a 24h 1-sample/sec ring per interface. We take only the chart's
    // window (last MAX_PTS seconds) and rebuild BM.chartData; live SSE ticks then
    // continue appending from where history left off. Called on every (re)connect.
    BM._backfillLiveTraffic = function() {
        // Only the chart window is kept (see cutoff below), so ask the server for just that
        // slice; servers predating the since param ignore it and return everything.
        return fetch('/api/interfaces/history?since=' + (Date.now() - BM.MAX_PTS * 1000)).then(function(r) { return r.json(); }).then(function(data) {
            if (!data) return;
            var cutoff = Date.now() - BM.MAX_PTS * 1000;
            var names = Object.keys(data);
            for (var ni = 0; ni < names.length; ni++) {
                var name = names[ni];
                var pts = data[name];
                if (!pts || !pts.length) continue;
                BM.knownIfaces.add(name);
                var rx = [], tx = [];
                for (var pi = 0; pi < pts.length; pi++) {
                    if (pts[pi].t < cutoff) continue;
                    var t = new Date(pts[pi].t);
                    rx.push({ x: t, y: pts[pi].rx || 0 });
                    tx.push({ x: t, y: -(pts[pi].tx || 0) });
                }
                if (rx.length > BM.MAX_PTS) { rx = rx.slice(-BM.MAX_PTS); tx = tx.slice(-BM.MAX_PTS); }
                BM.chartData[name] = { rx: rx, tx: tx };
            }
            if (BM.renderIfaceTabs) BM.renderIfaceTabs();
            if (BM.updateChart) BM.updateChart();
        }).catch(function(e) { console.error('live backfill:', e); });
    };

    // ── Doughnut chart instances ──
    function makeDoughnut(id) {
        return new Chart(document.getElementById(id).getContext('2d'), {
            type: 'doughnut',
            data: { labels: [], datasets: [{ data: [], backgroundColor: [], borderWidth: 0 }] },
            options: {
                responsive: true, maintainAspectRatio: true, cutout: '65%', animation: false,
                plugins: {
                    legend: { display: false },
                    tooltip: {
                        backgroundColor: '#18181b', titleColor: '#fafafa', bodyColor: '#a1a1aa',
                        borderColor: '#27272a', borderWidth: 1,
                        callbacks: { label: function(c) { return c.label + ': ' + (c.raw >= 1024 ? BM.formatBytes(c.raw) : c.raw.toLocaleString()); } }
                    }
                }
            }
        });
    }

    var protoChart = makeDoughnut('protoChart');
    var countryChart = makeDoughnut('countryChart');
    var asnChart = makeDoughnut('asnChart');
    var ipvChart = makeDoughnut('ipvChart');

    // Store charts needed by other modules
    BM._protoChart = protoChart;
    BM._countryChart = countryChart;
    BM._asnChart = asnChart;
    BM._ipvChart = ipvChart;

    // Restore the selected interface from the URL hash so a reload keeps it.
    var selectedIface = (BM._hashParams && BM._hashParams().get('iface')) || null;
    var _yAxisMax = 0;
    var _yAxisSig = '';  // signature of the visible series; changes on interface toggle
    var _yAxisTick = 0;  // throttles the percentile recompute (see updateChart)

    BM.updateChart = function() {
        var ds = [], ci = 0;
        // Sort so the series order (and thus colour assignment) is stable across
        // reloads and matches the 24h chart, instead of following the order
        // interfaces happened to be discovered in.
        var list = selectedIface ? [selectedIface] : Array.from(BM.knownIfaces).sort();
        for (var n of list) {
            if (!BM.chartData[n]) continue;
            var c = chartColors[ci % chartColors.length];
            ds.push({ label: n + ' RX', data: BM.chartData[n].rx, borderColor: c.rx, backgroundColor: c.rxBg, fill: false, tension: 0.4, pointRadius: 0, borderWidth: 1.5 });
            ds.push({ label: n + ' TX', data: BM.chartData[n].tx, borderColor: c.tx, backgroundColor: c.txBg, fill: false, tension: 0.4, pointRadius: 0, borderWidth: 1.5 });
            ci++;
        }
        trafficChart.data.datasets = ds;
        // Scale the y-axis to a robust (99th-percentile) max of the *currently
        // visible* series across the whole window, so a single transient spike
        // clips at the top instead of blowing the scale up. Hard min/max — not
        // suggestedMax — makes that clamp hold. Sorting the whole window is too
        // costly to run every frame, so recompute at most every 10th tick — but
        // always immediately on an interface toggle, so the axis still drops
        // right away when a series is hidden.
        var sig = selectedIface === null ? '__all__' : selectedIface;
        var recompute = (sig !== _yAxisSig) || (_yAxisTick % 10 === 0);
        _yAxisTick++;
        if (recompute) {
            var vals = [];
            for (var di = 0; di < ds.length; di++) {
                var pts = ds[di].data;
                for (var pi = 0; pi < pts.length; pi++) {
                    var av = Math.abs(pts[pi].y);
                    if (av > 0) vals.push(av);
                }
            }
            var target = 0;
            if (vals.length) {
                vals.sort(function(a, b) { return a - b; });
                target = vals[Math.floor((vals.length - 1) * 0.99)] * 1.15;
            }
            if (sig !== _yAxisSig || target >= _yAxisMax) {
                _yAxisMax = target;                              // toggle or genuine rise → snap
            } else {
                _yAxisMax = Math.max(target, _yAxisMax * 0.9);   // ease down smoothly, no flicker
            }
            _yAxisSig = sig;
        }
        if (_yAxisMax > 0) {
            trafficChart.options.scales.y.max = _yAxisMax;
            trafficChart.options.scales.y.min = -_yAxisMax;
        } else {
            trafficChart.options.scales.y.max = undefined;
            trafficChart.options.scales.y.min = undefined;
        }
        trafficChart.update('none');
    };

    BM.renderIfaceTabs = function() {
        var el = document.getElementById('ifaceTabs');
        var h = '<div class="iface-tab' + (selectedIface === null ? ' active' : '') + '" onclick="window._si(null)">All</div>';
        // Sorted so the tab order is the same on every load and matches the 24h row.
        Array.from(BM.knownIfaces).sort().forEach(function(n) {
            h += '<div class="iface-tab' + (selectedIface === n ? ' active' : '') + '" onclick="window._si(\'' + n + '\')">' + n + '</div>';
        });
        el.innerHTML = h;
    };

    window._si = function(n) { selectedIface = n; if (BM._setHashParam) BM._setHashParam('iface', n); BM.renderIfaceTabs(); BM.updateChart(); };

    function classifyIface(f) {
        if (f.wan) return 'wan';
        if (f.iface_type) return f.iface_type;
        var n = (f.name || '').toLowerCase();
        if (/^(tun|tap|wg|ipsec|gre|vti|ovpn)/.test(n)) return 'vpn';
        if (/\.\d+$/.test(n) || /^vlan/.test(n)) return 'vlan';
        return 'physical';
    }

    var groupMeta = {
        wan:      { label: 'WAN', order: 0 },
        vpn:      { label: 'VPN', order: 1 },
        vlan:     { label: 'VLAN', order: 2 },
        physical: { label: 'Physical', order: 3 },
        loopback: { label: 'Loopback', order: 4 }
    };

    function renderSystemCard(ifaces, d) {
        var totalRxRate = 0, totalTxRate = 0, totalRxBytes = 0, totalTxBytes = 0;
        for (var f of ifaces) {
            totalRxRate += f.rx_rate || 0;
            totalTxRate += f.tx_rate || 0;
            totalRxBytes += f.rx_bytes || 0;
            totalTxBytes += f.tx_bytes || 0;
        }
        var el = document.getElementById('systemStats');
        var sub = document.getElementById('systemSubtitle');
        if (!el) return;
        function stat(label, value, cls) {
            return '<div style="padding:8px 4px"><div style="font-size:10px;color:var(--text-2);margin-bottom:4px">' + label + '</div><div style="font-size:15px;font-weight:700;font-variant-numeric:tabular-nums' + (cls ? ';color:var(--' + cls + ')' : '') + '">' + value + '</div></div>';
        }
        var h = '';
        h += stat('Uptime', d && d.uptime_secs ? BM.formatUptime(d.uptime_secs) : '—');
        if (d && d.load_avg) {
            h += stat('Load 1m', d.load_avg[0].toFixed(2));
            h += stat('Load 5m', d.load_avg[1].toFixed(2));
            h += stat('Load 15m', d.load_avg[2].toFixed(2));
        }
        h += stat('Processes', d && d.processes ? d.processes.running + ' / ' + d.processes.total : '—');
        h += stat('Bandwidth', BM.formatRate(totalRxRate + totalTxRate));
        h += stat('RX Rate', BM.formatRate(totalRxRate), 'rx');
        h += stat('TX Rate', BM.formatRate(totalTxRate), 'tx');
        h += stat('Total RX', BM.formatBytes(totalRxBytes), 'rx');
        h += stat('Total TX', BM.formatBytes(totalTxBytes), 'tx');
        h += stat('IPs (24h)', d && d.unique_ips ? (d.unique_ips).toLocaleString() : '—');
        el.innerHTML = h;
        if (sub && d && d.uptime_secs) {
            sub.textContent = ifaces.length + ' interfaces · up ' + BM.formatUptime(d.uptime_secs);
        }
    }

    BM.renderStatsRow = function(ifaces, d) {
        renderSystemCard(ifaces, d);
        var groups = {};
        for (var f of ifaces) {
            var g = classifyIface(f);
            if (!groups[g]) groups[g] = { rx: 0, tx: 0, count: 0 };
            groups[g].rx += f.rx_rate || 0;
            groups[g].tx += f.tx_rate || 0;
            groups[g].count++;
        }
        var keys = Object.keys(groups).sort(function(a, b) {
            return (groupMeta[a] ? groupMeta[a].order : 99) - (groupMeta[b] ? groupMeta[b].order : 99);
        });
        var h = '';
        for (var i = 0; i < keys.length; i++) {
            var k = keys[i], meta = groupMeta[k] || { label: k }, grp = groups[k];
            h += '<div class="stats-group">';
            h += '<div class="stats-group-header">' + meta.label + '<span>' + grp.count + '</span></div>';
            h += '<div class="stats-group-body">';
            h += '<div><div class="stat-mini-label">RX</div><div class="stat-mini-value rx">' + BM.formatRate(grp.rx) + '</div></div>';
            h += '<div><div class="stat-mini-label">TX</div><div class="stat-mini-value tx">' + BM.formatRate(grp.tx) + '</div></div>';
            h += '</div></div>';
        }
        document.getElementById('statsRow').innerHTML = h;
    };

    function renderIfaceCard(f, groupLabel) {
        var hasErr = (f.rx_errors || 0) + (f.tx_errors || 0) > 0;
        var hasDrop = (f.rx_dropped || 0) + (f.tx_dropped || 0) > 0;
        var os = f.oper_state || 'unknown';
        var dotClass = (os === 'up') ? 'up' : (os === 'down' ? 'down' : 'unknown');
        var stateLabel = os === 'up' ? 'Up' : (os === 'down' ? 'Down' : os);
        var speedLabel = (f.speed && f.speed > 0) ? '<span style="font-size:10px;color:var(--text-2);font-weight:400;margin-right:4px">' + (f.speed >= 1000 ? (f.speed / 1000) + ' Gbit' : f.speed + ' Mbit') + '</span>' : '';
        var badge = groupLabel ? '<span class="iface-group-badge">' + groupLabel + '</span>' : '';
        var h = '<div class="iface-card"><div class="iface-name"><span>' + f.name + ' ' + badge + '</span><span class="iface-status">' + speedLabel + '<span class="iface-status-dot ' + dotClass + '"></span>' + stateLabel + '</span></div>';
        h += '<div class="sparkline-wrap"><canvas class="sparkline-canvas" data-iface="' + f.name + '"></canvas></div>';
        if (f.vpn_routing) {
            h += '<div class="vpn-routing active"><span class="iface-status-dot up"></span>Routing' + (f.vpn_routing_since ? ' since ' + f.vpn_routing_since : '') + '</div>';
        } else if (f.iface_type === 'vpn' && f.vpn_tracked) {
            h += '<div class="vpn-routing inactive">Not routing</div>';
        }
        h += '<div class="iface-stats">';
        h += '<div><div class="iface-stat-label label-rx">RX Rate</div><div class="iface-stat-value" style="color:var(--rx)">' + BM.formatRate(f.rx_rate || 0) + '</div></div>';
        h += '<div><div class="iface-stat-label label-tx">TX Rate</div><div class="iface-stat-value" style="color:var(--tx)">' + BM.formatRate(f.tx_rate || 0) + '</div></div>';
        h += '<div><div class="iface-stat-label">RX Total</div><div class="iface-stat-value">' + BM.formatBytes(f.rx_bytes || 0) + '</div></div>';
        h += '<div><div class="iface-stat-label">TX Total</div><div class="iface-stat-value">' + BM.formatBytes(f.tx_bytes || 0) + '</div></div>';
        h += '<div><div class="iface-stat-label">RX Pkts</div><div class="iface-stat-value">' + BM.formatPPS(f.rx_pps || 0) + '</div></div>';
        h += '<div><div class="iface-stat-label">TX Pkts</div><div class="iface-stat-value">' + BM.formatPPS(f.tx_pps || 0) + '</div></div>';
        if (hasErr || (f.rx_error_rate || 0) + (f.tx_error_rate || 0) > 0) h += '<div><div class="iface-stat-label label-err">Errors</div><div class="iface-stat-value" style="color:var(--danger)">' + (f.rx_error_rate > 0 || f.tx_error_rate > 0 ? BM.formatPPS(f.rx_error_rate || 0) + ' / ' + BM.formatPPS(f.tx_error_rate || 0) + '/s' : f.rx_errors + ' / ' + f.tx_errors) + '</div></div>';
        if (hasDrop || (f.rx_drop_rate || 0) + (f.tx_drop_rate || 0) > 0) h += '<div><div class="iface-stat-label">Drops</div><div class="iface-stat-value" style="color:var(--warning)">' + (f.rx_drop_rate > 0 || f.tx_drop_rate > 0 ? BM.formatPPS(f.rx_drop_rate || 0) + ' / ' + BM.formatPPS(f.tx_drop_rate || 0) + '/s' : f.rx_dropped + ' / ' + f.tx_dropped) + '</div></div>';
        h += '</div>';
        if (f.addrs && f.addrs.length) {
            var v4 = [], v6 = [];
            for (var ai = 0; ai < f.addrs.length; ai++) {
                if (f.addrs[ai].indexOf(':') !== -1) v6.push(f.addrs[ai]); else v4.push(f.addrs[ai]);
            }
            h += '<div class="iface-addrs">';
            if (v4.length) h += '<div class="iface-addr-row"><span class="iface-addr-tag v4">IPv4</span><span class="iface-addr-list">' + v4.join(', ') + '</span></div>';
            if (v6.length) h += '<div class="iface-addr-row"><span class="iface-addr-tag v6">IPv6</span><span class="iface-addr-list">' + v6.join(', ') + '</span></div>';
            h += '</div>';
        }
        h += '</div>';
        return h;
    }

    BM.renderIfaceCards = function(ifaces) {
        var el = document.getElementById('ifaceGroups');
        if (!ifaces || !ifaces.length) { el.innerHTML = ''; return; }

        var groups = {};
        for (var f of ifaces) {
            var g = classifyIface(f);
            if (!groups[g]) groups[g] = [];
            groups[g].push(f);
        }

        for (var k in groups) {
            groups[k].sort(function(a, b) {
                if (a.name === 'lo') return 1;
                if (b.name === 'lo') return -1;
                return a.name.localeCompare(b.name);
            });
        }

        var keys = Object.keys(groups).sort(function(a, b) {
            return (groupMeta[a] ? groupMeta[a].order : 99) - (groupMeta[b] ? groupMeta[b].order : 99);
        });

        var h = '<div class="iface-cards">';
        for (var gi = 0; gi < keys.length; gi++) {
            var gk = keys[gi];
            var meta = groupMeta[gk] || { label: gk };
            for (var fi = 0; fi < groups[gk].length; fi++) {
                h += renderIfaceCard(groups[gk][fi], meta.label);
            }
        }
        h += '</div>';
        el.innerHTML = h;
    };

    BM.renderTalkers = function(tid, talkers, vk, fmt, cls) {
        var tb = document.getElementById(tid);
        if (!talkers || !talkers.length) { tb.innerHTML = '<tr><td colspan="5" class="empty-state">No data &mdash; requires root / CAP_NET_RAW</td></tr>'; return; }

        var hasDirection = false;
        for (var di = 0; di < talkers.length; di++) {
            if ((talkers[di].rx_bytes || 0) > 0 || (talkers[di].tx_bytes || 0) > 0) { hasDirection = true; break; }
        }

        var mx = talkers[0][vk] || 1, h = '';
        var isRate = (vk === 'rate_bytes');
        talkers.forEach(function(t, i) {
            var pct = ((t[vk] / mx) * 100).toFixed(1);
            var flag = t.country ? BM.countryFlag(t.country) + ' ' : '';
            var geo = '';
            var geoName = (t.city && t.country_name) ? t.city + ', ' + t.country_name : (t.country_name || '');
            if (t.as_org) geo = '<span class="hostname">' + flag + geoName + ' &middot; AS' + (t.asn || '') + ' ' + t.as_org + '</span>';
            else if (geoName) geo = '<span class="hostname">' + flag + geoName + '</span>';
            var host = t.hostname && t.hostname !== t.ip
                ? '<span class="ip-cell ip-clickable" data-ip="' + t.ip + '">' + t.ip + '</span><span class="hostname">' + t.hostname + '</span>' + geo
                : '<span class="ip-cell ip-clickable" data-ip="' + t.ip + '">' + t.ip + '</span>' + geo;
            h += '<tr><td><span class="' + BM.rankClass(i) + '">' + (i + 1) + '</span></td>';
            h += '<td>' + host + '</td>';
            if (hasDirection) {
                if (isRate) {
                    h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums;color:var(--rx)">' + BM.formatRate(t.rx_rate || 0) + '</td>';
                    h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums;color:var(--tx)">' + BM.formatRate(t.tx_rate || 0) + '</td>';
                } else {
                    h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums;color:var(--rx)">' + BM.formatBytes(t.rx_bytes || 0) + '</td>';
                    h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums;color:var(--tx)">' + BM.formatBytes(t.tx_bytes || 0) + '</td>';
                }
            }
            h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums">' + fmt(t[vk]) + '</td>';
            h += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill ' + cls + '" style="width:' + pct + '%"></div></td></tr>';
        });

        var thead = tb.parentElement.querySelector('thead tr');
        if (thead) {
            if (hasDirection) {
                thead.innerHTML = '<th>#</th><th>Host</th><th style="width:12%">RX</th><th style="width:12%">TX</th><th style="width:12%">Total</th><th style="width:18%"></th>';
            } else {
                thead.innerHTML = '<th>#</th><th>Host</th><th>' + (isRate ? 'Rate' : 'Total') + '</th><th style="width:28%"></th>';
            }
        }
        tb.innerHTML = h;
    };

    function drawSparkline(canvas, points) {
        if (!points || points.length < 2 || !canvas) return;
        var dpr = window.devicePixelRatio || 1;
        var dw = canvas.offsetWidth, dh = canvas.offsetHeight;
        if (!dw || !dh) return;
        canvas.width = dw * dpr; canvas.height = dh * dpr;
        var c = canvas.getContext('2d'); c.scale(dpr, dpr);
        var maxVal = 0;
        for (var i = 0; i < points.length; i++) { if (points[i].rx > maxVal) maxVal = points[i].rx; if (points[i].tx > maxVal) maxVal = points[i].tx; }
        if (maxVal === 0) return;
        var stepX = dw / (points.length - 1), pad = 2, usableH = dh - pad * 2;
        function getY(val) { return dh - pad - (val / maxVal) * usableH; }
        function drawArea(key, fill, stroke) {
            var pts = [];
            for (var j = 0; j < points.length; j++) pts.push({ x: j * stepX, y: getY(points[j][key]) });
            c.beginPath(); c.moveTo(0, dh);
            c.lineTo(pts[0].x, pts[0].y);
            for (var j = 1; j < pts.length; j++) {
                var cx = (pts[j-1].x + pts[j].x) / 2;
                c.bezierCurveTo(cx, pts[j-1].y, cx, pts[j].y, pts[j].x, pts[j].y);
            }
            c.lineTo(pts[pts.length-1].x, dh); c.closePath(); c.fillStyle = fill; c.fill();
            c.beginPath(); c.moveTo(pts[0].x, pts[0].y);
            for (var j = 1; j < pts.length; j++) {
                var cx = (pts[j-1].x + pts[j].x) / 2;
                c.bezierCurveTo(cx, pts[j-1].y, cx, pts[j].y, pts[j].x, pts[j].y);
            }
            c.strokeStyle = stroke; c.lineWidth = 1.5; c.stroke();
        }
        drawArea('tx', 'rgba(167,139,250,0.15)', 'rgba(167,139,250,0.5)');
        drawArea('rx', 'rgba(34,211,238,0.15)', 'rgba(34,211,238,0.5)');
    }

    BM.drawAllSparklines = function() {
        var els = document.querySelectorAll('.sparkline-canvas');
        for (var i = 0; i < els.length; i++) drawSparkline(els[i], BM.sparklineData[els[i].getAttribute('data-iface')]);
    };

    // ── Protocol / Country / ASN / IP version doughnut charts ──

    function updateSimpleDoughnut(chart, legendEl, labels, values, colors, formatter) {
        chart.data.labels = labels;
        chart.data.datasets[0].data = values;
        chart.data.datasets[0].backgroundColor = colors;
        chart.update('none');
        var total = 0;
        for (var i = 0; i < values.length; i++) total += values[i];
        var h = '';
        for (var i = 0; i < labels.length; i++) {
            var pct = total > 0 ? ((values[i] / total) * 100).toFixed(1) : '0.0';
            var displayVal = formatter ? formatter(values[i]) : values[i].toLocaleString();
            h += '<div style="display:flex;align-items:center;gap:10px;margin-bottom:10px">';
            h += '<div style="width:10px;height:10px;border-radius:2px;background:' + colors[i] + ';flex-shrink:0"></div>';
            h += '<div><div style="font-size:13px;font-weight:600;color:var(--text-0)">' + labels[i] + '</div>';
            h += '<div style="font-size:11px;color:var(--text-2)">' + displayVal + ' &middot; ' + pct + '%</div></div></div>';
        }
        legendEl.innerHTML = h || '<div style="text-align:center;color:var(--text-2);font-size:12px;padding:8px">No data</div>';
    }
    // Export for NAT tab
    BM._updateSimpleDoughnut = updateSimpleDoughnut;

    BM.updateProtoChart = function(data) {
        if (!data) return;
        var order = ['TCP', 'UDP', 'ICMP', 'Other'], labels = [], values = [], colors = [];
        for (var i = 0; i < order.length; i++) { if (data[order[i]]) { labels.push(order[i]); values.push(data[order[i]]); colors.push(protoColors[order[i]]); } }
        for (var k in data) { if (order.indexOf(k) === -1) { labels.push(k); values.push(data[k]); colors.push('#71717a'); } }
        updateSimpleDoughnut(protoChart, document.getElementById('protoLegend'), labels, values, colors, BM.formatBytes);
    };

    // Helper: populate a doughnut chart + legend used internally by traffic charts
    function fillDoughnut(chart, legendEl, items, labelFn, valueFn, fmtFn) {
        if (!items || !items.length) {
            chart.data.labels = []; chart.data.datasets[0].data = []; chart.update('none');
            legendEl.innerHTML = '<div style="text-align:center;color:var(--text-2);font-size:12px;padding:8px">No data</div>';
            return;
        }
        var chartMax = 8, chartLabels = [], chartValues = [], chartColors = [], rest = 0;
        var total = 0;
        for (var i = 0; i < items.length; i++) total += valueFn(items[i]);
        for (var i = 0; i < items.length; i++) {
            if (i < chartMax) {
                chartLabels.push(labelFn(items[i]));
                chartValues.push(valueFn(items[i]));
                chartColors.push(geoChartPalette[i % geoChartPalette.length]);
            } else { rest += valueFn(items[i]); }
        }
        if (rest > 0) { chartLabels.push('Others'); chartValues.push(rest); chartColors.push('#52525b'); }
        chart.data.labels = chartLabels;
        chart.data.datasets[0].data = chartValues;
        chart.data.datasets[0].backgroundColor = chartColors;
        chart.update('none');
        var lh = '', legendCount = Math.min(items.length, chartMax);
        for (var i = 0; i < legendCount; i++) {
            var pct = total > 0 ? ((valueFn(items[i]) / total) * 100).toFixed(1) : '0.0';
            lh += '<div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">';
            lh += '<div style="width:8px;height:8px;border-radius:2px;background:' + chartColors[i] + ';flex-shrink:0"></div>';
            lh += '<div style="flex:1;min-width:0"><div style="font-size:12px;font-weight:500;color:var(--text-0);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' + labelFn(items[i]) + '</div>';
            lh += '<div style="font-size:10px;color:var(--text-2)">' + fmtFn(valueFn(items[i])) + ' &middot; ' + pct + '%</div></div></div>';
        }
        if (rest > 0) {
            var rpct = total > 0 ? ((rest / total) * 100).toFixed(1) : '0.0';
            lh += '<div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">';
            lh += '<div style="width:8px;height:8px;border-radius:2px;background:#52525b;flex-shrink:0"></div>';
            lh += '<div style="flex:1"><div style="font-size:12px;font-weight:500;color:var(--text-0)">Others</div>';
            lh += '<div style="font-size:10px;color:var(--text-2)">' + fmtFn(rest) + ' &middot; ' + rpct + '%</div></div></div>';
        }
        legendEl.innerHTML = lh;
    }
    // Export for other tabs (dns, wifi, nat)
    BM._fillDoughnut = fillDoughnut;

    function fillDetailTable(tbId, items, labelFn, valueFn, fmtFn, cls) {
        var tb = document.getElementById(tbId);
        if (!items || !items.length) { tb.innerHTML = '<tr><td colspan="4" class="empty-state">No data</td></tr>'; return; }
        var mx = valueFn(items[0]) || 1, h = '';
        for (var i = 0; i < items.length; i++) {
            var pct = mx > 0 ? ((valueFn(items[i]) / mx) * 100).toFixed(1) : '0';
            h += '<tr><td><span class="' + BM.rankClass(i) + '">' + (i + 1) + '</span></td>';
            h += '<td><span class="ip-cell">' + labelFn(items[i]) + '</span></td>';
            h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums">' + fmtFn(valueFn(items[i])) + '</td>';
            h += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill ' + cls + '" style="width:' + pct + '%"></div></td></tr>';
        }
        tb.innerHTML = h;
    }
    BM._fillDetailTable = fillDetailTable;

    function fillTrafficDetailTable(tbId, items, labelFn) {
        var tb = document.getElementById(tbId);
        if (!items || !items.length) { tb.innerHTML = '<tr><td colspan="7" class="empty-state">No data</td></tr>'; return; }
        var mx = 1;
        for (var i = 0; i < items.length; i++) { var t = (items[i].tx_bytes || 0) + (items[i].rx_bytes || 0); if (t > mx) mx = t; }
        var h = '';
        for (var i = 0; i < items.length; i++) {
            var it = items[i], total = (it.tx_bytes || 0) + (it.rx_bytes || 0);
            var pct = mx > 0 ? ((total / mx) * 100).toFixed(1) : '0';
            h += '<tr><td><span class="' + BM.rankClass(i) + '">' + (i + 1) + '</span></td>';
            h += '<td><span class="ip-cell">' + labelFn(it) + '</span></td>';
            h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums">' + BM.formatBytes(it.rx_bytes || 0) + '</td>';
            h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums">' + BM.formatBytes(it.tx_bytes || 0) + '</td>';
            h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums;color:var(--rx)">' + BM.formatRate(it.rx_rate || 0) + '</td>';
            h += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums;color:var(--tx)">' + BM.formatRate(it.tx_rate || 0) + '</td>';
            h += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill bw" style="width:' + pct + '%"></div></td></tr>';
        }
        tb.innerHTML = h;
    }
    BM._fillTrafficDetailTable = fillTrafficDetailTable;

    BM.updateCountries = function(countries) {
        var tb = document.getElementById('countryTable');
        var legend = document.getElementById('countryLegend');
        if (!countries || !countries.length) {
            tb.innerHTML = '<tr><td colspan="5" class="empty-state">No GeoIP data &mdash; place MMDB files next to binary</td></tr>';
            countryChart.data.labels = []; countryChart.data.datasets[0].data = []; countryChart.update('none');
            legend.innerHTML = '<div style="text-align:center;color:var(--text-2);font-size:12px;padding:8px">No data</div>';
            return;
        }
        fillDoughnut(countryChart, legend, countries,
            function(c) { return BM.countryFlag(c.country) + ' ' + (c.country_name || c.country); },
            function(c) { return c.bytes; },
            function(v) { return BM.formatBytes(v); }
        );
        fillDetailTable('countryTable', countries,
            function(c) { return BM.countryFlag(c.country) + ' <span style="font-weight:500">' + (c.country_name || c.country) + '</span> <span class="hostname" style="display:inline">' + c.country + '</span>'; },
            function(c) { return c.bytes; },
            function(v) { return BM.formatBytes(v); }, 'bw'
        );
    };

    var ipvColors = { 'IPv4': '#60a5fa', 'IPv6': '#a855f7' };

    BM.updateIPVersions = function(data) {
        var legend = document.getElementById('ipvLegend');
        if (!data) {
            ipvChart.data.labels = []; ipvChart.data.datasets[0].data = []; ipvChart.update('none');
            legend.innerHTML = '<div style="text-align:center;color:var(--text-2);font-size:12px;padding:8px">No data</div>';
            return;
        }
        var labels = [], values = [], colors = [];
        if (data['IPv4']) { labels.push('IPv4'); values.push(data['IPv4']); colors.push(ipvColors['IPv4']); }
        if (data['IPv6']) { labels.push('IPv6'); values.push(data['IPv6']); colors.push(ipvColors['IPv6']); }
        updateSimpleDoughnut(ipvChart, legend, labels, values, colors, BM.formatBytes);
    };

    BM.updateASNs = function(asns) {
        var tb = document.getElementById('asnTable');
        var legend = document.getElementById('asnLegend');
        if (!asns || !asns.length) {
            tb.innerHTML = '<tr><td colspan="5" class="empty-state">No ASN data &mdash; place GeoLite2-ASN.mmdb next to binary</td></tr>';
            asnChart.data.labels = []; asnChart.data.datasets[0].data = []; asnChart.update('none');
            legend.innerHTML = '<div style="text-align:center;color:var(--text-2);font-size:12px;padding:8px">No data</div>';
            return;
        }
        fillDoughnut(asnChart, legend, asns,
            function(a) { return a.as_org || 'AS' + a.asn; },
            function(a) { return a.bytes; },
            function(v) { return BM.formatBytes(v); }
        );
        fillDetailTable('asnTable', asns,
            function(a) { return '<span style="font-weight:500">' + (a.as_org || 'Unknown') + '</span> <span class="hostname" style="display:inline">AS' + a.asn + '</span>'; },
            function(a) { return a.bytes; },
            function(v) { return BM.formatBytes(v); }, 'vol'
        );
    };

    // ── Chart theme sync ──
    // These charts are created in other modules; we collect them at runtime
    BM._updateChartsForTheme = function() {
        var s = getComputedStyle(document.documentElement);
        var grid = s.getPropertyValue('--bg-3').trim();
        var tick = s.getPropertyValue('--text-2').trim();
        var bdr = s.getPropertyValue('--border').trim();
        var bg2 = s.getPropertyValue('--bg-2').trim();
        var t0 = s.getPropertyValue('--text-0').trim();
        var t1 = s.getPropertyValue('--text-1').trim();
        trafficChart.options.scales.x.grid.color = grid;
        trafficChart.options.scales.x.ticks.color = tick;
        trafficChart.options.scales.x.border.color = bdr;
        trafficChart.options.scales.y.grid.color = grid;
        trafficChart.options.scales.y.ticks.color = tick;
        trafficChart.options.scales.y.border.color = bdr;
        trafficChart.options.plugins.legend.labels.color = tick;
        trafficChart.options.plugins.tooltip.backgroundColor = bg2;
        trafficChart.options.plugins.tooltip.titleColor = t0;
        trafficChart.options.plugins.tooltip.bodyColor = t1;
        trafficChart.options.plugins.tooltip.borderColor = bdr;
        trafficChart.update('none');
        // Update all doughnut charts
        var doughnuts = [protoChart, countryChart, asnChart, ipvChart];
        if (BM._ssidChart) doughnuts.push(BM._ssidChart);
        if (BM._apClientsChart) doughnuts.push(BM._apClientsChart);
        if (BM._apTrafficChart) doughnuts.push(BM._apTrafficChart);
        if (BM._ssidTrafficChart) doughnuts.push(BM._ssidTrafficChart);
        if (BM._dnsClientsChart) doughnuts.push(BM._dnsClientsChart);
        if (BM._dnsDomainsChart) doughnuts.push(BM._dnsDomainsChart);
        if (BM._dnsBlockedDomainsChart) doughnuts.push(BM._dnsBlockedDomainsChart);
        if (BM._natProtoChart) doughnuts.push(BM._natProtoChart);
        if (BM._natStateChart) doughnuts.push(BM._natStateChart);
        if (BM._natTypeChart) doughnuts.push(BM._natTypeChart);
        if (BM._natIPvChart) doughnuts.push(BM._natIPvChart);
        doughnuts.forEach(function(ch) {
            if (!ch) return;
            ch.options.plugins.tooltip.backgroundColor = bg2;
            ch.options.plugins.tooltip.titleColor = t0;
            ch.options.plugins.tooltip.bodyColor = t1;
            ch.options.plugins.tooltip.borderColor = bdr;
            ch.update('none');
        });
    };
    // Also keep window ref for theme toggle handler
    window._updateChartsForTheme = BM._updateChartsForTheme;
    BM._updateChartsForTheme();

    window._toggleDetail = function(which) {
        var detail = document.getElementById(which + 'Detail');
        var toggle = document.getElementById(which + 'Toggle');
        var isOpen = detail.classList.contains('open');
        detail.classList.toggle('open');
        toggle.classList.toggle('open');
        toggle.querySelector('span').textContent = isOpen ? 'Show details' : 'Hide details';
    };
})();
