// Tokenizes fixtures/highlight-sample.go with the real embedded
// -highlight injection grammar layered on VS Code's own Go grammar
// (plus the HTML/CSS/SQL/JSON grammars it embeds), and checks each
// @highlight-annotated raw string gets that language's own scopes --
// and that a plain, unannotated raw string elsewhere in the same file
// still gets ordinary Go string coloring, proving the injection only
// fires when a marker opens the string, not on every backtick.
const fs = require("fs");
const path = require("path");
const oniguruma = require("vscode-oniguruma");
const vsctm = require("vscode-textmate");

const SYNTAXES_DIR = path.join(__dirname, "..", "syntaxes");
const GO_GRAMMAR_PATH = path.join(__dirname, "go.tmLanguage.json");
const EMBEDDED = {
  html: path.join(__dirname, "html.tmLanguage.json"),
  css: path.join(__dirname, "css.tmLanguage.json"),
  sql: path.join(__dirname, "sql.tmLanguage.json"),
  json: path.join(__dirname, "json.tmLanguage.json"),
};

async function main() {
  for (const p of [GO_GRAMMAR_PATH, ...Object.values(EMBEDDED)]) {
    if (!fs.existsSync(p)) {
      console.error(`${path.basename(p)} missing -- run \`npm run fetch-go-grammar\` and \`npm run fetch-embedded-grammars\` first`);
      process.exit(1);
    }
  }

  const wasmPath = path.join(require.resolve("vscode-oniguruma"), "..", "onig.wasm");
  await oniguruma.loadWASM(fs.readFileSync(wasmPath).buffer);
  const onigLib = {
    createOnigScanner: (patterns) => new oniguruma.OnigScanner(patterns),
    createOnigString: (s) => new oniguruma.OnigString(s),
  };

  const grammars = {
    "source.go": JSON.parse(fs.readFileSync(GO_GRAMMAR_PATH, "utf8")),
    "text.html.basic": JSON.parse(fs.readFileSync(EMBEDDED.html, "utf8")),
    "source.css": JSON.parse(fs.readFileSync(EMBEDDED.css, "utf8")),
    "source.sql": JSON.parse(fs.readFileSync(EMBEDDED.sql, "utf8")),
    "source.json": JSON.parse(fs.readFileSync(EMBEDDED.json, "utf8")),
    "gox.embedded-highlight.injection": JSON.parse(
      fs.readFileSync(path.join(SYNTAXES_DIR, "embedded-highlight.tmLanguage.json"), "utf8")
    ),
  };

  const registry = new vsctm.Registry({
    onigLib: Promise.resolve(onigLib),
    loadGrammar: async (scopeName) => grammars[scopeName] || null,
    getInjections: (scopeName) => (scopeName === "source.go" ? ["gox.embedded-highlight.injection"] : undefined),
  });

  const grammar = await registry.loadGrammar("source.go");
  const lines = fs.readFileSync(path.join(__dirname, "fixtures", "highlight-sample.go"), "utf8").split("\n");

  let ruleStack = vsctm.INITIAL;
  const seen = { html: false, css: false, sql: false, json: false, plainRawStringLines: 0 };
  let insidePlainRaw = false;
  for (const line of lines) {
    const result = grammar.tokenizeLine(line, ruleStack);
    for (const t of result.tokens) {
      const scopes = t.scopes.join(" ");
      if (scopes.includes("entity.name.tag.html")) seen.html = true;
      if (scopes.includes("meta.property-list.css")) seen.css = true;
      if (scopes.includes("keyword.other.DML.sql")) seen.sql = true;
      if (scopes.includes("meta.structure.dictionary.json")) seen.json = true;
      if (line.includes("plain raw string") && scopes === "source.go string.quoted.raw.go") {
        seen.plainRawStringLines++;
      }
    }
    ruleStack = result.ruleStack;
  }

  let ok = true;
  for (const [lang, want] of Object.entries({ html: seen.html, css: seen.css, sql: seen.sql, json: seen.json })) {
    console.log(`${want ? "PASS" : "FAIL"} @highlight: ${lang} embeds ${lang}'s grammar`);
    if (!want) ok = false;
  }
  const plainOk = seen.plainRawStringLines > 0;
  console.log(`${plainOk ? "PASS" : "FAIL"} unannotated raw string keeps plain Go string coloring`);
  if (!plainOk) ok = false;

  process.exit(ok ? 0 : 1);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
