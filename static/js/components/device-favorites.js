(function() {
    'use strict';
    var BM = window.BM;

    // ── Favorites / pinned devices ──
    // Purely client-side (localStorage), keyed the same way as custom
    // device labels (MAC if known, else IP). Lets operators star devices
    // they want to keep an eye on (a NAS, a kid's laptop, etc.) and filter
    // tables down to just those.

    var STORAGE_KEY = 'bm_device_favorites_v1';

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

    BM.isFavoriteDevice = function(key) {
        var k = normKey(key);
        if (!k) return false;
        return !!loadAll()[k];
    };

    BM.setFavoriteDevice = function(key, fav) {
        var k = normKey(key);
        if (!k) return;
        var map = loadAll();
        if (fav) map[k] = true; else delete map[k];
        saveAll(map);
    };

    BM.toggleFavoriteDevice = function(key) {
        var fav = !BM.isFavoriteDevice(key);
        BM.setFavoriteDevice(key, fav);
        return fav;
    };

    BM.hasAnyFavorites = function() {
        var map = loadAll();
        for (var k in map) if (map[k]) return true;
        return false;
    };

    // Renders a clickable star button for a device row. `key` is the
    // MAC/IP used for storage; stopPropagation keeps it from also
    // triggering a parent row's click-to-open-modal handler.
    BM.favoriteStarHtml = function(key) {
        if (!key) return '';
        var fav = BM.isFavoriteDevice(key);
        return '<span class="fav-star' + (fav ? ' fav-star-active' : '') + '" data-fav-key="' +
            BM.escSvg(key) + '" title="' + (fav ? 'Unpin device' : 'Pin device') + '" ' +
            'onclick="event.stopPropagation();window._toggleFavoriteStar(this)">' + (fav ? '\u2605' : '\u2606') + '</span>';
    };

    window._toggleFavoriteStar = function(el) {
        var key = el.getAttribute('data-fav-key');
        var fav = BM.toggleFavoriteDevice(key);
        el.classList.toggle('fav-star-active', fav);
        el.textContent = fav ? '\u2605' : '\u2606';
        el.title = fav ? 'Unpin device' : 'Pin device';
        if (window._refreshAllViews) window._refreshAllViews();
        else if (window._refreshFavoriteFilters) window._refreshFavoriteFilters();
    };
})();
