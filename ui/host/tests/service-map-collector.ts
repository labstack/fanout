/** Self-contained browser collector: run on a fitted map viewport in either surface. */
export function assertServiceMapDOM(viewport: HTMLElement, { requireNames = false, allowOverflow = false }: {requireNames?:boolean;allowOverflow?:boolean} = {}) {
  const body = viewport.getBoundingClientRect();
  const fail = (message: string) => { throw new Error(`Service map geometry: ${message}`); };
  if (body.width <= 0 || body.height <= 0) fail("empty viewport");
  const content=viewport.querySelector<HTMLElement>("[data-service-content]");
  const contentBox=content?.getBoundingClientRect();
  const canvas=allowOverflow && content ? contentBox?.width ? contentBox : DOMRect.fromRect({x:body.x,y:body.y,width:parseFloat(content.style.width),height:parseFloat(content.style.height)}) : body;
  const inside = (box: DOMRect, label: string) => {
    if (box.left < canvas.left + 1 || box.top < canvas.top + 1 || box.right > canvas.right - 1 || box.bottom > canvas.bottom - 1) fail(`${label} is clipped`);
  };
  const nodes = [...viewport.querySelectorAll<HTMLButtonElement>("button[title]")].map(element => ({ element, id: element.dataset.serviceNode ?? element.title.split(" · ")[0], box: element.getBoundingClientRect() }));
  if (!nodes.length) fail("no nodes");
  for (const node of nodes) {
    inside(node.box, node.id);
    for (const other of nodes) if (node !== other && node.box.left < other.box.right - .1 && node.box.right > other.box.left + .1 && node.box.top < other.box.bottom - .1 && node.box.bottom > other.box.top + .1) fail(`${node.id} overlaps ${other.id}`);
    const text = node.element.querySelector<HTMLElement>("[data-service-text]") ?? node.element.firstElementChild as HTMLElement;
    const minimumFont = 11;
    if (text && parseFloat(getComputedStyle(text).fontSize) + .01 < minimumFont) fail(`${node.id} text is below the 11 px floor`);
    if (requireNames) {
      const name=node.element.querySelector<HTMLElement>("[data-service-name]")??text?.children[1] as HTMLElement;
      const expected=Array.from(node.id).slice(0,24).join("")+(Array.from(node.id).length>24?"…":"");
      if(!name||name.textContent!==expected) fail(`${node.id} name is missing or abbreviated`);
      for(let ancestor:HTMLElement|null=name;ancestor&&ancestor!==viewport;ancestor=ancestor.parentElement) {
        const style=getComputedStyle(ancestor);
        if(style.visibility==="hidden"||style.display==="none"||style.opacity==="0") fail(`${node.id} name is hidden`);
      }
      const style=getComputedStyle(name),lineStyle=getComputedStyle(text);
      const fontSize=parseFloat(style.fontSize)||parseFloat(lineStyle.fontSize);
      if(!(fontSize+.01>=minimumFont)) fail(`${node.id} name is below the 11 px floor`);
      if(fontSize+2>node.box.height+1) fail(`${node.id} name exceeds its card height`);
      const rect=name.getBoundingClientRect();
      if(rect.width>0&&(rect.left<node.box.left||rect.right>node.box.right||rect.top<node.box.top||rect.bottom>node.box.bottom)) fail(`${node.id} name leaves its card`);
      if(name.scrollWidth>name.clientWidth+1) fail(`${node.id} name is truncated`);
      const context=document.createElement("canvas").getContext("2d");
      if(context) {
        context.font=`600 ${fontSize}px ${style.fontFamily||lineStyle.fontFamily}`;
        const glyph=text.firstElementChild!.textContent!;
        if(!glyph) fail(`${node.id} health icon is missing`);
      const padding = 14;
      if(context.measureText(name.textContent!+glyph).width+padding>node.box.width+1) fail(`${node.id} name exceeds its measured card budget`);
      }
    }
  }
  const edges = [...viewport.querySelectorAll<SVGPathElement>("svg > g > path")];
  for (const edge of edges) {
    inside(edge.getBoundingClientRect(), "edge");
    const [caller, callee] = edge.querySelector("title")!.textContent!.split("\n")[0].split(" → ");
    const source = nodes.find(n => n.id === caller), target = nodes.find(n => n.id === callee);
    const entry = !edges.some(candidate => candidate.querySelector("title")!.textContent!.split("\n")[0].endsWith(` → ${caller}`));
    if (entry && source && target && source.box.right >= target.box.left) fail(`${caller} does not precede ${callee}`);
    const coordinates = edge.getAttribute("d")!.match(/-?[\d.]+/g)!.map(Number);
    const matrix = edge.getScreenCTM?.();
    const points = coordinates.filter((_,i) => i % 2 === 0).map((x,i) => {
      const y = coordinates[i*2+1];
      return matrix ? {x:matrix.a*x+matrix.c*y+matrix.e,y:matrix.b*x+matrix.d*y+matrix.f} : {x,y};
    });
    for (let i=1;i<points.length;i++) for (const node of nodes) if (node.id !== caller && node.id !== callee) {
      const a=points[i-1], b=points[i], box=node.box;
      let lo=0, hi=1;
      for (const [value,delta,min,max] of [[a.x,b.x-a.x,box.left+.1,box.right-.1],[a.y,b.y-a.y,box.top+.1,box.bottom-.1]]) {
        if (!delta) { if (value < min || value > max) hi=-1; }
        else { const p=(min-value)/delta,q=(max-value)/delta;lo=Math.max(lo,Math.min(p,q));hi=Math.min(hi,Math.max(p,q)); }
      }
      if (lo<hi) fail(`${caller} → ${callee} crosses ${node.id}`);
    }
  }
  const label = viewport.querySelector<HTMLElement>("[data-service-uncalled-label]") ?? [...viewport.querySelectorAll("span")].find(element => element.textContent === "No traced calls in this window");
  if (label) {
    const box = label.getBoundingClientRect();
    inside(box, "uncalled label");
    const overlaps = (other: DOMRect) => box.left < other.right && box.right > other.left && box.top < other.bottom && box.bottom > other.top;
    for (const node of nodes) if (overlaps(node.box)) fail(`uncalled label overlaps ${node.id}`);
    for (const edge of edges) if (overlaps(edge.getBoundingClientRect())) fail("uncalled label overlaps an edge");
  }
  const width = Math.max(...nodes.map(n => n.box.right)) - Math.min(...nodes.map(n => n.box.left));
  const height = Math.max(...nodes.map(n => n.box.bottom)) - Math.min(...nodes.map(n => n.box.top));
  if (Math.max(width / body.width, height / body.height) < .7) fail("graph does not fill 70% of the limiting dimension");
  return { nodes: nodes.length, edges: edges.length, width, height, body: { width: body.width, height: body.height } };
}
