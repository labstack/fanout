(function() {
  'use strict';
  var V = window.FanoutViz;

  function sparklineSVG(values, color, expanded) {
    var sparkW = expanded ? 120 : 80;
    var sparkH = 20;
    var max = Math.max.apply(null, values);
    var min = Math.min.apply(null, values);
    var range = max - min || 1;
    var step = sparkW / (values.length - 1);

    var path = '';
    var areaPath = '';
    values.forEach(function(v, i) {
      var x = i * step;
      var y = sparkH - ((v - min) / range) * (sparkH - 4) - 2;
      if (i === 0) {
        path += 'M ' + x + ' ' + y;
        areaPath += 'M ' + x + ' ' + sparkH + 'L ' + x + ' ' + y;
      } else {
        path += ' L ' + x + ' ' + y;
        areaPath += ' L ' + x + ' ' + y;
      }
    });
    areaPath += ' L ' + ((values.length - 1) * step) + ' ' + sparkH + ' Z';

    return '<svg width="' + sparkW + '" height="' + sparkH + '" viewBox="0 0 ' + sparkW + ' ' + sparkH + '">' +
      '<path d="' + areaPath + '" fill="' + color + '" opacity="0.15"/>' +
      '<path d="' + path + '" fill="none" stroke="' + color + '" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>' +
    '</svg>';
  }

  function statusPill(status, errorRate) {
    if (status === 'degraded' || errorRate > 1) {
      return '<span class="status-pill" style="background:rgba(239,68,68,0.12);color:var(--danger)">DEGRADED</span>';
    }
    if (errorRate > 0.3) {
      return '<span class="status-pill" style="background:rgba(245,158,11,0.12);color:var(--warning)">WATCH</span>';
    }
    return '<span class="status-pill" style="background:rgba(34,197,94,0.12);color:var(--success)">OK</span>';
  }

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-endpoints');
    if (!data || !data.endpoints) return;

    var endpoints = data.endpoints;

    var html = '<table>' +
      '<thead><tr>' +
        '<th>Endpoint</th>' +
        '<th class="num">RPM</th>' +
        '<th class="num">P50</th>' +
        '<th class="num">P95</th>' +
        '<th class="num">P99</th>' +
        '<th class="num">Err%</th>' +
        '<th>Trend (P95)</th>' +
        '<th>Status</th>' +
      '</tr></thead><tbody>';

    endpoints.forEach(function(ep) {
      var color = ep.status === 'degraded' ? '#ef4444' : ep.errorRate > 0.3 ? '#f59e0b' : '#0ea5e9';
      html += '<tr>' +
        '<td class="endpoint-name"><code>' + V.util.escapeHtml(ep.method) + '</code> ' + V.util.escapeHtml(ep.path) + '</td>' +
        '<td class="num">' + V.util.escapeHtml(String(ep.rpm)) + '</td>' +
        '<td class="num">' + V.util.escapeHtml(String(ep.p50)) + 'ms</td>' +
        '<td class="num" style="color:' + (ep.p95 > 200 ? '#ef4444' : ep.p95 > 100 ? '#f59e0b' : 'inherit') + '">' + V.util.escapeHtml(String(ep.p95)) + 'ms</td>' +
        '<td class="num" style="color:' + (ep.p99 > 500 ? '#ef4444' : ep.p99 > 200 ? '#f59e0b' : 'inherit') + '">' + V.util.escapeHtml(String(ep.p99)) + 'ms</td>' +
        '<td class="num" style="color:' + (ep.errorRate > 1 ? '#ef4444' : ep.errorRate > 0.3 ? '#f59e0b' : 'inherit') + '">' + V.util.escapeHtml(String(ep.errorRate)) + '%</td>' +
        '<td class="sparkline-cell">' + sparklineSVG(ep.trend, color, expanded) + '</td>' +
        '<td>' + statusPill(ep.status, ep.errorRate) + '</td>' +
      '</tr>';
    });

    html += '</tbody></table>';
    container.innerHTML = html;
  }

  V.register('endpoint-breakdown', 'data-endpoints', render);
})();
