import { afterEach, expect, test } from "bun:test";
import { existsSync, mkdtempSync, mkdirSync, writeFileSync, symlinkSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { compare, tree } from "./ui-compare.mjs";

const roots: string[]=[];
afterEach(()=>{for(const root of roots.splice(0))rmSync(root,{recursive:true,force:true});});
function fixture(){const root=mkdtempSync(join(tmpdir(),"fanout-compare-"));roots.push(root);const a=join(root,"a"),b=join(root,"b");for(const dir of [a,b]){mkdirSync(join(dir,"nested"),{recursive:true});writeFileSync(join(dir,"nested/file"),"same");}return {a,b};}
test("compares paths and bytes without mutating either tree",()=>{const {a,b}=fixture();expect(compare(a,b)).toEqual([]);const original=tree(a);writeFileSync(join(b,"nested/file"),"changed");writeFileSync(join(b,"extra"),"extra");expect(compare(a,b)).toEqual(["extra","nested/file"]);rmSync(join(b,"nested/file"));expect(compare(b,a)).toEqual(["extra","nested/file"]);expect(tree(a)).toEqual(original);expect(readFileSync(join(b,"extra"),"utf8")).toBe("extra");});
test("rejects symlinks",()=>{const {a,b}=fixture();symlinkSync(join(a,"nested/file"),join(b,"link"));expect(()=>compare(a,b)).toThrow("Unexpected non-file asset");});
test("apps require exactly panels.html even when both trees share extra files",()=>{const {a,b}=fixture();for(const dir of [a,b]){rmSync(join(dir,"nested"),{recursive:true});writeFileSync(join(dir,"panels.html"),"app");}expect(compare(a,b,true)).toEqual([]);for(const dir of [a,b])writeFileSync(join(dir,"favicon.svg"),"icon");expect(compare(a,b,true)).toContain("Unexpected app assets: favicon.svg,panels.html");});
test.each([resolve(import.meta.dir,".."),resolve(import.meta.dir,"../ui/host")])("CLI works from %s",cwd=>{const {a,b}=fixture();const result=Bun.spawnSync([process.execPath,resolve(import.meta.dir,"ui-compare.mjs"),a,b],{cwd});expect(result.exitCode).toBe(0);expect(result.stdout.toString()).toContain("Embedded assets match.");writeFileSync(join(b,"extra"),"extra");expect(Bun.spawnSync([process.execPath,resolve(import.meta.dir,"ui-compare.mjs"),a,b],{cwd}).exitCode).toBe(1);});
test.skipIf(!existsSync(resolve(import.meta.dir,"../.superpowers/replay/build-audit.sh")))("audit command builds both outputs from one host graph",()=>{const script=readFileSync(resolve(import.meta.dir,"../.superpowers/replay/build-audit.sh"),"utf8");expect(script).toContain('bun run build:host -- --mode development --outDir "$stage/dist"');expect(script).toContain("bun run build:apps");expect(script).toContain('internal/mcp/apps/panels.html');expect(script).not.toContain("ui/apps");expect(script).not.toMatch(/git (?:checkout|restore|clean)/);});
