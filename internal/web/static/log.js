// Kahi Web UI log viewer. Vanilla JavaScript, no framework.
//
// The stream opens first; the backlog is read from the first open event, by
// which time the server has subscribed, so nothing published after open is
// lost (a line written in between may appear twice). Log text is rendered with
// textContent only (SEC-026); a minimal SGR subset becomes CSS classes and
// every other escape sequence is removed (FUNC-079).
(function () {
  'use strict';

  // CSI sequences (final byte @ to ~), OSC sequences, or any other ESC + char.
  var ESCAPES =
    /\x1b(?:\[([0-9;?]*)([@-~])|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[\s\S]?)/g;

  var root = document.getElementById('log');
  var output = document.getElementById('log-output');
  var viewer = document.getElementById('log-viewer');
  var statusEl = document.getElementById('connection-status');
  var name = root.dataset.name;
  var stream = root.dataset.stream;

  var pending = ''; // partial line carried across chunks
  var queued = []; // chunks streamed before the backlog arrived
  var backlogDone = false;
  var follow = true;
  var sgr = { bold: false, fg: 0 }; // carried across lines, as in a terminal

  function apiURL(suffix) {
    return '/api/v1/processes/' + encodeURIComponent(name) + suffix;
  }

  function setStatus(text) {
    statusEl.textContent = text;
  }

  function applySGR(params) {
    var codes = params === '' ? ['0'] : params.split(';');
    for (var i = 0; i < codes.length; i++) {
      var c = parseInt(codes[i] || '0', 10);
      if (c === 0) {
        sgr.bold = false;
        sgr.fg = 0;
      } else if (c === 1) {
        sgr.bold = true;
      } else if ((c >= 30 && c <= 37) || (c >= 90 && c <= 97)) {
        sgr.fg = c;
      }
    }
  }

  function appendText(parent, text) {
    if (text === '') return;
    var span = document.createElement('span');
    var classes = [];
    if (sgr.bold) classes.push('sgr-bold');
    if (sgr.fg) classes.push('sgr-fg-' + sgr.fg);
    if (classes.length) span.className = classes.join(' ');
    span.textContent = text;
    parent.appendChild(span);
  }

  function renderLine(line) {
    var div = document.createElement('div');
    var last = 0;
    var m;
    ESCAPES.lastIndex = 0;
    while ((m = ESCAPES.exec(line)) !== null) {
      appendText(div, line.slice(last, m.index));
      if (m[2] === 'm') applySGR(m[1]);
      last = ESCAPES.lastIndex;
    }
    appendText(div, line.slice(last));
    if (!div.firstChild) div.textContent = ' '; // keep empty lines visible
    output.appendChild(div);
  }

  function scrollToEnd() {
    if (follow) viewer.scrollTop = viewer.scrollHeight;
  }

  // Render complete lines; keep a trailing partial line until the next chunk.
  function appendChunk(chunk) {
    var parts = (pending + chunk).split('\n');
    pending = parts.pop();
    for (var i = 0; i < parts.length; i++) renderLine(parts[i]);
    scrollToEnd();
  }

  function finishBacklog() {
    backlogDone = true;
    for (var i = 0; i < queued.length; i++) appendChunk(queued[i]);
    queued = [];
  }

  function showBacklog(bytes) {
    var lines = new TextDecoder().decode(bytes).split('\n');
    // A full buffer starts mid-line: drop everything up to the first newline.
    if (bytes.byteLength === 65536) lines.shift(); // read size below
    pending = lines.pop() || '';
    var tail = lines.slice(-100); // the last 100 complete lines
    for (var i = 0; i < tail.length; i++) renderLine(tail[i]);
    scrollToEnd();
  }

  var source = new EventSource(apiURL('/log/' + stream + '/stream'));

  source.addEventListener('open', function () {
    setStatus('Connected');
    if (backlogDone) return; // reconnects do not re-read (no duplicates)
    loadBacklog();
  });

  source.onmessage = function (e) {
    if (backlogDone) appendChunk(e.data);
    else queued.push(e.data);
  };

  source.onerror = function () {
    // EventSource retries on its own.
    if (source.readyState !== EventSource.CLOSED) setStatus('Reconnecting...');
  };

  function loadBacklog() {
    var xhr = new XMLHttpRequest();
    xhr.open('GET', apiURL('/log/' + stream + '?length=65536'));
    xhr.responseType = 'arraybuffer';
    xhr.onload = function () {
      if (xhr.status === 404) {
        setStatus('No such process: ' + name);
        source.close();
        return;
      }
      if (xhr.status !== 200) {
        setStatus('Failed to load log: HTTP ' + xhr.status);
      } else if (xhr.response.byteLength === 0) {
        setStatus('No output captured for this stream yet');
      } else {
        showBacklog(xhr.response);
      }
      finishBacklog();
    };
    xhr.onerror = function () {
      setStatus('Failed to load log: connection error');
      finishBacklog();
    };
    xhr.send();
  }

  // Scrolling up pauses auto-scroll until the user returns to the bottom.
  viewer.addEventListener('scroll', function () {
    follow = viewer.scrollTop + viewer.clientHeight >= viewer.scrollHeight - 4;
  });
})();
