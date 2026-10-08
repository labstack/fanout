#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";

// The module's tool directive pins deadcode; every command main is a root,
// while exported code referenced solely by tests remains unreachable.
const result = spawnSync("bash", ["scripts/with-duckdb.sh", "go", "tool", "deadcode", "-test=false", "-json", "./cmd/..."], {encoding:"utf8", maxBuffer:32*1024*1024});
if (result.error) { console.error(result.error.message); process.exit(1); }
if (result.stderr) process.stderr.write(result.stderr);
if (result.status !== 0) { if(result.stdout) process.stdout.write(result.stdout); process.exit(result.status ?? 1); }
let packages;
try { packages = JSON.parse(result.stdout || "[]"); } catch(error) { console.error(`Invalid deadcode JSON: ${error.message}`); process.exit(1); }
if (!Array.isArray(packages)) { console.error("Invalid deadcode package array"); process.exit(1); }
const findings = packages.flatMap(pkg => {
 if(typeof pkg.Path!=="string" || !Array.isArray(pkg.Funcs)) throw new Error("Invalid deadcode package");
 return pkg.Funcs.map(fn=>({symbol:`${pkg.Path}.${fn.Name}`,file:fn.Position?.File,line:fn.Position?.Line}));
});
console.log("Unfiltered deadcode findings:");
for(const finding of findings) console.log(`${finding.file}:${finding.line}: ${finding.symbol}`);
if(!findings.length) console.log("(none)");
const allow = new Map();
let failed=false;
for(const [index,line] of readFileSync("scripts/go-deadcode.allowlist","utf8").split(/\r?\n/).entries()) {
 if(!line.trim() || line.startsWith("#")) continue;
 const parts=line.split("\t");
 if(parts.length!==2 || !parts[0] || !parts[1].trim() || /[*\s]/.test(parts[0]) || allow.has(parts[0])) {console.error(`Invalid allowlist entry at line ${index+1}`);failed=true;continue;}
 allow.set(parts[0],parts[1]);
}
const actual=new Set(findings.map(f=>f.symbol));
for(const [symbol,reason] of allow) {
 if(!actual.has(symbol)){console.error(`Stale allowlist entry: ${symbol}`);failed=true;}
 else console.log(`Allowed non-main entry: ${symbol} — ${reason}`);
}
for(const finding of findings) if(!allow.has(finding.symbol)){console.error(`Unreachable: ${finding.file}:${finding.line}: ${finding.symbol}`);failed=true;}
process.exitCode=failed?1:0;
