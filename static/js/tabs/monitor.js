(function() {
    'use strict';
    var BM = window.BM;

    // ── Country centroids ──
    var countryCentroids = {"AF":[33,65],"AL":[41,20],"DZ":[28,3],"AO":[-12,17],"AR":[-34,-64],"AM":[40,45],"AU":[-25,134],"AT":[47,14],"AZ":[41,48],"BD":[24,90],"BY":[53,28],"BE":[51,4],"BJ":[9,2],"BO":[-17,-65],"BA":[44,18],"BW":[-22,24],"BR":[-10,-55],"BG":[43,25],"BF":[12,-2],"KH":[13,105],"CM":[6,12],"CA":[56,-96],"CF":[7,21],"TD":[15,19],"CL":[-30,-71],"CN":[35,105],"CO":[4,-72],"CD":[-3,24],"CG":[-1,15],"CR":[10,-84],"CI":[8,-5],"HR":[45,16],"CU":[22,-80],"CY":[35,33],"CZ":[50,15],"DK":[56,10],"DO":[19,-70],"EC":[-2,-78],"EG":[27,30],"SV":[14,-89],"EE":[59,26],"ET":[9,40],"FI":[64,26],"FR":[46,2],"GA":[0,12],"DE":[51,9],"GH":[8,-2],"GR":[39,22],"GT":[16,-90],"GN":[11,-12],"HT":[19,-72],"HN":[15,-87],"HU":[47,20],"IS":[65,-18],"IN":[21,78],"ID":[-5,120],"IR":[32,53],"IQ":[33,44],"IE":[53,-8],"IL":[31,35],"IT":[43,12],"JM":[18,-77],"JP":[36,138],"JO":[31,37],"KZ":[48,68],"KE":[-1,38],"KW":[29,48],"KG":[41,75],"LA":[18,105],"LV":[57,25],"LB":[34,36],"LY":[27,17],"LT":[56,24],"LU":[50,6],"MG":[-19,47],"MY":[4,109],"ML":[17,-4],"MX":[23,-102],"MD":[47,29],"MN":[48,106],"ME":[43,19],"MA":[32,-5],"MZ":[-18,35],"MM":[22,96],"NA":[-22,17],"NP":[28,84],"NL":[52,5],"NZ":[-41,174],"NI":[13,-85],"NE":[18,8],"NG":[10,8],"KP":[40,127],"NO":[62,10],"OM":[21,57],"PK":[30,70],"PA":[9,-80],"PY":[-23,-58],"PE":[-10,-76],"PH":[13,122],"PL":[52,20],"PT":[39,-8],"QA":[25,51],"RO":[46,25],"RU":[62,105],"RW":[-2,30],"SA":[24,45],"SN":[14,-14],"RS":[44,21],"SG":[1,104],"SK":[49,20],"SI":[46,15],"ZA":[-29,24],"KR":[36,128],"ES":[40,-4],"LK":[8,81],"SD":[13,30],"SE":[62,16],"CH":[47,8],"SY":[35,38],"TW":[24,121],"TJ":[39,69],"TZ":[-7,35],"TH":[15,101],"TN":[34,9],"TR":[39,35],"TM":[39,60],"UA":[49,32],"AE":[24,54],"GB":[54,-2],"US":[38,-97],"UY":[-33,-56],"UZ":[41,65],"VE":[8,-66],"VN":[16,108],"YE":[16,48],"ZM":[-14,28],"ZW":[-19,30]};

    // ── World Traffic Map ──
    BM.updateWorldMap = function(countries, topBW, originCountry, originLat, originLon) {
        if (!countries || !countries.length) return;
        var wc = window._worldCountries || {};

        var container = document.getElementById('worldMapContainer');
        var W = container.clientWidth || 800;
        var H = Math.round(W * 0.5);

        function proj(lat, lon) {
            return [(lon + 180) / 360 * W, (90 - lat) / 180 * H];
        }

        var trafficByCC = {};
        var maxBytes = 1;
        for (var i = 0; i < countries.length; i++) {
            var c = countries[i];
            trafficByCC[c.country] = c;
            if (c.bytes > maxBytes) maxBytes = c.bytes;
        }

        var isDark = (function() {
            var t = document.documentElement.getAttribute('data-theme');
            if (t === 'dark') return true;
            if (t === 'light') return false;
            return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
        })();

        var oceanBg = isDark ? '#0c1929' : '#eaeff6';
        var landDefault = isDark ? '#1c2e4a' : '#c5cdd9';
        var landStroke = isDark ? '#263d5e' : '#a8b2bf';
        var activeColor = '#22d3ee';
        var flowColor = '#a78bfa';
        var labelColor = isDark ? '#0c1929' : '#fff';

        var svg = '<svg viewBox="0 0 ' + W + ' ' + H + '" style="width:100%;height:100%;border-radius:8px">';
        svg += '<defs><filter id="glow"><feGaussianBlur stdDeviation="2" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>';
        svg += '<rect width="' + W + '" height="' + H + '" fill="' + oceanBg + '" rx="8"/>';

        // Grid
        svg += '<g stroke="' + (isDark ? '#1a2a45' : '#d5dde8') + '" stroke-width="0.5" opacity="0.5">';
        for (var lon = -150; lon <= 180; lon += 30) { var gx = (lon + 180) / 360 * W; svg += '<line x1="' + gx + '" y1="0" x2="' + gx + '" y2="' + H + '"/>'; }
        for (var lat = -60; lat <= 80; lat += 30) { var gy = (90 - lat) / 180 * H; svg += '<line x1="0" y1="' + gy + '" x2="' + W + '" y2="' + gy + '"/>'; }
        svg += '</g>';

        // Country shapes
        var activeCCs = {};
        for (var cc in wc) {
            var polys = wc[cc];
            var traffic = trafficByCC[cc];
            var fill = landDefault, stroke = landStroke, sw = '0.3', fo = '1';
            if (traffic) {
                var ratio = traffic.bytes / maxBytes;
                fill = activeColor; fo = '' + (0.3 + ratio * 0.7).toFixed(2);
                stroke = activeColor; sw = '0.5';
                activeCCs[cc] = { ratio: ratio, traffic: traffic };
            }
            for (var pi = 0; pi < polys.length; pi++) {
                var ring = polys[pi];
                if (ring.length < 3) continue;
                var segments = [[]];
                segments[0].push(ring[0]);
                for (var ri = 1; ri < ring.length; ri++) {
                    if (Math.abs(ring[ri][0] - ring[ri-1][0]) > 170) {
                        segments.push([]);
                    }
                    segments[segments.length - 1].push(ring[ri]);
                }
                for (var si = 0; si < segments.length; si++) {
                    var seg = segments[si];
                    if (seg.length < 3) continue;
                    var d = '';
                    for (var ri = 0; ri < seg.length; ri++) {
                        var p = proj(seg[ri][1], seg[ri][0]);
                        d += (ri === 0 ? 'M' : 'L') + p[0].toFixed(0) + ',' + p[1].toFixed(0);
                    }
                    d += 'Z';
                    svg += '<path d="' + d + '" fill="' + fill + '" fill-opacity="' + fo + '" stroke="' + stroke + '" stroke-width="' + sw + '"';
                    if (traffic) {
                        svg += ' filter="url(#glow)" class="map-tip country-clickable" style="cursor:pointer" data-cc="' + cc + '" data-tip="' + BM.countryFlag(cc) + ' ' + (traffic.country_name || cc) + ': ' + BM.formatBytes(traffic.bytes) + ' (' + traffic.connections + ' IPs) \u2014 click to find in Top Talkers"';
                    }
                    svg += '/>';
                }
            }
        }

        // Flow lines
        if (topBW && topBW.length) {
            var oc = originCountry && countryCentroids[originCountry] ? originCountry : 'DE';
            var center = (originLat && originLon) ? proj(originLat, originLon) : proj(countryCentroids[oc][0], countryCentroids[oc][1]);
            svg += '<circle cx="' + center[0] + '" cy="' + center[1] + '" r="3" fill="' + flowColor + '" opacity="0.8"><animate attributeName="r" values="2;6;2" dur="2s" repeatCount="indefinite"/></circle>';

            var rxColor = isDark ? '#34d399' : '#059669';
            var txColor = isDark ? '#fb923c' : '#ea580c';

            var maxRate = 1;
            for (var i = 0; i < Math.min(topBW.length, 8); i++) {
                var rx = topBW[i].rx_rate || 0;
                var tx = topBW[i].tx_rate || 0;
                if (rx > maxRate) maxRate = rx;
                if (tx > maxRate) maxRate = tx;
            }
            var ccFlowIdx = {};
            for (var i = 0; i < Math.min(topBW.length, 8); i++) {
                var t = topBW[i];
                if (!t.country || !countryCentroids[t.country]) continue;
                var cc = t.country;
                if (!ccFlowIdx[cc]) ccFlowIdx[cc] = 0;
                var fi = ccFlowIdx[cc]++;
                var dest = (t.lat && t.lon) ? proj(t.lat, t.lon) : proj(countryCentroids[cc][0], countryCentroids[cc][1]);
                var curveOffset = 35 + fi * 18;
                var spreadX = (fi - 0.5) * 25;
                var mx = (center[0] + dest[0]) / 2 + spreadX;
                var my = Math.min(center[1], dest[1]) - curveOffset;
                var dur = (1.3 + fi * 0.2).toFixed(1);
                var host = t.hostname && t.hostname !== t.ip ? t.hostname + ' (' + t.ip + ')' : t.ip;
                var asInfo = t.as_org ? ' \u00b7 AS' + (t.asn || '') + ' ' + t.as_org : '';
                var rxRate = t.rx_rate || 0;
                var txRate = t.tx_rate || 0;

                var ldx = dest[0] - center[0];
                var ldy = dest[1] - center[1];
                var lineLen = Math.sqrt(ldx * ldx + ldy * ldy) || 1;
                var perpX = -ldy / lineLen;
                var perpY = ldx / lineLen;
                var sep = Math.min(40, Math.max(20, lineLen * 0.08));

                if (rxRate > 0) {
                    var rxRatio = rxRate / maxRate;
                    var rxSw = (0.8 + rxRatio * 3.2).toFixed(1);
                    var rxOp = (0.25 + rxRatio * 0.55).toFixed(2);
                    var rxMx = mx + perpX * sep;
                    var rxMy = my + perpY * sep;
                    var rxPath = 'M' + dest[0] + ',' + dest[1] + ' Q' + rxMx.toFixed(1) + ',' + rxMy.toFixed(1) + ' ' + center[0] + ',' + center[1];
                    var rxTip = host + asInfo + ' \u2192 \u2193 ' + BM.formatRate(rxRate);
                    svg += '<path d="' + rxPath + '" fill="none" stroke="transparent" stroke-width="8" style="cursor:pointer" class="map-tip" data-tip="' + rxTip + '"/>';
                    svg += '<path d="' + rxPath + '" fill="none" stroke="' + rxColor + '" stroke-width="' + rxSw + '" stroke-dasharray="6,4" opacity="' + rxOp + '" stroke-linecap="round" style="pointer-events:none"><animate attributeName="stroke-dashoffset" from="0" to="-20" dur="' + dur + 's" repeatCount="indefinite"/></path>';
                }

                if (txRate > 0) {
                    var txRatio = txRate / maxRate;
                    var txSw = (0.8 + txRatio * 3.2).toFixed(1);
                    var txOp = (0.25 + txRatio * 0.55).toFixed(2);
                    var txMx = mx - perpX * sep;
                    var txMy = my - perpY * sep;
                    var txPath = 'M' + center[0] + ',' + center[1] + ' Q' + txMx.toFixed(1) + ',' + txMy.toFixed(1) + ' ' + dest[0] + ',' + dest[1];
                    var txTip = host + asInfo + ' \u2192 \u2191 ' + BM.formatRate(txRate);
                    svg += '<path d="' + txPath + '" fill="none" stroke="transparent" stroke-width="8" style="cursor:pointer" class="map-tip" data-tip="' + txTip + '"/>';
                    svg += '<path d="' + txPath + '" fill="none" stroke="' + txColor + '" stroke-width="' + txSw + '" stroke-dasharray="6,4" opacity="' + txOp + '" stroke-linecap="round" style="pointer-events:none"><animate attributeName="stroke-dashoffset" from="0" to="-20" dur="' + dur + 's" repeatCount="indefinite"/></path>';
                }

                var totalRate = rxRate + txRate;
                var dotRatio = totalRate / (maxRate * 2 || 1);
                var dotR = (2 + dotRatio * 4).toFixed(1);
                var dotColor = rxRate >= txRate ? rxColor : txColor;
                var dotOp = (0.3 + dotRatio * 0.5).toFixed(2);
                svg += '<circle cx="' + dest[0] + '" cy="' + dest[1] + '" r="' + dotR + '" fill="' + dotColor + '" opacity="' + dotOp + '" style="pointer-events:none"/>';
            }
        }

        // Country code labels
        for (var cc in activeCCs) {
            if (!countryCentroids[cc]) continue;
            var p = proj(countryCentroids[cc][0], countryCentroids[cc][1]);
            var fs = Math.max(7, Math.min(12, 7 + activeCCs[cc].ratio * 8));
            svg += '<text x="' + p[0] + '" y="' + (p[1] + fs * 0.35) + '" text-anchor="middle" fill="' + labelColor + '" font-size="' + fs.toFixed(0) + 'px" font-weight="700" style="pointer-events:none;text-shadow:0 1px 3px rgba(0,0,0,0.6)">' + cc + '</text>';
        }
        svg += '</svg>';
        container.innerHTML = svg;

        // Tooltip
        var tipEl = document.getElementById('mapTooltip');
        if (!tipEl) {
            tipEl = document.createElement('div');
            tipEl.id = 'mapTooltip';
            tipEl.style.cssText = 'position:fixed;pointer-events:none;background:var(--card);color:var(--text-0);border:1px solid var(--border);padding:6px 10px;border-radius:6px;font-size:12px;font-family:Inter,sans-serif;z-index:9999;display:none;white-space:nowrap;box-shadow:0 4px 12px rgba(0,0,0,0.15)';
            document.body.appendChild(tipEl);
        }
        // Bind hover listeners once: the container persists across re-renders
        // (only its innerHTML is replaced), so re-adding them every render leaked
        // a handler per render. Delegation via closest() keeps working after swaps.
        if (!container._tipBound) {
            container._tipBound = true;
            container.addEventListener('mousemove', function(e) {
                var el = e.target.closest('.map-tip');
                if (el) {
                    tipEl.textContent = el.getAttribute('data-tip');
                    tipEl.style.display = '';
                    tipEl.style.left = (e.clientX + 12) + 'px';
                    tipEl.style.top = (e.clientY - 28) + 'px';
                } else {
                    tipEl.style.display = 'none';
                }
            });
            container.addEventListener('mouseleave', function() {
                tipEl.style.display = 'none';
            });
            container.addEventListener('click', function(e) {
                var el = e.target.closest('.country-clickable');
                if (!el && document.elementsFromPoint) {
                    // Invisible wide hit-strokes used for flow-line tooltips can
                    // sit on top of a country's shape (e.g. near the origin,
                    // where many flow arcs converge), so target.closest() alone
                    // can miss the country underneath. Walk the full stack of
                    // elements under the click point instead.
                    var stack = document.elementsFromPoint(e.clientX, e.clientY);
                    for (var i = 0; i < stack.length; i++) {
                        if (stack[i].classList && stack[i].classList.contains('country-clickable')) { el = stack[i]; break; }
                    }
                }
                if (el && window._focusCountryTraffic) window._focusCountryTraffic(el.getAttribute('data-cc'));
            });
        }

        var heatLegend = document.getElementById('mapHeatLegend');
        if (heatLegend) heatLegend.style.display = countries.length ? 'flex' : 'none';

        // Zoom/pan transform
        var svgEl = container.querySelector('svg');
        if (svgEl && window._mapZoom) {
            svgEl.style.transformOrigin = '0 0';
            svgEl.style.transform = 'scale(' + window._mapZoom.scale + ') translate(' + window._mapZoom.tx + 'px,' + window._mapZoom.ty + 'px)';
        }

        // Country traffic table
        var tableWrap = document.getElementById('mapCountryTable');
        var tableEl = document.getElementById('mapCountryList');
        if (tableWrap && tableEl) {
            tableWrap.style.display = '';
            var total = 0;
            for (var i = 0; i < countries.length; i++) total += countries[i].bytes;
            var th = '';
            for (var i = 0; i < countries.length; i++) {
                var c = countries[i];
                var pct = total > 0 ? (c.bytes / total * 100) : 0;
                th += '<div style="display:flex;align-items:center;gap:6px;padding:3px 6px;border-radius:4px;background:var(--bg-1);cursor:pointer" onclick="window._focusCountryTraffic(\'' + c.country + '\')" title="Find in Top Talkers">';
                th += '<span style="width:20px;text-align:center">' + BM.countryFlag(c.country) + '</span>';
                th += '<span style="font-weight:600;width:24px">' + c.country + '</span>';
                th += '<div style="flex:1;height:6px;background:var(--bg-2);border-radius:3px;overflow:hidden"><div style="width:' + Math.max(2, pct).toFixed(1) + '%;height:100%;background:' + activeColor + ';border-radius:3px;opacity:0.7"></div></div>';
                th += '<span style="font-variant-numeric:tabular-nums;color:var(--text-2);min-width:55px;text-align:right">' + BM.formatBytes(c.bytes) + '</span>';
                th += '<span style="color:var(--text-3);min-width:38px;text-align:right">' + pct.toFixed(1) + '%</span>';
                th += '</div>';
            }
            tableEl.innerHTML = th;
        }
    };

    // Switches to the Traffic tab and scrolls/highlights the Top Talkers
    // rows matching a given country code, so clicking a country on the map
    // (or in the Active Countries list) helps find who's actually talking
    // to it.
    window._focusCountryTraffic = function(cc) {
        if (!cc) return;
        if (window._switchTab) window._switchTab('traffic');
        setTimeout(function() {
            var rows = document.querySelectorAll('#bwTable tr[data-country="' + cc + '"], #volTable tr[data-country="' + cc + '"]');
            if (!rows.length) return;
            rows[0].scrollIntoView({ behavior: 'smooth', block: 'center' });
            rows.forEach(function(r) {
                r.classList.add('country-focus-flash');
                setTimeout(function() { r.classList.remove('country-focus-flash'); }, 2000);
            });
        }, 60);
    };

    // ── Map zoom/pan ──
    (function initMapZoom() {
        window._mapZoom = { scale: 1.6, tx: -120, ty: -40 };
        var mc = document.getElementById('worldMapContainer');
        if (!mc) return;

        var dragging = false, lastX = 0, lastY = 0;

        mc.addEventListener('wheel', function(e) {
            if (!e.ctrlKey && !e.metaKey) return;
            e.preventDefault();
            var z = window._mapZoom;
            var delta = e.deltaY > 0 ? 0.97 : 1.03;
            var newScale = Math.max(1, Math.min(6, z.scale * delta));
            var rect = mc.getBoundingClientRect();
            var mx = e.clientX - rect.left;
            var my = e.clientY - rect.top;
            z.tx = mx - (mx - z.tx) * (newScale / z.scale);
            z.ty = my - (my - z.ty) * (newScale / z.scale);
            z.scale = newScale;
            applyMapTransform(mc);
        }, { passive: false });

        mc.addEventListener('mousedown', function(e) {
            if (e.button !== 0) return;
            dragging = true;
            lastX = e.clientX;
            lastY = e.clientY;
            mc.style.cursor = 'grabbing';
            e.preventDefault();
        });
        window.addEventListener('mousemove', function(e) {
            if (!dragging) return;
            var z = window._mapZoom;
            z.tx += (e.clientX - lastX) / z.scale;
            z.ty += (e.clientY - lastY) / z.scale;
            lastX = e.clientX;
            lastY = e.clientY;
            applyMapTransform(mc);
        });
        window.addEventListener('mouseup', function() {
            if (dragging) {
                dragging = false;
                mc.style.cursor = 'grab';
            }
        });

        mc.addEventListener('dblclick', function(e) {
            e.preventDefault();
            window._mapZoom = { scale: 1.6, tx: -120, ty: -40 };
            applyMapTransform(mc);
        });
    })();

    function applyMapTransform(container) {
        var svgEl = container.querySelector('svg');
        if (!svgEl) return;
        var z = window._mapZoom;
        svgEl.style.transformOrigin = '0 0';
        svgEl.style.transform = 'scale(' + z.scale + ') translate(' + z.tx + 'px,' + z.ty + 'px)';
    }

    // ── Latency Monitor ──
    BM.updateLatency = function(targets) {
        var sec = document.getElementById('latencySection');
        if (!targets || !targets.length) return;

        var el = document.getElementById('latencyTargets');
        el.style.display = 'grid';
        el.style.gridTemplateColumns = 'repeat(auto-fill,minmax(380px,1fr))';
        el.style.gap = '16px';

        var h = '';
        for (var i = 0; i < targets.length; i++) {
            var t = targets[i];
            var statusColor = t.alive ? 'var(--success, #22c55e)' : 'var(--danger, #ef4444)';
            var statusText = t.alive ? 'UP' : 'DOWN';

            var hasV4 = t.ipv4 && t.ipv4 !== '';
            var hasV6 = t.ipv6 && t.ipv6 !== '';
            var dualStack = hasV4 && hasV6;

            h += '<div style="background:var(--bg-2);border-radius:8px;padding:16px;border:1px solid var(--border)">';

            h += '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">';
            h += '<div>';
            h += '<div style="font-weight:600;font-size:14px">' + t.name + '</div>';
            h += '<div style="font-size:10px;color:var(--text-2);font-family:var(--font-mono, monospace)">';
            if (hasV4) h += '<span style="background:var(--bg-1);padding:1px 4px;border-radius:3px;margin-right:4px">v4 ' + t.ipv4 + '</span>';
            if (hasV6) h += '<span style="background:var(--bg-1);padding:1px 4px;border-radius:3px">v6 ' + t.ipv6 + '</span>';
            if (!hasV4 && !hasV6 && t.ip) h += t.ip;
            h += '</div>';
            h += '</div>';
            h += '<div style="display:flex;align-items:center;gap:6px">';
            if (dualStack) h += '<span style="font-size:9px;font-weight:600;color:var(--text-2);background:var(--bg-1);padding:1px 5px;border-radius:3px">DUAL</span>';
            h += '<span style="width:10px;height:10px;border-radius:50%;background:' + statusColor + ';display:inline-block"></span>';
            h += '<span style="font-size:12px;font-weight:700;color:' + statusColor + '">' + statusText + '</span>';
            h += '</div></div>';

            function statsRow(label, labelColor, s) {
                if (!s) return '';
                var r = '<div style="display:grid;grid-template-columns:auto repeat(6,1fr);gap:8px;text-align:center;align-items:center">';
                r += '<div style="text-align:left;font-size:11px;font-weight:700;color:' + labelColor + '">' + label + '</div>';
                var items = [
                    ['RTT', s.rtt_ms >= 0 ? s.rtt_ms.toFixed(1) : '—', 'ms'],
                    ['Avg', s.avg_rtt_ms > 0 ? s.avg_rtt_ms.toFixed(1) : '—', 'ms'],
                    ['P95', s.p95_rtt_ms > 0 ? s.p95_rtt_ms.toFixed(1) : '—', 'ms'],
                    ['P99', s.p99_rtt_ms > 0 ? s.p99_rtt_ms.toFixed(1) : '—', 'ms'],
                    ['Jitter', s.jitter_ms > 0 ? s.jitter_ms.toFixed(2) : '—', 'ms'],
                    ['Loss', s.loss_pct.toFixed(1), '%']
                ];
                for (var si = 0; si < items.length; si++) {
                    var lossStyle = items[si][0] === 'Loss' && s.loss_pct > 0 ? ';color:var(--danger)' : '';
                    r += '<div><div style="font-size:9px;color:var(--text-2);margin-bottom:1px">' + items[si][0] + '</div>';
                    r += '<div style="font-size:12px;font-weight:600;font-variant-numeric:tabular-nums' + lossStyle + '">';
                    r += items[si][1] + '<span style="font-size:8px;color:var(--text-2);margin-left:1px">' + items[si][2] + '</span></div></div>';
                }
                r += '</div>';
                return r;
            }

            h += '<div style="margin-bottom:14px;display:flex;flex-direction:column;gap:6px">';
            h += statsRow('ICMP', '#22d3ee', t.icmp_stats);
            h += statsRow('HTTPS', '#a78bfa', t.https_stats);
            h += '</div>';

            var icmpV4 = t.icmp_v4 && t.icmp_v4.length > 1 ? t.icmp_v4 : null;
            var icmpV6 = t.icmp_v6 && t.icmp_v6.length > 1 ? t.icmp_v6 : null;
            var icmpLegacy = t.icmp && t.icmp.length > 1 ? t.icmp : null;

            function latencySection(label, v4Data, v6Data, legacyData, v4Color, v6Color, marginBottom) {
                var mb = marginBottom ? 'margin-bottom:10px' : '';
                var out = '';
                if (v4Data && v6Data) {
                    out += '<div style="margin-bottom:10px">';
                    out += '<div style="font-size:11px;font-weight:600;color:var(--text-2);margin-bottom:4px">' + label + ' <span style="color:' + v4Color + '">IPv4</span></div>';
                    out += renderLatencyChart(v4Data, v4Color, v4Color);
                    out += '</div>';
                    out += '<div style="' + mb + '">';
                    out += '<div style="font-size:11px;font-weight:600;color:var(--text-2);margin-bottom:4px">' + label + ' <span style="color:' + v6Color + '">IPv6</span></div>';
                    out += renderLatencyChart(v6Data, v6Color, v6Color);
                    out += '</div>';
                } else if (v4Data) {
                    out += '<div style="' + mb + '">';
                    out += '<div style="font-size:11px;font-weight:600;color:var(--text-2);margin-bottom:4px">' + label + ' (IPv4)</div>';
                    out += renderLatencyChart(v4Data, v4Color, v4Color);
                    out += '</div>';
                } else if (v6Data) {
                    out += '<div style="' + mb + '">';
                    out += '<div style="font-size:11px;font-weight:600;color:var(--text-2);margin-bottom:4px">' + label + ' (IPv6)</div>';
                    out += renderLatencyChart(v6Data, v6Color, v6Color);
                    out += '</div>';
                } else if (legacyData) {
                    out += '<div style="' + mb + '">';
                    out += '<div style="font-size:11px;font-weight:600;color:var(--text-2);margin-bottom:4px">' + label + '</div>';
                    out += renderLatencyChart(legacyData, v4Color, v4Color);
                    out += '</div>';
                }
                return out;
            }

            h += latencySection('ICMP Ping', icmpV4, icmpV6, icmpLegacy, '#22d3ee', '#34d399', true);

            var httpsV4 = t.https_v4 && t.https_v4.length > 1 ? t.https_v4 : null;
            var httpsV6 = t.https_v6 && t.https_v6.length > 1 ? t.https_v6 : null;
            var httpsLegacy = t.https && t.https.length > 1 ? t.https : null;

            h += latencySection('HTTPS', httpsV4, httpsV6, httpsLegacy, '#a78bfa', '#fb923c', false);

            h += '</div>';
        }
        el.innerHTML = h;
    };

    function renderLatencyChart(points, strokeColor, fillHex) {
        var ML = 42;
        var W = 360, H = 72;
        var chartW = W - ML;
        var PT = 4, PB = 12;

        var maxRTT = 1;
        for (var i = 0; i < points.length; i++) {
            if (points[i].rtt > maxRTT) maxRTT = points[i].rtt;
        }
        maxRTT = Math.max(maxRTT * 1.2, 5);

        var yTicks = niceScale(0, maxRTT, 3);

        function yPx(val) { return PT + (1 - val / maxRTT) * (H - PT - PB); }

        var svg = '<svg viewBox="0 0 ' + W + ' ' + H + '" style="width:100%;height:' + H + 'px;display:block">';
        svg += '<rect x="' + ML + '" y="0" width="' + chartW + '" height="' + H + '" fill="var(--bg-1)" rx="4"/>';

        for (var yi = 0; yi < yTicks.length; yi++) {
            var yVal = yTicks[yi];
            var y = yPx(yVal);
            if (y < PT || y > H - PB) continue;
            svg += '<line x1="' + ML + '" y1="' + y.toFixed(1) + '" x2="' + W + '" y2="' + y.toFixed(1) + '" stroke="var(--text-3)" stroke-width="0.5" opacity="0.25"/>';
            var label = yVal < 10 ? yVal.toFixed(1) : Math.round(yVal);
            svg += '<text x="' + (ML - 3) + '" y="' + (y + 3) + '" text-anchor="end" fill="var(--text-2)" font-size="9px" style="font-variant-numeric:tabular-nums">' + label + '</text>';
        }
        svg += '<text x="2" y="9" fill="var(--text-3)" font-size="8px" font-weight="600">ms</text>';

        var pathPts = [];
        var lossDots = '';
        for (var i = 0; i < points.length; i++) {
            var x = ML + (i / Math.max(points.length - 1, 1)) * chartW;
            if (points[i].rtt < 0) {
                lossDots += '<line x1="' + x.toFixed(1) + '" y1="' + PT + '" x2="' + x.toFixed(1) + '" y2="' + (H - PB) + '" stroke="var(--danger, #ef4444)" stroke-width="1" opacity="0.12"/>';
                continue;
            }
            pathPts.push({ x: x, y: yPx(points[i].rtt) });
        }
        var path = '', fillPath = '';
        if (pathPts.length > 1) {
            path = 'M' + pathPts[0].x.toFixed(1) + ',' + pathPts[0].y.toFixed(1);
            for (var pi = 1; pi < pathPts.length; pi++) {
                var cx = (pathPts[pi-1].x + pathPts[pi].x) / 2;
                path += ' C' + cx.toFixed(1) + ',' + pathPts[pi-1].y.toFixed(1) + ' ' + cx.toFixed(1) + ',' + pathPts[pi].y.toFixed(1) + ' ' + pathPts[pi].x.toFixed(1) + ',' + pathPts[pi].y.toFixed(1);
            }
            fillPath = 'M' + pathPts[0].x.toFixed(1) + ',' + (H - PB);
            fillPath += ' L' + pathPts[0].x.toFixed(1) + ',' + pathPts[0].y.toFixed(1);
            for (var pi = 1; pi < pathPts.length; pi++) {
                var cx = (pathPts[pi-1].x + pathPts[pi].x) / 2;
                fillPath += ' C' + cx.toFixed(1) + ',' + pathPts[pi-1].y.toFixed(1) + ' ' + cx.toFixed(1) + ',' + pathPts[pi].y.toFixed(1) + ' ' + pathPts[pi].x.toFixed(1) + ',' + pathPts[pi].y.toFixed(1);
            }
            fillPath += ' L' + pathPts[pathPts.length-1].x.toFixed(1) + ',' + (H - PB) + ' Z';
        } else if (pathPts.length === 1) {
            path = 'M' + pathPts[0].x.toFixed(1) + ',' + pathPts[0].y.toFixed(1);
        }

        svg += lossDots;
        if (fillPath) {
            svg += '<path d="' + fillPath + '" fill="' + fillHex + '" opacity="0.1"/>';
        }
        if (path) {
            svg += '<path d="' + path + '" fill="none" stroke="' + strokeColor + '" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>';
        }

        svg += '<text x="' + (ML + 3) + '" y="' + (H - 1) + '" fill="var(--text-3)" font-size="8px">15m ago</text>';
        svg += '<text x="' + (W - 3) + '" y="' + (H - 1) + '" text-anchor="end" fill="var(--text-3)" font-size="8px">now</text>';

        svg += '</svg>';
        return svg;
    }

    function niceScale(lo, hi, maxTicks) {
        var range = hi - lo;
        if (range <= 0) return [0];
        var rough = range / maxTicks;
        var mag = Math.pow(10, Math.floor(Math.log10(rough)));
        var res = rough / mag;
        var nice;
        if (res <= 1.5) nice = 1;
        else if (res <= 3) nice = 2;
        else if (res <= 7) nice = 5;
        else nice = 10;
        var step = nice * mag;
        var ticks = [];
        for (var v = 0; v <= hi; v += step) {
            ticks.push(Math.round(v * 1000) / 1000);
        }
        return ticks;
    }
})();
