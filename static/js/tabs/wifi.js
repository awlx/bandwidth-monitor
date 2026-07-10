(function() {
    'use strict';
    var BM = window.BM;

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

    var ssidChart = makeDoughnut('ssidChart');
    var apClientsChart = makeDoughnut('apClientsChart');
    var apTrafficChart = makeDoughnut('apTrafficChart');
    var ssidTrafficChart = makeDoughnut('ssidTrafficChart');

    // Expose for theme sync
    BM._ssidChart = ssidChart;
    BM._apClientsChart = apClientsChart;
    BM._apTrafficChart = apTrafficChart;
    BM._ssidTrafficChart = ssidTrafficChart;

    var fillDoughnut = BM._fillDoughnut;
    var fillDetailTable = BM._fillDetailTable;
    var fillTrafficDetailTable = BM._fillTrafficDetailTable;

    BM.updateWiFi = function(wifi) {
        if (!wifi) return;
        document.getElementById('wifiNoData').style.display = 'none';
        document.getElementById('wifiHasData').style.display = '';
        var pn = document.getElementById('wifiProviderName');
        if (pn && wifi.provider_name) pn.textContent = wifi.provider_name;

        document.getElementById('wifiTotalAPs').textContent = wifi.total_aps || 0;
        document.getElementById('wifiTotalClients').textContent = wifi.total_clients || 0;
        document.getElementById('wifiTotalSSIDs').textContent = (wifi.ssids ? wifi.ssids.length : 0);

        // AP cards
        var apEl = document.getElementById('wifiAPCards');
        if (wifi.aps && wifi.aps.length) {
            var h = '';
            for (var i = 0; i < wifi.aps.length; i++) {
                var ap = wifi.aps[i];
                var st = ap.status === 'connected' ? 'up' : (ap.status === 'disconnected' ? 'down' : 'unknown');
                var stLabel = ap.status === 'connected' ? 'Online' : (ap.status === 'disconnected' ? 'Offline' : (ap.status || 'Unknown'));
                h += '<div class="wifi-ap-card">';
                h += '<div class="wifi-ap-name"><span>' + BM.escHtml(ap.name || ap.mac || 'AP') + ' <span class="wifi-ap-model">' + BM.escHtml(ap.model || '') + '</span></span>';
                h += '<span class="iface-status"><span class="iface-status-dot ' + st + '"></span>' + BM.escHtml(stLabel) + '</span></div>';
                h += '<div class="wifi-ap-stats">';
                h += '<div><div class="wifi-ap-stat-label">Clients</div><div class="wifi-ap-stat-value" style="color:var(--rx)">' + BM.integer(ap.num_clients, 0) + '</div></div>';
                h += '<div><div class="wifi-ap-stat-label">Firmware</div><div class="wifi-ap-stat-value" style="font-size:11px">' + BM.escHtml(ap.version || '—') + '</div></div>';
                h += '<div><div class="wifi-ap-stat-label label-rx">RX Rate</div><div class="wifi-ap-stat-value" style="font-size:11px;color:var(--rx)">' + BM.formatRate(ap.rx_rate || 0) + '</div></div>';
                h += '<div><div class="wifi-ap-stat-label label-tx">TX Rate</div><div class="wifi-ap-stat-value" style="font-size:11px;color:var(--tx)">' + BM.formatRate(ap.tx_rate || 0) + '</div></div>';
                h += '<div><div class="wifi-ap-stat-label">RX Total</div><div class="wifi-ap-stat-value" style="font-size:11px">' + BM.formatBytes(ap.rx_bytes || 0) + '</div></div>';
                h += '<div><div class="wifi-ap-stat-label">TX Total</div><div class="wifi-ap-stat-value" style="font-size:11px">' + BM.formatBytes(ap.tx_bytes || 0) + '</div></div>';
                if (ap.ip) {
                    h += '<div><div class="wifi-ap-stat-label">IP</div><div class="wifi-ap-stat-value" style="font-size:11px;font-family:JetBrains Mono,monospace">' + BM.escHtml(ap.ip) + '</div></div>';
                }
                if (ap.mac) {
                    h += '<div><div class="wifi-ap-stat-label">MAC</div><div class="wifi-ap-stat-value" style="font-size:11px;font-family:JetBrains Mono,monospace">' + BM.escHtml(ap.mac) + '</div></div>';
                }
                if (ap.uptime) {
                    var days = Math.floor(ap.uptime / 86400);
                    var hrs = Math.floor((ap.uptime % 86400) / 3600);
                    h += '<div><div class="wifi-ap-stat-label">Uptime</div><div class="wifi-ap-stat-value" style="font-size:11px">' + days + 'd ' + hrs + 'h</div></div>';
                }
                h += '</div></div>';
            }
            apEl.innerHTML = h;
        } else {
            apEl.innerHTML = '<div class="empty-state">No access points found</div>';
        }

        // ── Clients per AP chart ──
        var apsByClients = (wifi.aps || []).slice().sort(function(a, b) { return (b.num_clients || 0) - (a.num_clients || 0); });
        fillDoughnut(apClientsChart, document.getElementById('apClientsLegend'),
            apsByClients,
            function(a) { return a.name || a.mac || 'AP'; },
            function(a) { return a.num_clients || 0; },
            function(v) { return v + ' clients'; }
        );
        fillDetailTable('apClientsTable', apsByClients,
            function(a) { return a.name || a.mac || 'AP'; },
            function(a) { return a.num_clients || 0; },
            function(v) { return v.toLocaleString(); }, 'bw'
        );

        // ── Traffic per AP chart ──
        var apsByTraffic = (wifi.aps || []).slice().sort(function(a, b) {
            return ((b.tx_bytes || 0) + (b.rx_bytes || 0)) - ((a.tx_bytes || 0) + (a.rx_bytes || 0));
        });
        fillDoughnut(apTrafficChart, document.getElementById('apTrafficLegend'),
            apsByTraffic,
            function(a) { return a.name || a.mac || 'AP'; },
            function(a) { return (a.tx_bytes || 0) + (a.rx_bytes || 0); },
            BM.formatBytes
        );
        fillTrafficDetailTable('apTrafficTable', apsByTraffic, function(a) { return a.name || a.mac || 'AP'; });

        // ── Clients per SSID chart ──
        fillDoughnut(ssidChart, document.getElementById('ssidLegend'),
            wifi.ssids || [],
            function(s) { return s.name || '(hidden)'; },
            function(s) { return s.num_clients || 0; },
            function(v) { return v + ' clients'; }
        );
        fillDetailTable('wifiSSIDTable', wifi.ssids || [],
            function(s) { return s.name || '—'; },
            function(s) { return s.num_clients || 0; },
            function(v) { return v.toLocaleString(); }, 'bw'
        );

        // ── Traffic per SSID chart ──
        var ssidsByTraffic = (wifi.ssids || []).slice().sort(function(a, b) {
            return ((b.tx_bytes || 0) + (b.rx_bytes || 0)) - ((a.tx_bytes || 0) + (a.rx_bytes || 0));
        });
        fillDoughnut(ssidTrafficChart, document.getElementById('ssidTrafficLegend'),
            ssidsByTraffic,
            function(s) { return s.name || '(hidden)'; },
            function(s) { return (s.tx_bytes || 0) + (s.rx_bytes || 0); },
            BM.formatBytes
        );
        fillTrafficDetailTable('ssidTrafficTable', ssidsByTraffic, function(s) { return s.name || '—'; });

        // ── Client traffic table ──
        var clients = (wifi.clients || []).slice();
        var sortKey = (document.getElementById('wifiClientSort') || {}).value || 'traffic';
        if (sortKey === 'name') {
            clients.sort(function(a, b) { return (a.hostname || a.mac || '').localeCompare(b.hostname || b.mac || ''); });
        } else if (sortKey === 'signal') {
            clients.sort(function(a, b) { return (a.signal || -100) - (b.signal || -100); });
        } else if (sortKey === 'rate') {
            clients.sort(function(a, b) {
                return ((b.rx_rate || 0) + (b.tx_rate || 0)) - ((a.rx_rate || 0) + (a.tx_rate || 0));
            });
        }

        var filter = ((document.getElementById('wifiClientSearch') || {}).value || '').toLowerCase();
        if (filter) {
            clients = clients.filter(function(cl) {
                return (cl.hostname || '').toLowerCase().indexOf(filter) !== -1 ||
                       (cl.ip || '').indexOf(filter) !== -1 ||
                       (cl.mac || '').toLowerCase().indexOf(filter) !== -1 ||
                       (cl.ssid || '').toLowerCase().indexOf(filter) !== -1 ||
                       (cl.ap_name || '').toLowerCase().indexOf(filter) !== -1;
            });
        }
        var pinnedOnly = !!(document.getElementById('wifiPinnedOnly') || {}).checked;
        if (pinnedOnly) {
            clients = clients.filter(function(cl) { return BM.isFavoriteDevice(BM.deviceLabelKey(cl.mac, cl.ip ? [cl.ip] : [])); });
        }

        var ctb = document.getElementById('wifiClientTable');
        if (!clients.length) {
            ctb.innerHTML = '<tr><td colspan="12" class="empty-state">' + (filter || pinnedOnly ? 'No matching clients' : 'No wireless clients') + '</td></tr>';
        } else {
            var maxBw = 1;
            for (var i = 0; i < clients.length; i++) {
                var t = (clients[i].tx_bytes || 0) + (clients[i].rx_bytes || 0);
                if (t > maxBw) maxBw = t;
            }
            var ch = '';
            for (var i = 0; i < clients.length; i++) {
                var cl = clients[i];
                var total = (cl.tx_bytes || 0) + (cl.rx_bytes || 0);
                var pct = maxBw > 0 ? ((total / maxBw) * 100).toFixed(1) : '0';
                var ips = cl.ip ? [cl.ip] : [];
                var name = BM.deviceDisplayName(cl.mac, ips, cl.hostname) || cl.ip || cl.mac || '—';
                var sub = cl.ip && cl.hostname ? cl.ip : (cl.mac || '');
                var sig = BM.num(cl.signal, 0);
                var sigClass = sig >= -50 ? 'sig-great' : sig >= -65 ? 'sig-good' : sig >= -75 ? 'sig-ok' : 'sig-weak';
                var favKey = BM.deviceLabelKey(cl.mac, ips);
                ch += '<tr>';
                ch += '<td><span class="' + BM.rankClass(i) + '">' + (i + 1) + '</span></td>';
                ch += '<td>' + BM.favoriteStarHtml(favKey) + '</td>';
                ch += '<td>';
                if (cl.ip) ch += '<span class="ip-cell ip-clickable" data-ip="' + BM.escAttr(cl.ip) + '">' + BM.escHtml(name) + '</span>';
                else ch += '<span class="ip-cell">' + BM.escHtml(name) + '</span>';
                if (sub && sub !== name) ch += '<div style="font-size:10px;color:var(--text-2);font-family:JetBrains Mono,monospace">' + BM.escHtml(sub) + '</div>';
                ch += '</td>';
                ch += '<td style="font-size:12px">' + BM.escHtml(cl.ssid || '—') + '</td>';
                ch += '<td style="font-size:12px">' + BM.escHtml(cl.ap_name || '—') + '</td>';
                ch += '<td style="font-size:11px;color:var(--text-2)">' + BM.escHtml(cl.radio || '—') + '</td>';
                ch += '<td style="font-size:11px;font-variant-numeric:tabular-nums">' + (BM.num(cl.channel) > 0 ? BM.integer(cl.channel, 0) : '—') + '</td>';
                ch += '<td><span class="signal-badge ' + sigClass + '">' + sig + ' dBm</span></td>';
                ch += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums">' + BM.formatBytes(cl.rx_bytes || 0) + '</td>';
                ch += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums">' + BM.formatBytes(cl.tx_bytes || 0) + '</td>';
                var clRate = (cl.rx_rate || 0) + (cl.tx_rate || 0);
                ch += '<td style="white-space:nowrap;font-variant-numeric:tabular-nums;font-size:11px">';
                ch += '<span style="color:var(--rx)">' + BM.formatRate(cl.rx_rate || 0) + '</span>';
                ch += ' <span style="color:var(--text-2)">/</span> ';
                ch += '<span style="color:var(--tx)">' + BM.formatRate(cl.tx_rate || 0) + '</span></td>';
                ch += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill bw" style="width:' + pct + '%"></div></td>';
                ch += '</tr>';
            }
            ctb.innerHTML = ch;
        }
    };
})();
