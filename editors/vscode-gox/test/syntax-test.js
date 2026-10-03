// (A .txt on purpose: goxc transpiles every *.gox it finds, and this file imports
// things that do not exist.)
// Checks the newer .gox syntax against fixtures/new-syntax.gox.txt with the
// real grammars: resource imports (`alias.(Type) "x.json"`, blank and
// named CSS imports), generic component tags (`<components.List[Item]>`),
// bare boolean attributes (`lockH`, `none`), multi-line opening tags, and
// that text between tags and tight comparisons are not mistaken for either.
const fs = require("fs");
const path = require("path");
const { loadGox, tokensOf } = require("./lib.js");

let failed = false;
function check(name, ok, detail) {
  console.log(`${ok ? "PASS" : "FAIL"} ${name}${ok ? "" : " -- " + (detail || "")}`);
  if (!ok) failed = true;
}

loadGox().then((grammar) => {
  const src = fs.readFileSync(path.join(__dirname, "fixtures", "new-syntax.gox.txt"), "utf8");
  const toks = tokensOf(grammar, src);
  const find = (text, scope, nth = 0) => toks.filter((t) => t.text === text && (!scope || t.scopes.includes(scope)))[nth];
  const has = (text, scope) => !!find(text, scope);

  // --- resource imports ---
  check("typed JSON import: path is a resource path", has("config", "entity.name.import.resource.gox") && has(".json", "constant.other.extension.gox"));
  check("typed JSON import: annotation type is a type", has("SiteConfig", "entity.name.type.go"));
  check("typed JSON import: alias is an import name", has("config", "variable.other.import.go"));
  check("blank CSS import: _ then a resource path", has("_", "variable.other.import.go") && has("github.com/elaous/gojiberry/components/button", "entity.name.import.resource.gox") && has(".css", "constant.other.extension.gox"));
  check("named CSS import with a relative path", has("theme", "variable.other.import.go") && has("./theme/dark", "entity.name.import.resource.gox"));
  check("JSONL import", has(".jsonl", "constant.other.extension.gox"));
  check("an ordinary Go import stays an ordinary import", has("net/http", "entity.name.import.go") && !has("net/http", "meta.import.resource.gox"));
  check("a package path ending in a Go-ish name is not a resource", has("github.com/elaous/gojiberry/canvas/gox", "entity.name.import.go"));

  // --- generic component tags ---
  check("generic tag: name", has("components.List", "entity.name.tag.gox"));
  check("generic tag: type argument is a type", has("Person", "meta.type-arguments.gox") && has("Person", "entity.name.type.go"));
  check("generic tag: brackets", has("[", "punctuation.definition.typeparameters.gox") && has("]", "punctuation.definition.typeparameters.gox"));

  // --- attributes ---
  check("multi-line opening tag: next-line attribute is an attribute", has("items", "meta.tag.attributes.gox") && has("items", "entity.other.attribute-name.gox"));
  check("attribute with {expr} value", has("width", "entity.other.attribute-name.gox"));
  check("bare boolean attributes", has("lockH", "entity.other.attribute-name.boolean.gox") && has("wrap", "entity.other.attribute-name.boolean.gox") && has("none", "entity.other.attribute-name.boolean.gox"));
  check("function prop is Go inside the attribute", has("func", "keyword.function.go") && find("func", "meta.tag.attributes.gox") !== undefined);
  check("a tag inside a function prop is a tag", has("canvas.Header", "entity.name.tag.gox") && has("canvas.PageNumber", "entity.name.tag.gox"));
  check("fragment inside a function prop", has("<", "meta.tag.fragment.gox"));
  const kid = find("canvas.TextFlow", "entity.name.tag.gox");
  check("a child tag is not inside its parent's attribute region", kid && !kid.scopes.includes("meta.tag.attributes.gox"), kid && kid.scopes);
  const prose = toks.find((t) => t.text.startsWith("plain words here"));
  check("text between tags is plain, not an attribute", prose && !prose.scopes.includes("attribute") && !prose.scopes.includes("source.go keyword"), prose && prose.scopes);
  const section = find("section", "entity.name.tag.gox");
  check("a simple <section> still highlights", !!section);

  // --- Go that only looks like a tag ---
  const cmpLine = src.split("\n").findIndex((l) => l.includes("x<y[0]"));
  const after = toks.slice(toks.findIndex((t) => t.text === "cmp"));
  check("tight comparison x<y[0] is not a tag", !after.some((t) => t.scopes.includes("entity.name.tag.gox")) && cmpLine > 0);
  check("code after it keeps highlighting", after.some((t) => t.text === "bool" && t.scopes.includes("entity.name.type.go") || t.text === "return" && t.scopes.includes("keyword.control.go")));

  process.exit(failed ? 1 : 0);
});
