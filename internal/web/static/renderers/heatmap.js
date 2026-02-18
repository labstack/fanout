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

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Y-axis labels (reversed so low latency at bottom)
    var reversedBuckets = buckets.slice().reverse();
    reversedBuckets.forEach(function(b, bi) {
      var y = padY + labelH + bi * cellH;
      svg += '<text class="hm-axis-label" x="' + (padX + labelW - 4) + '" y="' + (y + cellH/2 + 3) + '" text-anchor="end">' + b + 'ms</text>';
    });

    // X-axis labels
    times.forEach(function(t, ti) {
      var x = padX + labelW + ti * cellW;
      if (ti % (expanded ? 1 : 2) === 0) {
        svg += '<text class="hm-axis-label" x="' + (x + cellW/2) + '" y="' + (totalH - 8) + '" text-anchor="middle">' + t + '</text>';
      }
    });

    // Cells
    times.forEach(function(t, ti) {
      var reversedVals = values[ti].slice().reverse();
      reversedVals.forEach(function(val, bi) {
        var x = padX + labelW + ti * cellW;
        var y = padY + labelH + bi * cellH;
        var color = heatColor(val, maxVal);
        svg += '<rect class="hm-cell" x="' + (x + 0.5) + '" y="' + (y + 0.5) + '" width="' + (cellW - 1) + '" height="' + (cellH - 1) + '" fill="' + color + '" rx="2" ry="2"' +
          ' data-ti="' + ti + '" data-bi="' + bi + '" />';
      });
    });

    svg += '<text class="hm-axis-title" x="' + padX + '" y="' + (padY + 2) + '" text-anchor="start">Latency</text>';
    svg += '</svg>';

    // Color legend
    svg += '<div class="viz-legend">' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(14,165,233,0.15)"></span> Low</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(14,165,233,0.6)"></span> Medium</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(234,179,8,0.6)"></span> High</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(239,68,68,0.8)"></span> Critical</div>' +
    '</div>';

    container.innerHTML = svg;
    container._data = { times: times, reversedBuckets: reversedBuckets, values: values };

    // Event delegation
    container.addEventListener('mouseover', function(ev) {
      var cell = ev.target.closest('.hm-cell');
      if (!cell) return;
      var ti = parseInt(cell.getAttribute('data-ti'), 10);
      var bi = parseInt(cell.getAttribute('data-bi'), 10);
      var d = container._data;
      var val = d.values[ti].slice().reverse()[bi];
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(String(d.times[ti])) + ' \u00b7 ' + V.util.escapeHtml(String(d.reversedBuckets[bi])) + 'ms</div>' +
        '<div class="tt-row"><span>Requests</span><span class="tt-val">' + V.util.escapeHtml(String(val)) + '</span></div>',
        ev
      );
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.hm-cell')) V.tooltip.hide();
    });
  }

  V.register('latency-heatmap', 'data-heatmap', render);
})();
