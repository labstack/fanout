(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-graph');
    if (!data || !data.nodes) return;

    var nodes = data.nodes;
    var edges = data.edges;

    // DAG layout
    var layerGroups = V.layout.dagLayers(nodes, edges);
    var layerKeys = Object.keys(layerGroups);
    if (layerKeys.length === 0) return;
    var maxLayer = Math.max.apply(null, layerKeys.map(Number));
    var nodeW = expanded ? 120 : 100;
    var nodeH = expanded ? 48 : 40;
    var layerGap = expanded ? 180 : 140;
    var nodeGap = expanded ? 64 : 48;
    var padX = expanded ? 60 : 30;
    var padY = expanded ? 50 : 30;

    var maxPerLayer = Math.max.apply(null, Object.keys(layerGroups).map(function(k) { return layerGroups[k].length; }));

    // Position nodes
    var positions = {};
    for (var l = 0; l <= maxLayer; l++) {
      var group = layerGroups[l] || [];
      var totalH_group = group.length * nodeH + (group.length - 1) * nodeGap;
      var startY = padY + (maxPerLayer * (nodeH + nodeGap) - nodeGap - totalH_group) / 2;
      group.forEach(function(n, i) {
        positions[n.id] = { x: padX + l * layerGap, y: startY + i * (nodeH + nodeGap) };
      });
    }

    var totalW = padX * 2 + maxLayer * layerGap + nodeW;
    var totalH = padY * 2 + maxPerLayer * (nodeH + nodeGap) - nodeGap;

    var out = V.svg.viewBox(totalW, totalH);

    // Arrowhead markers
    out += '<defs>' +
      '<marker id="arrowhead" viewBox="0 0 10 7" refX="10" refY="3.5" markerWidth="8" markerHeight="6" orient="auto-start-reverse">' +
        '<polygon points="0 0, 10 3.5, 0 7" fill="var(--text-muted)" opacity="0.5"/>' +
      '</marker>' +
      '<marker id="arrowhead-warn" viewBox="0 0 10 7" refX="10" refY="3.5" markerWidth="8" markerHeight="6" orient="auto-start-reverse">' +
        '<polygon points="0 0, 10 3.5, 0 7" fill="var(--warning)" opacity="0.6"/>' +
      '</marker>' +
    '</defs>';

    // Edges
    edges.forEach(function(e, idx) {
      var from = positions[e.source];
      var to = positions[e.target];
      if (!from || !to) return;
      var x1 = from.x + nodeW, y1 = from.y + nodeH / 2;
      var x2 = to.x, y2 = to.y + nodeH / 2;
      var cx = (x1 + x2) / 2;
      var cls = e.errorRate > 1 ? ' degraded' : '';
      var marker = e.errorRate > 1 ? 'url(#arrowhead-warn)' : 'url(#arrowhead)';
      var sw = Math.max(1.5, Math.min(4, e.rpm / 400));
      out += V.svg.path(
        'M ' + x1 + ' ' + y1 + ' C ' + cx + ' ' + y1 + ', ' + cx + ' ' + y2 + ', ' + x2 + ' ' + y2,
        'class="topo-edge' + cls + '" style="stroke-width:' + sw + ';marker-end:' + marker + '" data-edge-idx="' + idx + '"'
      );
    });

    // Nodes
    nodes.forEach(function(n) {
      var pos = positions[n.id];
      if (!pos) return;
      var statusCol = V.colors.statusHex(n.status);
      out += '<g class="topo-node" data-node-id="' + V.util.escapeHtml(n.id) + '" transform="translate(' + pos.x + ', ' + pos.y + ')">';
      out += V.svg.rect(0, 0, nodeW, nodeH, 'class="topo-node-bg"');
      out += '<circle class="topo-node-status" cx="12" cy="' + (nodeH/2) + '" fill="' + statusCol + '" />';
      out += V.svg.text(nodeW/2 + 6, nodeH/2 - 4, n.id, 'class="topo-node-label"');
      out += V.svg.text(nodeW/2 + 6, nodeH/2 + 10, n.rpm + ' rpm \u00b7 P95 ' + n.p95 + 'ms', 'class="topo-node-metric"');
      out += '</g>';
    });

    out += '</svg>';
    container.innerHTML = out;

    // Store data for tooltips
    var nodeMap = {};
    nodes.forEach(function(n) { nodeMap[n.id] = n; });
    container._nodes = nodeMap;
    container._edges = edges;

    // Tooltip: nodes
    V.tooltip.wire(container, '.topo-node', function(el) {
      var n = container._nodes[el.getAttribute('data-node-id')];
      if (!n) return '';
      return '<div class="tt-title">' + V.util.escapeHtml(n.id) + '</div>' +
        '<div class="tt-row"><span>Status</span><span class="tt-val" style="color:' + V.colors.statusHex(n.status) + '">' + V.util.escapeHtml(String(n.status)) + '</span></div>' +
        '<div class="tt-row"><span>Throughput</span><span class="tt-val">' + V.util.escapeHtml(String(n.rpm)) + ' rpm</span></div>' +
        '<div class="tt-row"><span>P95 Latency</span><span class="tt-val">' + V.util.escapeHtml(String(n.p95)) + 'ms</span></div>' +
        '<div class="tt-row"><span>Error Rate</span><span class="tt-val">' + V.util.escapeHtml(String(n.errors)) + '%</span></div>';
    });

    // Tooltip: edges
    V.tooltip.wire(container, '.topo-edge', function(el) {
      var edge = container._edges[parseInt(el.getAttribute('data-edge-idx'), 10)];
      if (!edge) return '';
      return '<div class="tt-title">' + V.util.escapeHtml(edge.source) + ' \u2192 ' + V.util.escapeHtml(edge.target) + '</div>' +
        '<div class="tt-row"><span>Volume</span><span class="tt-val">' + V.util.escapeHtml(String(edge.rpm)) + ' rpm</span></div>' +
        '<div class="tt-row"><span>Error Rate</span><span class="tt-val" style="color:' + (edge.errorRate > 1 ? '#f59e0b' : 'inherit') + '">' + V.util.escapeHtml(String(edge.errorRate)) + '%</span></div>';
    });
  }

  V.register('topology-graph', 'data-graph', render);
})();
