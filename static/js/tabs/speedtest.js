(function() {
    'use strict';
    var BM = window.BM;

    var _stRunning = false;

    function drawGauge(canvasId, value, max, color) {
        var canvas = document.getElementById(canvasId);
        if (!canvas) return;
        var dpr = window.devicePixelRatio || 1;
        var dw = canvas.offsetWidth, dh = canvas.offsetHeight;
        if (!dw || !dh) return;
        canvas.width = dw * dpr; canvas.height = dh * dpr;
        var c = canvas.getContext('2d');
        c.scale(dpr, dpr);

        var cx = dw / 2, cy = dh - 10;
        var r = Math.min(cx - 10, cy - 5);
        var startAngle = Math.PI;
        var endAngle = 2 * Math.PI;

        c.beginPath();
        c.arc(cx, cy, r, startAngle, endAngle);
        c.lineWidth = 8;
        c.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue('--bg-3').trim() || '#1f1f23';
        c.lineCap = 'round';
        c.stroke();

        if (value > 0) {
            var pct = Math.min(value / max, 1);
            var valAngle = startAngle + pct * Math.PI;
            c.beginPath();
            c.arc(cx, cy, r, startAngle, valAngle);
            c.lineWidth = 8;
            c.strokeStyle = color;
            c.lineCap = 'round';
            c.stroke();
        }
    }

    function updateSpeedTestGauges(ping, download, upload, jitter, downloadSingle, uploadSingle) {
        var pingEl = document.getElementById('stPingValue');
        var downEl = document.getElementById('stDownValue');
        var upEl = document.getElementById('stUpValue');
        var jitterEl = document.getElementById('stJitterValue');
        var downSubEl = document.getElementById('stDownSingleValue');
        var upSubEl = document.getElementById('stUpSingleValue');

        pingEl.textContent = ping >= 0 ? ping.toFixed(1) : '--';
        downEl.textContent = download >= 0 ? download.toFixed(1) : '--';
        upEl.textContent = upload >= 0 ? upload.toFixed(1) : '--';
        jitterEl.textContent = jitter >= 0 ? jitter.toFixed(1) : '--';

        drawGauge('stPingGauge', ping >= 0 ? ping : 0, 100, '#22d3ee');
        drawGauge('stDownGauge', download >= 0 ? download : 0, 1000, '#3b82f6');
        drawGauge('stUpGauge', upload >= 0 ? upload : 0, 1000, '#a78bfa');
        drawGauge('stJitterGauge', jitter >= 0 ? jitter : 0, 50, '#f59e0b');

        if (downSubEl) {
            if (downloadSingle > 0 && download > 0) {
                var dRatio = download / downloadSingle;
                downSubEl.textContent = '1 stream: ' + downloadSingle.toFixed(1) + ' Mbps' +
                    (dRatio >= 1.3 ? ' (' + dRatio.toFixed(1) + '\u00d7 slower than 6 streams)' : ' (about the same as 6 streams)');
            } else {
                downSubEl.textContent = '';
            }
        }
        if (upSubEl) {
            if (uploadSingle > 0 && upload > 0) {
                var uRatio = upload / uploadSingle;
                upSubEl.textContent = '1 stream: ' + uploadSingle.toFixed(1) + ' Mbps' +
                    (uRatio >= 1.3 ? ' (' + uRatio.toFixed(1) + '\u00d7 slower than 6 streams)' : ' (about the same as 6 streams)');
            } else {
                upSubEl.textContent = '';
            }
        }
    }

    function renderSpeedTestHistory(results) {
        var tb = document.getElementById('speedtestHistory');
        if (!results || !results.length) {
            tb.innerHTML = '<tr><td colspan="7" class="empty-state">No tests yet &mdash; click Start Test</td></tr>';
            return;
        }
        var h = '';
        for (var i = 0; i < results.length; i++) {
            var r = results[i];
            var d = new Date(r.timestamp);
            var dateStr = d.toLocaleDateString() + ' ' + d.toLocaleTimeString();
            var ifaceCell;
            if (r.interface && r.interface_auto) {
                ifaceCell = BM.escSvg(r.interface) + ' <span style="color:var(--text-3)" title="Auto-detected from the OS default route, not explicitly selected">(auto)</span>';
            } else if (r.interface) {
                ifaceCell = BM.escSvg(r.interface);
            } else {
                ifaceCell = '<span style="color:var(--text-3)">auto</span>';
            }
            var dlSingle = r.download_single_mbps ? ' <span style="color:var(--text-3);font-weight:400" title="Single-stream download">(1 stream: ' + r.download_single_mbps.toFixed(1) + ' Mbps)</span>' : '';
            var ulSingle = r.upload_single_mbps ? ' <span style="color:var(--text-3);font-weight:400" title="Single-stream upload">(1 stream: ' + r.upload_single_mbps.toFixed(1) + ' Mbps)</span>' : '';
            h += '<tr>';
            h += '<td><span class="' + BM.rankClass(i) + '">' + (i + 1) + '</span></td>';
            h += '<td style="font-size:12px;white-space:nowrap">' + dateStr + '</td>';
            h += '<td style="font-size:12px;white-space:nowrap">' + ifaceCell + '</td>';
            h += '<td style="font-variant-numeric:tabular-nums;font-weight:600;color:var(--rx)">' + r.download_mbps.toFixed(1) + ' Mbps' + dlSingle + '</td>';
            h += '<td style="font-variant-numeric:tabular-nums;font-weight:600;color:var(--tx)">' + r.upload_mbps.toFixed(1) + ' Mbps' + ulSingle + '</td>';
            h += '<td style="font-variant-numeric:tabular-nums">' + r.ping_ms.toFixed(1) + ' ms</td>';
            h += '<td style="font-variant-numeric:tabular-nums">' + r.jitter_ms.toFixed(1) + ' ms</td>';
            h += '</tr>';
        }
        tb.innerHTML = h;
    }

    BM.loadSpeedTestHistory = function() {
        fetch('/api/speedtest/results').then(function(r) { return r.json(); }).then(function(data) {
            if (data.running) {
                document.getElementById('speedtestBtn').disabled = true;
                document.getElementById('speedtestBtn').textContent = 'Running...';
                _stRunning = true;
            }
            renderSpeedTestHistory(data.results || []);
            if (data.results && data.results.length) {
                var last = data.results[0];
                updateSpeedTestGauges(last.ping_ms, last.download_mbps, last.upload_mbps, last.jitter_ms, last.download_single_mbps, last.upload_single_mbps);
            }
        }).catch(function() {});
    };

    // On multi-WAN routers, populate a "test via" picker so the user can
    // pick which uplink to measure instead of whatever the default route
    // happens to send traffic through. Hidden entirely on single-WAN
    // setups (the common case) to avoid cluttering the UI.
    function loadSpeedTestInterfaces() {
        var sel = document.getElementById('speedtestIfaceSelect');
        if (!sel) return;
        fetch('/api/speedtest/interfaces').then(function(r) { return r.json(); }).then(function(data) {
            var ifaces = data.interfaces || [];
            if (ifaces.length < 2) {
                sel.style.display = 'none';
                return;
            }
            var h = '<option value="">Auto (default route)</option>';
            for (var i = 0; i < ifaces.length; i++) {
                h += '<option value="' + BM.escSvg(ifaces[i].name) + '">' + BM.escSvg(ifaces[i].name) + '</option>';
            }
            sel.innerHTML = h;
            sel.style.display = '';
        }).catch(function() {});
    }
    loadSpeedTestInterfaces();

    window._runSpeedTest = function() {
        if (_stRunning) return;
        _stRunning = true;

        var btn = document.getElementById('speedtestBtn');
        btn.disabled = true;
        btn.innerHTML = '<span class="speedtest-spinner"></span> Running...';

        var wrap = document.getElementById('speedtestProgressWrap');
        wrap.style.display = '';
        var bar = document.getElementById('speedtestProgressBar');
        var phase = document.getElementById('speedtestPhase');
        updateSpeedTestGauges(-1, -1, -1, -1, -1, -1);
        bar.style.width = '0%';
        phase.textContent = 'Connecting...';

        var currentPing = -1;
        var currentDownload = -1;
        var currentUpload = -1;
        var currentJitter = -1;
        var currentDownloadSingle = -1;
        var currentUploadSingle = -1;

        var ifaceSel = document.getElementById('speedtestIfaceSelect');
        var iface = (ifaceSel && ifaceSel.style.display !== 'none') ? ifaceSel.value : '';
        var runUrl = '/api/speedtest/run' + (iface ? ('?iface=' + encodeURIComponent(iface)) : '');

        var finished = false;

        BM.streamSSE({
            url: runUrl,
            method: 'POST',
            onMessage: function(p) {
                if (p.phase === 'ping') {
                    phase.textContent = 'Measuring latency...';
                    bar.style.width = (p.percent * 0.10) + '%';
                    bar.className = 'speedtest-progress-bar-fill ping';
                    if (p.percent >= 100 && p.value > 0) {
                        currentPing = p.value;
                    }
                    updateSpeedTestGauges(currentPing, currentDownload, currentUpload, currentJitter, currentDownloadSingle, currentUploadSingle);
                } else if (p.phase === 'download-single') {
                    currentDownloadSingle = p.value;
                    phase.textContent = 'Testing download (1 stream)... ' + p.value.toFixed(1) + ' Mbps';
                    bar.style.width = (10 + p.percent * 0.10) + '%';
                    bar.className = 'speedtest-progress-bar-fill download';
                    updateSpeedTestGauges(currentPing, currentDownload, currentUpload, currentJitter, currentDownloadSingle, currentUploadSingle);
                } else if (p.phase === 'download') {
                    currentDownload = p.value;
                    phase.textContent = 'Testing download (6 streams)... ' + p.value.toFixed(1) + ' Mbps';
                    bar.style.width = (20 + p.percent * 0.40) + '%';
                    bar.className = 'speedtest-progress-bar-fill download';
                    updateSpeedTestGauges(currentPing, currentDownload, currentUpload, currentJitter, currentDownloadSingle, currentUploadSingle);
                } else if (p.phase === 'upload-single') {
                    currentUploadSingle = p.value;
                    phase.textContent = 'Testing upload (1 stream)... ' + p.value.toFixed(1) + ' Mbps';
                    bar.style.width = (60 + p.percent * 0.10) + '%';
                    bar.className = 'speedtest-progress-bar-fill upload';
                    updateSpeedTestGauges(currentPing, currentDownload, currentUpload, currentJitter, currentDownloadSingle, currentUploadSingle);
                } else if (p.phase === 'upload') {
                    currentUpload = p.value;
                    phase.textContent = 'Testing upload (6 streams)... ' + p.value.toFixed(1) + ' Mbps';
                    bar.style.width = (70 + p.percent * 0.30) + '%';
                    bar.className = 'speedtest-progress-bar-fill upload';
                    updateSpeedTestGauges(currentPing, currentDownload, currentUpload, currentJitter, currentDownloadSingle, currentUploadSingle);
                } else if (p.phase === 'done' && p.result) {
                    phase.textContent = 'Complete!';
                    bar.style.width = '100%';
                    bar.className = 'speedtest-progress-bar-fill done';
                    var r = p.result;
                    updateSpeedTestGauges(r.ping_ms, r.download_mbps, r.upload_mbps, r.jitter_ms, r.download_single_mbps, r.upload_single_mbps);
                    BM.loadSpeedTestHistory();
                    finishTest();
                } else if (p.phase === 'error') {
                    phase.textContent = p.message || 'Error — test failed';
                    bar.className = 'speedtest-progress-bar-fill error';
                    finishTest();
                }
            },
            onDone: function() {
                if (finished) return;
                phase.textContent = 'Connection closed before test completed';
                bar.className = 'speedtest-progress-bar-fill error';
                finishTest();
            },
            onError: function(err) {
                phase.textContent = err && err.status === 409
                    ? 'Test already running'
                    : 'Error — ' + BM.describeStreamError(err);
                bar.className = 'speedtest-progress-bar-fill error';
                finishTest();
            }
        });

        function finishTest() {
            if (finished) return;
            finished = true;
            _stRunning = false;
            btn.disabled = false;
            btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px;height:14px"><polygon points="5 3 19 12 5 21 5 3"/></svg> Start Test';
            setTimeout(function() {
                wrap.style.display = 'none';
            }, 3000);
        }
    };
})();
