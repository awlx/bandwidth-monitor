(function() {
    'use strict';
    var BM = window.BM;

    // ── Host Detail Modal ──
    window._openHostModal = function(ip, mac) {
        var modal = document.getElementById('hostModal');
        var body = document.getElementById('hostModalBody');
        var title = document.getElementById('hostModalTitle');
        var subtitle = document.getElementById('hostModalSubtitle');
        modal.style.display = '';
        modal.dataset.ip = ip;
        modal.dataset.mac = mac || '';
        title.textContent = ip;
        subtitle.textContent = 'Loading…';
        body.innerHTML = '<div style="text-align:center;padding:32px;color:var(--text-2)">Loading…</div>';
        document.body.style.overflow = 'hidden';

        fetch('/api/host?ip=' + encodeURIComponent(ip))
            .then(function(r) { return r.json(); })
            .then(function(d) {
                d._mac = mac || '';
                renderHostModal(d, title, subtitle, body);
                loadHostDNSLog(ip, body);
            })
            .catch(function(e) { body.innerHTML = '<div style="text-align:center;padding:32px;color:var(--danger)">Failed to load: ' + e + '</div>'; });
    };

    window._renameHostDevice = function() {
        var modal = document.getElementById('hostModal');
        var ip = modal.dataset.ip;
        var mac = modal.dataset.mac;
        var key = BM.deviceLabelKey(mac, ip ? [ip] : []);
        if (!key) return;
        var current = BM.getDeviceLabel(key);
        var name = window.prompt('Custom name for ' + (mac || ip) + ':', current);
        if (name === null) return;
        BM.setDeviceLabel(key, name);
        var title = document.getElementById('hostModalTitle');
        var d = title._lastData;
        if (d) {
            var flag = d.country ? BM.countryFlag(d.country) + ' ' : '';
            title.innerHTML = flag + BM.escSvg(name.trim() || d.hostname || d.ip) + renamePencilHtml();
        }
        if (window._refreshAllViews) window._refreshAllViews();
    };

    function renamePencilHtml() {
        return ' <span class="device-name-edit" title="Rename this device" onclick="window._renameHostDevice()">&#9998;</span>';
    }

    window._closeHostModal = function() {
        document.getElementById('hostModal').style.display = 'none';
        document.body.style.overflow = '';
    };

    // Close on Escape key
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape' && document.getElementById('hostModal').style.display !== 'none') {
            window._closeHostModal();
        }
    });

    function renderHostModal(d, titleEl, subtitleEl, bodyEl) {
        var flag = d.country ? BM.countryFlag(d.country) + ' ' : '';
        var displayName = BM.deviceDisplayName(d._mac, [d.ip], d.hostname) || d.hostname || d.ip;
        titleEl.innerHTML = flag + BM.escSvg(displayName) + renamePencilHtml();
        titleEl._lastData = d;
        var sub = d.ip;
        if (d.city && d.country_name) sub += ' \u00b7 ' + d.city + ', ' + d.country_name;
        else if (d.country_name) sub += ' \u00b7 ' + d.country_name;
        if (d.as_org) sub += ' \u00b7 AS' + (d.asn || '') + ' ' + d.as_org;
        subtitleEl.textContent = sub;

        var h = '';

        // Stats grid
        h += '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(110px,1fr));gap:16px;padding:4px 0 20px;border-bottom:1px solid var(--border);margin-bottom:20px">';
        function stat(label, value, color) {
            return '<div><div style="font-size:11px;color:var(--text-2);margin-bottom:4px">' + label + '</div><div style="font-size:18px;font-weight:700;font-variant-numeric:tabular-nums' + (color ? ';color:var(--' + color + ')' : '') + '">' + value + '</div></div>';
        }
        h += stat('Total', BM.formatBytes(d.total_bytes));
        h += stat('RX', BM.formatBytes(d.rx_bytes), 'rx');
        h += stat('TX', BM.formatBytes(d.tx_bytes), 'tx');
        h += stat('Rate', BM.formatRate(d.rate_bytes));
        h += stat('RX Rate', BM.formatRate(d.rx_rate), 'rx');
        h += stat('TX Rate', BM.formatRate(d.tx_rate), 'tx');
        h += stat('Packets', (d.packets || 0).toLocaleString());
        h += stat('Connections', d.connections ? d.connections.length.toLocaleString() : '0');
        h += '</div>';

        // Bandwidth history chart (SVG)
        if (d.history && d.history.length > 1) {
            h += '<div style="margin-bottom:20px">';
            h += '<div style="font-size:14px;font-weight:600;margin-bottom:10px">Traffic Volume <span style="font-weight:400;color:var(--text-2);font-size:12px">(24h, per minute)</span></div>';
            h += renderHostHistoryChart(d.history);
            h += '</div>';
        }

        // Connection table
        if (d.connections && d.connections.length > 0) {
            h += '<div style="font-size:14px;font-weight:600;margin-bottom:10px">Active Connections <span style="font-weight:400;color:var(--text-2);font-size:12px">(' + d.connections.length + ')</span></div>';
            h += '<div style="overflow-x:auto;max-height:350px;overflow-y:auto;border:1px solid var(--border);border-radius:8px">';
            h += '<table style="width:100%;font-size:12px;border-collapse:collapse"><thead><tr style="background:var(--bg-2);position:sticky;top:0">';
            h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">Proto</th>';
            h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">State</th>';
            h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">Source</th>';
            h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">Destination</th>';
            h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">NAT</th>';
            h += '<th style="padding:8px 10px;text-align:right;font-size:11px;font-weight:600;color:var(--text-2)">Bytes</th>';
            h += '<th style="padding:8px 10px;text-align:right;font-size:11px;font-weight:600;color:var(--text-2)">Pkts</th>';
            h += '</tr></thead><tbody>';
            for (var i = 0; i < d.connections.length; i++) {
                var c = d.connections[i];
                var rowBg = i % 2 === 0 ? '' : ' style="background:var(--bg-1)"';
                var srcAddr = c.orig_src + (c.orig_sport ? ':' + c.orig_sport : '');
                var srcInfo = [];
                if (c.orig_src_host) srcInfo.push(c.orig_src_host);
                if (c.orig_src_city && c.orig_src_geo) srcInfo.push(BM.countryFlag(c.orig_src_geo) + ' ' + c.orig_src_city);
                else if (c.orig_src_geo) srcInfo.push(BM.countryFlag(c.orig_src_geo) + ' ' + c.orig_src_geo);
                if (c.orig_src_asn) srcInfo.push(c.orig_src_asn);
                var srcHtml = '<div style="font-family:var(--font-mono,monospace);font-size:11px">' + srcAddr + '</div>';
                if (srcInfo.length) srcHtml += '<div style="font-size:9px;color:var(--text-2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:200px">' + srcInfo.join(' &middot; ') + '</div>';
                var dstAddr = c.orig_dst + (c.orig_dport ? ':' + c.orig_dport : '');
                var dstInfo = [];
                if (c.orig_dst_host) dstInfo.push(c.orig_dst_host);
                if (c.orig_dst_city && c.orig_dst_geo) dstInfo.push(BM.countryFlag(c.orig_dst_geo) + ' ' + c.orig_dst_city);
                else if (c.orig_dst_geo) dstInfo.push(BM.countryFlag(c.orig_dst_geo) + ' ' + c.orig_dst_geo);
                if (c.orig_dst_asn) dstInfo.push(c.orig_dst_asn);
                var dstHtml = '<div style="font-family:var(--font-mono,monospace);font-size:11px">' + dstAddr + '</div>';
                if (dstInfo.length) dstHtml += '<div style="font-size:9px;color:var(--text-2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:200px">' + dstInfo.join(' &middot; ') + '</div>';
                var familyBadge = c.family === 'ipv6' ? '<span style="font-size:9px;color:var(--text-2);background:var(--bg-2);padding:1px 4px;border-radius:3px;margin-left:4px">v6</span>' : '';
                h += '<tr' + rowBg + '>';
                h += '<td style="padding:6px 10px">' + (c.protocol || '').toUpperCase() + familyBadge + '</td>';
                h += '<td style="padding:6px 10px">' + (c.state || '—') + '</td>';
                h += '<td style="padding:6px 10px">' + srcHtml + '</td>';
                h += '<td style="padding:6px 10px">' + dstHtml + '</td>';
                h += '<td style="padding:6px 10px">' + (c.nat_type || 'none') + '</td>';
                h += '<td style="padding:6px 10px;text-align:right;font-variant-numeric:tabular-nums">' + BM.formatBytes(c.bytes || 0) + '</td>';
                h += '<td style="padding:6px 10px;text-align:right;font-variant-numeric:tabular-nums;color:var(--text-2)">' + (c.packets ? c.packets.toLocaleString() : '—') + '</td>';
                h += '</tr>';
            }
            h += '</tbody></table></div>';
        } else {
            h += '<div style="text-align:center;padding:20px;color:var(--text-2);font-size:13px">No active connections tracked for this host</div>';
        }

        // Recent DNS queries — filled in asynchronously by loadHostDNSLog()
        // once /api/host/dns responds, since it may hit a slower remote
        // DNS provider API rather than in-memory state like the rest of
        // this modal.
        h += '<div id="hostDNSSection" style="margin-top:20px"></div>';

        bodyEl.innerHTML = h;
    }

    function loadHostDNSLog(ip, bodyEl) {
        var section = bodyEl.querySelector('#hostDNSSection');
        if (!section) return;
        fetch('/api/host/dns?ip=' + encodeURIComponent(ip))
            .then(function(r) { return r.json(); })
            .then(function(d) {
                if (!d || !d.available || !d.queries || !d.queries.length) return;
                var h = '<div style="font-size:14px;font-weight:600;margin-bottom:10px">Recent DNS Queries <span style="font-weight:400;color:var(--text-2);font-size:12px">(' + d.queries.length + ')</span></div>';
                h += '<div style="overflow-x:auto;max-height:250px;overflow-y:auto;border:1px solid var(--border);border-radius:8px">';
                h += '<table style="width:100%;font-size:12px;border-collapse:collapse"><thead><tr style="background:var(--bg-2);position:sticky;top:0">';
                h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">Time</th>';
                h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">Domain</th>';
                h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">Type</th>';
                h += '<th style="padding:8px 10px;text-align:left;font-size:11px;font-weight:600;color:var(--text-2)">Result</th>';
                h += '</tr></thead><tbody>';
                for (var i = 0; i < d.queries.length; i++) {
                    var q = d.queries[i];
                    var rowBg = i % 2 === 0 ? '' : ' style="background:var(--bg-1)"';
                    var t = q.time ? new Date(q.time) : null;
                    var timeStr = t && !isNaN(t.getTime()) ? t.toLocaleTimeString() : '—';
                    var resultHtml = q.blocked
                        ? '<span style="color:var(--danger)">Blocked</span>'
                        : '<span style="color:var(--text-2)">Allowed</span>';
                    h += '<tr' + rowBg + '>';
                    h += '<td style="padding:6px 10px;white-space:nowrap;color:var(--text-2)">' + timeStr + '</td>';
                    h += '<td style="padding:6px 10px;font-family:var(--font-mono,monospace);font-size:11px">' + BM.escSvg(q.domain || '') + '</td>';
                    h += '<td style="padding:6px 10px;color:var(--text-2)">' + (q.type || '—') + '</td>';
                    h += '<td style="padding:6px 10px">' + resultHtml + '</td>';
                    h += '</tr>';
                }
                h += '</tbody></table></div>';
                section.innerHTML = h;
            })
            .catch(function() { /* DNS provider unavailable or doesn't support per-client logs; leave section empty */ });
    }

    function renderHostHistoryChart(history) {
        var W = 760, H = 175, ML = 55, PT = 8, PB = 28;
        var chartW = W - ML;
        var maxVal = 1;
        for (var i = 0; i < history.length; i++) {
            if (history[i].bytes > maxVal) maxVal = history[i].bytes;
        }
        maxVal *= 1.2;

        function yPx(val) { return PT + (1 - val / maxVal) * (H - PT - PB); }

        var svg = '<svg viewBox="0 0 ' + W + ' ' + H + '" style="width:100%;height:' + H + 'px;display:block;border-radius:6px;background:var(--bg-1)">';

        var yTicks = [0, maxVal * 0.5, maxVal];
        for (var yi = 0; yi < yTicks.length; yi++) {
            var y = yPx(yTicks[yi]);
            svg += '<line x1="' + ML + '" y1="' + y.toFixed(1) + '" x2="' + W + '" y2="' + y.toFixed(1) + '" stroke="var(--text-3)" stroke-width="0.5" opacity="0.25"/>';
            svg += '<text x="' + (ML - 3) + '" y="' + (y + 3) + '" text-anchor="end" fill="var(--text-2)" font-size="9px">' + BM.formatBytes(yTicks[yi]) + '</text>';
        }

        var rxPts = [], txPts = [];
        for (var i = 0; i < history.length; i++) {
            var x = ML + (i / (history.length - 1)) * chartW;
            rxPts.push({ x: x, y: yPx(history[i].rx_bytes || 0) });
            txPts.push({ x: x, y: yPx(history[i].tx_bytes || 0) });
        }

        function smoothPath(pts) {
            if (pts.length < 2) return '';
            var p = 'M' + pts[0].x.toFixed(1) + ',' + pts[0].y.toFixed(1);
            for (var i = 1; i < pts.length; i++) {
                var cx = (pts[i-1].x + pts[i].x) / 2;
                p += ' C' + cx.toFixed(1) + ',' + pts[i-1].y.toFixed(1) + ' ' + cx.toFixed(1) + ',' + pts[i].y.toFixed(1) + ' ' + pts[i].x.toFixed(1) + ',' + pts[i].y.toFixed(1);
            }
            return p;
        }

        function smoothFill(pts) {
            if (pts.length < 2) return '';
            var p = 'M' + pts[0].x.toFixed(1) + ',' + (H - PB) + ' L' + pts[0].x.toFixed(1) + ',' + pts[0].y.toFixed(1);
            for (var i = 1; i < pts.length; i++) {
                var cx = (pts[i-1].x + pts[i].x) / 2;
                p += ' C' + cx.toFixed(1) + ',' + pts[i-1].y.toFixed(1) + ' ' + cx.toFixed(1) + ',' + pts[i].y.toFixed(1) + ' ' + pts[i].x.toFixed(1) + ',' + pts[i].y.toFixed(1);
            }
            p += ' L' + pts[pts.length-1].x.toFixed(1) + ',' + (H - PB) + ' Z';
            return p;
        }

        svg += '<path d="' + smoothFill(txPts) + '" fill="#a78bfa" opacity="0.1"/>';
        svg += '<path d="' + smoothPath(txPts) + '" fill="none" stroke="#a78bfa" stroke-width="1.5" stroke-linejoin="round"/>';
        svg += '<path d="' + smoothFill(rxPts) + '" fill="#22d3ee" opacity="0.1"/>';
        svg += '<path d="' + smoothPath(rxPts) + '" fill="none" stroke="#22d3ee" stroke-width="1.5" stroke-linejoin="round"/>';

        svg += '<circle cx="' + (W - 48) + '" cy="' + (PT + 6) + '" r="3" fill="#22d3ee"/>';
        svg += '<text x="' + (W - 42) + '" y="' + (PT + 9) + '" fill="var(--text-2)" font-size="8px">RX</text>';
        svg += '<circle cx="' + (W - 22) + '" cy="' + (PT + 6) + '" r="3" fill="#a78bfa"/>';
        svg += '<text x="' + (W - 16) + '" y="' + (PT + 9) + '" fill="var(--text-2)" font-size="8px">TX</text>';

        if (history.length > 1 && history[0].ts) {
            var pad2 = function(n) { return n < 10 ? '0' + n : '' + n; };
            var timeFmt = function(d) { return pad2(d.getHours()) + ':' + pad2(d.getMinutes()); };
            var nLabels = Math.min(6, Math.max(2, Math.floor(history.length / 60)));
            for (var li = 0; li < nLabels; li++) {
                var frac = li / (nLabels - 1);
                var idx = Math.round(frac * (history.length - 1));
                var lx = ML + (idx / (history.length - 1)) * chartW;
                var anchor = li === 0 ? 'start' : (li === nLabels - 1 ? 'end' : 'middle');
                svg += '<text x="' + lx.toFixed(1) + '" y="' + (H - 3) + '" text-anchor="' + anchor + '" fill="var(--text-3)" font-size="8px">' + timeFmt(new Date(history[idx].ts)) + '</text>';
            }
        }

        svg += '</svg>';
        return svg;
    }

    // Make IPs clickable in talkers tables (delegated click handler)
    document.addEventListener('click', function(e) {
        var el = e.target.closest('.ip-clickable');
        if (el) {
            e.preventDefault();
            window._openHostModal(el.getAttribute('data-ip'));
        }
    });
})();
