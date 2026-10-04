// Regression test for a real bug: a plain comment mentioning a bare
// <script>-shaped sequence (no closing tag anywhere nearby) used to be
// misread by the tag injection grammar as an unterminated tag-open,
// leaving the tokenizer stuck inside a fake meta.tag.xg scope for the
// rest of the file -- silently breaking ordinary Go keyword
// highlighting (func, identifiers, types), not just tag highlighting,
// for every function after the comment. Fixed by excluding comment
// scopes from the injection (injectionSelector:
// "L:source.xg - comment" in ../syntaxes/xango-tags.tmLanguage.json).
const fs = require("fs");
const path = require("path");
const oniguruma = require("vscode-oniguruma");
const vsctm = require("vscode-textmate");

const SYNTAXES_DIR = path.join(__dirname, "..", "syntaxes");
const GO_GRAMMAR_PATH = path.join(__dirname, "go.tmLanguage.json");
const FIXTURE_PATH = path.join(__dirname, "fixtures", "tag-mention-in-comment.xg");

async function main() {
  if (!fs.existsSync(GO_GRAMMAR_PATH)) {
    console.error(`${path.basename(GO_GRAMMAR_PATH)} missing -- run \`npm run fetch-go-grammar\` first`);
    process.exit(1);
  }

  const wasmPath = path.join(require.resolve("vscode-oniguruma"), "..", "onig.wasm");
  await oniguruma.loadWASM(fs.readFileSync(wasmPath).buffer);
  const onigLib = {
    createOnigScanner: (patterns) => new oniguruma.OnigScanner(patterns),
    createOnigString: (s) => new oniguruma.OnigString(s),
  };

  const grammars = {
    "source.go": JSON.parse(fs.readFileSync(GO_GRAMMAR_PATH, "utf8")),
    "source.xg": JSON.parse(fs.readFileSync(path.join(SYNTAXES_DIR, "xango.tmLanguage.json"), "utf8")),
    "xango.tags.injection": JSON.parse(fs.readFileSync(path.join(SYNTAXES_DIR, "xango-tags.tmLanguage.json"), "utf8")),
  };
  const registry = new vsctm.Registry({
    onigLib: Promise.resolve(onigLib),
    loadGrammar: async (scopeName) => grammars[scopeName] || null,
    getInjections: (scopeName) => (scopeName === "source.xg" ? ["xango.tags.injection"] : undefined),
  });

  const grammar = await registry.loadGrammar("source.xg");
  const lines = fs.readFileSync(FIXTURE_PATH, "utf8").split("\n");

  let ruleStack = vsctm.INITIAL;
  let commentLineTagged = false;
  let sawFuncBAsGoKeyword = false;
  let sawSecondTag = false;
  for (const line of lines) {
    const result = grammar.tokenizeLine(line, ruleStack);
    for (const t of result.tokens) {
      const scopes = t.scopes.join(" ");
      const text = line.slice(t.startIndex, t.endIndex);
      if (line.includes("doesn't write either <script>") && scopes.includes("entity.name.tag.xg")) {
        commentLineTagged = true;
      }
      if (text === "B" && scopes.includes("entity.name.function.go")) {
        sawFuncBAsGoKeyword = true;
      }
      if (text === "div" && scopes.includes("entity.name.tag.xg") && line.includes("<div>y</div>")) {
        sawSecondTag = true;
      }
    }
    ruleStack = result.ruleStack;
  }

  let ok = true;
  console.log(`${!commentLineTagged ? "PASS" : "FAIL"} bare <script> mention inside a comment is not treated as a tag-open`);
  if (commentLineTagged) ok = false;
  console.log(`${sawFuncBAsGoKeyword ? "PASS" : "FAIL"} func B's own name still gets ordinary Go highlighting after the comment`);
  if (!sawFuncBAsGoKeyword) ok = false;
  console.log(`${sawSecondTag ? "PASS" : "FAIL"} the second function's real <div> tag still highlights as a tag`);
  if (!sawSecondTag) ok = false;

  process.exit(ok ? 0 : 1);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
