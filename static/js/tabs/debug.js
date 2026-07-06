(function() {
    'use strict';
    var BM = window.BM;

    // ── WAN interface picker (shared across all Debug tools) ──
    // Multi-WAN setups get a "test via" select next to each tool so results
    // can be pinned to a specific uplink instead of whatever the default
    // route happens to send traffic through. Hidden entirely on single-WAN
    // setups (the common case) to avoid cluttering the UI.
    var _debugIfaceSelectIds = ['trIfaceSelect', 'mtuIfaceSelect', 'pubIPIfaceSelect', 'tcpCheckIfaceSelect'];
    function loadDebugInterfaces() {
        fetch('/api/speedtest/interfaces').then(function(r) { return r.json(); }).then(function(data) {
            var ifaces = data.interfaces || [];
            for (var s = 0; s < _debugIfaceSelectIds.length; s++) {
                var sel = document.getElementById(_debugIfaceSelectIds[s]);
                if (!sel) continue;
                if (ifaces.length < 2) {
                    sel.style.display = 'none';
                    continue;
                }
                var h = '<option value="">Auto (default route)</option>';
                for (var i = 0; i < ifaces.length; i++) {
                    h += '<option value="' + BM.escSvg(ifaces[i].name) + '">' + BM.escSvg(ifaces[i].name) + '</option>';
                }
                sel.innerHTML = h;
                sel.style.display = '';
            }
        }).catch(function() {});
    }
    loadDebugInterfaces();

    function selectedIface(selectId) {
        var sel = document.getElementById(selectId);
        return (sel && sel.style.display !== 'none') ? sel.value : '';
    }

    // ── Traceroute ──
    var _trRunning = false;

    window._runTraceroute = function() {
        if (_trRunning) return;
        var target = (document.getElementById('trTarget').value || '').trim();
        if (!target) { alert('Enter an IP or hostname'); return; }
        var count = parseInt(document.getElementById('trCount').value) || 20;
        var iface = selectedIface('trIfaceSelect');

        _trRunning = true;
        var btn = document.getElementById('trBtn');
        btn.disabled = true;
        btn.innerHTML = '<span class="speedtest-spinner"></span> Running...';

        var wrap = document.getElementById('trProgressWrap');
        wrap.style.display = '';
        var bar = document.getElementById('trProgressBar');
        var phase = document.getElementById('trPhase');
        bar.style.width = '0%';
        bar.className = 'speedtest-progress-bar-fill ping';
        phase.textContent = 'Running traceroute...';

        document.getElementById('trResults').style.display = 'none';

        BM.streamSSE({
            url: '/api/debug/traceroute?target=' + encodeURIComponent(target) + '&count=' + count + (iface ? '&iface=' + encodeURIComponent(iface) : ''),
            method: 'POST',

            onMessage: function(p) {
                if (p.phase === 'running') {
                    phase.textContent = p.message;
                    if (p.ttl > 0) {
                        bar.style.width = Math.min((p.ttl / 30) * 100, 99) + '%';
                    }
                } else if (p.phase === 'done' && p.result) {
                    phase.textContent = p.message;
                    bar.style.width = '100%';
                    bar.className = 'speedtest-progress-bar-fill done';
                    renderTrResults(p.result);
                    finishTraceroute();
                } else if (p.phase === 'error') {
                    phase.textContent = p.message;
                    bar.className = 'speedtest-progress-bar-fill error';
                    finishTraceroute();
                }
            },
            onDone: function() {},
            onError: function() {
                phase.textContent = 'Connection error';
                finishTraceroute();
            }
        });

        function finishTraceroute() {
            _trRunning = false;
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><polygon points="5 3 19 12 5 21 5 3"/></svg> Run';
            setTimeout(function() { wrap.style.display = 'none'; }, 2000);
        }
    };

    function renderTrResults(result) {
        document.getElementById('trResults').style.display = '';
        document.getElementById('trResultTitle').textContent = 'Traceroute to ' + result.target + ' (' + result.resolved_ip + ')';
        document.getElementById('trResultSub').textContent = result.probes_per_ttl + ' probes/hop — ' + result.hops.length + ' hops' + (result.reached_dest ? ' — destination reached' : '') + (result.error ? ' — ' + result.error : '');

        var tb = document.getElementById('trTable');
        if (!result.hops || !result.hops.length) {
            tb.innerHTML = '<tr><td colspan="5" class="empty-state">No hops received</td></tr>';
            return;
        }

        var h = '';
        for (var i = 0; i < result.hops.length; i++) {
            var hop = result.hops[i];
            var lossClass = hop.loss_pct > 0 ? (hop.loss_pct > 10 ? ' style="color:var(--danger);font-weight:600"' : ' style="color:var(--warning)"') : '';
            var rttStr = hop.received > 0 ? hop.avg_rtt_ms.toFixed(2) + ' ms' : '—';
            if (hop.received > 1) rttStr += ' <span style="color:var(--text-2);font-size:11px">(min ' + hop.min_rtt_ms.toFixed(2) + ' / max ' + hop.max_rtt_ms.toFixed(2) + ')</span>';
            var ipStr = hop.ip || '<span style="color:var(--text-2)">*</span>';
            var hostStr = hop.hostname || '—';

            h += '<tr>';
            h += '<td>' + hop.ttl + '</td>';
            h += '<td style="font-family:JetBrains Mono,monospace;font-size:12px">' + ipStr + '</td>';
            h += '<td style="font-size:12px;color:var(--text-2)">' + hostStr + '</td>';
            h += '<td style="font-variant-numeric:tabular-nums;font-size:12px">' + rttStr + '</td>';
            h += '<td' + lossClass + '>' + hop.loss_pct.toFixed(1) + '%</td>';
            h += '</tr>';
        }
        tb.innerHTML = h;
    }

    // ── MTU Discovery ──
    var _mtuRunning = false;
    window._runMTUDiscovery = function() {
        if (_mtuRunning) return;
        var target = (document.getElementById('mtuTarget').value || '').trim();
        if (!target) { alert('Enter an IP or hostname'); return; }
        var iface = selectedIface('mtuIfaceSelect');

        _mtuRunning = true;
        var btn = document.getElementById('mtuBtn');
        btn.disabled = true;
        btn.innerHTML = '<span class="speedtest-spinner"></span> Testing...';

        var wrap = document.getElementById('mtuProgressWrap');
        wrap.style.display = '';
        var bar = document.getElementById('mtuProgressBar');
        var phase = document.getElementById('mtuPhase');
        bar.style.width = '0%';
        bar.className = 'speedtest-progress-bar-fill ping';
        phase.textContent = 'Starting MTU discovery...';

        document.getElementById('mtuResults').style.display = 'none';

        var probeCount = 0;

        BM.streamSSE({
            url: '/api/debug/mtu?target=' + encodeURIComponent(target) + (iface ? '&iface=' + encodeURIComponent(iface) : ''),
            method: 'POST',
            onMessage: function(p) {
                if (p.phase === 'running') {
                    phase.textContent = p.message;
                    probeCount++;
                    bar.style.width = Math.min(probeCount * 8, 99) + '%';
                } else if (p.phase === 'done' && p.result) {
                    phase.textContent = p.message;
                    bar.style.width = '100%';
                    bar.className = 'speedtest-progress-bar-fill done';
                    renderMTUResults(p.result);
                    finishMTU();
                } else if (p.phase === 'error') {
                    phase.textContent = p.message;
                    bar.className = 'speedtest-progress-bar-fill error';
                    finishMTU();
                }
            },
            onDone: function() {},
            onError: function() {
                phase.textContent = 'Connection error';
                finishMTU();
            }
        });

        function finishMTU() {
            _mtuRunning = false;
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><path d="M12 2v20M2 12h20"/></svg> Discover';
            setTimeout(function() { wrap.style.display = 'none'; }, 2000);
        }
    };

    function renderMTUResults(result) {
        document.getElementById('mtuResults').style.display = '';
        var mtu = result.path_mtu;
        var titleText = 'Path MTU to ' + result.target + ' (' + result.resolved_ip + ')';
        document.getElementById('mtuResultTitle').textContent = titleText;

        var subParts = [];
        if (mtu > 0) {
            subParts.push('Path MTU: ' + mtu + ' bytes');
        } else {
            subParts.push('Could not determine path MTU');
        }
        if (result.local_mtu > 0) subParts.push('Local interface MTU: ' + result.local_mtu + ' bytes');
        subParts.push(result.probes.length + ' probes sent');
        document.getElementById('mtuResultSub').textContent = subParts.join(' — ');

        var body = document.getElementById('mtuResultBody');
        var h = '';

        if (mtu > 0) {
            var mtuColor = mtu >= 1500 ? 'var(--success)' : (mtu >= 1400 ? 'var(--warning)' : 'var(--danger)');
            var mtuNote = '';
            if (mtu >= 1500) {
                mtuNote = 'Standard Ethernet MTU — no issues expected';
            } else if (mtu >= 1400) {
                mtuNote = 'Slightly reduced — common with VPN/PPPoE tunnels';
            } else if (mtu >= 1280) {
                mtuNote = 'Below standard — possible tunnel or misconfigured link';
            } else {
                mtuNote = 'Very low MTU — likely causing performance issues';
            }
            h += '<div style="padding:16px 20px;border-bottom:1px solid var(--border);display:flex;align-items:center;gap:16px">';
            h += '<div style="font-size:32px;font-weight:700;font-family:JetBrains Mono,monospace;color:' + mtuColor + '">' + mtu + '</div>';
            h += '<div>';
            h += '<div style="font-size:13px;font-weight:600;color:var(--text-0)">bytes path MTU</div>';
            h += '<div style="font-size:12px;color:var(--text-2);margin-top:2px">' + mtuNote + '</div>';
            if (result.local_mtu > 0 && result.local_mtu !== mtu) {
                h += '<div style="font-size:11px;color:var(--warning);margin-top:4px">⚠ Local interface MTU (' + result.local_mtu + ') differs from path MTU (' + mtu + ')</div>';
            }
            h += '</div></div>';
        } else {
            h += '<div style="padding:16px 20px;border-bottom:1px solid var(--border);color:var(--warning);font-size:13px">';
            h += '⚠ Could not determine path MTU — host may be unreachable or blocking ICMP</div>';
        }

        if (result.probes && result.probes.length) {
            h += '<div style="max-height:400px;overflow-y:auto">';
            h += '<table><thead><tr>';
            h += '<th>Packet Size</th><th>Result</th><th>RTT (ms)</th><th>Detail</th>';
            h += '</tr></thead><tbody>';
            for (var i = 0; i < result.probes.length; i++) {
                var p = result.probes[i];
                var statusColor = p.success ? 'var(--success)' : 'var(--danger)';
                var statusIcon = p.success ? '✓ Pass' : '✗ Blocked';
                var bg = p.size === mtu ? 'background:color-mix(in srgb, var(--success) 10%, transparent)' : '';
                h += '<tr style="' + bg + '">';
                h += '<td style="font-family:JetBrains Mono,monospace;font-size:12px">' + p.size + ' bytes' + (p.size === mtu ? ' ←' : '') + '</td>';
                h += '<td style="color:' + statusColor + ';font-weight:600;font-size:12px">' + statusIcon + '</td>';
                h += '<td style="font-variant-numeric:tabular-nums;font-size:12px">' + (p.rtt_ms > 0 ? p.rtt_ms.toFixed(2) + ' ms' : '—') + '</td>';
                h += '<td style="font-size:11px;color:var(--text-2)">' + (p.error || '') + '</td>';
                h += '</tr>';
            }
            h += '</tbody></table></div>';
        }

        body.innerHTML = h;
    }

    // ── DNS Check ──
    window._runDNSCheck = function() {
        var domain = (document.getElementById('dnsCheckDomain').value || '').trim();
        if (!domain) { alert('Enter a domain'); return; }
        var qtype = document.getElementById('dnsCheckType').value;

        var btn = document.getElementById('dnsCheckBtn');
        btn.disabled = true;
        btn.innerHTML = '<span class="speedtest-spinner"></span> Querying...';

        fetch('/api/debug/dns?domain=' + encodeURIComponent(domain) + '&type=' + qtype)
        .then(function(r) { return r.json(); })
        .then(function(data) {
            renderDNSCheckResults(data);
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg> Query';
        }).catch(function() {
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg> Query';
        });
    };

    function renderDNSCheckResults(data) {
        var wrap = document.getElementById('dnsCheckResults');
        wrap.style.display = '';
        document.getElementById('dnsCheckTitle').textContent = data.domain + ' (' + data.type + ')';
        document.getElementById('dnsCheckSub').textContent = data.servers.length + ' DNS servers queried';

        var body = document.getElementById('dnsCheckBody');
        var h = '';

        // Resolver leak check info
        if (data.resolver_info) {
            var ri = data.resolver_info;
            h += '<div style="border-bottom:1px solid var(--border);padding:14px 20px;background:var(--bg-2)">';
            h += '<div style="font-size:12px;font-weight:600;color:var(--text-0);margin-bottom:8px">Resolver Chain</div>';

            if (ri.configured_resolver) {
                h += '<div style="display:flex;align-items:center;gap:8px;margin-bottom:10px">';
                h += '<span style="font-size:11px;color:var(--text-2);min-width:110px">Local resolver:</span>';
                h += '<span style="font-family:JetBrains Mono,monospace;font-size:12px;padding:3px 8px;border-radius:4px;background:var(--bg-3);color:var(--accent)">' + ri.configured_resolver + '</span>';
                h += '<span style="font-size:11px;color:var(--text-2)">(from /etc/resolv.conf)</span>';
                h += '</div>';
            }

            h += '<div style="display:flex;align-items:baseline;gap:8px;margin-bottom:4px">';
            h += '<span style="font-size:11px;color:var(--text-2);min-width:110px">Upstream egress:</span>';
            h += '<div style="display:flex;flex-wrap:wrap;gap:4px">';
            if (ri.resolver_ips && ri.resolver_ips.length) {
                for (var i = 0; i < ri.resolver_ips.length; i++) {
                    h += '<span style="font-family:JetBrains Mono,monospace;font-size:12px;padding:3px 8px;border-radius:4px;background:var(--bg-3);color:var(--text-0)">' + ri.resolver_ips[i] + '</span>';
                }
            } else {
                h += '<span style="font-size:12px;color:var(--warning)">' + (ri.error || 'No resolver IPs detected') + '</span>';
            }
            h += '</div></div>';
            h += '<div style="font-size:10px;color:var(--text-2);margin-top:4px;margin-bottom:6px;padding-left:118px">IPs seen by authoritative DNS servers (o-o.myaddr.l.google.com, dnscheck.tools)</div>';

            if (ri.ecs && ri.ecs.length) {
                h += '<div style="display:flex;align-items:baseline;gap:8px;margin-top:6px">';
                h += '<span style="font-size:11px;color:var(--text-2);min-width:110px">ECS:</span>';
                h += '<div style="display:flex;flex-wrap:wrap;gap:4px">';
                for (var ei = 0; ei < ri.ecs.length; ei++) {
                    h += '<span style="font-family:JetBrains Mono,monospace;font-size:11px;padding:2px 6px;border-radius:3px;background:var(--bg-3);color:var(--text-2)">' + ri.ecs[ei] + '</span>';
                }
                h += '</div></div>';
            }
            if (ri.dnscheck_info) {
                var dc = ri.dnscheck_info;
                h += '<div style="margin-top:10px;display:grid;grid-template-columns:110px 1fr;gap:3px 8px;font-size:12px">';
                var dcFields = [['resolverOrg', 'Upstream org:'], ['resolverGeo', 'Upstream geo:'], ['proto', 'Protocol:'], ['edns0', 'EDNS0:']];
                for (var di = 0; di < dcFields.length; di++) {
                    var key = dcFields[di][0], label = dcFields[di][1];
                    if (dc[key]) {
                        h += '<span style="color:var(--text-2);font-size:11px">' + label + '</span>';
                        h += '<span style="font-family:JetBrains Mono,monospace;font-size:11px;color:var(--text-0)">' + dc[key] + '</span>';
                    }
                }
                h += '</div>';
            }
            h += '</div>';
        }

        // Sort servers
        var servers = (data.servers || []).slice();
        servers.sort(function(a, b) {
            if (a.server === 'System Resolver') return -1;
            if (b.server === 'System Resolver') return 1;
            return a.server.localeCompare(b.server);
        });

        var fastestLatency = Infinity;
        for (var fi = 0; fi < servers.length; fi++) {
            if (servers[fi].latency_ms > 0 && servers[fi].latency_ms < fastestLatency) fastestLatency = servers[fi].latency_ms;
        }

        var allValues = {};
        for (var ai = 0; ai < servers.length; ai++) {
            if (servers[ai].records) {
                for (var ari = 0; ari < servers[ai].records.length; ari++) {
                    var v = servers[ai].records[ari].value;
                    allValues[v] = (allValues[v] || 0) + 1;
                }
            }
        }

        var uniqueRecords = [];
        var recordSeen = {};
        for (var mi = 0; mi < servers.length; mi++) {
            if (servers[mi].records) {
                for (var mri = 0; mri < servers[mi].records.length; mri++) {
                    var key = servers[mi].records[mri].type + ':' + servers[mi].records[mri].value;
                    if (!recordSeen[key]) {
                        recordSeen[key] = true;
                        uniqueRecords.push({ type: servers[mi].records[mri].type, value: servers[mi].records[mri].value });
                    }
                }
            }
        }

        if (servers.length > 1) {
            var shortNames = servers.map(function(s) {
                return s.server.replace(/ \(.*\)/, '').replace('System Resolver', 'System');
            });

            h += '<div style="border-bottom:1px solid var(--border);padding:14px 16px;overflow-x:auto">';
            h += '<div style="font-size:12px;font-weight:600;color:var(--text-0);margin-bottom:10px">Comparison Matrix</div>';
            h += '<table style="width:100%;border-collapse:collapse;font-size:11px">';

            h += '<thead><tr><th style="text-align:left;padding:4px 8px;font-weight:600;color:var(--text-2);border-bottom:1px solid var(--border);min-width:80px">Record</th>';
            for (var ci = 0; ci < servers.length; ci++) {
                var sColor = servers[ci].rcode === 'NOERROR' ? 'var(--success)' : 'var(--danger)';
                h += '<th style="text-align:center;padding:4px 4px;font-weight:600;color:var(--text-0);border-bottom:1px solid var(--border);white-space:nowrap;font-size:10px">';
                h += '<span style="display:inline-block;width:6px;height:6px;border-radius:50%;background:' + sColor + ';margin-right:3px;vertical-align:middle"></span>';
                h += shortNames[ci] + '</th>';
            }
            h += '</tr></thead><tbody>';

            h += '<tr><td style="padding:4px 8px;font-weight:600;color:var(--text-2)">Latency</td>';
            for (var ci = 0; ci < servers.length; ci++) {
                var lat = servers[ci].latency_ms;
                var lc = lat > 0 && Math.abs(lat - fastestLatency) < 0.1 ? 'var(--success);font-weight:700' : (lat > 100 ? 'var(--warning)' : 'var(--text-0)');
                h += '<td style="text-align:center;padding:4px;font-family:JetBrains Mono,monospace;font-size:10px;color:' + lc + '">' + (lat > 0 ? lat.toFixed(1) : '—') + '</td>';
            }
            h += '</tr>';

            h += '<tr style="background:var(--bg-2)"><td style="padding:4px 8px;font-weight:600;color:var(--text-2)">Status</td>';
            for (var ci = 0; ci < servers.length; ci++) {
                var rc = servers[ci].rcode || 'ERROR';
                var rcc = rc === 'NOERROR' ? 'var(--success)' : (rc === 'NXDOMAIN' ? 'var(--danger)' : 'var(--warning)');
                h += '<td style="text-align:center;padding:4px;font-size:10px;font-weight:600;color:' + rcc + '">' + rc + '</td>';
            }
            h += '</tr>';

            for (var uri = 0; uri < uniqueRecords.length; uri++) {
                var rec = uniqueRecords[uri];
                var bg = uri % 2 === 0 ? '' : 'background:var(--bg-2)';
                var hasCount = 0;
                for (var ci = 0; ci < servers.length; ci++) {
                    if (servers[ci].records) {
                        for (var rci = 0; rci < servers[ci].records.length; rci++) {
                            if (servers[ci].records[rci].type === rec.type && servers[ci].records[rci].value === rec.value) { hasCount++; break; }
                        }
                    }
                }
                var isPartial = hasCount > 0 && hasCount < servers.length;
                h += '<tr style="' + bg + '"><td style="padding:4px 8px;font-family:JetBrains Mono,monospace;font-size:10px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:200px" title="' + rec.value + '">';
                h += '<span style="color:var(--text-2);margin-right:4px">' + rec.type + '</span>' + rec.value + '</td>';
                for (var ci = 0; ci < servers.length; ci++) {
                    var found = false;
                    if (servers[ci].records) {
                        for (var rci = 0; rci < servers[ci].records.length; rci++) {
                            if (servers[ci].records[rci].type === rec.type && servers[ci].records[rci].value === rec.value) { found = true; break; }
                        }
                    }
                    if (found) {
                        h += '<td style="text-align:center;padding:4px;color:var(--success)">✓</td>';
                    } else {
                        h += '<td style="text-align:center;padding:4px;color:' + (isPartial ? 'var(--danger)' : 'var(--text-3)') + '">' + (isPartial ? '✗' : '—') + '</td>';
                    }
                }
                h += '</tr>';
            }
            h += '</tbody></table></div>';
        }

        // Per-server detail cards
        h += '<div style="font-size:12px;font-weight:600;color:var(--text-2);padding:14px 20px 6px">Server Details</div>';

        for (var si = 0; si < servers.length; si++) {
            var srv = servers[si];
            var srvName = srv.server;
            var latencyStr = srv.latency_ms > 0 ? srv.latency_ms.toFixed(1) + ' ms' : '—';
            var isFastest = srv.latency_ms > 0 && Math.abs(srv.latency_ms - fastestLatency) < 0.1;
            var rcodeColor = srv.rcode === 'NOERROR' ? 'var(--success)' : (srv.rcode === 'NXDOMAIN' ? 'var(--danger)' : 'var(--warning)');
            var latencyColor = isFastest ? 'var(--success)' : (srv.latency_ms > 100 ? 'var(--warning)' : 'var(--text-2)');

            h += '<div class="dns-check-server">';

            h += '<div class="dns-check-server-header">';
            h += '<div style="display:flex;align-items:center;gap:8px">';
            h += '<span class="dns-check-server-dot" style="background:' + rcodeColor + '"></span>';
            h += '<span style="font-size:13px;font-weight:600;color:var(--text-0)">' + srvName + '</span>';
            if (srv.ad) h += '<span class="dns-check-badge dnssec">DNSSEC</span>';
            if (srv.truncated) h += '<span class="dns-check-badge truncated">TRUNCATED</span>';
            h += '</div>';
            h += '<div style="display:flex;align-items:center;gap:16px">';
            h += '<span class="dns-check-rcode" style="color:' + rcodeColor + '">' + (srv.rcode || 'ERROR') + '</span>';
            h += '<span class="dns-check-latency" style="color:' + latencyColor + '">' + latencyStr + (isFastest ? ' ⚡' : '') + '</span>';
            h += '</div>';
            h += '</div>';

            if (srv.error) {
                h += '<div class="dns-check-error">' + srv.error + '</div>';
            }

            if (srv.records && srv.records.length) {
                h += '<div class="dns-check-records">';
                for (var ri = 0; ri < srv.records.length; ri++) {
                    var rec = srv.records[ri];
                    var isUnique = allValues[rec.value] === 1 && Object.keys(allValues).length > 1;
                    h += '<div class="dns-check-record' + (isUnique ? ' unique' : '') + '">';
                    h += '<span class="dns-check-rec-type">' + rec.type + '</span>';
                    h += '<span class="dns-check-rec-value">' + rec.value + '</span>';
                    h += '<span class="dns-check-rec-ttl">' + (rec.ttl > 0 ? rec.ttl + 's' : '—') + '</span>';
                    h += '</div>';
                }
                h += '</div>';
            } else if (!srv.error) {
                h += '<div style="padding:8px 16px;font-size:12px;color:var(--text-2)">No records returned</div>';
            }

            h += '</div>';
        }
        body.innerHTML = h;
    }

    // ── Public IP ──
    window._runPublicIPCheck = function() {
        var iface = selectedIface('pubIPIfaceSelect');
        var btn = document.getElementById('pubIPBtn');
        var out = document.getElementById('pubIPResult');
        btn.disabled = true;
        btn.innerHTML = '<span class="speedtest-spinner"></span> Checking...';
        out.textContent = '';
        out.style.color = '';

        fetch('/api/debug/publicip' + (iface ? '?iface=' + encodeURIComponent(iface) : ''))
        .then(function(r) { return r.json(); })
        .then(function(data) {
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg> Check';
            if (data.error) {
                out.style.color = 'var(--danger)';
                out.textContent = data.error;
            } else {
                out.style.color = 'var(--success)';
                out.textContent = data.ip + (data.interface ? ' (via ' + data.interface + ')' : '');
            }
        }).catch(function() {
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg> Check';
            out.style.color = 'var(--danger)';
            out.textContent = 'Connection error';
        });
    };

    // ── TCP Port Check ──
    window._runTCPCheck = function() {
        var target = (document.getElementById('tcpCheckTarget').value || '').trim();
        if (!target) { alert('Enter a host:port'); return; }
        var iface = selectedIface('tcpCheckIfaceSelect');
        var btn = document.getElementById('tcpCheckBtn');
        var out = document.getElementById('tcpCheckResult');
        btn.disabled = true;
        btn.innerHTML = '<span class="speedtest-spinner"></span> Testing...';
        out.textContent = '';
        out.style.color = '';

        fetch('/api/debug/tcpcheck?target=' + encodeURIComponent(target) + (iface ? '&iface=' + encodeURIComponent(iface) : ''))
        .then(function(r) { return r.json(); })
        .then(function(data) {
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><path d="M4 17V7a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z"/><path d="M4 8h16"/></svg> Test';
            if (data.success) {
                out.style.color = 'var(--success)';
                out.textContent = '✓ Connected in ' + data.elapsed_ms.toFixed(1) + ' ms' + (data.interface ? ' (via ' + data.interface + ')' : '');
            } else {
                out.style.color = 'var(--danger)';
                out.textContent = '✗ ' + (data.error || 'Connection failed');
            }
        }).catch(function() {
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><path d="M4 17V7a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z"/><path d="M4 8h16"/></svg> Test';
            out.style.color = 'var(--danger)';
            out.textContent = 'Connection error';
        });
    };
})();
