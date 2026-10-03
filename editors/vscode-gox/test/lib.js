// Loads the gox grammar (base + tag injection) over VS Code's Go grammar.
const fs = require("fs");
const path = require("path");
const oniguruma = require("vscode-oniguruma");
const vsctm = require("vscode-textmate");

const SYNTAXES_DIR = path.join(__dirname, "..", "syntaxes");
const read = (p) => JSON.parse(fs.readFileSync(p, "utf8"));

async function loadGox() {
  const goPath = path.join(__dirname, "go.tmLanguage.json");
  if (!fs.existsSync(goPath)) {
    console.error("go.tmLanguage.json missing -- run `npm run fetch-go-grammar` first");
    process.exit(1);
  }
  const wasmPath = path.join(require.resolve("vscode-oniguruma"), "..", "onig.wasm");
  await oniguruma.loadWASM(fs.readFileSync(wasmPath).buffer);
  const onigLib = {
    createOnigScanner: (patterns) => new oniguruma.OnigScanner(patterns),
    createOnigString: (s) => new oniguruma.OnigString(s),
  };
  const grammars = {
    "source.go": read(goPath),
    "source.gox": read(path.join(SYNTAXES_DIR, "gox.tmLanguage.json")),
    "gox.tags.injection": read(path.join(SYNTAXES_DIR, "gox-tags.tmLanguage.json")),
    "gox.imports.injection": read(path.join(SYNTAXES_DIR, "gox-imports.tmLanguage.json")),
  };
  const registry = new vsctm.Registry({
    onigLib: Promise.resolve(onigLib),
    loadGrammar: async (scope) => grammars[scope] || null,
    getInjections: (scope) => (scope === "source.gox" ? ["gox.tags.injection", "gox.imports.injection"] : undefined),
  });
  return registry.loadGrammar("source.gox");
}

// tokensOf returns [{text, scopes}] for every token of src.
function tokensOf(grammar, src) {
  let stack = vsctm.INITIAL;
  const out = [];
  for (const line of src.split("\n")) {
    const r = grammar.tokenizeLine(line, stack);
    for (const t of r.tokens) out.push({ text: line.slice(t.startIndex, t.endIndex), scopes: t.scopes.join(" ") });
    stack = r.ruleStack;
  }
  return out;
}

module.exports = { loadGox, tokensOf };
