(function() {
    'use strict';
    var BM = window.BM;

    // ── Custom device names/labels ──
    // Purely client-side (localStorage), keyed by MAC address when known,
    // falling back to IP. Lets operators rename "client 192.168.1.47" to
    // something meaningful ("Kid's Xbox") without any backend changes —
    // the override is applied wherever a hostname/name would be shown.

    var STORAGE_KEY = 'bm_device_labels_v1';

    function loadAll() {
        try {
            return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}') || {};
        } catch (e) {
            return {};
        }
    }

    function saveAll(map) {
        try {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(map));
        } catch (e) { /* localStorage unavailable or full; ignore */ }
    }

    function normKey(key) {
        return (key || '').toString().trim().toLowerCase();
    }

    // Returns the raw custom label for a single key, or '' if none set.
    BM.getDeviceLabel = function(key) {
        var k = normKey(key);
        if (!k) return '';
        var map = loadAll();
        return map[k] || '';
    };

    // Sets (or clears, if name is empty) the custom label for a key.
    BM.setDeviceLabel = function(key, name) {
        var k = normKey(key);
        if (!k) return;
        var map = loadAll();
        name = (name || '').trim();
        if (name) map[k] = name; else delete map[k];
        saveAll(map);
    };

    // Resolves the best display name for a device: custom label (by MAC,
    // then by IP) takes priority over the discovered hostname/fallback.
    BM.deviceDisplayName = function(mac, ips, fallback) {
        var label = BM.getDeviceLabel(mac);
        if (!label && ips && ips.length) {
            for (var i = 0; i < ips.length && !label; i++) {
                label = BM.getDeviceLabel(ips[i]);
            }
        }
        return label || fallback || '';
    };

    // Returns the primary key (mac if present, else first ip) used to store
    // a label for a device, so callers can wire up rename UI consistently.
    BM.deviceLabelKey = function(mac, ips) {
        if (mac) return mac;
        if (ips && ips.length) return ips[0];
        return '';
    };
})();
