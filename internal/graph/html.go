package graph

import (
	"encoding/json"
	"os"
	"strings"
)

// WriteHTML writes a single self-contained HTML file that renders g as
// an interactive force-directed graph -- no server, no CDN, no
// external JS/CSS: everything needed is embedded in the file, so it
// keeps working fully offline once generated. Just open it in a
// browser. Nodes are colored by node_type (the entity source), edges
// are labeled by relationship_type, and both are draggable/hoverable
// for detail.
func WriteHTML(g Graph, path string) error {
	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	// Defend against a node/edge string field containing a literal
	// "</script>" sequence, which would otherwise prematurely close the
	// embedded script tag -- entity names/evidence come from live
	// external APIs, not input this program controls.
	safeData := strings.ReplaceAll(string(data), "</", "<\\/")

	html := strings.Replace(htmlViewerTemplate, "/*__GRAPH_DATA__*/", safeData, 1)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(html)
	return err
}

const htmlViewerTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>paper-trail graph</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  :root {
    color-scheme: light dark;
    --bg: #ffffff;
    --fg: #1a1a1a;
    --muted: #666666;
    --panel-bg: #f5f5f5;
    --panel-border: #dddddd;
    --edge: #999999;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #121212;
      --fg: #e8e8e8;
      --muted: #999999;
      --panel-bg: #1e1e1e;
      --panel-border: #333333;
      --edge: #666666;
    }
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; width: 100%; height: 100%; overflow: hidden; }
  body { background: var(--bg); color: var(--fg); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
  svg { width: 100%; height: 100%; display: block; cursor: grab; }
  svg.panning { cursor: grabbing; }
  .node circle { stroke: var(--bg); stroke-width: 1.5px; cursor: pointer; }
  .node circle.high-weight { stroke: #e15759; stroke-width: 3px; }
  .node text { font-size: 10px; fill: var(--fg); pointer-events: none; }
  .edge { stroke: var(--edge); fill: none; }
  .node.dim circle, .node.dim text { opacity: 0.15; }
  .edge.dim { opacity: 0.08; }
  .edge.lit { stroke: #e67e22; }
  #panel {
    position: fixed; top: 12px; right: 12px; max-width: 320px;
    background: var(--panel-bg); border: 1px solid var(--panel-border); border-radius: 8px;
    padding: 12px 14px; font-size: 13px; line-height: 1.5;
  }
  #panel h1 { font-size: 14px; margin: 0 0 8px; }
  #panel .hint { color: var(--muted); font-size: 12px; }
  #detail { margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--panel-border); display: none; }
  #detail.show { display: block; }
  #detail .row { margin: 2px 0; word-break: break-word; }
  #legend { margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--panel-border); }
  #legend .item { display: flex; align-items: center; gap: 6px; margin: 3px 0; }
  #legend .swatch { width: 10px; height: 10px; border-radius: 50%; flex: none; }
</style>
</head>
<body>
<svg id="graph"></svg>
<div id="panel">
  <h1>paper-trail graph</h1>
  <div class="hint">Drag nodes to rearrange. Click a node to highlight its connections. Scroll to zoom, drag background to pan.</div>
  <div id="legend"></div>
  <div id="detail"></div>
</div>
<script>
const graphData = /*__GRAPH_DATA__*/;

const svg = document.getElementById('graph');
const NS = 'http://www.w3.org/2000/svg';
let width = window.innerWidth, height = window.innerHeight;

const nodes = graphData.nodes.map((n, i) => ({
  ...n,
  type: n.node_type, // the exported JSON field is "node_type"; normalize once here
  maxWeight: n.maxWeight || 0,
  x: width / 2 + Math.cos(i) * 100 + (Math.random() - 0.5) * 50,
  y: height / 2 + Math.sin(i) * 100 + (Math.random() - 0.5) * 50,
  vx: 0, vy: 0,
}));
const nodeById = new Map(nodes.map(n => [n.id, n]));
const edges = graphData.edges
  .map(e => ({ ...e, s: nodeById.get(e.source), t: nodeById.get(e.target) }))
  .filter(e => e.s && e.t);

// A handful of distinct colors, assigned per node_type in the order
// types are first seen -- deterministic across a given graph, not
// tied to any particular source name.
const PALETTE = ['#4e79a7', '#f28e2b', '#e15759', '#76b7b2', '#59a14f', '#edc948', '#b07aa1', '#ff9da7', '#9c755f'];
const typeColor = new Map();
function colorFor(type) {
  const key = type || 'unknown';
  if (!typeColor.has(key)) typeColor.set(key, PALETTE[typeColor.size % PALETTE.length]);
  return typeColor.get(key);
}
nodes.forEach(n => colorFor(n.type));

const legend = document.getElementById('legend');
for (const [type, color] of typeColor) {
  const item = document.createElement('div');
  item.className = 'item';
  item.innerHTML = '<span class="swatch" style="background:' + color + '"></span>' + type;
  legend.appendChild(item);
}
// Only shown when this graph actually carries indicator weights (i.e.
// it came from risk --graph/--html, not a plain lookup/graph export,
// which never sets maxWeight) -- otherwise the note would be
// meaningless noise.
if (nodes.some(n => n.maxWeight > 0)) {
  const note = document.createElement('div');
  note.className = 'hint';
  note.style.marginTop = '6px';
  note.textContent = 'Larger node = higher-weight indicator involved; red outline = a high-weight one (>=5).';
  legend.appendChild(note);
}

// -- physics: simple Coulomb repulsion + spring edges + centering,
// damped each tick, no external library. CENTER_K is deliberately
// strong relative to REPULSION -- confirmed live that a weaker value
// let disconnected nodes (no edges at all, so nothing but repulsion
// and centering act on them) drift indefinitely off toward a viewport
// edge instead of settling near the rest of the graph.
const REPULSION = 1400;
const EDGE_LENGTH = 90;
const SPRING_K = 0.02;
const CENTER_K = 0.04;
const DAMPING = 0.85;

// step advances the physics by one increment -- pure data update, no
// drawing, so it can also be run in a tight synchronous loop to
// pre-settle the layout (see the bottom of this script).
function step() {
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      const a = nodes[i], b = nodes[j];
      let dx = a.x - b.x, dy = a.y - b.y;
      let distSq = dx * dx + dy * dy || 1;
      let dist = Math.sqrt(distSq);
      let force = REPULSION / distSq;
      let fx = (dx / dist) * force, fy = (dy / dist) * force;
      a.vx += fx; a.vy += fy;
      b.vx -= fx; b.vy -= fy;
    }
  }
  for (const e of edges) {
    let dx = e.t.x - e.s.x, dy = e.t.y - e.s.y;
    let dist = Math.sqrt(dx * dx + dy * dy) || 1;
    let force = (dist - EDGE_LENGTH) * SPRING_K;
    let fx = (dx / dist) * force, fy = (dy / dist) * force;
    e.s.vx += fx; e.s.vy += fy;
    e.t.vx -= fx; e.t.vy -= fy;
  }
  for (const n of nodes) {
    n.vx += (width / 2 - n.x) * CENTER_K;
    n.vy += (height / 2 - n.y) * CENTER_K;
  }
  for (const n of nodes) {
    if (n === dragging) continue;
    n.vx *= DAMPING; n.vy *= DAMPING;
    n.x += n.vx; n.y += n.vy;
  }
}

