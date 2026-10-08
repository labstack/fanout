/** Measurements of pinned ECharts/zrender output for the controller's collector. */
type Rect = {x:number;y:number;width:number;height:number;clone():Rect;applyTransform(transform:number[]):void};
type Element = {
  type:string; ignore?:boolean;invisible?:boolean; shape?:{r?:number}; style?: {text?:string;font?:string;fontSize?:number;fontFamily?:string;fill?:unknown;stroke?:unknown;shadowColor?:string;lineWidth?:number;opacity?:number};
  getBoundingRect():Rect;getComputedTransform():number[]|null;traverse?(visit:(element:Element)=>void):void;
};
type Model = {subType?:string;getData():{eachItemGraphicEl(visit:(element:Element,index:number)=>void):void;getRawDataItem(index:number):unknown};};
type Native = {
  getZr?():{storage:{getDisplayList(force:boolean):Element[]}};
  getModel?():{getSeries():Model[];getComponent(name:string,index?:number):unknown};
  getViewOfSeriesModel?(model:Model):{group:Element};getViewOfComponentModel?(model:unknown):{group:Element};
};
type Option = {tooltip?:{backgroundColor?:string};grid?:unknown;series?:{type?:string;data?:{value?:number}[];label?:{show?:boolean;formatter?:(point:{value:number})=>string}}[];yAxis?:{type?:string;data?:string[];axisLabel?:{formatter?:(text:string)=>string}};};
const bounds=(element:Element)=>{
  const rect=element.getBoundingRect().clone(),transform=element.getComputedTransform();if(transform) rect.applyTransform(transform);
  return {left:rect.x,top:rect.y,right:rect.x+rect.width,bottom:rect.y+rect.height};
};
const visit=(element:Element,callback:(element:Element)=>void)=>{callback(element);element.traverse?.(callback);};

