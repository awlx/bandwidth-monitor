(function() {
    'use strict';
    var BM = window.BM = window.BM || {};

    // ── Theme management ──
    (function initTheme() {
        var saved = localStorage.getItem('bw-theme') || 'auto';
        document.documentElement.setAttribute('data-theme', saved);
        var toggle = document.getElementById('themeToggle');
        if (toggle) {
            var btns = toggle.querySelectorAll('.theme-btn');
            btns.forEach(function(b) {
                b.classList.toggle('active', b.getAttribute('data-theme-val') === saved);
            });
            toggle.addEventListener('click', function(e) {
                var btn = e.target.closest('.theme-btn');
                if (!btn) return;
                var val = btn.getAttribute('data-theme-val');
                document.documentElement.setAttribute('data-theme', val);
                localStorage.setItem('bw-theme', val);
                btns.forEach(function(b) { b.classList.toggle('active', b.getAttribute('data-theme-val') === val); });
                if (BM._updateChartsForTheme) BM._updateChartsForTheme();
            });
        }
        if (window.matchMedia) {
            window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function() {
                if ((localStorage.getItem('bw-theme') || 'auto') === 'auto' && BM._updateChartsForTheme) BM._updateChartsForTheme();
            });
        }
    })();

    // ── Tab navigation ──
    BM._activeTab = 'traffic';
    BM._lastPayload = null;

    // Chart data state (shared with traffic.js)
    BM.chartData = {};
    BM.knownIfaces = new Set();
    BM.sparklineData = {};
    BM._emaState = {};
    BM.EMA_ALPHA = 0.3;
    BM.MAX_PTS = 3600;

    var _stHistoryLoaded = false;

    // ── Tab Capability Tracking ──
    // Track which optional tabs are available based on configured integrations
    BM._capabilities = {
        dns: false,   // AdGuard Home, NextDNS, or Pi-hole
        wifi: false   // UniFi or Omada
    };

    function _updateTabVisibility() {
        // Hide/show DNS tab
        var dnsButton = document.querySelector('.main-nav-tab[data-tab="dns"]');
        var dnsPanel = document.getElementById('tabDns');
        if (dnsButton) dnsButton.style.display = BM._capabilities.dns ? '' : 'none';
        if (dnsPanel) dnsPanel.style.display = BM._capabilities.dns ? '' : 'none';

        // Hide/show WiFi tab
        var wifiButton = document.querySelector('.main-nav-tab[data-tab="wifi"]');
        var wifiPanel = document.getElementById('tabWifi');
        if (wifiButton) wifiButton.style.display = BM._capabilities.wifi ? '' : 'none';
        if (wifiPanel) wifiPanel.style.display = BM._capabilities.wifi ? '' : 'none';

        // If current tab is now unavailable, fallback to traffic
        if ((BM._activeTab === 'dns' && !BM._capabilities.dns) ||
            (BM._activeTab === 'wifi' && !BM._capabilities.wifi)) {
            window._switchTab('traffic');
        }
    }

    function _isTabAvailable(tab) {
        if (tab === 'dns') return BM._capabilities.dns;
        if (tab === 'wifi') return BM._capabilities.wifi;
        return true; // All other tabs are always available
    }

    // Hash-state helpers (BM._hashTab / _hashParams / _setHashParam) live in
    // utils.js so they're defined before the deferred tab modules parse.

    window._switchTab = function(tab) {
        // Guard: if tab is unavailable, fallback to traffic
        if (!_isTabAvailable(tab)) {
            tab = 'traffic';
        }

        BM._activeTab = tab;
        var panels = { traffic: 'tabTraffic', nat: 'tabNat', dns: 'tabDns', wifi: 'tabWifi', network: 'tabNetwork', monitor: 'tabMonitor', speedtest: 'tabSpeedtest', debug: 'tabDebug' };
        for (var k in panels) {
            var p = document.getElementById(panels[k]);
            if (p) p.classList.toggle('active', k === tab);
        }
        document.querySelectorAll('.main-nav-tab').forEach(function(t) {
            t.classList.toggle('active', t.getAttribute('data-tab') === tab);
        });
        // Preserve any per-tab params (e.g. the selected interface) so switching
        // away and back — or reloading — keeps the view intact.
        var qs = BM._hashParams().toString();
        var newHash = '#' + tab + (qs ? '?' + qs : '');
        if (history.replaceState) {
            history.replaceState(null, '', newHash);
        } else {
            location.hash = newHash;
        }
        if (tab === 'speedtest' && !_stHistoryLoaded) {
            _stHistoryLoaded = true;
            if (BM.loadSpeedTestHistory) BM.loadSpeedTestHistory();
        }
        if (BM._lastPayload) _renderTab(tab, BM._lastPayload, true);
    };

    function _renderTab(tab, d, force) {
        var bw = d.top_bandwidth || [], vol = d.top_volume || [];
        var bwRemote = bw.filter(function(t) { return !t.is_local; });
        var volRemote = vol.filter(function(t) { return !t.is_local; });
        if (tab === 'traffic') {
            if (BM.updateProtoChart) BM.updateProtoChart(d.protocols);
            if (BM.updateIPVersions) BM.updateIPVersions(d.ip_versions);
            if (BM.updateCountries) BM.updateCountries(d.countries);
            if (BM.updateASNs) BM.updateASNs(d.asns);
            if (BM.renderTalkers) {
                BM.renderTalkers('bwTable', bwRemote, 'rate_bytes', BM.formatRate, 'bw');
                BM.renderTalkers('volTable', volRemote, 'total_bytes', BM.formatBytes, 'vol');
            }
            if (!BM._historyLoaded && BM._loadInterfaceHistory) BM._loadInterfaceHistory();
        } else if (tab === 'monitor') {
            var now = Date.now();
            if (force || !window._lastMapUpdate || now - window._lastMapUpdate > 5000) {
                if (BM.updateWorldMap) BM.updateWorldMap(d.countries, bwRemote, d.origin_country, d.origin_lat, d.origin_lon);
                window._lastMapUpdate = now;
            }
            if (force || !window._lastLatUpdate || now - window._lastLatUpdate > 2000) {
                if (BM.updateLatency) BM.updateLatency(d.latency);
                window._lastLatUpdate = now;
            }
        } else if (tab === 'dns') {
            if (BM.updateDNS) BM.updateDNS(d.dns || null);
        } else if (tab === 'wifi') {
            if (BM.updateWiFi) BM.updateWiFi(d.wifi || null);
        } else if (tab === 'network') {
            if (BM.updateNetwork) BM.updateNetwork(d.topology || null, d.topology_bandwidth || d.top_bandwidth || []);
        } else if (tab === 'nat') {
            if (BM.updateNAT) BM.updateNAT(d.conntrack || null);
        }
    }
    BM._renderTab = _renderTab;

    // Restore tab from URL hash on load
    (function() {
        var hash = BM._hashTab();
        var validTabs = ['traffic', 'nat', 'dns', 'wifi', 'network', 'monitor', 'speedtest', 'debug'];
        if (hash && validTabs.indexOf(hash) !== -1 && _isTabAvailable(hash)) {
            setTimeout(function() { window._switchTab(hash); }, 0);
        }
        window.addEventListener('hashchange', function() {
            var h = BM._hashTab();
            if (h && validTabs.indexOf(h) !== -1 && h !== BM._activeTab && _isTabAvailable(h)) {
                window._switchTab(h);
            }
        });
    })();

    // Initialize tab visibility (DNS/WiFi start hidden until capabilities are confirmed)
    _updateTabVisibility();

    // ── SSE connection ──
    var sse = null;

    function connect() {
        if (sse) { sse.close(); sse = null; }

        sse = new EventSource('/api/events');

        sse.onopen = function() {
            document.getElementById('statusDot').className = 'status-dot';
            document.getElementById('statusDot').setAttribute('aria-label', 'Connection status: live');
            document.getElementById('statusText').textContent = 'Live';
            // Backfill the Live Traffic chart from stored history so a reload or
            // a gap from a disconnect/hibernate is filled in, not lost.
            if (BM._backfillLiveTraffic) BM._backfillLiveTraffic();
        };
        sse.onerror = function() {
            document.getElementById('statusDot').className = 'status-dot error';
            document.getElementById('statusDot').setAttribute('aria-label', 'Connection status: reconnecting');
            document.getElementById('statusText').textContent = 'Reconnecting';
        };
        sse.onmessage = function(e) {
            try {
                var d = JSON.parse(e.data);
                if (d.timestamp && (Date.now() - d.timestamp) > 5000) return;
                process(d);
            } catch(ex) { console.error(ex); }
        };
    }

    document.addEventListener('visibilitychange', function() {
        if (document.visibilityState === 'visible') {
            if (!sse || sse.readyState === EventSource.CLOSED) {
                connect();
            }
        }
    });

    function process(d) {
        BM._lastPayload = d;

        // Update tab capabilities based on payload key presence
        var hasUpdated = false;
        var newDnsCapability = 'dns' in d;
        var newWifiCapability = 'wifi' in d;
        if (newDnsCapability !== BM._capabilities.dns || newWifiCapability !== BM._capabilities.wifi) {
            BM._capabilities.dns = newDnsCapability;
            BM._capabilities.wifi = newWifiCapability;
            hasUpdated = true;
        }

        var ifaces = d.interfaces || [];
        var rx = 0, tx = 0;
        for (var f of ifaces) { rx += f.rx_rate || 0; tx += f.tx_rate || 0; BM.knownIfaces.add(f.name); }

        if (BM.renderStatsRow) BM.renderStatsRow(ifaces, d);

        // VPN routing banner
        var vpnActive = false, vpnSince = '', vpnName = '';
        for (var f of ifaces) {
            if (f.vpn_routing) {
                vpnActive = true;
                vpnName = f.name;
                vpnSince = f.vpn_routing_since || '';
                break;
            }
        }
        var banner = document.getElementById('vpnBanner');
        if (vpnActive) {
            banner.className = 'vpn-banner active';
            var txt = 'Traffic routed via ' + vpnName;
            document.getElementById('vpnBannerSince').textContent = vpnSince ? '(since ' + vpnSince + ')' : '';
            banner.querySelector('.vpn-banner-text').firstChild.textContent = txt + ' ';
        } else {
            banner.className = 'vpn-banner inactive';
        }

        // Use the server timestamp so live points share a clock with the
        // backfilled history points (see BM._backfillLiveTraffic), keeping the
        // Live Traffic chart contiguous across reloads/reconnects.
        var ts = d.timestamp || Date.now();
        var now = new Date(ts);
        for (var f of ifaces) {
            if (!BM.chartData[f.name]) BM.chartData[f.name] = { rx: [], tx: [] };
            if (!BM._emaState[f.name]) BM._emaState[f.name] = { rx: 0, tx: 0 };
            var em = BM._emaState[f.name];
            var rawRx = f.rx_rate || 0;
            var rawTx = f.tx_rate || 0;
            em.rx = em.rx === 0 ? rawRx : BM.EMA_ALPHA * rawRx + (1 - BM.EMA_ALPHA) * em.rx;
            em.tx = em.tx === 0 ? rawTx : BM.EMA_ALPHA * rawTx + (1 - BM.EMA_ALPHA) * em.tx;
            var arr = BM.chartData[f.name];
            var lastTs = arr.rx.length ? +arr.rx[arr.rx.length - 1].x : 0;
            if (ts <= lastTs) continue; // already covered by backfill or a prior tick
            arr.rx.push({ x: now, y: em.rx });
            arr.tx.push({ x: now, y: -(em.tx) });
            if (arr.rx.length > BM.MAX_PTS) { arr.rx.shift(); arr.tx.shift(); }
        }

        if (BM.renderIfaceCards) BM.renderIfaceCards(ifaces);
        BM.sparklineData = d.sparklines || {};
        if (BM.drawAllSparklines) BM.drawAllSparklines();
        if (BM.renderIfaceTabs) BM.renderIfaceTabs();
        if (BM.updateChart) BM.updateChart();

        // Update tab visibility if capabilities changed
        if (hasUpdated) {
            _updateTabVisibility();
        }

        _renderTab(BM._activeTab, d);
    }

    function tick() { document.getElementById('clock').textContent = new Date().toLocaleTimeString(); }
    setInterval(tick, 1000); tick();

    // Wire search/sort on WiFi client table to re-render
    var _lastWiFi = null;
    BM._origUpdateWiFi = null;
    // Wrap updateWiFi after wifi-tab.js loads — defer wiring
    (function wireWiFiRerender() {
        ['wifiClientSearch', 'wifiClientSort'].forEach(function(id) {
            var el = document.getElementById(id);
            if (el) el.addEventListener(id === 'wifiClientSearch' ? 'input' : 'change', function() {
                if (_lastWiFi && BM.updateWiFi) BM.updateWiFi(_lastWiFi);
            });
        });
        // Intercept updateWiFi to cache wifi data
        var _checkInterval = setInterval(function() {
            if (BM.updateWiFi && !BM._wifiWrapped) {
                BM._wifiWrapped = true;
                var orig = BM.updateWiFi;
                BM.updateWiFi = function(wifi) {
                    if (wifi) _lastWiFi = wifi;
                    orig(wifi);
                };
                clearInterval(_checkInterval);
            }
        }, 100);
    })();

    connect();
})();