// -- rendering
let view = { x: 0, y: 0, scale: 1 };
const root = document.createElementNS(NS, 'g');
svg.appendChild(root);

const edgeEls = edges.map(e => {
  const line = document.createElementNS(NS, 'line');
  line.setAttribute('class', 'edge');
  line.setAttribute('stroke-width', Math.max(1, e.weight || 1));
  const title = document.createElementNS(NS, 'title');
  title.textContent = (e.relationship_type || '') + (e.evidence ? (': ' + e.evidence) : '');
  line.appendChild(title);
  root.appendChild(line);
  return line;
});

// radiusFor sizes a node by the highest-weight indicator it's involved
// in (see Node.MaxWeight) -- capped so a single very high weight
// doesn't dominate the layout visually. Purely cosmetic: it doesn't
// feed the physics simulation above, which sizes forces off fixed
// constants regardless of node radius.
function radiusFor(n) {
  return 7 + Math.min(n.maxWeight || 0, 8) * 1.5;
}

const nodeEls = nodes.map(n => {
  const g = document.createElementNS(NS, 'g');
  g.setAttribute('class', 'node');
  const r = radiusFor(n);
  const circle = document.createElementNS(NS, 'circle');
  circle.setAttribute('r', r);
  circle.setAttribute('fill', colorFor(n.type));
  // High-weight threshold matches this project's own "HIGH weight"
  // convention (internal/risk's confidenceBand: weight >= 5 alone is
  // enough to push confidence to HIGH) -- a distinct outline so the
  // node stands out regardless of its type color.
  if (n.maxWeight >= 5) circle.classList.add('high-weight');
  const text = document.createElementNS(NS, 'text');
  text.setAttribute('x', r + 3);
  text.setAttribute('y', 4);
  text.textContent = n.label;
  const title = document.createElementNS(NS, 'title');
  title.textContent = n.label + ' (' + n.type + ')' + (n.maxWeight ? ' -- highest indicator weight involved: ' + n.maxWeight : '');
  g.appendChild(circle);
  g.appendChild(text);
  g.appendChild(title);
  root.appendChild(g);
  makeDraggable(g, n);
  g.addEventListener('click', (ev) => { ev.stopPropagation(); selectNode(n); });
  return g;
});

