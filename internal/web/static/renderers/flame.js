(function() {
  'use strict';
  var V = window.FanoutViz;

  var SERVICE_COLORS = {
    'frontend': '#6366f1',
    'api-gateway': '#0ea5e9',
    'auth-service': '#22c55e',
    'inventory-svc': '#14b8a6',
    'payment-svc': '#f59e0b',
    'stripe-api': '#ef4444',
    'notification': '#8b5cf6',
    'postgres': '#06b6d4'
  };

  function render(container, expanded) {
    var frames = JSON.parse(container.getAttribute('data-frames'));
    if (!frames || frames.length === 0) return;

    var totalW = expanded ? 1200 : 780;
    var frameH = expanded ? 22 : 18;
    var padX = expanded ? 10 : 4;
    var padY = 4;
    var maxDepth = Math.max.apply(null, frames.map(function(f) { return f.depth; }));
    var totalH = padY * 2 + (maxDepth + 1) * (frameH + 1) + 20;
    var barAreaW = totalW - padX * 2;

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    frames.forEach(function(frame, i) {
      var x = padX + frame.x * barAreaW;
      var w = frame.w * barAreaW;
      var y = padY + frame.depth * (frameH + 1);
      var color = SERVICE_COLORS[frame.service] || V.colors.service(frame.service);

      var minLabelW = expanded ? 40 : 30;
      var label = w > minLabelW ? frame.name : '';

      svg += '<g class="flame-frame" data-idx="' + i + '">';
      svg += '<rect x="' + x + '" y="' + y + '" width="' + Math.max(w - 0.5, 1) + '" height="' + frameH + '" fill="' + color + '" rx="2" ry="2" opacity="0.85"/>';
      if (label) {
        svg += '<text class="flame-label" x="' + (x + 4) + '" y="' + (y + frameH/2 + 3) + '" style="font-size:' + (expanded ? '10px' : '9px') + '">' + V.util.escapeHtml(V.util.truncate(label, w - 8, expanded ? 6 : 5)) + '</text>';
      }
      svg += '</g>';
    });

    // Percentage axis at bottom
    var axisY = totalH - 12;
    for (var i = 0; i <= 4; i++) {
      var pct = i * 25;
      var x = padX + (pct / 100) * barAreaW;
      svg += '<text class="flame-axis-label" x="' + x + '" y="' + axisY + '" text-anchor="middle">' + pct + '%</text>';
      svg += '<line x1="' + x + '" y1="' + padY + '" x2="' + x + '" y2="' + (axisY - 10) + '" stroke="var(--border-subtle, var(--border-color))" stroke-dasharray="2 3"/>';
    }

    svg += '</svg>';

    // Legend
    var seen = {};
    var svcs = [];
    frames.forEach(function(f) { if (!seen[f.service]) { seen[f.service] = true; svcs.push(f.service); } });
    svg += '<div class="viz-legend">';
    svcs.forEach(function(svc) {
      svg += '<div class="viz-legend-item"><span class="swatch" style="background:' + (SERVICE_COLORS[svc] || V.colors.service(svc)) + '"></span> ' + V.util.escapeHtml(svc) + '</div>';
    });
    svg += '</div>';

    container.innerHTML = svg;
    container._frames = frames;

    // Event delegation
    container.addEventListener('mouseover', function(ev) {
      var g = ev.target.closest('.flame-frame');
      if (!g) return;
      var idx = parseInt(g.getAttribute('data-idx'), 10);
      var f = container._frames[idx];
      if (!f) return;
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(f.service) + ': ' + V.util.escapeHtml(f.name) + '</div>' +
        '<div class="tt-row"><span>Total</span><span class="tt-val">' + (f.total * 100).toFixed(1) + '%</span></div>' +
        '<div class="tt-row"><span>Self</span><span class="tt-val">' + (f.self * 100).toFixed(1) + '%</span></div>' +
        '<div class="tt-row"><span>Samples</span><span class="tt-val">' + f.samples + '</span></div>' +
        '<div class="tt-row"><span>Service</span><span class="tt-val">' + V.util.escapeHtml(f.service) + '</span></div>',
        ev
      );
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.flame-frame')) V.tooltip.hide();
    });
  }

  V.register('flame-graph', 'data-frames', render);
})();
