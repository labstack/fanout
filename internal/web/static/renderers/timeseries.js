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

    // Global min/max
    var allVals = [];
    series.forEach(function(s) { allVals = allVals.concat(s.values); });
    if (allVals.length === 0) return;
    var minVal = Math.min.apply(null, allVals);
    var maxVal = Math.max.apply(null, allVals);
    var range = maxVal - minVal || 1;
    minVal = Math.max(0, minVal - range * 0.05);
    maxVal = maxVal + range * 0.1;

    var n = series[0].values.length;
    var stepX = chartW / Math.max(n - 1, 1);
    var yScale = V.scale.linear([minVal, maxVal], [chartH, 0]);
    var yTicks = [];
    for (var g = 0; g <= 5; g++) yTicks.push(minVal + (g / 5) * (maxVal - minVal));
    var fmtType = yLabel.indexOf('%') !== -1 ? 'pct' : 'ms';

    var out = V.svg.viewBox(totalW, totalH);

    // Y-axis gridlines + labels
    out += '<g transform="translate(' + padL + ',' + padT + ')">';
    out += V.draw.gridY(yScale, yTicks, chartW, 'class="ts-gridline"');
    yTicks.forEach(function(t) {
      out += V.svg.text(-6, Math.round(yScale(t)) + 3, V.util.format(t, fmtType), 'class="ts-axis-label" text-anchor="end"');
    });
    out += '</g>';

    // X-axis labels
    if (labels.length > 0) {
      var xSkip = Math.max(1, Math.ceil(labels.length / (expanded ? 12 : 8)));
      labels.forEach(function(lbl, i) {
        if (i % xSkip !== 0 && i !== labels.length - 1) return;
        out += V.svg.text(padL + i * stepX, totalH - 4, lbl, 'class="ts-axis-label" text-anchor="middle"');
      });
    }

    // Y-axis title
    if (yLabel) {
      out += V.svg.text(12, padT + chartH / 2, yLabel,
        'class="ts-axis-title" text-anchor="middle" transform="rotate(-90 12 ' + (padT + chartH / 2) + ')"');
    }

    // Series
    series.forEach(function(s, si) {
      var color = s.color || V.colors.service(s.label || ('series-' + si));
      var type = s.type || 'line';
      var values = s.values;

      // Build points array for draw helpers
      var xFn = function(p) { return padL + p.i * stepX; };
      var yFn = function(p) { return padT + yScale(p.v); };
      var points = values.map(function(v, i) { return {i: i, v: v}; });

      var linePath = V.draw.linePath(points, xFn, yFn);

      if (type === 'area') {
        var areaPath = V.draw.areaPath(points, xFn, yFn, padT + chartH);
        out += V.svg.path(areaPath, 'class="ts-area" fill="' + color + '"');
      }
      out += V.svg.path(linePath, 'class="ts-line" stroke="' + color + '"');

      // Data points
      values.forEach(function(v, i) {
        var x = padL + i * stepX;
        var y = padT + yScale(v);
        out += V.svg.circle(x, y, 3,
          'class="ts-dot" fill="' + color + '" stroke="var(--bg-secondary)" stroke-width="1.5" data-series="' + si + '" data-idx="' + i + '"');
      });
    });

    out += '</svg>';

    // Legend
    if (series.length > 1) {
      out += V.legend(series.map(function(s, si) {
        return { color: s.color || V.colors.service(s.label || ('series-' + si)), label: s.label || ('Series ' + (si + 1)) };
      }));
    }

    container.innerHTML = out;
    container._data = data;

    V.tooltip.wire(container, '.ts-dot', function(el) {
      var si = parseInt(el.getAttribute('data-series'), 10);
      var idx = parseInt(el.getAttribute('data-idx'), 10);
      var d = container._data;
      var s = d.series[si];
      var val = s.values[idx];
      var lbl = d.labels && d.labels[idx] ? d.labels[idx] : '#' + idx;
      return '<div class="tt-title">' + V.util.escapeHtml(s.label || ('Series ' + (si + 1))) + '</div>' +
        '<div class="tt-row"><span>' + V.util.escapeHtml(lbl) + '</span><span class="tt-val">' + V.util.escapeHtml(String(val)) + '</span></div>';
    });
  }

  V.register('timeseries-chart', 'data-timeseries', render);
})();