function draw() {
  edgeEls.forEach((el, i) => {
    const e = edges[i];
    el.setAttribute('x1', e.s.x); el.setAttribute('y1', e.s.y);
    el.setAttribute('x2', e.t.x); el.setAttribute('y2', e.t.y);
  });
  nodeEls.forEach((el, i) => {
    el.setAttribute('transform', 'translate(' + nodes[i].x + ',' + nodes[i].y + ')');
  });
  root.setAttribute('transform', 'translate(' + view.x + ',' + view.y + ') scale(' + view.scale + ')');
}

// -- selection / highlighting
let selected = null;
function selectNode(n) {
  selected = selected === n ? null : n;
  const connected = new Set();
  if (selected) {
    connected.add(selected);
    edges.forEach(e => { if (e.s === selected) connected.add(e.t); if (e.t === selected) connected.add(e.s); });
  }
  nodeEls.forEach((el, i) => el.classList.toggle('dim', selected != null && !connected.has(nodes[i])));
  edgeEls.forEach((el, i) => {
    const e = edges[i];
    const lit = selected != null && (e.s === selected || e.t === selected);
    el.classList.toggle('lit', lit);
    el.classList.toggle('dim', selected != null && !lit);
  });
  const detail = document.getElementById('detail');
  if (!selected) { detail.classList.remove('show'); detail.innerHTML = ''; return; }
  const rows = [
    '<div class="row"><b>' + selected.label + '</b></div>',
    '<div class="row">source: ' + selected.type + '</div>',
    '<div class="row">id: ' + selected.id + '</div>',
  ];
  edges.forEach(e => {
    if (e.s !== selected && e.t !== selected) return;
    const other = e.s === selected ? e.t : e.s;
    rows.push('<div class="row">&rarr; ' + other.label + ' <i>(' + e.relationship_type + ')</i></div>');
  });
  detail.innerHTML = rows.join('');
  detail.classList.add('show');
}
svg.addEventListener('click', () => selectNode(null));

