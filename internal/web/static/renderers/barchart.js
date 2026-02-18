(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var data = JSON.parse(container.getAttribute('data-barchart'));
    if (!data || !data.bars || data.bars.length === 0) return;

    var bars = data.bars;
    var yLabel = data.yLabel || '';
    var horizontal = data.horizontal || false;

    var maxVal = Math.max.apply(null, bars.map(function(b) { return b.value; }));
    maxVal = maxVal * 1.15; // headroom

    if (horizontal) {
      renderHorizontal(container, bars, maxVal, yLabel, expanded);
    } else {
      renderVertical(container, bars, maxVal, yLabel, expanded);
    }
  }

  function renderVertical(container, bars, maxVal, yLabel, expanded) {
    var n = bars.length;
    var padL = expanded ? 50 : 40;
    var padR = expanded ? 20 : 10;
    var padT = expanded ? 16 : 10;
    var padB = expanded ? 40 : 30;
    var totalW = expanded ? 1200 : 780;
    var totalH = expanded ? 300 : 200;
    var chartW = totalW - padL - padR;
    var chartH = totalH - padT - padB;

    var barW = Math.min(expanded ? 60 : 40, chartW / n * 0.7);
    var gap = (chartW - n * barW) / (n + 1);

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Y-axis gridlines
    var yTicks = 5;
    for (var g = 0; g <= yTicks; g++) {
      var yVal = (g / yTicks) * maxVal;
      var gy = padT + chartH - (g / yTicks) * chartH;
      svg += '<line class="bc-gridline" x1="' + padL + '" y1="' + gy + '" x2="' + (padL + chartW) + '" y2="' + gy + '"/>';
      svg += '<text class="bc-axis-label" x="' + (padL - 6) + '" y="' + (gy + 3) + '" text-anchor="end">' + Math.round(yVal) + '</text>';
    }

    // Bars
    bars.forEach(function(b, i) {
      var x = padL + gap + i * (barW + gap);
      var h = (b.value / maxVal) * chartH;
      var y = padT + chartH - h;
      var color = b.color || V.colors.service(b.label);

      svg += '<rect class="bc-bar" x="' + x + '" y="' + y + '" width="' + barW + '" height="' + h + '" fill="' + color + '" data-idx="' + i + '" />';

      // Value label above bar
      svg += '<text class="bc-value-label" x="' + (x + barW / 2) + '" y="' + (y - 4) + '" text-anchor="middle">' + V.util.escapeHtml(String(b.value)) + '</text>';

      // X-axis label
      var labelText = V.util.truncate(b.label, barW + gap - 4, 6);
      svg += '<text class="bc-axis-label" x="' + (x + barW / 2) + '" y="' + (totalH - 6) + '" text-anchor="middle">' + V.util.escapeHtml(labelText) + '</text>';
    });

    // Y-axis title
    if (yLabel) {
      svg += '<text class="bc-axis-title" x="' + 10 + '" y="' + (padT + chartH / 2) + '" text-anchor="middle" transform="rotate(-90 10 ' + (padT + chartH / 2) + ')">' + V.util.escapeHtml(yLabel) + '</text>';
    }

    svg += '</svg>';
    container.innerHTML = svg;
    container._bars = bars;
    wireEvents(container);
  }

  function renderHorizontal(container, bars, maxVal, yLabel, expanded) {
    var n = bars.length;
    var padL = expanded ? 120 : 80;
    var padR = expanded ? 40 : 20;
    var padT = expanded ? 16 : 10;
    var padB = expanded ? 24 : 16;
    var totalW = expanded ? 1200 : 780;
    var barH = expanded ? 24 : 18;
    var gap = expanded ? 8 : 6;
    var totalH = padT + n * (barH + gap) + padB;
    var chartW = totalW - padL - padR;

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // X-axis gridlines
    var xTicks = 5;
    for (var g = 0; g <= xTicks; g++) {
      var xVal = (g / xTicks) * maxVal;
      var gx = padL + (g / xTicks) * chartW;
      svg += '<line class="bc-gridline" x1="' + gx + '" y1="' + padT + '" x2="' + gx + '" y2="' + (totalH - padB) + '"/>';
      svg += '<text class="bc-axis-label" x="' + gx + '" y="' + (totalH - 4) + '" text-anchor="middle">' + Math.round(xVal) + '</text>';
    }

    // Bars
    bars.forEach(function(b, i) {
      var y = padT + i * (barH + gap);
      var w = (b.value / maxVal) * chartW;
      var color = b.color || V.colors.service(b.label);

      svg += '<rect class="bc-bar" x="' + padL + '" y="' + y + '" width="' + w + '" height="' + barH + '" fill="' + color + '" data-idx="' + i + '" />';

      // Value label
      svg += '<text class="bc-value-label" x="' + (padL + w + 6) + '" y="' + (y + barH / 2 + 3) + '" text-anchor="start">' + V.util.escapeHtml(String(b.value)) + '</text>';

      // Y-axis label
      svg += '<text class="bc-axis-label" x="' + (padL - 6) + '" y="' + (y + barH / 2 + 3) + '" text-anchor="end">' + V.util.escapeHtml(V.util.truncate(b.label, padL - 16, 6)) + '</text>';
    });

    svg += '</svg>';
    container.innerHTML = svg;
    container._bars = bars;
    wireEvents(container);
  }

  function wireEvents(container) {
    container.addEventListener('mouseover', function(ev) {
      var bar = ev.target.closest('.bc-bar');
      if (!bar) return;
      var idx = parseInt(bar.getAttribute('data-idx'), 10);
      var b = container._bars[idx];
      if (!b) return;
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(b.label) + '</div>' +
        '<div class="tt-row"><span>Value</span><span class="tt-val">' + b.value + '</span></div>',
        ev
      );
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.bc-bar')) V.tooltip.hide();
    });
  }

  V.register('bar-chart', 'data-barchart', render);
})();
