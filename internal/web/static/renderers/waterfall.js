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

    // Dimensions
    var rowH = expanded ? 30 : 26;
    var labelW = expanded ? 260 : 180;
    var rulerH = 20;
    var totalW = expanded ? 1200 : 780;
    var barAreaW = totalW - labelW - 60;
    var totalH = rulerH + flat.length * rowH + 8;

    // Time scale
    var maxTime = Math.max.apply(null, spans.map(function(s) { return s.start + s.dur; }));
    var scale = barAreaW / maxTime;

    // Build SVG
    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Ruler
    svg += '<g class="wf-ruler">';
    var tickCount = expanded ? 8 : 5;
    for (var i = 0; i <= tickCount; i++) {
      var t = (maxTime / tickCount) * i;
      var x = labelW + t * scale;
      svg += '<line x1="' + x + '" y1="' + (rulerH - 4) + '" x2="' + x + '" y2="' + totalH + '" />';
      svg += '<text x="' + x + '" y="' + (rulerH - 8) + '" text-anchor="middle">' + Math.round(t) + 'ms</text>';
    }
    svg += '</g>';

    // Spans
    flat.forEach(function(span, idx) {
      var y = rulerH + idx * rowH;
      var indent = span.depth * (expanded ? 16 : 12);
      var barX = labelW + span.start * scale;
      var barW = Math.max(span.dur * scale, 2);
      var color = V.colors.statusHex(span.status);

      svg += '<g class="wf-span-row" data-idx="' + idx + '">';

      // Connector line from parent
      if (span.parent && byId[span.parent]) {
        var parentIdx = -1;
        for (var p = 0; p < flat.length; p++) { if (flat[p].id === span.parent) { parentIdx = p; break; } }
        if (parentIdx >= 0) {
          var parentY = rulerH + parentIdx * rowH + rowH / 2;
          var cx = indent + 3;
          svg += '<path class="wf-connector" d="M ' + cx + ' ' + (parentY + rowH/2 + 2) + ' L ' + cx + ' ' + (y + rowH/2) + ' L ' + (cx + 8) + ' ' + (y + rowH/2) + '" />';
        }
      }

      // Label
      var fontSize = expanded ? '12px' : '11px';
      svg += '<text class="wf-span-label" x="' + (indent + 14) + '" y="' + (y + rowH / 2 + 3.5) + '" style="font-size:' + fontSize + '">';
      svg += '<tspan class="wf-service">' + V.util.escapeHtml(span.service) + '</tspan>';
      svg += '<tspan style="fill:var(--text-muted)"> ' + V.util.escapeHtml(span.op) + '</tspan>';
      svg += '</text>';

      // Bar
      svg += '<rect class="wf-span-bar" x="' + barX + '" y="' + (y + 4) + '" width="' + barW + '" height="' + (rowH - 8) + '" fill="' + color + '" data-idx="' + idx + '" />';

      // Duration label
      svg += '<text class="wf-span-dur" x="' + (barX + barW + 6) + '" y="' + (y + rowH / 2 + 3) + '">' + V.util.escapeHtml(String(span.dur)) + 'ms</text>';

      svg += '</g>';
    });

    svg += '</svg>';

    // Legend
    svg += '<div class="viz-legend">' +
      '<div class="viz-legend-item"><span class="swatch" style="background:var(--success)"></span> OK</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:var(--warning)"></span> Slow</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:var(--danger)"></span> Error</div>' +
    '</div>';

    container.innerHTML = svg;
    container._spans = flat;

    // Event delegation for tooltips
    container.addEventListener('mouseover', function(e) {
      var bar = e.target.closest('.wf-span-bar');
      if (!bar) return;
      var idx = parseInt(bar.getAttribute('data-idx'), 10);
      var s = container._spans[idx];
      if (!s) return;
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(s.service) + ': ' + V.util.escapeHtml(s.op) + '</div>' +
        '<div class="tt-row"><span>Duration</span><span class="tt-val">' + V.util.escapeHtml(String(s.dur)) + 'ms</span></div>' +
        '<div class="tt-row"><span>Start</span><span class="tt-val">' + V.util.escapeHtml(String(s.start)) + 'ms</span></div>' +
        '<div class="tt-row"><span>Status</span><span class="tt-val" style="color:' + V.colors.statusHex(s.status) + '">' + V.util.escapeHtml(String(s.status)) + '</span></div>' +
        '<div class="tt-row"><span>Span ID</span><span class="tt-val">' + V.util.escapeHtml(String(s.id)) + '</span></div>',
        e
      );
    });
    container.addEventListener('mouseout', function(e) {
      if (e.target.closest('.wf-span-bar')) V.tooltip.hide();
    });
  }

  V.register('trace-waterfall', 'data-spans', render);
})();
