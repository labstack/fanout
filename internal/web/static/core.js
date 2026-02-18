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
    entry.renderFn(clone, true);

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
    }
  };

  // ─── Registry ────────────────────────────────
  function register(className, dataAttr, renderFn) {
    renderers.push({ className: className, dataAttr: dataAttr, renderFn: renderFn });
  }

  function init(root) {
    if (!root || root.nodeType !== 1) return;
    renderers.forEach(function(entry) {
      var els = root.querySelectorAll('.' + entry.className + '[' + entry.dataAttr + ']');
      els.forEach(function(el) {
        if (el._vizInit) return;
        el._vizInit = true;
        entry.renderFn(el, false);
      });
      // Also check root itself
      if (root.classList && root.classList.contains(entry.className) && root.hasAttribute(entry.dataAttr) && !root._vizInit) {
        root._vizInit = true;
        entry.renderFn(root, false);
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
    util: util
  };
})();
