// Kahi Web UI status page. Vanilla JavaScript, no framework.
//
// Process names and other API data reach the page only through textContent
// and dataset, and reach URLs only through encodeURIComponent (SEC-026).
(function () {
  'use strict';

  var STATE_EVENTS = [
    'PROCESS_STATE_STOPPED',
    'PROCESS_STATE_STARTING',
    'PROCESS_STATE_RUNNING',
    'PROCESS_STATE_BACKOFF',
    'PROCESS_STATE_STOPPING',
    'PROCESS_STATE_EXITED',
    'PROCESS_STATE_FATAL',
  ];
  var KNOWN_STATES = [
    'stopped',
    'starting',
    'running',
    'backoff',
    'stopping',
    'exited',
    'fatal',
  ];
  var ACTIONS = ['start', 'stop', 'restart'];
  var LABELS = { start: 'Start', stop: 'Stop', restart: 'Restart' };

  function processURL(name, suffix) {
    return '/api/v1/processes/' + encodeURIComponent(name) + suffix;
  }

  function logPageURL(name, stream) {
    return '/ui/log/' + encodeURIComponent(name) + '/' + stream;
  }

  function stateClass(state) {
    var lower = String(state).toLowerCase();
    return KNOWN_STATES.indexOf(lower) >= 0
      ? 'state-' + lower
      : 'state-unknown';
  }

  function formatUptime(seconds) {
    var d = Math.floor(seconds / 86400);
    var h = Math.floor((seconds % 86400) / 3600);
    var m = Math.floor((seconds % 3600) / 60);
    if (d > 0) return d + 'd ' + h + 'h ' + m + 'm';
    if (h > 0) return h + 'h ' + m + 'm';
    return m + 'm';
  }

  function errorText(xhr) {
    try {
      var resp = JSON.parse(xhr.responseText);
      if (resp && resp.error) return resp.error;
    } catch (e) {
      // Not JSON: fall through to the status line.
    }
    return xhr.status ? 'HTTP ' + xhr.status : 'connection error';
  }

  function errorCode(xhr) {
    try {
      return JSON.parse(xhr.responseText).code || '';
    } catch (e) {
      return '';
    }
  }

  function showToast(msg, type) {
    var toast = document.createElement('div');
    toast.className = 'toast ' + type;
    toast.textContent = msg;
    document.body.appendChild(toast);
    setTimeout(function () {
      toast.classList.add('show');
    }, 10);
    setTimeout(function () {
      toast.classList.remove('show');
      setTimeout(function () {
        toast.remove();
      }, 300);
    }, 4000);
  }

  function actionButtons() {
    return document.querySelectorAll('button[data-action], button[data-bulk]');
  }

  function lockButtons() {
    var buttons = actionButtons();
    for (var i = 0; i < buttons.length; i++) buttons[i].disabled = true;
  }

  function unlockButtons() {
    var buttons = actionButtons();
    for (var i = 0; i < buttons.length; i++) buttons[i].disabled = false;
  }

  function cell(className, label, text) {
    var td = document.createElement('td');
    td.className = className;
    td.dataset.label = label;
    if (text !== undefined) td.textContent = text;
    return td;
  }

  function actionButton(name, action) {
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'btn btn-' + action + ' btn-sm';
    b.dataset.name = name;
    b.dataset.action = action;
    b.textContent = LABELS[action];
    return b;
  }

  function logLink(name, stream, label) {
    var a = document.createElement('a');
    a.className = 'btn btn-log btn-sm';
    a.href = logPageURL(name, stream);
    a.textContent = label;
    return a;
  }

  function buildRow(p) {
    var tr = document.createElement('tr');
    tr.dataset.name = p.name;
    tr.appendChild(cell('col-name', 'Name', p.name));
    tr.appendChild(cell('col-group', 'Group', p.group));

    var stateTd = cell('col-state', 'State');
    var badge = document.createElement('span');
    badge.className = 'state ' + stateClass(p.state);
    badge.textContent = p.state;
    stateTd.appendChild(badge);
    tr.appendChild(stateTd);

    tr.appendChild(cell('col-pid', 'PID', p.pid > 0 ? String(p.pid) : '-'));
    tr.appendChild(cell('col-uptime', 'Uptime', formatUptime(p.uptime || 0)));
    tr.appendChild(cell('col-desc', 'Description', p.description || ''));

    var actions = cell('col-actions', 'Actions');
    for (var i = 0; i < ACTIONS.length; i++) {
      actions.appendChild(actionButton(p.name, ACTIONS[i]));
    }
    actions.appendChild(logLink(p.name, 'stdout', 'Tail Stdout'));
    actions.appendChild(logLink(p.name, 'stderr', 'Tail Stderr'));
    tr.appendChild(actions);
    return tr;
  }

  function updateTable(processes) {
    var tbody = document.getElementById('process-list');
    if (!tbody) return;
    var rows = [];
    for (var i = 0; i < processes.length; i++) {
      rows.push(buildRow(processes[i]));
    }
    tbody.replaceChildren.apply(tbody, rows);
  }

  function updateTimestamp() {
    var el = document.getElementById('last-update');
    if (el) el.textContent = 'Last update: ' + new Date().toLocaleTimeString();
  }

  // On failure the last rendered table stays in place.
  function refreshProcessList() {
    var xhr = new XMLHttpRequest();
    xhr.open('GET', '/api/v1/processes');
    xhr.onload = function () {
      if (xhr.status !== 200) {
        showToast('Failed to refresh status: ' + errorText(xhr), 'error');
        return;
      }
      try {
        updateTable(JSON.parse(xhr.responseText));
        updateTimestamp();
      } catch (e) {
        showToast('Failed to refresh status: ' + e.message, 'error');
      }
    };
    xhr.onerror = function () {
      showToast('Failed to refresh status: ' + errorText(xhr), 'error');
    };
    xhr.send();
  }

  function doAction(name, action) {
    var xhr = new XMLHttpRequest();
    xhr.open('POST', processURL(name, '/' + action));
    xhr.onload = function () {
      if (xhr.status === 200) {
        showToast(name + ': ' + action + ' requested', 'success');
        refreshProcessList();
      } else {
        showToast(
          'Failed to ' + action + ' ' + name + ': ' + errorText(xhr),
          'error',
        );
      }
    };
    xhr.onerror = function () {
      showToast(
        'Failed to ' + action + ' ' + name + ': ' + errorText(xhr),
        'error',
      );
    };
    xhr.send();
  }

  // A bulk stop can take as long as a full sequential stop, so there is no
  // request timeout; the buttons stay disabled until it answers.
  function doBulk(action) {
    lockButtons();
    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/v1/all/' + action);
    xhr.onload = function () {
      unlockButtons();
      if (xhr.status !== 200) {
        var code = errorCode(xhr);
        if (code === 'CONFLICT') {
          showToast('Another bulk action is in progress', 'error');
        } else if (code === 'SHUTTING_DOWN') {
          showToast('Daemon is shutting down', 'error');
        } else {
          showToast('Failed to ' + action + ' all: ' + errorText(xhr), 'error');
        }
        return;
      }
      var results = JSON.parse(xhr.responseText).results || [];
      var failed = 0;
      for (var i = 0; i < results.length; i++) {
        var r = results[i];
        if (r.status === 'error') {
          failed++;
          showToast(
            'Failed to ' + action + ' ' + r.name + ': ' + r.error,
            'error',
          );
        }
      }
      if (failed === 0) showToast(LABELS[action] + ' all: done', 'success');
      refreshProcessList();
    };
    xhr.onerror = function () {
      unlockButtons();
      showToast('Failed to ' + action + ' all: ' + errorText(xhr), 'error');
    };
    xhr.send();
  }

  document.addEventListener('click', function (e) {
    var target = e.target;
    if (!(target instanceof HTMLButtonElement) || target.disabled) return;
    if (target.dataset.bulk) {
      doBulk(target.dataset.bulk);
    } else if (target.dataset.action && target.dataset.name !== undefined) {
      doAction(target.dataset.name, target.dataset.action);
    }
  });

  // The server renders buttons disabled for the no-JavaScript case.
  unlockButtons();

  if (typeof EventSource !== 'undefined') {
    // Named events need addEventListener; onmessage never fires for them. The
    // browser reconnects on its own, and every open (including after a
    // reconnect) refreshes, so changes missed while disconnected show up.
    var source = new EventSource('/api/v1/events/stream');
    source.addEventListener('open', refreshProcessList);
    for (var i = 0; i < STATE_EVENTS.length; i++) {
      source.addEventListener(STATE_EVENTS[i], refreshProcessList);
    }
  } else {
    setInterval(refreshProcessList, 5000);
  }
  refreshProcessList();
})();
