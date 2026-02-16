// Kahi Web UI - Vanilla JavaScript (no framework)
(function() {
    'use strict';

    // Auto-refresh process list via SSE.
    function connectSSE() {
        var source = new EventSource('/api/v1/events/stream');

        source.onmessage = function() {
            refreshProcessList();
        };

        source.addEventListener('PROCESS_STATE_RUNNING', function() { refreshProcessList(); });
        source.addEventListener('PROCESS_STATE_STOPPED', function() { refreshProcessList(); });
        source.addEventListener('PROCESS_STATE_STARTING', function() { refreshProcessList(); });
        source.addEventListener('PROCESS_STATE_FATAL', function() { refreshProcessList(); });
        source.addEventListener('PROCESS_STATE_EXITED', function() { refreshProcessList(); });
        source.addEventListener('PROCESS_STATE_STOPPING', function() { refreshProcessList(); });
        source.addEventListener('PROCESS_STATE_BACKOFF', function() { refreshProcessList(); });

        source.onerror = function() {
            source.close();
            setTimeout(connectSSE, 5000);
        };
    }

    function refreshProcessList() {
        var xhr = new XMLHttpRequest();
        xhr.open('GET', '/api/v1/processes');
        xhr.onload = function() {
            if (xhr.status === 200) {
                var processes = JSON.parse(xhr.responseText);
                updateTable(processes);
                updateTimestamp();
            }
        };
        xhr.send();
    }

    function updateTable(processes) {
        var tbody = document.getElementById('process-list');
        if (!tbody) return;

        tbody.innerHTML = '';
        processes.sort(function(a, b) { return a.name.localeCompare(b.name); });

        for (var i = 0; i < processes.length; i++) {
            var p = processes[i];
            var tr = document.createElement('tr');
            tr.setAttribute('data-name', p.name);

            var stateLower = p.state.toLowerCase();
            var pid = p.pid > 0 ? String(p.pid) : '-';
            var uptime = p.uptime > 0 ? formatDuration(p.uptime) : '-';
            var desc = p.state === 'EXITED' ? 'exit ' + p.exitstatus : (p.description || '');

            tr.innerHTML =
                '<td class="col-name" data-label="Name">' + esc(p.name) + '</td>' +
                '<td class="col-group" data-label="Group">' + esc(p.group) + '</td>' +
                '<td class="col-state" data-label="State"><span class="state state-' + stateLower + '">' + esc(p.state) + '</span></td>' +
                '<td class="col-pid" data-label="PID">' + pid + '</td>' +
                '<td class="col-uptime" data-label="Uptime">' + uptime + '</td>' +
                '<td class="col-desc" data-label="Description">' + esc(desc) + '</td>' +
                '<td class="col-actions">' +
                    '<button class="btn btn-start btn-sm" onclick="doAction(\'' + esc(p.name) + '\', \'start\')">Start</button>' +
                    '<button class="btn btn-stop btn-sm" onclick="doAction(\'' + esc(p.name) + '\', \'stop\')">Stop</button>' +
                    '<button class="btn btn-restart btn-sm" onclick="doAction(\'' + esc(p.name) + '\', \'restart\')">Restart</button>' +
                    '<a href="/log/' + encodeURIComponent(p.name) + '/stdout" class="btn btn-log btn-sm">Tail Stdout</a>' +
                    '<a href="/log/' + encodeURIComponent(p.name) + '/stderr" class="btn btn-log btn-sm">Tail Stderr</a>' +
                '</td>';

            tbody.appendChild(tr);
        }
    }

    function formatDuration(seconds) {
        var d = Math.floor(seconds / 86400);
        var h = Math.floor((seconds % 86400) / 3600);
        var m = Math.floor((seconds % 3600) / 60);
        if (d > 0) return d + 'd ' + h + 'h ' + m + 'm';
        if (h > 0) return h + 'h ' + m + 'm';
        return m + 'm';
    }

    function esc(s) {
        if (!s) return '';
        var d = document.createElement('div');
        d.appendChild(document.createTextNode(s));
        return d.innerHTML;
    }

    function updateTimestamp() {
        var el = document.getElementById('last-update');
        if (el) {
            el.textContent = 'Last update: ' + new Date().toLocaleTimeString();
        }
    }

    // Process action handler (global).
    window.doAction = function(name, action) {
        var xhr = new XMLHttpRequest();
        xhr.open('POST', '/api/v1/processes/' + encodeURIComponent(name) + '/' + action);
        xhr.onload = function() {
            if (xhr.status === 200) {
                showToast(name + ': ' + action + 'ed', 'success');
                refreshProcessList();
            } else {
                var resp = {};
                try { resp = JSON.parse(xhr.responseText); } catch(e) {}
                showToast('Failed to ' + action + ' ' + name + ': ' + (resp.error || 'unknown error'), 'error');
            }
        };
        xhr.onerror = function() {
            showToast('Failed to ' + action + ' ' + name + ': connection error', 'error');
        };
        xhr.send();
    };

    function showToast(msg, type) {
        var toast = document.createElement('div');
        toast.className = 'toast ' + type;
        toast.textContent = msg;
        document.body.appendChild(toast);
        setTimeout(function() { toast.classList.add('show'); }, 10);
        setTimeout(function() {
            toast.classList.remove('show');
            setTimeout(function() { toast.remove(); }, 300);
        }, 3000);
    }

    // Fallback: poll every 5 seconds if SSE is not supported.
    if (typeof EventSource !== 'undefined') {
        connectSSE();
    } else {
        setInterval(refreshProcessList, 5000);
    }

    // Initial refresh.
    refreshProcessList();
})();
