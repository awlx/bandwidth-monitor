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

    var dnsClientsChart = makeDoughnut('dnsClientsChart');
    var dnsDomainsChart = makeDoughnut('dnsDomainsChart');
    var dnsBlockedDomainsChart = makeDoughnut('dnsBlockedDomainsChart');

    // Expose for theme sync
    BM._dnsClientsChart = dnsClientsChart;
    BM._dnsDomainsChart = dnsDomainsChart;
    BM._dnsBlockedDomainsChart = dnsBlockedDomainsChart;

    // DNS mini-bar charts
    function makeBarChart(canvasId, color) {
        return new Chart(document.getElementById(canvasId).getContext('2d'), {
            type: 'bar',
            data: { labels: [], datasets: [{ data: [], backgroundColor: color, borderWidth: 0, borderRadius: 1 }] },
            options: {
                responsive: true, maintainAspectRatio: false, animation: false,
                plugins: { legend: { display: false }, tooltip: { enabled: false } },
                scales: {
                    x: { display: false },
                    y: { display: false, beginAtZero: true }
                }
            }
        });
    }
    var dnsQChart = null, dnsBChart = null;

    var fillDoughnut = BM._fillDoughnut;
    var fillDetailTable = BM._fillDetailTable;

    BM.updateDNS = function(dns) {
        if (!dns) return;
        document.getElementById('dnsNoData').style.display = 'none';
        document.getElementById('dnsHasData').style.display = '';

        var providerName = dns.provider_name || 'DNS';
        document.getElementById('dnsCardTitle').textContent = 'DNS \u2014 ' + providerName;

        document.getElementById('dnsTotalQueries').textContent = BM.formatCount(dns.total_queries);
        document.getElementById('dnsBlocked').textContent = BM.formatCount(dns.blocked_total);
        document.getElementById('dnsBlockPct').textContent = BM.num(dns.blocked_pct).toFixed(1) + '%';
        document.getElementById('dnsLatency').textContent = BM.num(dns.avg_latency_ms).toFixed(1) + ' ms';

        // Queries time-series bar chart
        if (dns.queries_series && dns.queries_series.length) {
            if (!dnsQChart) dnsQChart = makeBarChart('dnsQueriesChart', 'rgba(59,130,246,0.6)');
            var labels = [];
            for (var i = 0; i < dns.queries_series.length; i++) labels.push(i);
            dnsQChart.data.labels = labels;
            dnsQChart.data.datasets[0].data = dns.queries_series;
            dnsQChart.update('none');
        }

        // Blocked time-series bar chart
        if (dns.blocked_series && dns.blocked_series.length) {
            if (!dnsBChart) dnsBChart = makeBarChart('dnsBlockedChart', 'rgba(239,68,68,0.6)');
            var labels2 = [];
            for (var i = 0; i < dns.blocked_series.length; i++) labels2.push(i);
            dnsBChart.data.labels = labels2;
            dnsBChart.data.datasets[0].data = dns.blocked_series;
            dnsBChart.update('none');
        }

        // ── Top DNS Clients pie chart ──
        fillDoughnut(dnsClientsChart, document.getElementById('dnsClientsLegend'),
            dns.top_clients || [],
            function(c) { return c.hostname ? c.hostname + ' (' + c.ip + ')' : (c.ip || 'Unknown'); },
            function(c) { return c.count || 0; },
            function(v) { return v.toLocaleString() + ' queries'; }
        );
        fillDetailTable('dnsClientsTable', dns.top_clients || [],
            function(c) { return c.hostname ? c.hostname + ' (' + c.ip + ')' : (c.ip || 'Unknown'); },
            function(c) { return c.count || 0; },
            function(v) { return v.toLocaleString(); }, 'bw'
        );

        // ── Top Queried Domains pie chart ──
        fillDoughnut(dnsDomainsChart, document.getElementById('dnsDomainsLegend'),
            dns.top_queried || [],
            function(d) { return d.domain || '—'; },
            function(d) { return d.count || 0; },
            function(v) { return v.toLocaleString() + ' queries'; }
        );
        fillDetailTable('dnsDomainsTable', dns.top_queried || [],
            function(d) { return d.domain || '—'; },
            function(d) { return d.count || 0; },
            function(v) { return v.toLocaleString(); }, 'bw'
        );

        // ── Top Blocked Domains pie chart ──
        fillDoughnut(dnsBlockedDomainsChart, document.getElementById('dnsBlockedDomainsLegend'),
            dns.top_blocked || [],
            function(d) { return d.domain || '—'; },
            function(d) { return d.count || 0; },
            function(v) { return v.toLocaleString() + ' blocked'; }
        );
        fillDetailTable('dnsBlockedDomainsTable', dns.top_blocked || [],
            function(d) { return d.domain || '—'; },
            function(d) { return d.count || 0; },
            function(v) { return v.toLocaleString(); }, 'vol'
        );

        // ── Upstream DNS Servers table ──
        var upTb = document.getElementById('dnsUpstreamTable');
        var ups = dns.upstreams || [];
        if (!ups.length) {
            upTb.innerHTML = '<tr><td colspan="5" class="empty-state">No upstream data</td></tr>';
        } else {
            var totalResp = 0;
            for (var i = 0; i < ups.length; i++) totalResp += ups[i].responses || 0;
            var uh = '';
            for (var i = 0; i < ups.length; i++) {
                var u = ups[i];
                var pct = totalResp > 0 ? ((u.responses / totalResp) * 100) : 0;
                var latStr = u.avg_ms > 0 ? u.avg_ms.toFixed(1) + ' ms' : '—';
                var barCls = u.avg_ms <= 20 ? 'bw' : (u.avg_ms <= 100 ? 'vol' : 'vol');
                uh += '<tr><td class="rank-cell">' + (i + 1) + '</td>';
                uh += '<td class="host-cell" title="' + BM.escAttr(u.address) + '"><code>' + BM.escHtml(u.address) + '</code></td>';
                uh += '<td>' + BM.formatCount(u.responses) + '</td>';
                uh += '<td>' + latStr + '</td>';
                uh += '<td class="bar-cell"><div class="bar-bg"></div><div class="bar-fill ' + barCls + '" style="width:' + pct.toFixed(1) + '%"></div></td></tr>';
            }
            upTb.innerHTML = uh;
        }
    };
})();
