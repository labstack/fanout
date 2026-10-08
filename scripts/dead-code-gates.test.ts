import { afterEach, expect, test } from "bun:test";
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
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
function goCopy(cache = join(source, ".cache")) {
 const root=mkdtempSync(join(tmpdir(),"fanout-deadcode-"));roots.push(root);
 for(const path of ["go.mod","go.sum","cmd","internal","scripts","ui/host/package.json"])
  cpSync(join(source,path),join(root,path),{recursive:true});
 mkdirSync(cache, { recursive: true });
 symlinkSync(cache,join(root,".cache"),"dir");return root;
}
for(const mode of ["exported","test-only","cmd-helper","dbmigrate-helper","notices-helper","new-main-helper"]) {
 test(`rejects ${mode} Go code outside production reachability`,()=>{
  const root=goCopy(),dir=mode==="cmd-helper"?"cmd/fanout":mode==="dbmigrate-helper"?"internal/cmd/dbmigrate":mode==="notices-helper"?"internal/cmd/notices":mode==="new-main-helper"?"internal/gatecommand":"internal/id",name=mode.endsWith("helper")?"unusedCmdGateProbe":"UnusedExportGateProbe";
  put(root,dir+"/gate_probe.go",`package ${mode.endsWith("helper")?"main":"id"}\nfunc ${name}() {}\n`);
  if(mode==="new-main-helper") put(root,dir+"/main.go","package main\nfunc main() {}\n");
  if(mode==="test-only") put(root,dir+"/gate_probe_test.go",'package id\nimport "testing"\nfunc TestGateProbe(t *testing.T) { UnusedExportGateProbe() }\n');
  const out=Bun.spawnSync(["node","scripts/go-deadcode.mjs"],{cwd:root,env:{...process.env,GOCACHE:join(source,".superpowers/gocache"),GOFLAGS:"-mod=readonly"}});
  expect(out.exitCode).not.toBe(0);expect(out.stdout.toString()+out.stderr.toString()).toContain(name);expect(out.stdout.toString()+out.stderr.toString()).toContain("gate_probe.go");
 },120_000);
}

test("rejects stale and wildcard Go allowlist entries", () => {
 const root = goCopy(), file = join(root, "scripts/go-deadcode.allowlist"), original = readFileSync(file, "utf8");
 for (const [entry, message] of [["github.com/labstack/fanout/internal/id.AbsentGateProbe\tNot a real entry point", "Stale allowlist entry"], ["github.com/labstack/fanout/internal/id.*\tNo wildcard allowance", "Invalid allowlist entry"]]) {
  writeFileSync(file, original + "\n" + entry + "\n");
  const out = Bun.spawnSync(["node", "scripts/go-deadcode.mjs"], { cwd: root, env: { ...process.env, GOCACHE: join(source, ".superpowers/gocache"),  GOFLAGS: "-mod=readonly" } });
  expect(out.exitCode).not.toBe(0);
  expect(out.stdout.toString() + out.stderr.toString()).toContain(message);
 }
}, 120_000);

test("the configured production graph contains every entry and a nonempty project", () => {
 const ui = join(source, "ui"), config = JSON.parse(readFileSync(join(ui, "knip.json"), "utf8"));
 for (const pattern of config.entry) {
  const glob = new Bun.Glob(pattern.replace(/!$/, ""));
  expect([...glob.scanSync({ cwd: ui, onlyFiles: true })].length).toBeGreaterThan(0);
 }
 const projects = new Set<string>();
 for (let pattern of config.project) {
  const exclude = pattern.startsWith("!");
  pattern = pattern.replace(/^!/, "").replace(/!$/, "");
  for (const file of new Bun.Glob(pattern).scanSync({ cwd: ui, onlyFiles: true })) {
   if (exclude) projects.delete(file); else projects.add(file);
  }
 }
 expect(projects.size).toBeGreaterThanOrEqual(50);
});


test("Go probe copies share a usable cache even before the first native build", () => {
 const shared = mkdtempSync(join(tmpdir(), "fanout-native-cache-")); roots.push(shared);
 const cache = join(shared, "cache"), root = goCopy(cache);
 put(root, ".cache/probe", "shared cache");
 expect(readFileSync(join(cache, "probe"), "utf8")).toBe("shared cache");
});
