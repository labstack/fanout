(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var spans = V.util.parseData(container, 'data-spans');
    if (!spans || spans.length === 0) return;

    // Build tree
    var byId = {};
    spans.forEach(function(s) { byId[s.id] = Object.assign({}, s, { children: [] }); });
    var roots = [];
    spans.forEach(function(s) {
      if (s.parent && byId[s.parent]) byId[s.parent].children.push(byId[s.id]);
      else roots.push(byId[s.id]);
    });

    // Flatten via DFS
    var flat = [];
    function dfs(node, depth) {
      flat.push(Object.assign({}, node, { depth: depth }));
      node.children.forEach(function(c) { dfs(c, depth + 1); });
    }
    roots.forEach(function(r) { dfs(r, 0); });

    var rowH = expanded ? 30 : 26;
    var labelW = expanded ? 260 : 180;
    var rulerH = 20;
    var totalW = expanded ? 1200 : 780;
    var barAreaW = totalW - labelW - 60;
    var totalH = rulerH + flat.length * rowH + 8;

    var maxTime = Math.max.apply(null, spans.map(function(s) { return s.start + s.dur; })) || 1;
    var timeScale = barAreaW / maxTime;

    var out = V.svg.viewBox(totalW, totalH);

    // Ruler
    out += '<g class="wf-ruler">';
    var tickCount = expanded ? 8 : 5;
    for (var i = 0; i <= tickCount; i++) {
      var t = (maxTime / tickCount) * i;
      var x = labelW + t * timeScale;
      out += V.svg.line(x, rulerH - 4, x, totalH, '');
      out += V.svg.text(x, rulerH - 8, Math.round(t) + 'ms', 'text-anchor="middle"');
    }
    out += '</g>';

    // Spans
    flat.forEach(function(span, idx) {
      var y = rulerH + idx * rowH;
      var indent = span.depth * (expanded ? 16 : 12);
      var barX = labelW + span.start * timeScale;
      var barW = Math.max(span.dur * timeScale, 2);
      var color = V.colors.statusHex(span.status);

      out += '<g class="wf-span-row" data-idx="' + idx + '">';

      // Connector
      if (span.parent && byId[span.parent]) {
        var parentIdx = -1;
        for (var p = 0; p < flat.length; p++) { if (flat[p].id === span.parent) { parentIdx = p; break; } }
        if (parentIdx >= 0) {
          var parentY = rulerH + parentIdx * rowH + rowH / 2;
          var cx = indent + 3;
          out += V.svg.path('M ' + cx + ' ' + (parentY + rowH/2 + 2) + ' L ' + cx + ' ' + (y + rowH/2) + ' L ' + (cx + 8) + ' ' + (y + rowH/2), 'class="wf-connector"');
        }
      }

      // Label
      var fontSize = expanded ? '12px' : '11px';
      out += '<text class="wf-span-label" x="' + (indent + 14) + '" y="' + (y + rowH / 2 + 3.5) + '" style="font-size:' + fontSize + '">';
      out += '<tspan class="wf-service">' + V.util.escapeHtml(span.service) + '</tspan>';
      out += '<tspan style="fill:var(--text-muted)"> ' + V.util.escapeHtml(span.op) + '</tspan>';
      out += '</text>';

      // Bar
      out += V.svg.rect(barX, y + 4, barW, rowH - 8, 'class="wf-span-bar" fill="' + color + '" data-idx="' + idx + '"');

      // Duration label
      out += V.svg.text(barX + barW + 6, y + rowH / 2 + 3, span.dur + 'ms', 'class="wf-span-dur"');

      out += '</g>';
    });

    out += '</svg>';

    // Legend
    out += V.legend([
      { color: '#22c55e', label: 'OK' },
      { color: '#f59e0b', label: 'Slow' },
      { color: '#ef4444', label: 'Error' }
    ]);

    container.innerHTML = out;
    container._spans = flat;

    V.tooltip.wire(container, '.wf-span-bar', function(el) {
      var s = container._spans[parseInt(el.getAttribute('data-idx'), 10)];
      if (!s) return '';
      return '<div class="tt-title">' + V.util.escapeHtml(s.service) + ': ' + V.util.escapeHtml(s.op) + '</div>' +
        '<div class="tt-row"><span>Duration</span><span class="tt-val">' + V.util.escapeHtml(String(s.dur)) + 'ms</span></div>' +
        '<div class="tt-row"><span>Start</span><span class="tt-val">' + V.util.escapeHtml(String(s.start)) + 'ms</span></div>' +
        '<div class="tt-row"><span>Status</span><span class="tt-val" style="color:' + V.colors.statusHex(s.status) + '">' + V.util.escapeHtml(String(s.status)) + '</span></div>' +
        '<div class="tt-row"><span>Span ID</span><span class="tt-val">' + V.util.escapeHtml(String(s.id)) + '</span></div>';
    });
  }

  V.register('trace-waterfall', 'data-spans', render);
})();
