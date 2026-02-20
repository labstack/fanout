(function() {
  'use strict';
  var V = window.FanoutViz;

  function render(container, expanded) {
    var data = V.util.parseData(container, 'data-flow');
    if (!data || !data.nodes) return;

    var nodes = data.nodes;
    var links = data.links;

    // DAG layout
    var layerGroups = V.layout.dagLayers(nodes, links);
    var layerKeys = Object.keys(layerGroups);
    if (layerKeys.length === 0) return;
    var maxLayer = Math.max.apply(null, layerKeys.map(Number));

    var padX = expanded ? 60 : 20;
    var padY = expanded ? 40 : 24;
    var layerW = expanded ? 240 : 180;
    var nodeW = expanded ? 14 : 10;
    var maxNodeH = expanded ? 300 : 180;

    var maxRPM = Math.max.apply(null, nodes.map(function(n) { return n.rpm; })) || 1;
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

    var outOffset = {}, inOffset = {};
    nodes.forEach(function(n) { outOffset[n.id] = 0; inOffset[n.id] = 0; });

    var sortedLinks = links.slice().sort(function(a, b) { return b.value - a.value; });

    var out = '<svg viewBox="0 0 ' + totalW + ' ' + totalH + '" xmlns="http://www.w3.org/2000/svg">';

    // Links
    sortedLinks.forEach(function(link, idx) {
      var srcPos = positions[link.source];
      var tgtPos = positions[link.target];
      var srcNode = nodeById[link.source];
      var tgtNode = nodeById[link.target];
      if (!srcPos || !tgtPos) return;

      var linkH = Math.max(4, (link.value / (srcNode.rpm || 1)) * srcPos.h);
      var linkHTarget = Math.max(4, (link.value / (tgtNode.rpm || 1)) * tgtPos.h);

      var x1 = srcPos.x + nodeW, y1 = srcPos.y + outOffset[link.source];
      var x2 = tgtPos.x, y2 = tgtPos.y + inOffset[link.target];

      outOffset[link.source] += linkH;
      inOffset[link.target] += linkHTarget;

      var cx = (x1 + x2) / 2;
      var color = (tgtNode.status === 'degraded' || srcNode.status === 'degraded') ? '#f59e0b' : '#0ea5e9';

      out += V.svg.path(
        'M ' + x1 + ' ' + y1 + ' C ' + cx + ' ' + y1 + ', ' + cx + ' ' + y2 + ', ' + x2 + ' ' + y2 +
        ' L ' + x2 + ' ' + (y2 + linkHTarget) +
        ' C ' + cx + ' ' + (y2 + linkHTarget) + ', ' + cx + ' ' + (y1 + linkH) + ', ' + x1 + ' ' + (y1 + linkH) + ' Z',
        'class="sankey-link" data-link-idx="' + idx + '" fill="' + color + '" stroke="' + color + '"'
      );
    });

    // Nodes
    nodes.forEach(function(n) {
      var pos = positions[n.id];
      if (!pos) return;
      var color = V.colors.statusHex(n.status || 'healthy');
      out += V.svg.rect(pos.x, pos.y, nodeW, pos.h, 'class="sankey-node-bg" fill="' + color + '"');

      var lyr = n.layer || 0;
      var labelX = lyr === maxLayer ? pos.x + nodeW + 8 : pos.x - 8;
      var anchor = lyr === maxLayer ? 'start' : 'end';
      var labelY = pos.y + pos.h / 2;

      out += V.svg.text(labelX, labelY - 4, V.util.escapeHtml(n.label), 'class="sankey-node-label" text-anchor="' + anchor + '"');
      out += V.svg.text(labelX, labelY + 9, V.util.escapeHtml(String(n.rpm)) + ' rpm', 'class="sankey-node-value" text-anchor="' + anchor + '"');
    });

    out += '</svg>';
    container.innerHTML = out;
    container._links = sortedLinks;

    V.tooltip.wire(container, '.sankey-link', function(el) {
      var l = container._links[parseInt(el.getAttribute('data-link-idx'), 10)];
      if (!l) return '';
      return '<div class="tt-title">' + V.util.escapeHtml(l.source) + ' \u2192 ' + V.util.escapeHtml(l.target) + '</div>' +
        '<div class="tt-row"><span>Volume</span><span class="tt-val">' + V.util.escapeHtml(String(l.value)) + ' rpm</span></div>';
    });
  }

  V.register('flow-sankey', 'data-flow', render);
})();
