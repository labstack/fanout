import { afterEach, expect, test } from "bun:test";
import { cpSync, existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
const source = resolve(import.meta.dir, "..");
const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });
function put(root: string, file: string, text: string) { mkdirSync(dirname(join(root,file)),{recursive:true}); writeFileSync(join(root,file),text); }
function uiCopy() {
 const root=mkdtempSync(join(tmpdir(),"fanout-knip-")); roots.push(root);
 cpSync(join(source,"ui"),join(root,"ui"),{recursive:true,filter:path=>!path.includes("node_modules")});
 symlinkSync(join(source,"ui/host/node_modules"),join(root,"ui/host/node_modules"),"dir");return root;
}
for(const area of ["host/src","panels",""]) {
 test(`rejects an unused production file in ui/${area}`,()=>{
  const root=uiCopy(),file=`${area ? area+"/" : ""}gate-unused.ts`;put(root,"ui/"+file,"export const unusedFileProbe = 1;\n");
  const out=Bun.spawnSync([process.execPath,"run","deadcode"],{cwd:join(root,"ui/host")});
  expect(out.exitCode).not.toBe(0);expect(out.stdout.toString()+out.stderr.toString()).toContain(file);
 });
 test(`rejects an unused export in an imported production module in ui/${area}`,()=>{
  const root=uiCopy(),file=`${area ? area+"/" : ""}gate-export.ts`;
  put(root,"ui/"+file,"export const liveProbe = 1; export function unusedExportProbe() { return 2; }\n");
  const entry=join(root,"ui/host/src/main.tsx");const prefix=area==="host/src"?"./":area==="panels"?"../../panels/":"../../";
  writeFileSync(entry,readFileSync(entry,"utf8")+`\nimport {liveProbe} from "${prefix}gate-export"; console.log(liveProbe);\n`);
  const out=Bun.spawnSync([process.execPath,"run","deadcode"],{cwd:join(root,"ui/host")});
  expect(out.exitCode).not.toBe(0);expect(out.stdout.toString()+out.stderr.toString()).toContain("unusedExportProbe");
 });
}
function goCopy() {
 const root=mkdtempSync(join(tmpdir(),"fanout-deadcode-"));roots.push(root);
 for(const path of ["go.mod","go.sum","cmd","internal","scripts","ui/host/package.json"])
  cpSync(join(source,path),join(root,path),{recursive:true});
 symlinkSync(join(source,".cache"),join(root,".cache"),"dir");return root;
}
for(const mode of ["exported","test-only","cmd-helper"]) {
 test(`rejects ${mode} Go code outside production reachability`,()=>{
  const root=goCopy(),dir=mode==="cmd-helper"?"cmd/fanout":"internal/id",name=mode==="cmd-helper"?"unusedCmdGateProbe":"UnusedExportGateProbe";
  put(root,dir+"/gate_probe.go",`package ${mode==="cmd-helper"?"main":"id"}\nfunc ${name}() {}\n`);
  if(mode==="test-only") put(root,dir+"/gate_probe_test.go",'package id\nimport "testing"\nfunc TestGateProbe(t *testing.T) { UnusedExportGateProbe() }\n');
  const out=Bun.spawnSync(["node","scripts/go-deadcode.mjs"],{cwd:root,env:{...process.env,GOCACHE:join(source,".superpowers/gocache"),GOTMPDIR:join(source,".superpowers/gotmp"),GOFLAGS:"-mod=readonly",GOPROXY:"off"}});
  expect(out.exitCode).not.toBe(0);expect(out.stdout.toString()+out.stderr.toString()).toContain(name);expect(out.stdout.toString()+out.stderr.toString()).toContain("gate_probe.go");
 },120_000);
}
test("the Go runner analyzes all command mains and excludes test roots",()=>{
 const runner=readFileSync(join(source,"scripts/go-deadcode.mjs"),"utf8");
 expect(runner).toContain('"./cmd/..."');expect(runner).toContain('"-test=false"');
});

test("rejects stale and wildcard Go allowlist entries", () => {
 const root = goCopy(), file = join(root, "scripts/go-deadcode.allowlist"), original = readFileSync(file, "utf8");
 for (const [entry, message] of [["github.com/labstack/fanout/internal/id.AbsentGateProbe\tNot a real entry point", "Stale allowlist entry"], ["github.com/labstack/fanout/internal/id.*\tNo wildcard allowance", "Invalid allowlist entry"]]) {
  writeFileSync(file, original + "\n" + entry + "\n");
  const out = Bun.spawnSync(["node", "scripts/go-deadcode.mjs"], { cwd: root, env: { ...process.env, GOCACHE: join(source, ".superpowers/gocache"), GOTMPDIR: join(source, ".superpowers/gotmp"), GOFLAGS: "-mod=readonly", GOPROXY: "off" } });
  expect(out.exitCode).not.toBe(0);
  expect(out.stdout.toString() + out.stderr.toString()).toContain(message);
 }
}, 120_000);
