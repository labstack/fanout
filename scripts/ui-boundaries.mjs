// Reuse the parser in the host's locked build dependencies. Parse before type
// erasure so type-only imports obey the same boundaries as runtime imports.
import { parse } from "../ui/host/node_modules/@babel/parser/lib/index.js";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(process.argv[2] ?? fileURLToPath(new URL("..", import.meta.url)));
const ui = resolve(root, "ui");
const excluded = new Set(["node_modules", "dist", ".git", ".tanstack"]);
let failures = 0;

function workspace(file) {
  const first = relative(ui, file).split(sep)[0];
  return first === "host" ? "host" : "shared";
}

function check(file, specifier, offset, source) {
  const owner = workspace(file);
  const local = specifier.startsWith("./") || specifier.startsWith("../");
  const target = local ? resolve(dirname(file), specifier) : null;
  const destination = target ? workspace(target) : null;
  const outsideShared = target && (relative(ui, target).startsWith("..") ||
    destination !== "shared" || relative(ui, target).split(sep).includes("node_modules"));
  if (owner === "shared" && (!local || outsideShared)) {
    const line = source.slice(0, offset).split("\n").length;
    console.error(`${relative(root, file)}:${line}: forbidden ${owner} import ${JSON.stringify(specifier)}`);
    failures++;
  }
}

function scan(file) {
  const source = readFileSync(file, "utf8");
  if (file.endsWith(".css")) {
    const css = source.replace(/\/\*[\s\S]*?\*\//g, match => match.replace(/[^\n]/g, " "));
    const imports = /@import\s+(?:url\(\s*)?(?:"([^"]+)"|'([^']+)'|([^\s;)]+))/g;
    for (const match of css.matchAll(imports)) check(file, match[1] ?? match[2] ?? match[3], match.index, source);
    return;
  }
  function visit(node) {
    if (!node || typeof node !== "object") return;
    const literal = node.source ?? (node.type === "TSImportType" ? node.argument :
      node.type === "TSExternalModuleReference" ? node.expression :
      node.type === "CallExpression" && node.callee?.name === "require" ? node.arguments[0] : null);
    if (literal?.type === "StringLiteral") check(file, literal.value, literal.start, source);
    else if (literal?.type === "TemplateLiteral" && literal.expressions.length === 0) {
      check(file, literal.quasis[0].value.cooked, literal.start, source);
    }
    for (const value of Object.values(node)) {
      if (Array.isArray(value)) value.forEach(visit);
      else if (value && typeof value === "object" && value.type) visit(value);
    }
  }
  const plugins = ["typescript", ...(/\.[jt]sx$/.test(file) ? ["jsx"] : [])];
  if (file.endsWith(".html")) {
    for (const match of source.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script[^>]*>/gi)) {
      const src = /\bsrc=["']([^"']+)["']/.exec(match[1]);
      if (src) check(file, src[1], match.index, source);
      if (match[2].trim()) visit(parse(match[2], { sourceType: "unambiguous", plugins, createImportExpressions: true }));
    }
  } else visit(parse(source, { sourceType: "unambiguous", plugins, createImportExpressions: true }));
}

function walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (excluded.has(entry.name)) continue;
    const file = resolve(dir, entry.name);
    if (entry.isDirectory()) walk(file);
    else if (/\.(?:[cm]?[jt]sx?|css|html)$/.test(entry.name)) scan(file);
  }
}

walk(ui);
if (failures) process.exit(1);
console.log("UI workspace boundaries passed.");