export function nativeAudit(instance:unknown, compiled:unknown, size:{width:number;height:number}, plot?:{x?:number;y?:number;width:number;height:number}) {
  const chart=instance as Native,option=compiled as Option;
  const surface=option.tooltip?.backgroundColor;
  const display=chart.getZr?.().storage.getDisplayList(true)??[];
  const texts=display.flatMap(el=>{
    const style=el.style;if(!style?.text?.trim()) return [];
    const size=style.fontSize??Number(style.font?.match(/([\d.]+)px/)?.[1]);
    const halo=typeof style.stroke==="string"&&(style.lineWidth??0)>=3?style.stroke:undefined;
    return [{text:style.text,size,family:style.font??style.fontFamily??"",color:typeof style.fill==="string"?style.fill:"",surface,halo,...bounds(el)}];
  });
  const marks:{fill?:string;stroke?:string;shadow?:string;surface?:string;density?:boolean}[]=[];
  const cells:{box:ReturnType<typeof bounds>;value:number[];fill?:string}[]=[];
  let gaugeDiameter=0;
  const paints=new Set<string>();
  const paint=(el:Element,density=false)=>{
    const s=el.style;if(!s||s.text||s.opacity!==undefined&&s.opacity<.5) return;
    const mark={fill:typeof s.fill==="string"?s.fill:undefined,stroke:typeof s.stroke==="string"&&s.lineWidth?s.stroke:undefined,shadow:s.shadowColor,surface,density};
    if([mark.fill,mark.stroke].every(c=>!c||c==="none"))return;
    const key=JSON.stringify(mark);if(!paints.has(key)){paints.add(key);marks.push(mark);}
  };
  const model=chart.getModel?.();
  for(const series of model?.getSeries?.()??[]) {
    if(series.subType==="gauge") chart.getViewOfSeriesModel?.(series).group.traverse?.(el=>{if(el.shape?.r)gaugeDiameter=Math.max(gaugeDiameter,el.shape.r*2);});
    const density=series.subType==="custom"&&Boolean(model?.getComponent("visualMap"));
    if(series.subType==="line") chart.getViewOfSeriesModel?.(series).group.traverse?.(el=>{if(el.type==="ec-polyline")paint(el);});
    series.getData().eachItemGraphicEl((element,index)=>{
      visit(element,el=>{if(["rect","path","circle","sector"].includes(el.type)) paint(el,density);});
      const raw=series.getData().getRawDataItem(index);
      const value=raw&&typeof raw==="object"&&"value" in raw ? raw.value : raw;
      if(series.subType==="custom"&&Array.isArray(value)&&value.length>=5&&option.yAxis?.type==="category"&&option.series?.[0]?.label===undefined&&element.type==="rect") cells.push({box:bounds(element),value:value as number[],fill:typeof element.style?.fill==="string"?element.style.fill:undefined});
    });
  }
  const visualMap=model?.getComponent("visualMap");
  let scale:{width:number;height:number}|undefined;
  if(visualMap) chart.getViewOfComponentModel?.(visualMap).group.traverse?.(el=>{
    if(typeof el.style?.fill!=="object"||!el.style.fill)return;
    const box=bounds(el),width=box.right-box.left,height=box.bottom-box.top;
    if(width>height&&width>50)scale={width,height};
  });
  const gaps:number[]=[];
  const byTime=new Map<string,typeof cells[number]>();
  for(const cell of cells)byTime.set(`${cell.value[0]}:${cell.value[1]}`,cell);
  for(const c of cells){
    const right=byTime.get(`${c.value[3]}:${c.value[1]}`),above=byTime.get(`${c.value[0]}:${c.value[1]+1}`);
    if(right)gaps.push(right.box.left-c.box.right);
    if(above)gaps.push(c.box.top-above.box.bottom);
  }
  const category=option.yAxis?.data?.map(text=>option.yAxis?.axisLabel?.formatter?.(text)??text)??[];
  const categories=texts.filter(t=>category.includes(t.text));
  const expected=option.series?.flatMap(s=>s.type==="bar"&&s.label?.show?s.data?.flatMap(d=>typeof d.value==="number"?[s.label!.formatter!({value:d.value})]:[])??[]:[])??[];
  const axes=(Array.isArray(option.yAxis)?option.yAxis:[option.yAxis]) as {name?:string;type?:string;data?:string[];splitNumber?:number}[];
  const ticks:ReturnType<typeof bounds>[]=[];
  const labelled:string[]=[];
  axes.forEach((axis,index)=>{
    const component=model?.getComponent("yAxis",index);
    if(component)chart.getViewOfComponentModel?.(component).group.traverse?.(el=>{
      if(!el.ignore&&!el.invisible&&el.type==="text"&&el.style?.text&&el.style.text!==axis?.name){ticks.push(bounds(el));labelled.push(el.style.text);}
    });
  });
  const xAxis=(option as {xAxis?:{name?:string}}).xAxis;
  const axisTitles=option.series?.some(s=>s.type==="scatter")?[xAxis?.name,axes[0]?.name].filter((n):n is string=>Boolean(n)):[];
  const gauge=(option.series as {type?:string;detail?:{formatter?:()=>string}}[]|undefined)?.find(s=>s.type==="gauge");
  return {texts,marks,
    ...(gauge?{gauge:{width:size.width,height:size.height,diameter:gaugeDiameter,texts,value:gauge.detail?.formatter?.()}}:{}),
    ...(plot?{plot:{fraction:plot.height/size.height,height:plot.height,y_ticks:ticks,...(axisTitles.length?{titles:axisTitles.map(name=>({name,boxes:texts.filter(t=>t.text===name)})),rect:{left:plot.x??0,top:plot.y??0,right:(plot.x??0)+plot.width,bottom:(plot.y??0)+plot.height}}:{}),split_number:axes[0]?.splitNumber,...(axes[0]?.type==="category"&&option.series?.[0]?.type==="custom"&&!scale?{rows:axes[0].data,labelled}: {})}}:{}),
    ...(scale?{heat:{cells,gaps,gap:gaps.length ? (Math.abs(gaps[0])<.5?0:1) : undefined,scale_width:scale.width,scale_height:scale.height,plot_fraction:(plot?.height??0)/size.height}}:{}),
    ...(option.yAxis?.type==="category"&&option.series?.some(s=>s.type==="bar")?{bars:{category_fraction:Math.max(0,...categories.map(c=>c.right-c.left))/size.width,expected_labels:[...new Set(expected)],value_labels:texts.map(t=>t.text)}}:{})};
}
