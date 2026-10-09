import { readdirSync,readFileSync } from "node:fs";
import { resolve,relative } from "node:path";
import { fileURLToPath } from "node:url";
export function tree(root) {
  const out=new Map();
  function walk(dir){for(const e of readdirSync(dir,{withFileTypes:true})){const p=resolve(dir,e.name);if(e.isDirectory())walk(p);else if(e.isFile())out.set(relative(root,p),readFileSync(p));else throw new Error("Unexpected non-file asset");}}
  walk(root);return out;
}
export function compare(a,b,apps=false){
  const x=tree(resolve(a)),y=tree(resolve(b));
  const errors=[...new Set([...x.keys(),...y.keys()])].sort().filter(k=>!x.get(k)||!y.get(k)||!x.get(k).equals(y.get(k)));
  if(apps)for(const assets of [x,y]){const paths=[...assets.keys()].sort();if(paths.length!==1 || paths[0]!=="panels.html")errors.push(`Unexpected app assets: ${paths.join(",")}`);}
  return [...new Set(errors)];
}
if(process.argv[1] && fileURLToPath(import.meta.url)===resolve(process.argv[1])){const errors=compare(process.argv[2],process.argv[3],process.argv[4]==="--apps");if(errors.length){console.error(errors.join("\n"));process.exit(1);}console.log("Embedded assets match.");}
