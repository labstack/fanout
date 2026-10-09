#!/usr/bin/env node
import { readdirSync, readFileSync } from "node:fs";
import { basename, join, relative, resolve } from "node:path";

const root = resolve(process.argv[2] ?? ".");
const roundTitle = /\b(?:[VPQHFWIRBNM]\d+[a-d]?|[Tt]ask\s+\d+|[Ff]inal\d+|[Ff]ix\d+|[Rr]ound\d+|[Pp]review[-_ ]part\d*)\b/;
const roundFile = /(?:^|[-_])(?:[vpqhfwirbnm]\d+[a-z]*|task[-_ ]?\d+|final\w*|fix\d*|round\d+|preview[-_]part\d*)(?:[-_.]|$)/i;
const roundGoName = /^(?:Test|Benchmark|Example)(?:[VPQHFWIRBNM]\d+|Task\d+|Final(?![a-z])|Fix(?![a-z])|Round\d+|PreviewPart\d+)/;
let failures = 0;
function reject(file, line, kind, value) {
  console.error(`${relative(root, file)}:${line}: review identifier in ${kind}: ${value}`);
  failures++;
}
function check(file) {
  if (!/(?:\.test\.[jt]sx?|_test\.go)$/.test(file)) return;
  if (roundFile.test(basename(file))) reject(file, 1, "test filename", basename(file));
  const text = readFileSync(file, "utf8");
  const lineAt = index => text.slice(0, index).split("\n").length;
  if (file.endsWith(".go")) {
    for (const match of text.matchAll(/^func\s+((?:Test|Benchmark|Example)\w*)\s*\(/gm)) {
      if (roundGoName.test(match[1])) reject(file, lineAt(match.index), "Go test name", match[1]);
    }
  }
  // Tokenize literals/comments so fixture strings and commented tests cannot
  // masquerade as registered titles. No compiler or installed parser is needed.
  const tokens = [...text.matchAll(/\/\/[^\n]*|\/\*[\s\S]*?\*\/|"(?:\\[\s\S]|[^"\\])*"|'(?:\\[\s\S]|[^'\\])*'|`(?:\\[\s\S]|[^`\\])*`|[A-Za-z_$][\w$]*|[().]/g)]
    .filter(token => !token[0].startsWith("//") && !token[0].startsWith("/*"));
  const skipCall = start => {
    let depth = 0;
    for (let i = start; i < tokens.length; i++) {
      if (tokens[i][0] === "(") depth++;
      if (tokens[i][0] === ")" && --depth === 0) return i + 1;
    }
    return tokens.length;
  };
  for (let i = 0; i < tokens.length; i++) {
    const name = tokens[i][0];
    const goSubtest = file.endsWith(".go") && name === "Run" && tokens[i - 1]?.[0] === "." && /^(?:t|b)$/.test(tokens[i - 2]?.[0]);
    if (!goSubtest && !["it", "test", "describe"].includes(name)) continue;
    let next = i + 1;
    while (tokens[next]?.[0] === ".") {
      const modifier = tokens[next + 1]?.[0];
      next += 2;
      if (["each", "for", "skipIf", "runIf"].includes(modifier) && tokens[next]?.[0] === "(") next = skipCall(next);
      else if (modifier === "each" && tokens[next]?.[0].startsWith("`")) next++;
    }
    if (tokens[next]?.[0] !== "(") continue;
    const literal = tokens[next + 1];
    if (!literal || !/^["'`]/.test(literal[0])) continue;
    const title = literal[0].slice(1, -1);
    if (roundTitle.test(title)) reject(file, lineAt(literal.index), "test title", title);
  }
}
function walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name.startsWith(".") || ["node_modules", "vendor", "dist"].includes(entry.name)) continue;
    const path = join(dir, entry.name);
    if (entry.isDirectory()) walk(path);
    else if (entry.isFile()) check(path);
  }
}
walk(root);
if (failures) process.exitCode = 1;
else console.log("Behavioral test names passed.");
