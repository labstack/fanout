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

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    var step = chartW / (n - 1);

    // X-axis labels at bottom
    times.forEach(function(t, i) {
      if (i % (expanded ? 1 : 2) === 0) {
        var x = padL + i * step;
        svg += '<text class="corr-axis-label" x="' + x + '" y="' + (totalH - 4) + '" text-anchor="middle">' + t + '</text>';
      }
    });

    panels.forEach(function(panel, pi) {
      var yOff = padT + pi * (panelH + panelGap);
      var values = panel.values;
      var color = panel.color;
      var maxVal = Math.max.apply(null, values) * 1.2;
      var minVal = 0;
      var range = maxVal - minVal || 1;

      // Panel title
      svg += '<text class="corr-panel-title" x="' + (padL - 4) + '" y="' + (yOff + 10) + '" text-anchor="end" style="font-size:' + (expanded ? '9px' : '8px') + '">' + V.util.escapeHtml(panel.label) + '</text>';

      // Gridlines
      for (var g = 0; g <= 3; g++) {
        var gy = yOff + panelH - (g / 3) * panelH;
        var gval = minVal + (g / 3) * range;
        svg += '<line class="corr-gridline" x1="' + padL + '" y1="' + gy + '" x2="' + (padL + chartW) + '" y2="' + gy + '"/>';
        svg += '<text class="corr-axis-label" x="' + (padL - 6) + '" y="' + (gy + 3) + '" text-anchor="end">' + formatValue(gval, panel.label) + '</text>';
      }

      // Baseline
      if (panel.baseline !== undefined) {
        var by = yOff + panelH - ((panel.baseline - minVal) / range) * panelH;
        svg += '<line x1="' + padL + '" y1="' + by + '" x2="' + (padL + chartW) + '" y2="' + by + '" stroke="' + color + '" stroke-width="0.5" stroke-dasharray="4 3" opacity="0.4"/>';
      }

      // Area + Line
      var linePath = '';
      var areaPath = '';
      values.forEach(function(v, i) {
        var x = padL + i * step;
        var y = yOff + panelH - ((v - minVal) / range) * panelH;
        if (i === 0) {
          linePath += 'M ' + x + ' ' + y;
          areaPath += 'M ' + x + ' ' + (yOff + panelH) + ' L ' + x + ' ' + y;
        } else {
          linePath += ' L ' + x + ' ' + y;
          areaPath += ' L ' + x + ' ' + y;
        }
      });
      areaPath += ' L ' + (padL + (n - 1) * step) + ' ' + (yOff + panelH) + ' Z';

      svg += '<path class="corr-area" d="' + areaPath + '" fill="' + color + '"/>';
      svg += '<path class="corr-line" d="' + linePath + '" stroke="' + color + '"/>';

      // Event markers
      if (panel.markers) {
        panel.markers.forEach(function(m) {
          var mx = padL + m.t * step;
          var mv = values[m.t];
          var my = yOff + panelH - ((mv - minVal) / range) * panelH;
          var markerColor = m.severity === 'critical' ? '#ef4444' : '#f59e0b';
          svg += '<circle class="corr-marker" cx="' + mx + '" cy="' + my + '" r="3.5" fill="' + markerColor + '" stroke="var(--bg-secondary)" stroke-width="1.5"' +
            ' data-marker-panel="' + pi + '" data-marker-t="' + m.t + '" />';
        });
      }
    });

    svg += '</svg>';

    container.innerHTML = svg;
    container._data = data;

    // Event delegation for markers
    container.addEventListener('mouseover', function(ev) {
      var marker = ev.target.closest('.corr-marker');
      if (!marker) return;
      var pi = parseInt(marker.getAttribute('data-marker-panel'), 10);
      var t = parseInt(marker.getAttribute('data-marker-t'), 10);
      var panel = container._data.panels[pi];
      if (!panel || !panel.markers) return;
      var m = null;
      for (var i = 0; i < panel.markers.length; i++) {
        if (panel.markers[i].t === t) { m = panel.markers[i]; break; }
      }
      if (!m) return;
      var sevColor = m.severity === 'critical' ? '#ef4444' : '#f59e0b';
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(m.label) + '</div>' +
        '<div class="tt-row"><span>Severity</span><span class="tt-val" style="color:' + sevColor + '">' + V.util.escapeHtml(String(m.severity)) + '</span></div>' +
        '<div class="tt-row"><span>Time</span><span class="tt-val">' + V.util.escapeHtml(String(container._data.times[m.t])) + '</span></div>' +
        '<div class="tt-row"><span>Value</span><span class="tt-val">' + V.util.escapeHtml(String(panel.values[m.t])) + '</span></div>',
        ev
      );
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.corr-marker')) V.tooltip.hide();
    });
  }

  V.register('correlation-view', 'data-correlation', render);
})();
