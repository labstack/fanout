(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-graph');
    if (!data || !data.nodes) return;

    var nodes = data.nodes;
    var edges = data.edges;

    // Simple layered DAG layout
    var adjacency = {};
    var inDegree = {};
    nodes.forEach(function(n) { adjacency[n.id] = []; inDegree[n.id] = 0; });
    edges.forEach(function(e) {
      adjacency[e.source].push(e.target);
      inDegree[e.target]++;
    });

    // Assign layers via BFS
    var layers = {};
    var queue = nodes.filter(function(n) { return inDegree[n.id] === 0; }).map(function(n) { return n.id; });
    queue.forEach(function(id) { layers[id] = 0; });

    var head = 0;
    while (head < queue.length) {
      var curr = queue[head++];
      adjacency[curr].forEach(function(next) {
        layers[next] = Math.max(layers[next] || 0, layers[curr] + 1);
        if (queue.indexOf(next) === -1) queue.push(next);
      });
    }

    // Group by layer
    var layerGroups = {};
    nodes.forEach(function(n) {
      var l = layers[n.id] || 0;
      if (!layerGroups[l]) layerGroups[l] = [];
      layerGroups[l].push(n);
    });

    var maxLayer = Math.max.apply(null, Object.keys(layerGroups).map(Number));
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

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Arrowhead markers
    svg += '<defs>' +
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
      var x1 = from.x + nodeW;
      var y1 = from.y + nodeH / 2;
      var x2 = to.x;
      var y2 = to.y + nodeH / 2;
      var cx = (x1 + x2) / 2;
      var cls = e.errorRate > 1 ? ' degraded' : '';
      var marker = e.errorRate > 1 ? 'url(#arrowhead-warn)' : 'url(#arrowhead)';
      var sw = Math.max(1.5, Math.min(4, e.rpm / 400));
      svg += '<path class="topo-edge' + cls + '" d="M ' + x1 + ' ' + y1 + ' C ' + cx + ' ' + y1 + ', ' + cx + ' ' + y2 + ', ' + x2 + ' ' + y2 + '"' +
        ' style="stroke-width:' + sw + ';marker-end:' + marker + '" data-edge-idx="' + idx + '" />';
    });

    // Nodes
    nodes.forEach(function(n) {
      var pos = positions[n.id];
      if (!pos) return;
      var statusCol = V.colors.statusHex(n.status);

      svg += '<g class="topo-node" data-node-id="' + V.util.escapeHtml(n.id) + '" transform="translate(' + pos.x + ', ' + pos.y + ')">';
      svg += '<rect class="topo-node-bg" width="' + nodeW + '" height="' + nodeH + '" />';
      svg += '<circle class="topo-node-status" cx="12" cy="' + (nodeH/2) + '" fill="' + statusCol + '" />';
      svg += '<text class="topo-node-label" x="' + (nodeW/2 + 6) + '" y="' + (nodeH/2 - 4) + '">' + V.util.escapeHtml(n.id) + '</text>';
      svg += '<text class="topo-node-metric" x="' + (nodeW/2 + 6) + '" y="' + (nodeH/2 + 10) + '">' + V.util.escapeHtml(String(n.rpm)) + ' rpm · P95 ' + V.util.escapeHtml(String(n.p95)) + 'ms</text>';
      svg += '</g>';
    });

    svg += '</svg>';

    container.innerHTML = svg;
    container._nodes = {};
    nodes.forEach(function(n) { container._nodes[n.id] = n; });
    container._edges = edges;

    // Event delegation
    container.addEventListener('mouseover', function(ev) {
      var nodeG = ev.target.closest('.topo-node');
      if (nodeG) {
        var id = nodeG.getAttribute('data-node-id');
        var n = container._nodes[id];
        if (!n) return;
        V.tooltip.show(
          '<div class="tt-title">' + V.util.escapeHtml(n.id) + '</div>' +
          '<div class="tt-row"><span>Status</span><span class="tt-val" style="color:' + V.colors.statusHex(n.status) + '">' + V.util.escapeHtml(String(n.status)) + '</span></div>' +
          '<div class="tt-row"><span>Throughput</span><span class="tt-val">' + V.util.escapeHtml(String(n.rpm)) + ' rpm</span></div>' +
          '<div class="tt-row"><span>P95 Latency</span><span class="tt-val">' + V.util.escapeHtml(String(n.p95)) + 'ms</span></div>' +
          '<div class="tt-row"><span>Error Rate</span><span class="tt-val">' + V.util.escapeHtml(String(n.errors)) + '%</span></div>',
          ev
        );
        return;
      }
      var edgePath = ev.target.closest('.topo-edge');
      if (edgePath) {
        var idx = parseInt(edgePath.getAttribute('data-edge-idx'), 10);
        var edge = container._edges[idx];
        if (!edge) return;
        V.tooltip.show(
          '<div class="tt-title">' + V.util.escapeHtml(edge.source) + ' \u2192 ' + V.util.escapeHtml(edge.target) + '</div>' +
          '<div class="tt-row"><span>Volume</span><span class="tt-val">' + V.util.escapeHtml(String(edge.rpm)) + ' rpm</span></div>' +
          '<div class="tt-row"><span>Error Rate</span><span class="tt-val" style="color:' + (edge.errorRate > 1 ? '#f59e0b' : 'inherit') + '">' + V.util.escapeHtml(String(edge.errorRate)) + '%</span></div>',
          ev
        );
      }
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.topo-node') || ev.target.closest('.topo-edge')) V.tooltip.hide();
    });
  }

  V.register('topology-graph', 'data-graph', render);
})();
