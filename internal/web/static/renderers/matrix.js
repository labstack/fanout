(function() {
  'use strict';
  var V = window.FanoutViz;

  function matrixColor(errRate) {
    if (errRate === undefined || errRate === null) return 'var(--bg-tertiary)';
    if (errRate === 0) return 'rgba(34, 197, 94, 0.15)';
    if (errRate < 0.5) return 'rgba(34, 197, 94, 0.3)';
    if (errRate < 1.0) return 'rgba(234, 179, 8, 0.3)';
    if (errRate < 2.0) return 'rgba(234, 179, 8, 0.6)';
    return 'rgba(239, 68, 68, 0.6)';
  }

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-matrix');
    if (!data) return;

    var services = data.services;
    var cells = data.cells;
    var n = services.length;

    var labelW = expanded ? 100 : 75;
    var cellSize = expanded ? 50 : 36;
    var padX = expanded ? 20 : 8;
    var padY = expanded ? 20 : 8;
    var headerH = expanded ? 80 : 60;
    var totalW = padX + labelW + n * cellSize + 10;
    var totalH = padY + headerH + n * cellSize + 10;

    var lookup = {};
    cells.forEach(function(c) { lookup[c.from + '\u2192' + c.to] = c; });

    var out = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Column headers
    services.forEach(function(svc, i) {
      var x = padX + labelW + i * cellSize + cellSize / 2;
      var y = padY + headerH - 4;
      out += V.svg.text(x, y, V.util.escapeHtml(svc),
        'class="dm-label" text-anchor="end" transform="rotate(-45 ' + x + ' ' + y + ')" style="font-size:' + (expanded ? '9px' : '8px') + '"');
    });

    // Row headers + cells
    services.forEach(function(from, ri) {
      var y = padY + headerH + ri * cellSize;
      out += V.svg.text(padX + labelW - 6, y + cellSize/2 + 3, V.util.escapeHtml(from),
        'class="dm-label" text-anchor="end" style="font-size:' + (expanded ? '9px' : '8px') + '"');

      services.forEach(function(to, ci) {
        var x = padX + labelW + ci * cellSize;
        var cell = lookup[from + '\u2192' + to];

        if (from === to) {
          out += V.svg.rect(x + 0.5, y + 0.5, cellSize - 1, cellSize - 1, 'fill="var(--bg-tertiary)" rx="2" ry="2" opacity="0.3"');
          out += V.svg.line(x + 4, y + 4, x + cellSize - 4, y + cellSize - 4, 'stroke="var(--border-color)" stroke-width="0.5"');
        } else if (cell) {
          var color = matrixColor(cell.errorRate);
          out += V.svg.rect(x + 0.5, y + 0.5, cellSize - 1, cellSize - 1,
            'class="dm-cell" fill="' + color + '" rx="2" ry="2" data-from="' + V.util.escapeHtml(from) + '" data-to="' + V.util.escapeHtml(to) + '"');
          if (cellSize >= 36) {
            out += V.svg.text(x + cellSize/2, y + cellSize/2 + 3, V.util.escapeHtml(String(cell.errorRate)) + '%', 'class="dm-value"');
          }
        } else {
          out += V.svg.rect(x + 0.5, y + 0.5, cellSize - 1, cellSize - 1, 'fill="var(--bg-primary)" rx="2" ry="2" opacity="0.5"');
        }
      });
    });

    // Headers
    out += V.svg.text(padX + labelW + (n * cellSize) / 2, padY + 8, 'CALLEE \u2192', 'class="dm-header"');
    out += V.svg.text(padX + 4, padY + headerH + (n * cellSize) / 2, 'CALLER \u2192',
      'class="dm-header" transform="rotate(-90 ' + (padX + 4) + ' ' + (padY + headerH + (n * cellSize) / 2) + ')"');

    out += '</svg>';

    out += V.legend([
      { color: 'rgba(34,197,94,0.3)', label: '< 0.5%' },
      { color: 'rgba(234,179,8,0.3)', label: '0.5-1%' },
      { color: 'rgba(234,179,8,0.6)', label: '1-2%' },
      { color: 'rgba(239,68,68,0.6)', label: '> 2%' },
      { color: 'var(--bg-primary)', label: 'No connection' }
    ]);

    container.innerHTML = out;
    container._cells = lookup;

    V.tooltip.wire(container, '.dm-cell', function(el) {
      var from = el.getAttribute('data-from');
      var to = el.getAttribute('data-to');
      var cell = container._cells[from + '\u2192' + to];
      if (!cell) return '';
      return '<div class="tt-title">' + V.util.escapeHtml(from) + ' \u2192 ' + V.util.escapeHtml(to) + '</div>' +
        '<div class="tt-row"><span>Error Rate</span><span class="tt-val" style="color:' + (cell.errorRate > 1 ? '#ef4444' : cell.errorRate > 0.5 ? '#f59e0b' : 'inherit') + '">' + V.util.escapeHtml(String(cell.errorRate)) + '%</span></div>' +
        '<div class="tt-row"><span>Volume</span><span class="tt-val">' + V.util.escapeHtml(String(cell.rpm)) + ' rpm</span></div>' +
        '<div class="tt-row"><span>P95 Latency</span><span class="tt-val">' + V.util.escapeHtml(String(cell.p95)) + 'ms</span></div>';
    });
  }

  V.register('dep-matrix', 'data-matrix', render);
})();
