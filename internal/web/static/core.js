// FanoutViz — Visualization registry and shared utilities.
// Each renderer is an IIFE that calls FanoutViz.register().
(function() {
  'use strict';

  var renderers = [];
  var tooltipEl = null;
  var tooltipVisible = false;
  var overlayEl = null;
  var currentExpanded = null;

  // Service color palette (deterministic hash)
  var SERVICE_PALETTE = [
    '#6366f1','#0ea5e9','#22c55e','#14b8a6','#f59e0b',
    '#ef4444','#8b5cf6','#06b6d4','#ec4899','#84cc16'
  ];
  var serviceColorMap = {};

  function hashStr(s) {
    var h = 0;
    for (var i = 0; i < s.length; i++) {
      h = ((h << 5) - h) + s.charCodeAt(i);
      h |= 0;
    }
    return Math.abs(h);
  }

  // ─── Tooltip ─────────────────────────────────
  function ensureTooltip() {
    if (tooltipEl) return;
    tooltipEl = document.createElement('div');
    tooltipEl.className = 'viz-tooltip';
    document.body.appendChild(tooltipEl);
    document.addEventListener('mousemove', moveTooltip);
  }

  function moveTooltip(evt) {
    if (!tooltipVisible || !tooltipEl) return;
    var x = evt.clientX + 12;
    var y = evt.clientY - 8;
    var rect = tooltipEl.getBoundingClientRect();
    var maxX = window.innerWidth - rect.width - 16;
    var maxY = window.innerHeight - rect.height - 16;
    tooltipEl.style.left = Math.min(x, maxX) + 'px';
    tooltipEl.style.top = Math.min(y, maxY) + 'px';
  }

  var tooltip = {
    show: function(html, evt) {
      ensureTooltip();
      tooltipEl.innerHTML = html;
      tooltipEl.classList.add('visible');
      tooltipVisible = true;
      moveTooltip(evt);
    },
    hide: function() {
      if (tooltipEl) tooltipEl.classList.remove('visible');
      tooltipVisible = false;
    }
  };

  // ─── Expand overlay ──────────────────────────
  function ensureOverlay() {
    if (overlayEl) return;
    overlayEl = document.createElement('div');
    overlayEl.className = 'expand-overlay';
    overlayEl.innerHTML =
      '<div class="expand-backdrop"></div>' +
      '<div class="expand-content">' +
        '<div class="expand-header">' +
          '<div class="viz-card-title" id="viz-expand-title"></div>' +
          '<button class="btn-close">Close <kbd style="opacity:0.5;margin-left:0.25rem">ESC</kbd></button>' +
        '</div>' +
        '<div class="expand-body" id="viz-expand-body"></div>' +
      '</div>';
    document.body.appendChild(overlayEl);

    overlayEl.querySelector('.expand-backdrop').addEventListener('click', closeExpand);
    overlayEl.querySelector('.btn-close').addEventListener('click', closeExpand);
    document.addEventListener('keydown', function(e) {
      if (e.key === 'Escape' && currentExpanded) closeExpand();
    });
  }

  function expand(el) {
    ensureOverlay();
    // Find the renderer entry for this element
    var entry = null;
    for (var i = 0; i < renderers.length; i++) {
      if (el.classList.contains(renderers[i].className)) {
        entry = renderers[i];
        break;
      }
    }
    if (!entry) return;

    // Find the card wrapper to get the title
    var card = el.closest('.viz-card');
    var titleEl = overlayEl.querySelector('#viz-expand-title');
    if (card) {
      var cardTitle = card.querySelector('.viz-card-title');
      titleEl.innerHTML = cardTitle ? cardTitle.innerHTML : '';
    }

    var body = overlayEl.querySelector('#viz-expand-body');
    body.innerHTML = '';

    var clone = document.createElement('div');
    clone.className = entry.className;
    clone.setAttribute(entry.dataAttr, el.getAttribute(entry.dataAttr));
    body.appendChild(clone);
    safeRender(entry, clone, true);

    overlayEl.classList.add('active');
    currentExpanded = entry;
  }

  function closeExpand() {
    if (overlayEl) overlayEl.classList.remove('active');
    currentExpanded = null;
  }

  // ─── Colors ──────────────────────────────────
  var colors = {
    status: function(s) {
      if (s === 'error' || s === 'critical') return 'var(--danger)';
      if (s === 'degraded' || s === 'slow') return 'var(--warning)';
      return 'var(--success)';
    },
    statusHex: function(s) {
      if (s === 'error' || s === 'critical') return '#ef4444';
      if (s === 'degraded' || s === 'slow') return '#f59e0b';
      return '#22c55e';
    },
    service: function(name) {
      if (serviceColorMap[name]) return serviceColorMap[name];
      var idx = hashStr(name) % SERVICE_PALETTE.length;
      serviceColorMap[name] = SERVICE_PALETTE[idx];
      return SERVICE_PALETTE[idx];
    },
    threshold: function(value, stops) {
      if (!stops || stops.length === 0) return '#888';
      if (typeof value !== 'number' || isNaN(value)) return '#888';
      for (var i = stops.length - 1; i >= 0; i--) {
        if (value >= stops[i][0]) return stops[i][1];
      }
      return stops[0][1];
    }
  };

  // ─── Utilities ───────────────────────────────
  var util = {
    truncate: function(text, maxW, charW) {
      var maxChars = Math.floor(maxW / charW);
      if (text.length <= maxChars) return text;
      if (maxChars < 4) return '';
      return text.slice(0, maxChars - 1) + '\u2026';
    },
    format: function(val, type) {
      if (type === 'ms') return Math.round(val) + '';
      if (type === 'pct') return val.toFixed(1);
      if (type === 'rpm') return val >= 1000 ? (val / 1000).toFixed(1) + 'K' : val + '';
      return Math.round(val) + '';
    },
    escapeHtml: function(s) {
      var d = document.createElement('div');
      d.textContent = s;
      return d.innerHTML;
    },
    parseData: function(container, attr) {
      var raw = container.getAttribute(attr);
      if (!raw) return null;
      try {
        return JSON.parse(raw);
      } catch (e) {
        container.innerHTML = '<div style="color:var(--danger);font-size:0.8rem;padding:0.5rem">Chart data could not be parsed</div>';
        console.error('FanoutViz: invalid JSON in ' + attr + ':', e);
        return null;
      }
    }
  };

  // ─── SVG element builders ──────────────────
  var svg = {
    open: function(w, h) { return '<svg width="'+w+'" height="'+h+'" viewBox="0 0 '+w+' '+h+'">'; },
    viewBox: function(w, h) { return '<svg viewBox="0 0 '+w+' '+h+'" xmlns="http://www.w3.org/2000/svg">'; },
    close: '</svg>',
    g: function(transform, content) { return '<g transform="'+transform+'">'+content+'</g>'; },
    line: function(x1,y1,x2,y2,attrs) { return '<line x1="'+x1+'" y1="'+y1+'" x2="'+x2+'" y2="'+y2+'" '+(attrs||'')+' />'; },
    rect: function(x,y,w,h,attrs) { return '<rect x="'+x+'" y="'+y+'" width="'+w+'" height="'+h+'" '+(attrs||'')+' />'; },
    text: function(x,y,text,attrs) { return '<text x="'+x+'" y="'+y+'" '+(attrs||'')+'>'+util.escapeHtml(String(text))+'</text>'; },
    circle: function(cx,cy,r,attrs) { return '<circle cx="'+cx+'" cy="'+cy+'" r="'+r+'" '+(attrs||'')+' />'; },
    path: function(d,attrs) { return '<path d="'+d+'" '+(attrs||'')+' />'; }
  };

  // ─── Scales ────────────────────────────────
  var scale = {
    linear: function(domain, range) {
      var d0=+domain[0], d1=+domain[1], r0=+range[0], r1=+range[1];
      if (isNaN(d0) || isNaN(d1) || isNaN(r0) || isNaN(r1)) {
        var fallback = function() { return r0 || 0; };
        fallback.domain = domain; fallback.range = range;
        fallback.invert = function() { return 0; };
        return fallback;
      }
      var span = d1 - d0;
      if (span === 0) {
        var mid = (r0 + r1) / 2;
        var fn = function() { return mid; };
        fn.domain = domain; fn.range = range;
        fn.invert = function() { return d0; };
        return fn;
      }
      var m = (r1-r0)/span;
      var fn = function(v) { var n = +v; return isNaN(n) ? r0 : r0 + (n-d0)*m; };
      fn.domain = domain; fn.range = range;
      fn.invert = function(px) { return d0 + (px-r0)/m; };
      return fn;
    },
    band: function(labels, range, padding) {
      var p = padding || 0.1;
      var n = labels.length;
      if (n === 0) {
        var fn = function() { return range[0]; };
        fn.bandwidth = function() { return 0; };
        fn.step = function() { return 0; };
        return fn;
      }
      var total = range[1]-range[0];
      var step = total / (n + (n+1)*p);
      var gap = step*p;
      var fn = function(label) {
        var i = labels.indexOf(label);
        if (i === -1) return range[0];
        return range[0] + gap + i*(step+gap);
      };
      fn.bandwidth = function() { return step; };
      fn.step = function() { return step + gap; };
      return fn;
    }
  };

  // ─── Drawing primitives ────────────────────
  var draw = {
    gridY: function(yScale, ticks, innerW, attrs) {
      var a = attrs || 'stroke="var(--border-subtle)" stroke-dasharray="2,2"';
      return ticks.map(function(t) {
        var y = Math.round(yScale(t));
        return '<line x1="0" y1="'+y+'" x2="'+innerW+'" y2="'+y+'" '+a+' />';
      }).join('');
    },
    axisX: function(labels, xScale, innerH, bandwidth) {
      return labels.map(function(l) {
        var x = xScale(l) + (bandwidth||0)/2;
        return '<text x="'+x+'" y="'+(innerH+16)+'" text-anchor="middle" fill="var(--text-muted)" font-size="10">'+util.escapeHtml(String(l))+'</text>';
      }).join('');
    },
    axisY: function(yScale, ticks, attrs) {
      var a = attrs || 'text-anchor="end" fill="var(--text-muted)" font-size="10"';
      return ticks.map(function(t) {
        var y = Math.round(yScale(t));
        return '<text x="-6" y="'+(y+3)+'" '+a+'>'+util.escapeHtml(String(t))+'</text>';
      }).join('');
    },
    linePath: function(points, xFn, yFn) {
      if (points.length === 0) return '';
      var segs = [], started = false;
      points.forEach(function(p) {
        var x = xFn(p), y = yFn(p);
        if (!isFinite(x) || !isFinite(y)) return;
        segs.push((started ? 'L' : 'M') + x + ',' + y);
        started = true;
      });
      return segs.join(' ');
    },
    areaPath: function(points, xFn, yFn, baseline) {
      if (points.length === 0) return '';
      var valid = points.filter(function(p) { return isFinite(xFn(p)) && isFinite(yFn(p)); });
      if (valid.length === 0) return '';
      var top = valid.map(function(p,i) { return (i===0?'M':'L')+xFn(p)+','+yFn(p); }).join(' ');
      return top + 'L'+xFn(valid[valid.length-1])+','+baseline+'L'+xFn(valid[0])+','+baseline+'Z';
    },
    arc: function(cx, cy, r, startAngle, endAngle) {
      var x1=cx+r*Math.cos(startAngle), y1=cy+r*Math.sin(startAngle);
      var x2=cx+r*Math.cos(endAngle), y2=cy+r*Math.sin(endAngle);
      var large = (endAngle-startAngle > Math.PI) ? 1 : 0;
      return 'M'+x1+','+y1+' A'+r+','+r+' 0 '+large+' 1 '+x2+','+y2;
    }
  };

  // ─── Layout algorithms ─────────────────────
  var layout = {
    dagLayers: function(nodes, edges) {
      var adj = {}, inDeg = {};
      nodes.forEach(function(n) { adj[n.id] = []; inDeg[n.id] = 0; });
      edges.forEach(function(e) {
        if (adj[e.source] && inDeg[e.target] !== undefined) {
          adj[e.source].push(e.target);
          inDeg[e.target]++;
        }
      });

      // BFS from roots — push all neighbors (handles cycles)
      var layers = {};
      var queued = {};
      var queue = [];
      nodes.forEach(function(n) {
        if (inDeg[n.id] === 0) { layers[n.id] = 0; queue.push(n.id); queued[n.id] = true; }
      });
      // If no roots (pure cycle), seed first node to break the cycle
      if (queue.length === 0 && nodes.length > 0) {
        layers[nodes[0].id] = 0; queue.push(nodes[0].id); queued[nodes[0].id] = true;
      }
      var head = 0;
      while (head < queue.length) {
        var curr = queue[head++];
        (adj[curr]||[]).forEach(function(next) {
          layers[next] = Math.max(layers[next]||0, layers[curr]+1);
          if (!queued[next]) { queue.push(next); queued[next] = true; }
        });
      }

      // Assign layers to nodes (always overwrite to avoid stale state on re-render)
      nodes.forEach(function(n) { n.layer = layers[n.id] || 0; });

      var groups = {};
      nodes.forEach(function(n) {
        if (!groups[n.layer]) groups[n.layer] = [];
        groups[n.layer].push(n);
      });
      return groups;
    }
  };

  // ─── Legend builder ────────────────────────
  function legend(items) {
    return '<div class="viz-legend">' + items.map(function(it) {
      return '<div class="viz-legend-item"><span class="swatch" style="background:'+it.color+'"></span> '+util.escapeHtml(it.label)+'</div>';
    }).join('') + '</div>';
  }

  // ─── Tooltip extensions ────────────────────
  tooltip.html = function(rows) {
    return '<div style="font-size:12px;line-height:1.6">' + rows.map(function(r) {
      var label = Array.isArray(r) ? r[0] : r.label;
      var value = Array.isArray(r) ? r[1] : r.value;
      var dot = r.color ? '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:'+r.color+';margin-right:4px"></span>' : '';
      return dot + '<strong>' + util.escapeHtml(String(label)) + '</strong> ' + util.escapeHtml(String(value));
    }).join('<br>') + '</div>';
  };
  tooltip.wire = function(container, selector, buildFn) {
    var key = '_tw_' + selector;
    if (container[key]) return;
    container[key] = true;
    container.addEventListener('mouseover', function(e) {
      var el = e.target.closest(selector);
      if (!el) return;
      try {
        var html = buildFn(el);
        if (html) tooltip.show(html, e);
      } catch (err) {
        console.error('FanoutViz: tooltip error for ' + selector + ':', err);
      }
    });
    container.addEventListener('mouseout', function(e) {
      if (e.target.closest(selector)) tooltip.hide();
    });
  };

  // ─── Registry ────────────────────────────────
  function register(className, dataAttr, renderFn) {
    renderers.push({ className: className, dataAttr: dataAttr, renderFn: renderFn });
  }

  function safeRender(entry, el, expanded) {
    try {
      entry.renderFn(el, expanded);
    } catch (e) {
      console.error('FanoutViz render error (' + entry.className + '):', e);
      el.innerHTML = '<div style="color:var(--danger);font-size:0.8rem;padding:0.5rem">Chart failed to render</div>';
    }
  }

  function init(root) {
    if (!root || root.nodeType !== 1) return;
    renderers.forEach(function(entry) {
      var els = root.querySelectorAll('.' + entry.className + '[' + entry.dataAttr + ']');
      els.forEach(function(el) {
        if (el._vizInit) return;
        el._vizInit = true;
        safeRender(entry, el, false);
      });
      // Also check root itself
      if (root.classList && root.classList.contains(entry.className) && root.hasAttribute(entry.dataAttr) && !root._vizInit) {
        root._vizInit = true;
        safeRender(entry, root, false);
      }
    });

    // Wire expand buttons (event delegation not needed for these — wire once)
    var expandBtns = root.querySelectorAll('.btn-viz-expand');
    expandBtns.forEach(function(btn) {
      if (btn._vizWired) return;
      btn._vizWired = true;
      btn.addEventListener('click', function() {
        var card = btn.closest('.viz-card');
        if (!card) return;
        var body = card.querySelector('.viz-card-body');
        if (!body) return;
        // Find the renderer element inside the body
        for (var i = 0; i < renderers.length; i++) {
          var el = body.querySelector('.' + renderers[i].className);
          if (el) { expand(el); break; }
        }
      });
    });
  }

  // ─── Public API ──────────────────────────────
  window.FanoutViz = {
    register: register,
    init: init,
    expand: expand,
    closeExpand: closeExpand,
    tooltip: tooltip,
    colors: colors,
    util: util,
    svg: svg,
    scale: scale,
    draw: draw,
    layout: layout,
    legend: legend
  };
})();
