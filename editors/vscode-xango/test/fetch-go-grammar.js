// Downloads VS Code's own bundled Go TextMate grammar so tokenize.js can
// load a real source.go to include, without redistributing Microsoft's
// file in this repo. Re-run this if go.tmLanguage.json goes missing.
const fs = require("fs");
const https = require("https");

const URL =
  "https://raw.githubusercontent.com/microsoft/vscode/main/extensions/go/syntaxes/go.tmLanguage.json";
const dest = __dirname + "/go.tmLanguage.json";

https.get(URL, (res) => {
  if (res.statusCode !== 200) {
    console.error(`fetch failed: HTTP ${res.statusCode}`);
    process.exit(1);
  }
  const chunks = [];
  res.on("data", (c) => chunks.push(c));
  res.on("end", () => {
    fs.writeFileSync(dest, Buffer.concat(chunks));
    console.log(`wrote ${dest}`);
  });
}).on("error", (err) => {
  console.error(err);
  process.exit(1);
});
