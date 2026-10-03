// Prints each token's text and scopes for a .gox file (debugging aid):
//   node dump.js fixtures/new-syntax.gox.txt
const fs = require("fs");
const path = require("path");
const { loadGox } = require("./lib.js");

loadGox().then((grammar) => {
  const lines = fs.readFileSync(process.argv[2], "utf8").split("\n");
  let stack = require("vscode-textmate").INITIAL;
  lines.forEach((line, i) => {
    const r = grammar.tokenizeLine(line, stack);
    console.log(`--- ${i + 1}: ${line}`);
    for (const t of r.tokens) {
      const text = line.slice(t.startIndex, t.endIndex);
      if (text.trim()) console.log(`  ${JSON.stringify(text)}  ${t.scopes.slice(1).join(" ")}`);
    }
    stack = r.ruleStack;
  });
});
