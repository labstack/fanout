(function() {
  'use strict';
  var V = window.FanoutViz;

  function heatColor(val, maxVal) {
    if (val === 0) return 'var(--bg-tertiary)';
    var ratio = val / maxVal;
    if (ratio < 0.1) return 'rgba(14, 165, 233, 0.15)';
    if (ratio < 0.25) return 'rgba(14, 165, 233, 0.35)';
    if (ratio < 0.5) return 'rgba(14, 165, 233, 0.6)';
    if (ratio < 0.7) return 'rgba(234, 179, 8, 0.6)';
    if (ratio < 0.85) return 'rgba(249, 115, 22, 0.7)';
    return 'rgba(239, 68, 68, 0.8)';
  }

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-heatmap');
    if (!data) return;

    var buckets = data.buckets;
    var times = data.times;
    var values = data.values;

    var labelW = expanded ? 70 : 55;
    var labelH = expanded ? 24 : 18;
    var cellW = expanded ? 70 : 52;
    var cellH = expanded ? 26 : 20;
    var padX = expanded ? 20 : 8;
    var padY = expanded ? 10 : 4;
    var totalW = padX + labelW + times.length * cellW + 10;
    var totalH = padY + labelH + buckets.length * cellH + 30;

    var maxVal = Math.max.apply(null, values.map(function(row) { return Math.max.apply(null, row); }));

    var reversedBuckets = buckets.slice().reverse();

    var out = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Y-axis labels
    reversedBuckets.forEach(function(b, bi) {
      var y = padY + labelH + bi * cellH;
      out += V.svg.text(padX + labelW - 4, y + cellH/2 + 3, b + 'ms', 'class="hm-axis-label" text-anchor="end"');
    });

    // X-axis labels
    times.forEach(function(t, ti) {
      if (ti % (expanded ? 1 : 2) === 0) {
        out += V.svg.text(padX + labelW + ti * cellW + cellW/2, totalH - 8, t, 'class="hm-axis-label" text-anchor="middle"');
      }
    });

    // Cells
    times.forEach(function(t, ti) {
      var reversedVals = values[ti].slice().reverse();
      reversedVals.forEach(function(val, bi) {
        var x = padX + labelW + ti * cellW;
        var y = padY + labelH + bi * cellH;
        out += V.svg.rect(x + 0.5, y + 0.5, cellW - 1, cellH - 1,
          'class="hm-cell" fill="' + heatColor(val, maxVal) + '" rx="2" ry="2" data-ti="' + ti + '" data-bi="' + bi + '"');
      });
    });

    out += V.svg.text(padX, padY + 2, 'Latency', 'class="hm-axis-title" text-anchor="start"');
    out += '</svg>';

    out += V.legend([
      { color: 'rgba(14,165,233,0.15)', label: 'Low' },
      { color: 'rgba(14,165,233,0.6)', label: 'Medium' },
      { color: 'rgba(234,179,8,0.6)', label: 'High' },
      { color: 'rgba(239,68,68,0.8)', label: 'Critical' }
    ]);

    container.innerHTML = out;
    container._data = { times: times, reversedBuckets: reversedBuckets, values: values };

    V.tooltip.wire(container, '.hm-cell', function(el) {
      var ti = parseInt(el.getAttribute('data-ti'), 10);
      var bi = parseInt(el.getAttribute('data-bi'), 10);
      var d = container._data;
      var val = d.values[ti].slice().reverse()[bi];
      return '<div class="tt-title">' + V.util.escapeHtml(String(d.times[ti])) + ' \u00b7 ' + V.util.escapeHtml(String(d.reversedBuckets[bi])) + 'ms</div>' +
        '<div class="tt-row"><span>Requests</span><span class="tt-val">' + V.util.escapeHtml(String(val)) + '</span></div>';
    });
  }

  V.register('latency-heatmap', 'data-heatmap', render);
})();