// -- drag to move a node
let dragging = null;
function makeDraggable(el, n) {
  el.addEventListener('mousedown', (ev) => {
    ev.stopPropagation();
    dragging = n;
  });
}
window.addEventListener('mousemove', (ev) => {
  if (dragging) {
    dragging.x = (ev.clientX - view.x) / view.scale;
    dragging.y = (ev.clientY - view.y) / view.scale;
    dragging.vx = 0; dragging.vy = 0;
    // The layout is otherwise static (see the one-shot settle at the
    // bottom of this script, and why) -- a short burst here lets the
    // rest of the graph gently react to the node being moved, without
    // running physics continuously and drifting on its own.
    for (let i = 0; i < 15; i++) step();
    draw();
  } else if (panning) {
    view.x = panStart.vx + (ev.clientX - panStart.x);
    view.y = panStart.vy + (ev.clientY - panStart.y);
    draw();
  }
});
window.addEventListener('mouseup', () => { dragging = null; panning = false; svg.classList.remove('panning'); });

// -- pan background, zoom with the wheel
let panning = false, panStart = { x: 0, y: 0, vx: 0, vy: 0 };
svg.addEventListener('mousedown', (ev) => {
  panning = true;
  panStart = { x: ev.clientX, y: ev.clientY, vx: view.x, vy: view.y };
  svg.classList.add('panning');
});
svg.addEventListener('wheel', (ev) => {
  ev.preventDefault();
  const factor = ev.deltaY < 0 ? 1.1 : 0.9;
  view.scale = Math.max(0.1, Math.min(4, view.scale * factor));
  draw();
}, { passive: false });

window.addEventListener('resize', () => { width = window.innerWidth; height = window.innerHeight; draw(); });

// The layout settles once -- it does not keep animating in the
// background afterward. Confirmed live this was the right call:
// relying on requestAnimationFrame for the initial settle leaves nodes
// stuck near their random starting positions if the tab is
// backgrounded (rAF gets throttled/paused), and even when rAF does run
// continuously, a springy layout like this one doesn't fully damp out
// -- it keeps visibly drifting indefinitely instead of coming to rest,
// which makes nodes a moving target to click. Dragging still feels
// alive: it reruns a short burst of this same physics around the node
// being moved (see the mousemove handler above), it just doesn't run
// unprompted.
function settle() {
  // Re-read the viewport size and re-anchor every node's starting
  // position around it, rather than trusting whatever width/height
  // this script captured when it first ran -- confirmed live that in
  // at least one embedded-preview context, window.innerWidth/
  // innerHeight reports a stale (near-zero) size at that point, before
  // the page has actually finished sizing itself, and using it centers
  // the whole layout on the wrong "middle" and leaves it clipped in a
  // corner. A plain browser tab has the correct size from the start,
  // so this is a no-op cost there.
  width = window.innerWidth; height = window.innerHeight;
  nodes.forEach((n, i) => {
    n.x = width / 2 + Math.cos(i) * 100 + (Math.random() - 0.5) * 50;
    n.y = height / 2 + Math.sin(i) * 100 + (Math.random() - 0.5) * 50;
    n.vx = 0; n.vy = 0;
  });
  for (let i = 0; i < 300; i++) step();

  // Force-align the settled layout's centroid to the viewport center
  // as an explicit last step, rather than trusting the centering force
  // alone to land exactly there -- belt-and-suspenders on top of the
  // re-anchor above.
  let cx = 0, cy = 0;
  for (const n of nodes) { cx += n.x; cy += n.y; }
  cx /= nodes.length; cy /= nodes.length;
  const dx = width / 2 - cx, dy = height / 2 - cy;
  for (const n of nodes) { n.x += dx; n.y += dy; }

  draw();
}

// Deferred one frame past the window's load event -- in that same
// embedded-preview context, even a load-event handler alone still saw
// a stale viewport size; one more animation frame after it was
// consistently enough for the surrounding page to finish sizing the
// preview area first.
window.addEventListener('load', () => requestAnimationFrame(settle));
</script>
</body>
</html>
`
