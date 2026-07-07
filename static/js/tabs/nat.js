(function() {
    'use strict';
    var BM = window.BM;

    var _currentNatVersion = 'v4';
    var _lastConntrack = null;

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

    var natProtoChart = makeDoughnut('natProtoChart');
    var natStateChart = makeDoughnut('natStateChart');
    var natTypeChart = makeDoughnut('natTypeChart');
    var natIPvChart = makeDoughnut('natIPvChart');

    // Expose for theme sync
    BM._natProtoChart = natProtoChart;
    BM._natStateChart = natStateChart;
    BM._natTypeChart = natTypeChart;
    BM._natIPvChart = natIPvChart;

    window._switchNatVersion = function(ver) {
        _currentNatVersion = ver;
        document.querySelectorAll('.nat-version-tab').forEach(function(t) {
            t.classList.toggle('active', t.getAttribute('data-ver') === ver);
        });
        var title = document.getElementById('natTableTitle');
        var sub = document.getElementById('natTableSubtitle');
        if (ver === 'v4') {
            title.textContent = 'IPv4 NAT Translations';
            sub.textContent = 'Active conntrack entries (top 200 by traffic volume)';
        } else {
            title.textContent = 'IPv6 NAT Translations';
            sub.textContent = 'Active conntrack entries (top 200 by traffic volume)';
        }
        if (_lastConntrack) renderNATEntries(_lastConntrack);
    };

    var natProtoColors = { 'TCP': '#3b82f6', 'UDP': '#22d3ee', 'ICMP': '#f59e0b', 'SCTP': '#34d399', 'Other': '#71717a' };
    var natStateColors = {
        'ESTABLISHED': '#22c55e', 'TIME_WAIT': '#eab308', 'SYN_SENT': '#3b82f6',
        'SYN_RECV': '#60a5fa', 'FIN_WAIT': '#f59e0b', 'CLOSE_WAIT': '#ef4444',
        'CLOSE': '#f87171', 'LAST_ACK': '#fb923c', 'NONE': '#71717a'
    };
    var natTypeColors = { 'snat': '#22d3ee', 'dnat': '#a78bfa', 'both': '#fb923c', 'none': '#71717a' };

    function updateSimpleDoughnut(chart, legendEl, labels, values, colors, formatter) {
        if (BM._updateSimpleDoughnut) {
            BM._updateSimpleDoughnut(chart, legendEl, labels, values, colors, formatter);
        }
    }

    BM.updateNAT = function(ct) {
        if (!ct) return;
        _lastConntrack = ct;
        document.getElementById('natNoData').style.display = 'none';
        document.getElementById('natHasData').style.display = '';

        document.getElementById('natTotal').textContent = (ct.total || 0).toLocaleString();
        document.getElementById('natMax').textContent = (ct.max || 0).toLocaleString();
        document.getElementById('natUsagePct').textContent = (ct.usage_pct || 0).toFixed(1) + '%';
        document.getElementById('natIPv4').textContent = (ct.ipv4 || 0).toLocaleString();
        document.getElementById('natIPv6').textContent = (ct.ipv6 || 0).toLocaleString();

        // Protocol doughnut
        var protocols = ct.protocols || {};
        var pLabels = [], pValues = [], pColors = [];
        var pOrder = ['TCP', 'UDP', 'ICMP'];
        for (var i = 0; i < pOrder.length; i++) {
            if (protocols[pOrder[i]]) { pLabels.push(pOrder[i]); pValues.push(protocols[pOrder[i]]); pColors.push(natProtoColors[pOrder[i]] || '#71717a'); }
        }
        for (var k in protocols) { if (pOrder.indexOf(k) === -1) { pLabels.push(k); pValues.push(protocols[k]); pColors.push(natProtoColors['Other']); } }
        updateSimpleDoughnut(natProtoChart, document.getElementById('natProtoLegend'), pLabels, pValues, pColors);

        // State doughnut
        var states = ct.states || {};
        var sLabels = [], sValues = [], sColors = [];
        var sOrder = ['ESTABLISHED', 'TIME_WAIT', 'SYN_SENT', 'SYN_RECV', 'FIN_WAIT', 'CLOSE_WAIT', 'CLOSE', 'LAST_ACK'];
        for (var i = 0; i < sOrder.length; i++) {
            if (states[sOrder[i]]) { sLabels.push(sOrder[i]); sValues.push(states[sOrder[i]]); sColors.push(natStateColors[sOrder[i]] || '#71717a'); }
        }
        for (var k in states) { if (sOrder.indexOf(k) === -1) { sLabels.push(k); sValues.push(states[k]); sColors.push('#71717a'); } }
        updateSimpleDoughnut(natStateChart, document.getElementById('natStateLegend'), sLabels, sValues, sColors);

        // NAT type doughnut
        var natTypes = ct.nat_types || {};
        var nLabels = [], nValues = [], nColors = [];
        var nOrder = ['snat', 'dnat', 'both', 'none'];
        var nDisplayNames = { 'snat': 'SNAT', 'dnat': 'DNAT', 'both': 'Both', 'none': 'No NAT' };
        for (var i = 0; i < nOrder.length; i++) {
            if (natTypes[nOrder[i]]) { nLabels.push(nDisplayNames[nOrder[i]]); nValues.push(natTypes[nOrder[i]]); nColors.push(natTypeColors[nOrder[i]]); }
        }
        updateSimpleDoughnut(natTypeChart, document.getElementById('natTypeLegend'), nLabels, nValues, nColors);

        // IP version doughnut
        var vLabels = [], vValues = [], vColors = [];
        if (ct.ipv4) { vLabels.push('IPv4'); vValues.push(ct.ipv4); vColors.push('#60a5fa'); }
        if (ct.ipv6) { vLabels.push('IPv6'); vValues.push(ct.ipv6); vColors.push('#a855f7'); }
        updateSimpleDoughnut(natIPvChart, document.getElementById('natIPvLegend'), vLabels, vValues, vColors);

        // Top LAN clients / remote destinations tables
        renderHostTable('natSrcTable', ct.top_lan_clients || []);
        renderHostTable('natDstTable', ct.top_remote_destinations || []);

        // Entry table
        renderNATEntries(ct);
    };

    var _natHostSortMode = { natSrc: 'bytes', natDst: 'bytes' };
    var _natHostData = { natSrc: [], natDst: [] };

    window._toggleHostSort = function(btn) {
        var target = btn.getAttribute('data-target');
        var mode = btn.getAttribute('data-mode');
        _natHostSortMode[target] = mode;
        var siblings = btn.parentElement.querySelectorAll('.toggle-btn');
        for (var i = 0; i < siblings.length; i++) siblings[i].classList.remove('active');
        btn.classList.add('active');
        var subtitle = mode === 'bytes' ? 'By traffic volume' : 'By connection count';
        var header = mode === 'bytes' ? 'Traffic' : 'Connections';
        document.getElementById(target + 'Subtitle').textContent = subtitle;
        document.getElementById(target + 'MetricHeader').textContent = header;
        renderHostTable(target + 'Table', _natHostData[target], mode);
    };

    function renderHostTable(tbId, hosts, mode) {
        if (!mode) mode = _natHostSortMode[tbId.replace('Table', '')] || 'bytes';
        var target = tbId.replace('Table', '');
        _natHostData[target] = hosts;
        var tb = document.getElementById(tbId);
        if (!hosts || !hosts.length) { tb.innerHTML = '<tr><td colspan="4" class="empty-state">No data</td></tr>'; return; }
        hosts = hosts.slice().sort(function(a, b) {
            if (mode === 'bytes') return (b.bytes || 0) - (a.bytes || 0);
            return (b.connections || 0) - (a.connections || 0);
        });
        var useBytes = mode === 'bytes';
        var mx = useBytes ? (hosts[0].bytes || 1) : (hosts[0].connections || 1);
        var h = '';
        for (var i = 0; i < hosts.length; i++) {
            var host = hosts[i];
            var val = useBytes ? (host.bytes || 0) : host.connections;
            var pct = ((val / mx) * 100).toFixed(1);
            var display = useBytes ? BM.formatBytes(val) : val.toLocaleString();
            var flag = host.country ? BM.countryFlag(host.country) + ' ' : '';
            var geo = '';
            var geoName = (host.city && host.country_name) ? host.city + ', ' + host.country_name : (host.country_name || '');
            if (host.as_org) geo = '<span class="hostname">' + flag + geoName + ' &middot; AS' + (host.asn || '') + ' ' + host.as_org + '</span>';
            else if (geoName) geo = '<span class="hostname">' + flag + geoName + '</span>';
            var cell = host.hostname
                ? '<span class="ip-cell ip-clickable" data-ip="' + host.ip + '">' + host.ip + '</span><span class="hostname">' + host.hostname + '</span>' + geo
                : '<span class="ip-cell ip-clickable" data-ip="' + host.ip + '">' + host.ip + '</span>' + geo;
            h += '<tr><td><span class="' + BM.rankClass(i) + '">' + (i + 1) + '</span></td>';
            h += '<td>' + cell + '</td>';
            h += '<td style="font-variant-numeric:tabular-nums">' + display + '</td>';
            h += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill bw" style="width:' + pct + '%"></div></td></tr>';
        }
        tb.innerHTML = h;
    }

    function renderNATEntries(ct) {
        var entries = _currentNatVersion === 'v4' ? (ct.ipv4_entries || []) : (ct.ipv6_entries || []);
        var filter = ((document.getElementById('natFilter') || {}).value || 'all');
        var search = ((document.getElementById('natSearch') || {}).value || '').toLowerCase();

        if (filter !== 'all') {
            entries = entries.filter(function(e) { return e.nat_type === filter; });
        }
        if (search) {
            entries = entries.filter(function(e) {
                return (e.orig_src || '').indexOf(search) !== -1 ||
                       (e.orig_dst || '').indexOf(search) !== -1 ||
                       (e.repl_src || '').indexOf(search) !== -1 ||
                       (e.repl_dst || '').indexOf(search) !== -1 ||
                       (e.orig_src_host || '').toLowerCase().indexOf(search) !== -1 ||
                       (e.orig_dst_host || '').toLowerCase().indexOf(search) !== -1 ||
                       (e.orig_dst_asn || '').toLowerCase().indexOf(search) !== -1 ||
                       (e.orig_src_asn || '').toLowerCase().indexOf(search) !== -1 ||
                       (e.protocol || '').toLowerCase().indexOf(search) !== -1 ||
                       (e.state || '').toLowerCase().indexOf(search) !== -1 ||
                       (e.orig_sport || '').indexOf(search) !== -1 ||
                       (e.orig_dport || '').indexOf(search) !== -1;
            });
        }

        var tb = document.getElementById('natEntryTable');
        if (!entries.length) {
            tb.innerHTML = '<tr><td colspan="10" class="empty-state">' + (search || filter !== 'all' ? 'No matching entries' : 'No ' + (_currentNatVersion === 'v4' ? 'IPv4' : 'IPv6') + ' entries') + '</td></tr>';
            return;
        }

        var h = '';
        for (var i = 0; i < entries.length; i++) {
            var e = entries[i];
            var stateClass = 'other';
            if (e.state === 'ESTABLISHED') stateClass = 'established';
            else if (e.state === 'TIME_WAIT') stateClass = 'time-wait';
            else if (e.state === 'SYN_SENT' || e.state === 'SYN_RECV') stateClass = 'syn-sent';
            else if (e.state === 'CLOSE_WAIT' || e.state === 'CLOSE' || e.state === 'LAST_ACK') stateClass = 'close-wait';

            var origSrc = e.orig_src + (e.orig_sport ? ':' + e.orig_sport : '');
            var origDst = e.orig_dst + (e.orig_dport ? ':' + e.orig_dport : '');
            var replSrc = e.repl_src + (e.repl_sport ? ':' + e.repl_sport : '');
            var replDst = e.repl_dst + (e.repl_dport ? ':' + e.repl_dport : '');

            var replSrcHL = (e.repl_src !== e.orig_dst) ? ' style="color:var(--tx);font-weight:600"' : '';
            var replDstHL = (e.repl_dst !== e.orig_src) ? ' style="color:var(--rx);font-weight:600"' : '';

            function ipCell(addr, host, geo, city, asn, hlStyle) {
                var s = '<td' + (hlStyle || '') + '><div style="font-family:JetBrains Mono,monospace;font-size:11px;white-space:nowrap">' + addr + '</div>';
                var info = [];
                if (host) info.push(host);
                if (city && geo) info.push(BM.countryFlag(geo) + ' ' + city + ', ' + geo);
                else if (geo) info.push(BM.countryFlag(geo) + ' ' + geo);
                if (asn) info.push(asn);
                if (info.length) s += '<div style="font-size:9px;color:var(--text-2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:220px">' + info.join(' · ') + '</div>';
                s += '</td>';
                return s;
            }

            h += '<tr>';
            h += '<td style="font-size:12px;font-weight:500">' + (e.protocol || '').toUpperCase() + '</td>';
            h += '<td>' + (e.state ? '<span class="nat-state-badge ' + stateClass + '">' + e.state + '</span>' : '<span style="color:var(--text-2)">—</span>') + '</td>';
            h += ipCell(origSrc, e.orig_src_host, e.orig_src_geo, e.orig_src_city, e.orig_src_asn, '');
            h += ipCell(origDst, e.orig_dst_host, e.orig_dst_geo, e.orig_dst_city, e.orig_dst_asn, '');
            h += '<td style="font-family:JetBrains Mono,monospace;font-size:11px;white-space:nowrap"' + replSrcHL + '>' + replSrc + '</td>';
            h += '<td style="font-family:JetBrains Mono,monospace;font-size:11px;white-space:nowrap"' + replDstHL + '>' + replDst + '</td>';
            h += '<td><span class="nat-badge ' + e.nat_type + '">' + (e.nat_type || 'none').toUpperCase() + '</span></td>';
            h += '<td style="font-variant-numeric:tabular-nums;font-size:11px;white-space:nowrap">' + (e.bytes ? BM.formatBytes(e.bytes) : '<span style="color:var(--text-2)">—</span>') + '</td>';
            h += '<td style="font-variant-numeric:tabular-nums;font-size:11px;white-space:nowrap">' + (e.packets ? e.packets.toLocaleString() : '<span style="color:var(--text-2)">—</span>') + '</td>';
            h += '<td style="font-variant-numeric:tabular-nums;font-size:12px;color:var(--text-2)">' + e.ttl + 's</td>';
            h += '</tr>';
        }
        tb.innerHTML = h;
    }

    // The NAT table used to ride along on the 1s SSE tick, but the full
    // conntrack table (up to 200 IPv4 + 200 IPv6 entries with hostname/geo/ASN
    // enrichment) resent every second — whether or not this tab is even open —
    // was the dominant cost of that stream. Poll /api/conntrack directly
    // instead, only while this tab is visible.
    var _natPollInterval = null;

    function _fetchConntrack() {
        return fetch('/api/conntrack').then(function(r) { return r.json(); }).then(function(data) {
            if (data) BM.updateNAT(data);
        }).catch(function(e) { console.error('conntrack load:', e); });
    }

    BM._startNATPolling = function() {
        if (_natPollInterval) return;
        _fetchConntrack();
        _natPollInterval = setInterval(_fetchConntrack, 5000);
    };

    BM._stopNATPolling = function() {
        if (_natPollInterval) { clearInterval(_natPollInterval); _natPollInterval = null; }
    };

    // Wire search/filter on NAT entry table to re-render (with debounce on search)
    var _natSearchTimer = null;
    (function() {
        var searchEl = document.getElementById('natSearch');
        if (searchEl) searchEl.addEventListener('input', function() {
            clearTimeout(_natSearchTimer);
            _natSearchTimer = setTimeout(function() {
                if (_lastConntrack) renderNATEntries(_lastConntrack);
            }, 150);
        });
        var filterEl = document.getElementById('natFilter');
        if (filterEl) filterEl.addEventListener('change', function() {
            if (_lastConntrack) renderNATEntries(_lastConntrack);
        });
    })();
})();
