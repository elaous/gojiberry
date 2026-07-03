// Tokenizes every *.gox file under the repo's examples/ with the real
// gox grammar (base + tag injection) layered on VS Code's own Go
// grammar, and checks that Go keywords and (where the source actually
// has tags) entity.name.tag.gox tokens both show up -- including deep
// inside nested blocks/loops, which is exactly the case a naive
// top-level {include:"#tag"} pattern list fails at silently (see the
// comment in ../syntaxes/gox.tmLanguage.json).
const fs = require("fs");
const path = require("path");
const oniguruma = require("vscode-oniguruma");
const vsctm = require("vscode-textmate");

const REPO_ROOT = path.join(__dirname, "..", "..", "..");
const SYNTAXES_DIR = path.join(__dirname, "..", "syntaxes");
const GO_GRAMMAR_PATH = path.join(__dirname, "go.tmLanguage.json");

function findGoxFiles(dir) {
  let out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) out = out.concat(findGoxFiles(full));
    else if (entry.name.endsWith(".gox")) out.push(full);
  }
  return out;
}

async function main() {
  if (!fs.existsSync(GO_GRAMMAR_PATH)) {
    console.error("go.tmLanguage.json missing -- run `npm run fetch-go-grammar` first");
    process.exit(1);
  }

  const wasmPath = path.join(require.resolve("vscode-oniguruma"), "..", "onig.wasm");
  await oniguruma.loadWASM(fs.readFileSync(wasmPath).buffer);
  const onigLib = {
    createOnigScanner: (patterns) => new oniguruma.OnigScanner(patterns),
    createOnigString: (s) => new oniguruma.OnigString(s),
  };

  const goGrammar = JSON.parse(fs.readFileSync(GO_GRAMMAR_PATH, "utf8"));
  const goxBase = JSON.parse(fs.readFileSync(path.join(SYNTAXES_DIR, "gox.tmLanguage.json"), "utf8"));
  const goxInjection = JSON.parse(fs.readFileSync(path.join(SYNTAXES_DIR, "gox-tags.tmLanguage.json"), "utf8"));

  const registry = new vsctm.Registry({
    onigLib: Promise.resolve(onigLib),
    loadGrammar: async (scopeName) => {
      if (scopeName === "source.go") return goGrammar;
      if (scopeName === "source.gox") return goxBase;
      if (scopeName === "gox.tags.injection") return goxInjection;
      return null;
    },
    getInjections: (scopeName) => (scopeName === "source.gox" ? ["gox.tags.injection"] : undefined),
  });

  const grammar = await registry.loadGrammar("source.gox");
  const files = findGoxFiles(path.join(REPO_ROOT, "examples"));
  if (files.length === 0) {
    console.error("no .gox files found under examples/");
    process.exit(1);
  }

  let allOk = true;
  for (const filePath of files) {
    const src = fs.readFileSync(filePath, "utf8");
    const lines = src.split("\n");
    let ruleStack = vsctm.INITIAL;
    let tagCount = 0,
      keywordCount = 0,
      error = null;
    for (let i = 0; i < lines.length; i++) {
      let result;
      try {
        result = grammar.tokenizeLine(lines[i], ruleStack);
      } catch (e) {
        error = `line ${i + 1}: ${e.message}`;
        break;
      }
      for (const t of result.tokens) {
        const scopes = t.scopes.join(" ");
        if (scopes.includes("entity.name.tag.gox")) tagCount++;
        if (scopes.includes("keyword")) keywordCount++;
      }
      ruleStack = result.ruleStack;
    }
    const expectTags = src.includes("<");
    const ok = !error && keywordCount > 0 && (!expectTags || tagCount > 0);
    const rel = path.relative(REPO_ROOT, filePath);
    console.log(`${ok ? "PASS" : "FAIL"} ${rel} (tags=${tagCount} keywords=${keywordCount}${error ? " error=" + error : ""})`);
    if (!ok) allOk = false;
  }
  process.exit(allOk ? 0 : 1);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
