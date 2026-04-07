import { execSync } from "child_process";
import { createHash } from "crypto";
import { readFileSync, writeFileSync, readdirSync, unlinkSync } from "fs";
import { join } from "path";

const dir = "static/css";
const input = join(dir, "input.css");
const tmp = join(dir, "_output.css");
const manifest = "static/manifest.json";

execSync(`npx @tailwindcss/cli -i ${input} -o ${tmp} --minify`, {
  stdio: "inherit",
});

const css = readFileSync(tmp);
const hash = createHash("md5").update(css).digest("hex").slice(0, 8);
const filename = `output-${hash}.css`;
const out = join(dir, filename);

// Remove old hashed files
for (const f of readdirSync(dir)) {
  if (f.startsWith("output-") && f.endsWith(".css")) {
    unlinkSync(join(dir, f));
  }
}

writeFileSync(out, css);
unlinkSync(tmp);
writeFileSync(manifest, JSON.stringify({ "output.css": `/static/css/${filename}` }));
console.log(`built: ${filename}`);
