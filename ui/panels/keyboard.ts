export type PointEvent={name?:string;seriesName?:string;value?:unknown;data?:unknown;dataType?:string;interactive?:boolean};
export type KeyboardPoint={series_index:number;data_index:number;label:string;event:PointEvent};
export function keyboardPoints(series:readonly {name?:string;data?:readonly unknown[];interactive?:boolean}[]):KeyboardPoint[] {
  const out:KeyboardPoint[]=[];
  series.forEach((s,series_index)=>{
    if(!s.data || s.interactive===false)return;
    s.data.forEach((data,data_index)=>{
      const object=data!==null&&typeof data==='object'&&!Array.isArray(data)?data as {value?:unknown;name?:string}:undefined;
      const value=object?.value??data;
      if(value===null || value===undefined)return;
      out.push({series_index,data_index,label:`${s.name??'Series'} · point ${data_index+1}`,event:{seriesName:s.name,name:object?.name,value,data,interactive:s.interactive}});
    });
  });
  return out;
}
