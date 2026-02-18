(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-timeseries');
    if (!data || !data.series || data.series.length === 0) return;

    var series = data.series;
    var labels = data.labels || [];
    var yLabel = data.yLabel || '';

    var padL = expanded ? 60 : 45;
    var padR = expanded ? 20 : 10;
    var padT = expanded ? 16 : 10;
    var padB = expanded ? 30 : 24;
    var totalW = expanded ? 1200 : 780;
    var totalH = expanded ? 300 : 200;
    var chartW = totalW - padL - padR;
    var chartH = totalH - padT - padB;

    // Compute global min/max across all series
    var allVals = [];
    series.forEach(function(s) { allVals = allVals.concat(s.values); });
    var minVal = Math.min.apply(null, allVals);
    var maxVal = Math.max.apply(null, allVals);
    // Add some padding
    var range = maxVal - minVal || 1;
    minVal = Math.max(0, minVal - range * 0.05);
    maxVal = maxVal + range * 0.1;
    range = maxVal - minVal;

    var n = series[0].values.length;
    var stepX = chartW / Math.max(n - 1, 1);

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Y-axis gridlines and labels
    var yTicks = 5;
    for (var g = 0; g <= yTicks; g++) {
      var yVal = minVal + (g / yTicks) * range;
      var gy = padT + chartH - (g / yTicks) * chartH;
      svg += '<line class="ts-gridline" x1="' + padL + '" y1="' + gy + '" x2="' + (padL + chartW) + '" y2="' + gy + '"/>';
      svg += '<text class="ts-axis-label" x="' + (padL - 6) + '" y="' + (gy + 3) + '" text-anchor="end">' + V.util.format(yVal, yLabel.indexOf('%') !== -1 ? 'pct' : 'ms') + '</text>';
    }

    // X-axis labels
    if (labels.length > 0) {
      var xSkip = Math.max(1, Math.ceil(labels.length / (expanded ? 12 : 8)));
      labels.forEach(function(lbl, i) {
        if (i % xSkip !== 0 && i !== labels.length - 1) return;
        var x = padL + i * stepX;
        svg += '<text class="ts-axis-label" x="' + x + '" y="' + (totalH - 4) + '" text-anchor="middle">' + lbl + '</text>';
      });
    }

    // Y-axis title
    if (yLabel) {
      svg += '<text class="ts-axis-title" x="' + 12 + '" y="' + (padT + chartH / 2) + '" text-anchor="middle" transform="rotate(-90 12 ' + (padT + chartH / 2) + ')">' + V.util.escapeHtml(yLabel) + '</text>';
    }

    // Render each series
    series.forEach(function(s, si) {
      var color = s.color || V.colors.service(s.label || ('series-' + si));
      var type = s.type || 'line';
      var values = s.values;

      var linePath = '';
      var areaPath = '';
      values.forEach(function(v, i) {
        var x = padL + i * stepX;
        var y = padT + chartH - ((v - minVal) / range) * chartH;
        if (i === 0) {
          linePath += 'M ' + x + ' ' + y;
          areaPath += 'M ' + x + ' ' + (padT + chartH) + ' L ' + x + ' ' + y;
        } else {
          linePath += ' L ' + x + ' ' + y;
          areaPath += ' L ' + x + ' ' + y;
        }
      });
      areaPath += ' L ' + (padL + (values.length - 1) * stepX) + ' ' + (padT + chartH) + ' Z';

      if (type === 'area') {
        svg += '<path class="ts-area" d="' + areaPath + '" fill="' + color + '"/>';
      }
      svg += '<path class="ts-line" d="' + linePath + '" stroke="' + color + '"/>';

      // Data points (hover targets)
      values.forEach(function(v, i) {
        var x = padL + i * stepX;
        var y = padT + chartH - ((v - minVal) / range) * chartH;
        svg += '<circle class="ts-dot" cx="' + x + '" cy="' + y + '" r="3" fill="' + color + '" stroke="var(--bg-secondary)" stroke-width="1.5"' +
          ' data-series="' + si + '" data-idx="' + i + '" />';
      });
    });

    svg += '</svg>';

    // Legend
    if (series.length > 1) {
      svg += '<div class="viz-legend">';
      series.forEach(function(s, si) {
        var color = s.color || V.colors.service(s.label || ('series-' + si));
        svg += '<div class="viz-legend-item"><span class="swatch" style="background:' + color + '"></span> ' + V.util.escapeHtml(s.label || ('Series ' + (si + 1))) + '</div>';
      });
      svg += '</div>';
    }

    container.innerHTML = svg;
    container._data = data;

    // Event delegation
    container.addEventListener('mouseover', function(ev) {
      var dot = ev.target.closest('.ts-dot');
      if (!dot) return;
      var si = parseInt(dot.getAttribute('data-series'), 10);
      var idx = parseInt(dot.getAttribute('data-idx'), 10);
      var d = container._data;
      var s = d.series[si];
      var val = s.values[idx];
      var lbl = d.labels && d.labels[idx] ? d.labels[idx] : '#' + idx;
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(s.label || ('Series ' + (si + 1))) + '</div>' +
        '<div class="tt-row"><span>' + V.util.escapeHtml(lbl) + '</span><span class="tt-val">' + val + '</span></div>',
        ev
      );
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.ts-dot')) V.tooltip.hide();
    });
  }

  V.register('timeseries-chart', 'data-timeseries', render);
})();
