(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-barchart');
    if (!data || !data.bars || data.bars.length === 0) return;

    var bars = data.bars;
    var yLabel = data.yLabel || '';
    var horizontal = data.horizontal || false;

    var maxVal = Math.max.apply(null, bars.map(function(b) { return b.value; })) * 1.15;
    if (maxVal === 0) maxVal = 1;

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

    var yScale = V.scale.linear([0, maxVal], [chartH, 0]);
    var yTicks = [];
    for (var g = 0; g <= 5; g++) yTicks.push((g / 5) * maxVal);

    var out = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Gridlines + Y-axis labels
    out += '<g transform="translate(' + padL + ',' + padT + ')">';
    out += V.draw.gridY(yScale, yTicks, chartW, 'class="bc-gridline"');
    yTicks.forEach(function(t) {
      out += V.svg.text(-6, Math.round(yScale(t)) + 3, Math.round(t), 'class="bc-axis-label" text-anchor="end"');
    });

    // Bars
    bars.forEach(function(b, i) {
      var x = gap + i * (barW + gap);
      var h = (b.value / maxVal) * chartH;
      var y = chartH - h;
      var color = b.color || V.colors.service(b.label);

      out += V.svg.rect(x, y, barW, h, 'class="bc-bar" fill="' + color + '" data-idx="' + i + '"');
      out += V.svg.text(x + barW / 2, y - 4, V.util.escapeHtml(String(b.value)), 'class="bc-value-label" text-anchor="middle"');

      var labelText = V.util.truncate(b.label, barW + gap - 4, 6);
      out += V.svg.text(x + barW / 2, chartH + padB - 6, V.util.escapeHtml(labelText), 'class="bc-axis-label" text-anchor="middle"');
    });
    out += '</g>';

    if (yLabel) {
      out += V.svg.text(10, padT + chartH / 2, V.util.escapeHtml(yLabel),
        'class="bc-axis-title" text-anchor="middle" transform="rotate(-90 10 ' + (padT + chartH / 2) + ')"');
    }

    out += '</svg>';
    container.innerHTML = out;
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

    var xScale = V.scale.linear([0, maxVal], [0, chartW]);
    var xTicks = [];
    for (var g = 0; g <= 5; g++) xTicks.push((g / 5) * maxVal);

    var out = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // X-axis gridlines
    xTicks.forEach(function(t) {
      var gx = padL + xScale(t);
      out += V.svg.line(gx, padT, gx, totalH - padB, 'class="bc-gridline"');
      out += V.svg.text(gx, totalH - 4, Math.round(t), 'class="bc-axis-label" text-anchor="middle"');
    });

    // Bars
    bars.forEach(function(b, i) {
      var y = padT + i * (barH + gap);
      var w = xScale(b.value);
      var color = b.color || V.colors.service(b.label);

      out += V.svg.rect(padL, y, w, barH, 'class="bc-bar" fill="' + color + '" data-idx="' + i + '"');
      out += V.svg.text(padL + w + 6, y + barH / 2 + 3, V.util.escapeHtml(String(b.value)), 'class="bc-value-label" text-anchor="start"');
      out += V.svg.text(padL - 6, y + barH / 2 + 3, V.util.escapeHtml(V.util.truncate(b.label, padL - 16, 6)), 'class="bc-axis-label" text-anchor="end"');
    });

    out += '</svg>';
    container.innerHTML = out;
    container._bars = bars;
    wireEvents(container);
  }

  function wireEvents(container) {
    V.tooltip.wire(container, '.bc-bar', function(el) {
      var b = container._bars[parseInt(el.getAttribute('data-idx'), 10)];
      if (!b) return '';
      return '<div class="tt-title">' + V.util.escapeHtml(b.label) + '</div>' +
        '<div class="tt-row"><span>Value</span><span class="tt-val">' + b.value + '</span></div>';
    });
  }

  V.register('bar-chart', 'data-barchart', render);
})();
