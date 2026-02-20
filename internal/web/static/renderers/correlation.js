(function() {
  'use strict';
  var V = window.FanoutViz;

  function formatValue(val, label) {
    if (label.indexOf('ms') !== -1) return Math.round(val) + '';
    if (label.indexOf('%') !== -1) return val.toFixed(1);
    return Math.round(val) + '';
  }

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-correlation');
    if (!data || !data.panels) return;

    var times = data.times;
    if (!times || times.length < 2) return;
    var panels = data.panels;
    var n = times.length;

    var padL = expanded ? 70 : 50;
    var padR = expanded ? 20 : 10;
    var padT = expanded ? 8 : 4;
    var panelH = expanded ? 90 : 60;
    var panelGap = expanded ? 30 : 20;
    var totalW = expanded ? 1200 : 780;
    var chartW = totalW - padL - padR;
    var totalH = padT + panels.length * (panelH + panelGap) + 20;

    var step = chartW / (n - 1);

    var out = V.svg.viewBox(totalW, totalH);

    // X-axis labels
    times.forEach(function(t, i) {
      if (i % (expanded ? 1 : 2) === 0) {
        out += V.svg.text(padL + i * step, totalH - 4, t, 'class="corr-axis-label" text-anchor="middle"');
      }
    });

    panels.forEach(function(panel, pi) {
      var yOff = padT + pi * (panelH + panelGap);
      var values = panel.values;
      var color = panel.color;
      var maxVal = Math.max.apply(null, values) * 1.2 || 1;

      var yScale = V.scale.linear([0, maxVal], [panelH, 0]);

      // Panel title
      out += V.svg.text(padL - 4, yOff + 10, panel.label,
        'class="corr-panel-title" text-anchor="end" style="font-size:' + (expanded ? '9px' : '8px') + '"');

      // Gridlines + labels
      for (var g = 0; g <= 3; g++) {
        var gy = yOff + panelH - (g / 3) * panelH;
        var gval = (g / 3) * maxVal;
        out += V.svg.line(padL, gy, padL + chartW, gy, 'class="corr-gridline"');
        out += V.svg.text(padL - 6, gy + 3, formatValue(gval, panel.label), 'class="corr-axis-label" text-anchor="end"');
      }

      // Baseline
      if (panel.baseline !== undefined) {
        var by = yOff + yScale(panel.baseline);
        out += V.svg.line(padL, by, padL + chartW, by,
          'stroke="' + color + '" stroke-width="0.5" stroke-dasharray="4 3" opacity="0.4"');
      }

      // Area + Line using draw helpers
      var points = values.map(function(v, i) { return {i: i, v: v}; });
      var xFn = function(p) { return padL + p.i * step; };
      var yFn = function(p) { return yOff + yScale(p.v); };

      var areaPath = V.draw.areaPath(points, xFn, yFn, yOff + panelH);
      var linePath = V.draw.linePath(points, xFn, yFn);

      out += V.svg.path(areaPath, 'class="corr-area" fill="' + color + '"');
      out += V.svg.path(linePath, 'class="corr-line" stroke="' + color + '"');

      // Event markers
      if (panel.markers) {
        panel.markers.forEach(function(m) {
          var mx = padL + m.t * step;
          var my = yOff + yScale(values[m.t]);
          var markerColor = m.severity === 'critical' ? '#ef4444' : '#f59e0b';
          out += V.svg.circle(mx, my, 3.5,
            'class="corr-marker" fill="' + markerColor + '" stroke="var(--bg-secondary)" stroke-width="1.5" data-marker-panel="' + pi + '" data-marker-t="' + m.t + '"');
        });
      }
    });

    out += '</svg>';
    container.innerHTML = out;
    container._data = data;

    V.tooltip.wire(container, '.corr-marker', function(el) {
      var pi = parseInt(el.getAttribute('data-marker-panel'), 10);
      var t = parseInt(el.getAttribute('data-marker-t'), 10);
      var panel = container._data.panels[pi];
      if (!panel || !panel.markers) return '';
      var m = null;
      for (var i = 0; i < panel.markers.length; i++) {
        if (panel.markers[i].t === t) { m = panel.markers[i]; break; }
      }
      if (!m) return '';
      var sevColor = m.severity === 'critical' ? '#ef4444' : '#f59e0b';
      return '<div class="tt-title">' + V.util.escapeHtml(m.label) + '</div>' +
        '<div class="tt-row"><span>Severity</span><span class="tt-val" style="color:' + sevColor + '">' + V.util.escapeHtml(String(m.severity)) + '</span></div>' +
        '<div class="tt-row"><span>Time</span><span class="tt-val">' + V.util.escapeHtml(String(container._data.times[m.t])) + '</span></div>' +
        '<div class="tt-row"><span>Value</span><span class="tt-val">' + V.util.escapeHtml(String(panel.values[m.t])) + '</span></div>';
    });
  }

  V.register('correlation-view', 'data-correlation', render);
})();
