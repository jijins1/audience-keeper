// Writes the licence notices of every package bundled into ak-editor.js
// (esbuild's metafile lists them), next to the bundle.
import { readFileSync, writeFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";

const meta = JSON.parse(readFileSync("meta.json", "utf8"));
const pkgs = new Set();
for (const input of Object.keys(meta.inputs)) {
  const m = /node_modules\/((?:@[^/]+\/)?[^/]+)\//.exec(input);
  if (m) pkgs.add(m[1]);
}
const parts = [...pkgs].sort().map((name) => {
  const dir = join("node_modules", name);
  const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
  const file = readdirSync(dir).find((f) => /^licen[cs]e/i.test(f));
  const text = file ? readFileSync(join(dir, file), "utf8").trim() : `License: ${pkg.license}`;
  return `${name}@${pkg.version} (${pkg.license})\n\n${text}`;
});
const out = join(dirname(process.argv[2]), "LICENSES.txt");
writeFileSync(out, "Third-party code bundled into ak-editor.js\n\n" + parts.join("\n\n" + "-".repeat(72) + "\n\n") + "\n");
console.log(`${pkgs.size} packages → ${out}`);
