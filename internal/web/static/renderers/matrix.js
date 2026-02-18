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

    // Build lookup
    var lookup = {};
    cells.forEach(function(c) { lookup[c.from + '\u2192' + c.to] = c; });

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Column headers (rotated)
    services.forEach(function(svc, i) {
      var x = padX + labelW + i * cellSize + cellSize / 2;
      var y = padY + headerH - 4;
      svg += '<text class="dm-label" x="' + x + '" y="' + y + '" text-anchor="end" transform="rotate(-45 ' + x + ' ' + y + ')" style="font-size:' + (expanded ? '9px' : '8px') + '">' + V.util.escapeHtml(svc) + '</text>';
    });

    // Row headers + cells
    services.forEach(function(from, ri) {
      var y = padY + headerH + ri * cellSize;
      svg += '<text class="dm-label" x="' + (padX + labelW - 6) + '" y="' + (y + cellSize/2 + 3) + '" text-anchor="end" style="font-size:' + (expanded ? '9px' : '8px') + '">' + V.util.escapeHtml(from) + '</text>';

      services.forEach(function(to, ci) {
        var x = padX + labelW + ci * cellSize;
        var cell = lookup[from + '\u2192' + to];

        if (from === to) {
          svg += '<rect x="' + (x + 0.5) + '" y="' + (y + 0.5) + '" width="' + (cellSize - 1) + '" height="' + (cellSize - 1) + '" fill="var(--bg-tertiary)" rx="2" ry="2" opacity="0.3"/>';
          svg += '<line x1="' + (x + 4) + '" y1="' + (y + 4) + '" x2="' + (x + cellSize - 4) + '" y2="' + (y + cellSize - 4) + '" stroke="var(--border-color)" stroke-width="0.5"/>';
        } else if (cell) {
          var color = matrixColor(cell.errorRate);
          svg += '<rect class="dm-cell" x="' + (x + 0.5) + '" y="' + (y + 0.5) + '" width="' + (cellSize - 1) + '" height="' + (cellSize - 1) + '" fill="' + color + '" rx="2" ry="2"' +
            ' data-from="' + V.util.escapeHtml(from) + '" data-to="' + V.util.escapeHtml(to) + '" />';
          if (cellSize >= 36) {
            svg += '<text class="dm-value" x="' + (x + cellSize/2) + '" y="' + (y + cellSize/2 + 3) + '">' + V.util.escapeHtml(String(cell.errorRate)) + '%</text>';
          }
        } else {
          svg += '<rect x="' + (x + 0.5) + '" y="' + (y + 0.5) + '" width="' + (cellSize - 1) + '" height="' + (cellSize - 1) + '" fill="var(--bg-primary)" rx="2" ry="2" opacity="0.5"/>';
        }
      });
    });

    // Headers
    svg += '<text class="dm-header" x="' + (padX + labelW + (n * cellSize) / 2) + '" y="' + (padY + 8) + '">CALLEE \u2192</text>';
    svg += '<text class="dm-header" x="' + (padX + 4) + '" y="' + (padY + headerH + (n * cellSize) / 2) + '" transform="rotate(-90 ' + (padX + 4) + ' ' + (padY + headerH + (n * cellSize) / 2) + ')">CALLER \u2192</text>';

    svg += '</svg>';

    // Legend
    svg += '<div class="viz-legend">' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(34,197,94,0.3)"></span> &lt; 0.5%</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(234,179,8,0.3)"></span> 0.5-1%</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(234,179,8,0.6)"></span> 1-2%</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:rgba(239,68,68,0.6)"></span> &gt; 2%</div>' +
      '<div class="viz-legend-item"><span class="swatch" style="background:var(--bg-primary)"></span> No connection</div>' +
    '</div>';

    container.innerHTML = svg;
    container._cells = lookup;

    // Event delegation
    container.addEventListener('mouseover', function(ev) {
      var el = ev.target.closest('.dm-cell');
      if (!el) return;
      var from = el.getAttribute('data-from');
      var to = el.getAttribute('data-to');
      var cell = container._cells[from + '\u2192' + to];
      if (!cell) return;
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(from) + ' \u2192 ' + V.util.escapeHtml(to) + '</div>' +
        '<div class="tt-row"><span>Error Rate</span><span class="tt-val" style="color:' + (cell.errorRate > 1 ? '#ef4444' : cell.errorRate > 0.5 ? '#f59e0b' : 'inherit') + '">' + V.util.escapeHtml(String(cell.errorRate)) + '%</span></div>' +
        '<div class="tt-row"><span>Volume</span><span class="tt-val">' + V.util.escapeHtml(String(cell.rpm)) + ' rpm</span></div>' +
        '<div class="tt-row"><span>P95 Latency</span><span class="tt-val">' + V.util.escapeHtml(String(cell.p95)) + 'ms</span></div>',
        ev
      );
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.dm-cell')) V.tooltip.hide();
    });
  }

  V.register('dep-matrix', 'data-matrix', render);
})();
