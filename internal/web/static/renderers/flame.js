(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var frames = V.util.parseData(container, 'data-frames');
    if (!frames || frames.length === 0) return;

    var totalW = expanded ? 1200 : 780;
    var frameH = expanded ? 22 : 18;
    var padX = expanded ? 10 : 4;
    var padY = 4;
    var maxDepth = Math.max.apply(null, frames.map(function(f) { return f.depth; }));
    var totalH = padY * 2 + (maxDepth + 1) * (frameH + 1) + 20;
    var barAreaW = totalW - padX * 2;

    var out = V.svg.viewBox(totalW, totalH);

    frames.forEach(function(frame, i) {
      var x = padX + frame.x * barAreaW;
      var w = frame.w * barAreaW;
      var y = padY + frame.depth * (frameH + 1);
      var color = V.colors.service(frame.service);

      var minLabelW = expanded ? 40 : 30;
      var label = w > minLabelW ? frame.name : '';

      out += '<g class="flame-frame" data-idx="' + i + '">';
      out += V.svg.rect(x, y, Math.max(w - 0.5, 1), frameH, 'fill="' + color + '" rx="2" ry="2" opacity="0.85"');
      if (label) {
        out += V.svg.text(x + 4, y + frameH/2 + 3,
          V.util.truncate(label, w - 8, expanded ? 6 : 5),
          'class="flame-label" style="font-size:' + (expanded ? '10px' : '9px') + '"');
      }
      out += '</g>';
    });

    // Percentage axis
    var axisY = totalH - 12;
    for (var i = 0; i <= 4; i++) {
      var pct = i * 25;
      var x = padX + (pct / 100) * barAreaW;
      out += V.svg.text(x, axisY, pct + '%', 'class="flame-axis-label" text-anchor="middle"');
      out += V.svg.line(x, padY, x, axisY - 10, 'stroke="var(--border-subtle, var(--border-color))" stroke-dasharray="2 3"');
    }

    out += '</svg>';

    // Legend — unique services
    var seen = {};
    var svcs = [];
    frames.forEach(function(f) { if (!seen[f.service]) { seen[f.service] = true; svcs.push(f.service); } });
    out += V.legend(svcs.map(function(svc) {
      return { color: V.colors.service(svc), label: svc };
    }));

    container.innerHTML = out;
    container._frames = frames;

    V.tooltip.wire(container, '.flame-frame', function(el) {
      var f = container._frames[parseInt(el.getAttribute('data-idx'), 10)];
      if (!f) return '';
      return '<div class="tt-title">' + V.util.escapeHtml(f.service) + ': ' + V.util.escapeHtml(f.name) + '</div>' +
        '<div class="tt-row"><span>Total</span><span class="tt-val">' + (f.total * 100).toFixed(1) + '%</span></div>' +
        '<div class="tt-row"><span>Self</span><span class="tt-val">' + (f.self * 100).toFixed(1) + '%</span></div>' +
        '<div class="tt-row"><span>Samples</span><span class="tt-val">' + f.samples + '</span></div>' +
        '<div class="tt-row"><span>Service</span><span class="tt-val">' + V.util.escapeHtml(f.service) + '</span></div>';
    });
  }

  V.register('flame-graph', 'data-frames', render);
})();
