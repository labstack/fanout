(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var data = JSON.parse(container.getAttribute('data-flow'));
    if (!data || !data.nodes) return;

    var nodes = data.nodes;
    var links = data.links;

    // Compute node layers
    var adjacency = {};
    var inDegree = {};
    nodes.forEach(function(n) { adjacency[n.id] = []; inDegree[n.id] = 0; });
    links.forEach(function(l) {
      adjacency[l.source].push(l.target);
      inDegree[l.target]++;
    });

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

    var layerGroups = {};
    nodes.forEach(function(n) {
      var l = layers[n.id] || 0;
      if (!layerGroups[l]) layerGroups[l] = [];
      layerGroups[l].push(n);
    });

    var maxLayer = Math.max.apply(null, Object.keys(layerGroups).map(Number));
    var padX = expanded ? 60 : 20;
    var padY = expanded ? 40 : 24;
    var layerW = expanded ? 240 : 180;
    var nodeW = expanded ? 14 : 10;
    var maxNodeH = expanded ? 300 : 180;

    var maxRPM = Math.max.apply(null, nodes.map(function(n) { return n.rpm; }));
    var nodeById = {};
    nodes.forEach(function(n) {
      n._h = Math.max(20, (n.rpm / maxRPM) * maxNodeH);
      nodeById[n.id] = n;
    });

    // Position nodes
    var positions = {};
    for (var l = 0; l <= maxLayer; l++) {
      var group = layerGroups[l] || [];
      var totalH_group = group.reduce(function(sum, n) { return sum + n._h; }, 0) + (group.length - 1) * 12;
      var yOff = padY + (maxNodeH + 40 - totalH_group) / 2;
      group.forEach(function(n) {
        positions[n.id] = { x: padX + l * layerW, y: yOff, h: n._h };
        yOff += n._h + 12;
      });
    }

    var totalW = padX * 2 + maxLayer * layerW + nodeW;
    var totalH = padY * 2 + maxNodeH + 40;

    var outOffset = {};
    var inOffset = {};
    nodes.forEach(function(n) { outOffset[n.id] = 0; inOffset[n.id] = 0; });

    var sortedLinks = links.slice().sort(function(a, b) { return b.value - a.value; });

    var svg = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Links
    sortedLinks.forEach(function(link, idx) {
      var srcPos = positions[link.source];
      var tgtPos = positions[link.target];
      var srcNode = nodeById[link.source];
      var tgtNode = nodeById[link.target];
      if (!srcPos || !tgtPos) return;

      var linkH = Math.max(4, (link.value / srcNode.rpm) * srcPos.h);
      var linkHTarget = Math.max(4, (link.value / tgtNode.rpm) * tgtPos.h);

      var x1 = srcPos.x + nodeW;
      var y1 = srcPos.y + outOffset[link.source];
      var x2 = tgtPos.x;
      var y2 = tgtPos.y + inOffset[link.target];

      outOffset[link.source] += linkH;
      inOffset[link.target] += linkHTarget;

      var cx = (x1 + x2) / 2;
      var color = (tgtNode.status === 'degraded' || srcNode.status === 'degraded') ? '#f59e0b' : '#0ea5e9';

      svg += '<path class="sankey-link" data-link-idx="' + idx + '" d="' +
        'M ' + x1 + ' ' + y1 +
        ' C ' + cx + ' ' + y1 + ', ' + cx + ' ' + y2 + ', ' + x2 + ' ' + y2 +
        ' L ' + x2 + ' ' + (y2 + linkHTarget) +
        ' C ' + cx + ' ' + (y2 + linkHTarget) + ', ' + cx + ' ' + (y1 + linkH) + ', ' + x1 + ' ' + (y1 + linkH) +
        ' Z" fill="' + color + '" stroke="' + color + '" />';
    });

    // Nodes
    nodes.forEach(function(n) {
      var pos = positions[n.id];
      if (!pos) return;
      var color = V.colors.statusHex(n.status || 'healthy');
      svg += '<rect class="sankey-node-bg" x="' + pos.x + '" y="' + pos.y + '" width="' + nodeW + '" height="' + pos.h + '" fill="' + color + '" />';

      var lyr = layers[n.id] || 0;
      var labelX = lyr === maxLayer ? pos.x + nodeW + 8 : pos.x - 8;
      var anchor = lyr === maxLayer ? 'start' : 'end';
      var labelY = pos.y + pos.h / 2;

      svg += '<text class="sankey-node-label" x="' + labelX + '" y="' + (labelY - 4) + '" text-anchor="' + anchor + '">' + V.util.escapeHtml(n.label) + '</text>';
      svg += '<text class="sankey-node-value" x="' + labelX + '" y="' + (labelY + 9) + '" text-anchor="' + anchor + '">' + n.rpm + ' rpm</text>';
    });

    svg += '</svg>';
    container.innerHTML = svg;
    container._links = sortedLinks;

    // Event delegation
    container.addEventListener('mouseover', function(ev) {
      var link = ev.target.closest('.sankey-link');
      if (!link) return;
      var idx = parseInt(link.getAttribute('data-link-idx'), 10);
      var l = container._links[idx];
      if (!l) return;
      V.tooltip.show(
        '<div class="tt-title">' + V.util.escapeHtml(l.source) + ' \u2192 ' + V.util.escapeHtml(l.target) + '</div>' +
        '<div class="tt-row"><span>Volume</span><span class="tt-val">' + l.value + ' rpm</span></div>',
        ev
      );
    });
    container.addEventListener('mouseout', function(ev) {
      if (ev.target.closest('.sankey-link')) V.tooltip.hide();
    });
  }

  V.register('flow-sankey', 'data-flow', render);
})();
