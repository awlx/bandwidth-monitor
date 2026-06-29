(function() {
    'use strict';
    var BM = window.BM;

    var _lastTopology = null;
    var _lastBandwidth = [];

    BM.updateNetwork = function(topo, bandwidth) {
        if (!topo || !topo.nodes || !topo.nodes.length) {
            document.getElementById('networkNoData').style.display = '';
            document.getElementById('networkHasData').style.display = 'none';
            return;
        }
        document.getElementById('networkNoData').style.display = 'none';
        document.getElementById('networkHasData').style.display = '';

        _lastTopology = topo;
        _lastBandwidth = bandwidth || [];

        var nodes = topo.nodes || [];
        var aps = nodes.filter(function(n) { return n.type === 'ap'; });
        var clients = nodes.filter(function(n) { return n.type === 'client'; });
        var gw = nodes.filter(function(n) { return n.type === 'gateway'; });
        var sources = {};
        nodes.forEach(function(n) {
            (n.source || '').split(',').forEach(function(s) { if (s) sources[s] = true; });
        });

        document.getElementById('netTotalNodes').textContent = nodes.length;
        document.getElementById('netTotalAPs').textContent = aps.length;
        document.getElementById('netTotalClients').textContent = clients.length;
        document.getElementById('netSources').textContent = Object.keys(sources).join(', ') || '—';

        if (gw.length > 0) {
            var gwn = gw[0];
            document.getElementById('netGateway').textContent = gwn.hostname || (gwn.ips && gwn.ips[0]) || gwn.mac || '—';
        } else {
            document.getElementById('netGateway').textContent = '—';
        }

        document.getElementById('networkClientCount').textContent = nodes.length + ' nodes discovered';

        renderNetworkTopology(topo, _lastBandwidth);
        window._filterNetworkClients();
    };

    // ── Client table rendering ──

    window._filterNetworkClients = function() {
        if (!_lastTopology) return;
        var nodes = (_lastTopology.nodes || []).slice();
        var filter = ((document.getElementById('networkClientSearch') || {}).value || '').toLowerCase();
        var sortKey = ((document.getElementById('networkClientSort') || {}).value || 'type');

        if (filter) {
            nodes = nodes.filter(function(n) {
                return (n.hostname || '').toLowerCase().indexOf(filter) !== -1 ||
                       (n.ips || []).join(' ').toLowerCase().indexOf(filter) !== -1 ||
                       (n.mac || '').toLowerCase().indexOf(filter) !== -1 ||
                       (n.type || '').toLowerCase().indexOf(filter) !== -1 ||
                       (n.ssid || '').toLowerCase().indexOf(filter) !== -1 ||
                       (n.source || '').toLowerCase().indexOf(filter) !== -1;
            });
        }

        if (sortKey === 'name') {
            nodes.sort(function(a, b) { return (a.hostname || a.mac || '').localeCompare(b.hostname || b.mac || ''); });
        } else if (sortKey === 'ip') {
            nodes.sort(function(a, b) { return ((a.ips||[])[0]||'').localeCompare(((b.ips||[])[0]||''), undefined, {numeric:true}); });
        } else if (sortKey === 'source') {
            nodes.sort(function(a, b) { return (a.source||'').localeCompare(b.source||''); });
        }

        var tb = document.getElementById('networkClientTable');
        if (!nodes.length) {
            tb.innerHTML = '<tr><td colspan="9" class="empty-state">' + (filter ? 'No matching nodes' : 'No nodes discovered') + '</td></tr>';
            return;
        }
        var h = '';
        for (var i = 0; i < nodes.length; i++) {
            var n = nodes[i];
            var typeIcon = n.device_class ? deviceClassIcon(n.device_class) : nodeTypeIcon(n.type);
            var typeLabel = n.device_class || n.type;
            var sigStr = '';
            if (n.signal && n.signal !== 0) {
                sigStr = n.signal + ' dBm';
            }
            h += '<tr>';
            h += '<td><span class="net-type-badge net-type-' + n.type + '">' + typeIcon + ' ' + typeLabel + '</span></td>';
            h += '<td>' + (n.hostname || '<span style="color:var(--text-2)">—</span>') + '</td>';
            h += '<td style="font-family:JetBrains Mono,monospace;font-size:12px">' + formatClickableIPs(n.ips) + '</td>';
            h += '<td style="font-family:JetBrains Mono,monospace;font-size:12px">' + (n.mac || '—') + '</td>';
            h += '<td>' + (n.iface || '—') + '</td>';
            h += '<td>' + (n.ssid || '—') + '</td>';
            h += '<td>' + (sigStr || '—') + '</td>';
            h += '<td>' + (n.state || '—') + '</td>';
            h += '<td><span style="font-size:11px">' + (n.source || '—') + '</span></td>';
            h += '</tr>';
        }
        tb.innerHTML = h;
    };

    function formatClickableIPs(ips) {
        if (!ips || !ips.length) return '—';
        return ips.map(function(ip) {
            return '<span class="ip-clickable" data-ip="' + ip + '">' + ip + '</span>';
        }).join('<br>');
    }

    function nodeTypeIcon(type) {
        switch (type) {
            case 'gateway': return '🌐';
            case 'wan_gw': return '🏢';
            case 'tunnel': return '🔒';
            case 'switch': return '🔲';
            case 'ap': return '📡';
            case 'self': return '💻';
            case 'client': return '📱';
            default: return '❓';
        }
    }

    function deviceClassIcon(dc) {
        switch (dc) {
            case 'camera': return '📷';
            case 'iot': return '💡';
            case 'voice': return '🎙️';
            case 'phone': return '📱';
            case 'computer': return '💻';
            case 'media': return '🎵';
            case 'server': return '🖥️';
            case 'printer': return '🖨️';
            case 'gaming': return '🎮';
            default: return '📱';
        }
    }

    // ── SVG Topology Visualization ──

    var _networkLayout = 'tree';
    window._updateNetworkLayout = function() {
        _networkLayout = (document.getElementById('networkLayout') || {}).value || 'tree';
        if (_lastTopology) renderNetworkTopology(_lastTopology, _lastBandwidth);
    };

    function renderNetworkTopology(topo, bandwidth) {
        var svg = document.getElementById('networkTopologySVG');
        if (!svg) return;
        var container = document.getElementById('networkTopologyContainer');
        var W = container.clientWidth || 800;
        var nodes = topo.nodes || [];
        var links = topo.links || [];
        bandwidth = bandwidth || [];

        if (nodes.length === 0) {
            svg.innerHTML = '<text x="50%" y="50%" text-anchor="middle" fill="var(--text-2)" font-size="14">No topology data</text>';
            return;
        }

        var ipRates = {};
        function normalizeIP(ip) {
            if (!ip) return '';
            var z = ip.indexOf('%');
            if (z >= 0) ip = ip.slice(0, z);
            return ip.toLowerCase();
        }
        for (var bi = 0; bi < bandwidth.length; bi++) {
            var t = bandwidth[bi];
            var ipKey = normalizeIP(t.ip);
            if (!ipKey) continue;
            if (!ipRates[ipKey]) {
                ipRates[ipKey] = { rx: 0, tx: 0, total: 0, hostname: t.hostname || '' };
            }
            ipRates[ipKey].rx += t.rx_rate || 0;
            ipRates[ipKey].tx += t.tx_rate || 0;
            ipRates[ipKey].total += t.rate_bytes || 0;
            if (!ipRates[ipKey].hostname && t.hostname) ipRates[ipKey].hostname = t.hostname;
        }

        var nodeRates = {};
        var maxNodeRate = 1;
        nodes.forEach(function(n) {
            var sum = { rx: 0, tx: 0, total: 0, hostname: '' };
            var seen = {};
            (n.ips || []).forEach(function(ip) {
                var ipKey = normalizeIP(ip);
                if (!ipKey || seen[ipKey]) return;
                seen[ipKey] = true;
                var r = ipRates[ipKey];
                if (!r) return;
                sum.rx += r.rx || 0;
                sum.tx += r.tx || 0;
                sum.total += r.total || 0;
                if (!sum.hostname && r.hostname) sum.hostname = r.hostname;
            });
            if (sum.total > 0) {
                nodeRates[n.id] = sum;
                if (sum.total > maxNodeRate) maxNodeRate = sum.total;
            }
        });

        var nodeById = {};
        nodes.forEach(function(n) { nodeById[n.id] = n; });

        var rootId = null;
        var typePriority = { 'tunnel': 0, 'wan_gw': 1, 'gateway': 2, 'self': 3 };
        var bestPrio = 99;
        nodes.forEach(function(n) {
            var p = typePriority[n.type];
            if (p !== undefined && p < bestPrio) {
                bestPrio = p;
                rootId = n.id;
            }
        });
        if (!rootId) rootId = topo.gateway || topo.self_node || nodes[0].id;
        if (!nodeById[rootId]) rootId = nodes[0].id;

        var childrenMap = {};
        var linkedSet = {};
        // Build a bidirectional adjacency list so that BFS from the root
        // can reach every connected node regardless of link direction.
        links.forEach(function(l) {
            var a = l.source, b = l.target;
            if (!childrenMap[a]) childrenMap[a] = [];
            childrenMap[a].push({ id: b, link: l });
            if (!childrenMap[b]) childrenMap[b] = [];
            childrenMap[b].push({ id: a, link: l });
            linkedSet[a] = true;
            linkedSet[b] = true;
        });

        nodes.forEach(function(n) {
            if (!linkedSet[n.id] && n.id !== rootId) {
                if (!childrenMap[rootId]) childrenMap[rootId] = [];
                childrenMap[rootId].push({ id: n.id, link: { source: rootId, target: n.id, type: 'wired' } });
            }
        });

        // Sort children so that downstream infrastructure (gateway, switch,
        // ap) comes first, keeping WAN/tunnel nodes to the edges.
        var childSortPrio = { 'gateway': 0, 'self': 1, 'switch': 2, 'ap': 3, 'client': 4, 'wan_gw': 5, 'tunnel': 6 };
        for (var parentId in childrenMap) {
            childrenMap[parentId].sort(function(a, b) {
                var at = (nodeById[a.id] || {}).type || '';
                var bt = (nodeById[b.id] || {}).type || '';
                var ap = childSortPrio[at] !== undefined ? childSortPrio[at] : 4;
                var bp = childSortPrio[bt] !== undefined ? childSortPrio[bt] : 4;
                if (ap !== bp) return ap - bp;
                return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
            });
        }

        var layout = _networkLayout;
        var nodePositions = {};

        if (layout === 'radial') {
            computeRadialLayout(rootId, childrenMap, nodeById, nodePositions, W, nodes.length);
        } else {
            computeTreeLayout(rootId, childrenMap, nodeById, nodePositions, W, nodes.length);
        }

        // Use the computed tree width if the tree is wider than the container
        var svgW = (nodePositions._treeWidth && nodePositions._treeWidth > W) ? nodePositions._treeWidth : W;
        delete nodePositions._treeWidth;

        var maxY = 100;
        for (var id in nodePositions) {
            if (nodePositions[id] && nodePositions[id].y + 40 > maxY) maxY = nodePositions[id].y + 40;
        }
        var H = Math.max(500, maxY + 60);
        svg.setAttribute('viewBox', '0 0 ' + svgW + ' ' + H);
        svg.style.width = svgW + 'px';
        svg.style.minWidth = '100%';
        svg.style.height = H + 'px';
        container.style.overflowX = svgW > W ? 'auto' : 'hidden';

        var svgHtml = '';
        var textCol = getComputedStyle(document.documentElement).getPropertyValue('--text-0').trim() || '#fafafa';
        var textCol2 = getComputedStyle(document.documentElement).getPropertyValue('--text-2').trim() || '#71717a';
        var lineCol = getComputedStyle(document.documentElement).getPropertyValue('--border').trim() || '#27272a';
        var rxCol = getComputedStyle(document.documentElement).getPropertyValue('--rx').trim() || '#22d3ee';

        var rxColor = '#34d399';
        var txColor = '#f97316';
        links.forEach(function(l) {
            var from = nodePositions[l.source];
            var to = nodePositions[l.target];
            if (!from || !to) return;
            var dash = '';
            var col = lineCol;
            var sw = '1.5';
            if (l.type === 'wireless') { dash = 'stroke-dasharray="6 4"'; col = rxCol; }
            else if (l.type === 'tunnel') { dash = 'stroke-dasharray="8 3"'; col = '#8b5cf6'; sw = '2'; }
            else if (l.type === 'wan') { col = '#f97316'; sw = '2'; }

            var childRate = nodeRates[l.target] || nodeRates[l.source];
            if (childRate && childRate.total > 0) {
                var dx = to.x - from.x;
                var dy = to.y - from.y;
                var len = Math.sqrt(dx * dx + dy * dy) || 1;
                var perpX = -dy / len;
                var perpY = dx / len;

                var visualRx = childRate.rx || 0;
                var visualTx = childRate.tx || 0;
                if (visualRx <= 0 && visualTx <= 0 && childRate.total > 0) {
                    // Direction unknown for this host in current sample;
                    // split total evenly so both lanes remain visible.
                    visualRx = childRate.total / 2;
                    visualTx = childRate.total / 2;
                }

                var rxRatio = Math.min(visualRx / maxNodeRate, 1);
                var txRatio = Math.min(visualTx / maxNodeRate, 1);
                var totalRatio = Math.min(childRate.total / maxNodeRate, 1);
                var rxSw = (1.5 + rxRatio * 3).toFixed(1);
                var txSw = (1.5 + txRatio * 3).toFixed(1);
                var hasTwoLanes = visualRx > 0 && visualTx > 0;

                svgHtml += '<line x1="' + from.x + '" y1="' + from.y + '" x2="' + to.x + '" y2="' + to.y + '" stroke="' + lineCol + '" stroke-width="1" opacity="0.15"/>';

                // Always render a clear total-traffic backbone for active links,
                // even when directional split (rx/tx) is zero or unavailable.
                var totalSw = (1.2 + totalRatio * 2.6).toFixed(1);
                var totalOp = (hasTwoLanes ? 0.18 : (0.45 + totalRatio * 0.35)).toFixed(2);
                var sepBase = Math.max(parseFloat(rxSw), parseFloat(txSw), parseFloat(totalSw));
                var sep = Math.max(4, Math.min(10, sepBase * 1.15));
                svgHtml += '<line x1="' + from.x + '" y1="' + from.y + '" x2="' + to.x + '" y2="' + to.y + '" stroke="' + rxColor + '" stroke-width="' + totalSw + '" opacity="' + totalOp + '" stroke-linecap="round"/>';

                if (visualRx > 0) {
                    var rxOp = (0.3 + rxRatio * 0.5).toFixed(2);
                    var rx1x = (from.x + perpX * sep).toFixed(1), rx1y = (from.y + perpY * sep).toFixed(1);
                    var rx2x = (to.x + perpX * sep).toFixed(1), rx2y = (to.y + perpY * sep).toFixed(1);
                    svgHtml += '<line x1="' + rx1x + '" y1="' + rx1y + '" x2="' + rx2x + '" y2="' + rx2y + '" stroke="' + rxColor + '" stroke-width="' + rxSw + '" stroke-dasharray="6 4" opacity="' + rxOp + '" stroke-linecap="round"><animate attributeName="stroke-dashoffset" from="0" to="-20" dur="1.5s" repeatCount="indefinite"/></line>';
                }
                if (visualTx > 0) {
                    var txOp = (0.3 + txRatio * 0.5).toFixed(2);
                    var tx1x = (to.x - perpX * sep).toFixed(1), tx1y = (to.y - perpY * sep).toFixed(1);
                    var tx2x = (from.x - perpX * sep).toFixed(1), tx2y = (from.y - perpY * sep).toFixed(1);
                    svgHtml += '<line x1="' + tx1x + '" y1="' + tx1y + '" x2="' + tx2x + '" y2="' + tx2y + '" stroke="' + txColor + '" stroke-width="' + txSw + '" stroke-dasharray="6 4" opacity="' + txOp + '" stroke-linecap="round"><animate attributeName="stroke-dashoffset" from="0" to="-20" dur="1.5s" repeatCount="indefinite"/></line>';
                }

                var tip = (childRate.hostname || l.target) + ': \u2193 ' + BM.formatRate(childRate.rx) + ' \u2191 ' + BM.formatRate(childRate.tx) + ' Total ' + BM.formatRate(childRate.total);
                svgHtml += '<line x1="' + from.x + '" y1="' + from.y + '" x2="' + to.x + '" y2="' + to.y + '" stroke="transparent" stroke-width="12" style="cursor:pointer" class="net-link-hover" data-tip="' + BM.escSvg(tip) + '"/>';
            } else {
                svgHtml += '<line x1="' + from.x + '" y1="' + from.y + '" x2="' + to.x + '" y2="' + to.y + '" stroke="' + col + '" stroke-width="' + sw + '" ' + dash + ' opacity="0.6"/>';
            }
            if (l.label && l.type !== 'tunnel' && l.type !== 'wan') {
                var mx = (from.x + to.x) / 2;
                var my = (from.y + to.y) / 2;
                svgHtml += '<text x="' + mx + '" y="' + (my - 4) + '" text-anchor="middle" font-size="9" fill="' + textCol2 + '">' + BM.escSvg(l.label) + '</text>';
            }
        });

        for (var nid in nodePositions) {
            var pos = nodePositions[nid];
            var node = nodeById[nid];
            if (!node) continue;
            var r = nodeRadius(node.type);
            var fill = nodeColor(node.type);
            svgHtml += '<circle cx="' + pos.x + '" cy="' + pos.y + '" r="' + r + '" fill="' + fill + '" stroke="' + fill + '" stroke-width="2" fill-opacity="0.15"/>';
            var nr = nodeRates[nid];
            if (nr && nr.total > 0) {
                var nratio = Math.min(nr.total / maxNodeRate, 1);
                var rr = (r + 2 + nratio * 6).toFixed(1);
                var rop = (0.3 + nratio * 0.45).toFixed(2);
                var rsw = (1 + nratio * 2.2).toFixed(1);
                svgHtml += '<circle cx="' + pos.x + '" cy="' + pos.y + '" r="' + rr + '" fill="none" stroke="' + rxColor + '" stroke-width="' + rsw + '" opacity="' + rop + '"><animate attributeName="opacity" values="' + rop + ';' + Math.max(0.15, nratio * 0.2).toFixed(2) + ';' + rop + '" dur="1.8s" repeatCount="indefinite"/></circle>';
            }
            var emoji = node.type === 'client' && node.device_class ? deviceClassEmoji(node.device_class) : nodeTypeEmoji(node.type);
            svgHtml += '<text x="' + pos.x + '" y="' + (pos.y + 4) + '" text-anchor="middle" font-size="' + (r > 14 ? 14 : 11) + '" fill="' + fill + '">' + emoji + '</text>';
            var label = node.hostname || (node.ips && node.ips[0]) || node.mac || node.id;
            if (label.length > 20) label = label.substring(0, 18) + '…';
            svgHtml += '<text x="' + pos.x + '" y="' + (pos.y + r + 14) + '" text-anchor="middle" font-size="11" fill="' + textCol + '">' + BM.escSvg(label) + '</text>';
            var typeLabel = node.device_class || node.type;
            svgHtml += '<text x="' + pos.x + '" y="' + (pos.y + r + 26) + '" text-anchor="middle" font-size="9" fill="' + textCol2 + '">' + typeLabel + '</text>';
        }

        svg.innerHTML = svgHtml;

        // Tooltip
        var tipEl = document.getElementById('netTooltip');
        if (!tipEl) {
            tipEl = document.createElement('div');
            tipEl.id = 'netTooltip';
            tipEl.style.cssText = 'position:fixed;pointer-events:none;background:var(--card);color:var(--text-0);border:1px solid var(--border);padding:6px 10px;border-radius:6px;font-size:12px;font-family:Inter,sans-serif;z-index:9999;display:none;white-space:nowrap;box-shadow:0 4px 12px rgba(0,0,0,0.15)';
            document.body.appendChild(tipEl);
        }
        // Bind hover listeners once: the SVG element persists across re-renders
        // (only its innerHTML is replaced), so re-adding them every render — which
        // runs on every SSE tick — leaked a handler per tick. Delegation via
        // closest() keeps working after the innerHTML swaps.
        if (!svg._tipBound) {
            svg._tipBound = true;
            svg.addEventListener('mousemove', function(e) {
                var el = e.target.closest('.net-link-hover');
                if (el) {
                    tipEl.textContent = el.getAttribute('data-tip');
                    tipEl.style.display = '';
                    tipEl.style.left = (e.clientX + 12) + 'px';
                    tipEl.style.top = (e.clientY - 8) + 'px';
                } else {
                    tipEl.style.display = 'none';
                }
            });
            svg.addEventListener('mouseleave', function() { tipEl.style.display = 'none'; });
        }
    }

    function computeTreeLayout(rootId, childrenMap, nodeById, positions, W, totalNodes) {
        // Build a proper tree with subtree width calculation to prevent overlap.
        var MIN_X_GAP = 120; // minimum horizontal distance between nodes
        var Y_GAP = 100;     // vertical distance between levels
        var visited = {};

        // 1. Compute the width (in node-slots) of each subtree.
        function subtreeWidth(id) {
            if (visited[id]) return 0;
            visited[id] = true;
            var kids = childrenMap[id] || [];
            var unvisitedKids = kids.filter(function(k) { return !visited[k.id]; });
            if (unvisitedKids.length === 0) return 1;
            var w = 0;
            for (var i = 0; i < unvisitedKids.length; i++) {
                w += subtreeWidth(unvisitedKids[i].id);
            }
            return Math.max(1, w);
        }
        var widths = {};
        // We need to compute widths in BFS order to handle the visited logic
        // correctly, so reset and do a two-pass approach.
        // Pass 1: determine tree membership via BFS
        var treeChildren = {}; // parentId -> [childId, ...]
        var bfsVisited = {};
        var bfsQueue = [rootId];
        bfsVisited[rootId] = true;
        while (bfsQueue.length > 0) {
            var cur = bfsQueue.shift();
            treeChildren[cur] = [];
            var kids = childrenMap[cur] || [];
            for (var i = 0; i < kids.length; i++) {
                if (!bfsVisited[kids[i].id]) {
                    bfsVisited[kids[i].id] = true;
                    treeChildren[cur].push(kids[i].id);
                    bfsQueue.push(kids[i].id);
                }
            }
        }
        // Add orphans
        var orphans = [];
        for (var nid in nodeById) {
            if (!bfsVisited[nid]) orphans.push(nid);
        }
        if (orphans.length > 0) {
            if (!treeChildren[rootId]) treeChildren[rootId] = [];
            for (var oi = 0; oi < orphans.length; oi++) {
                treeChildren[rootId].push(orphans[oi]);
                treeChildren[orphans[oi]] = [];
            }
        }

        // Re-parent WAN/tunnel nodes: move them from the gateway to the root
        // so the gateway stays centered over its downstream network.
        var rootType = (nodeById[rootId] || {}).type;
        if (rootType === 'tunnel' || rootType === 'wan_gw') {
            for (var pid in treeChildren) {
                if (pid === rootId) continue;
                var kept = [];
                for (var ki = 0; ki < treeChildren[pid].length; ki++) {
                    var cid = treeChildren[pid][ki];
                    var ctype = (nodeById[cid] || {}).type;
                    if (ctype === 'tunnel' || ctype === 'wan_gw') {
                        treeChildren[rootId].push(cid);
                    } else {
                        kept.push(cid);
                    }
                }
                treeChildren[pid] = kept;
            }
        }

        // Pass 2: compute subtree widths bottom-up
        function calcWidth(id) {
            var ch = treeChildren[id] || [];
            if (ch.length === 0) { widths[id] = 1; return 1; }
            var w = 0;
            for (var i = 0; i < ch.length; i++) w += calcWidth(ch[i]);
            widths[id] = w;
            return w;
        }
        calcWidth(rootId);

        // Pass 3: assign x positions based on subtree widths, with a minimum total width
        var totalSlots = widths[rootId] || 1;
        var neededWidth = totalSlots * MIN_X_GAP + 120;
        var useW = Math.max(W, neededWidth);

        // Collect depths for y positions
        var nodeDepth = {};
        var depthQueue = [{ id: rootId, depth: 0 }];
        var maxDepth = 0;
        var idx = 0;
        while (idx < depthQueue.length) {
            var item = depthQueue[idx++];
            nodeDepth[item.id] = item.depth;
            if (item.depth > maxDepth) maxDepth = item.depth;
            var ch = treeChildren[item.id] || [];
            for (var ci = 0; ci < ch.length; ci++) {
                depthQueue.push({ id: ch[ci], depth: item.depth + 1 });
            }
        }

        function assignPositions(id, left, right, depth) {
            var cx = (left + right) / 2;
            positions[id] = { x: cx, y: 40 + depth * Y_GAP };
            var ch = treeChildren[id] || [];
            if (ch.length === 0) return;
            var parentWidth = widths[id] || 1;
            var span = right - left;
            var offset = left;
            for (var i = 0; i < ch.length; i++) {
                var childSlots = widths[ch[i]] || 1;
                var childSpan = (childSlots / parentWidth) * span;
                assignPositions(ch[i], offset, offset + childSpan, depth + 1);
                offset += childSpan;
            }
        }
        var padding = 40;
        assignPositions(rootId, padding, useW - padding, 0);

        // If the tree is wider than the container, update W reference via positions._width
        positions._treeWidth = useW;
    }

    function computeRadialLayout(rootId, childrenMap, nodeById, positions, W, totalNodes) {
        var cx = W / 2, cy = 250;
        positions[rootId] = { x: cx, y: cy };

        var visited = {};
        visited[rootId] = true;
        var rings = [[rootId]];
        var current = [rootId];
        while (current.length > 0) {
            var next = [];
            for (var i = 0; i < current.length; i++) {
                var kids = childrenMap[current[i]] || [];
                for (var j = 0; j < kids.length; j++) {
                    if (!visited[kids[j].id]) {
                        visited[kids[j].id] = true;
                        next.push(kids[j].id);
                    }
                }
            }
            if (next.length > 0) rings.push(next);
            current = next;
        }
        var orphans = [];
        for (var id in nodeById) {
            if (!visited[id]) orphans.push(id);
        }
        if (orphans.length > 0) rings.push(orphans);

        var radiusStep = Math.min(180, (Math.min(W, 500) - 80) / (rings.length || 1));
        for (var ri = 1; ri < rings.length; ri++) {
            var ring = rings[ri];
            var radius = ri * radiusStep;
            for (var ni = 0; ni < ring.length; ni++) {
                var angle = (2 * Math.PI * ni) / ring.length - Math.PI / 2;
                positions[ring[ni]] = {
                    x: cx + radius * Math.cos(angle),
                    y: cy + radius * Math.sin(angle)
                };
            }
        }
    }

    function nodeRadius(type) {
        switch (type) {
            case 'gateway': return 22;
            case 'wan_gw': return 20;
            case 'tunnel': return 20;
            case 'switch': return 18;
            case 'ap': return 18;
            case 'self': return 18;
            default: return 14;
        }
    }

    function nodeColor(type) {
        switch (type) {
            case 'gateway': return '#22d3ee';
            case 'wan_gw': return '#f97316';
            case 'tunnel': return '#8b5cf6';
            case 'switch': return '#a78bfa';
            case 'ap': return '#34d399';
            case 'self': return '#fbbf24';
            case 'client': return '#71717a';
            default: return '#71717a';
        }
    }

    function nodeTypeEmoji(type) {
        switch (type) {
            case 'gateway': return '&#x1F310;';
            case 'wan_gw': return '&#x1F3E2;';
            case 'tunnel': return '&#x1F512;';
            case 'switch': return '&#x1F532;';
            case 'ap': return '&#x1F4E1;';
            case 'self': return '&#x1F4BB;';
            case 'client': return '&#x1F4F1;';
            default: return '?';
        }
    }

    function deviceClassEmoji(dc) {
        switch (dc) {
            case 'camera': return '&#x1F4F7;';
            case 'iot': return '&#x1F4A1;';
            case 'voice': return '&#x1F399;';
            case 'phone': return '&#x1F4F1;';
            case 'computer': return '&#x1F4BB;';
            case 'media': return '&#x1F3B5;';
            case 'server': return '&#x1F5A5;';
            case 'printer': return '&#x1F5A8;';
            case 'gaming': return '&#x1F3AE;';
            default: return '&#x1F4F1;';
        }
    }
})();
