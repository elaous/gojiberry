// Downloads VS Code's own bundled HTML/CSS/SQL/JSON grammars so
// highlight-test.js can verify the language= injection actually
// embeds each one, without redistributing Microsoft's files in this
// repo. Re-run this if any of them go missing.
const fs = require("fs");
const https = require("https");

const GRAMMARS = {
  "html.tmLanguage.json": "https://raw.githubusercontent.com/microsoft/vscode/main/extensions/html/syntaxes/html.tmLanguage.json",
  "css.tmLanguage.json": "https://raw.githubusercontent.com/microsoft/vscode/main/extensions/css/syntaxes/css.tmLanguage.json",
  "sql.tmLanguage.json": "https://raw.githubusercontent.com/microsoft/vscode/main/extensions/sql/syntaxes/sql.tmLanguage.json",
  "json.tmLanguage.json": "https://raw.githubusercontent.com/microsoft/vscode/main/extensions/json/syntaxes/JSON.tmLanguage.json",
};

function fetch(name, url) {
  return new Promise((resolve, reject) => {
    https.get(url, (res) => {
      if (res.statusCode !== 200) {
        reject(new Error(`fetch ${name} failed: HTTP ${res.statusCode}`));
        return;
      }
      const chunks = [];
      res.on("data", (c) => chunks.push(c));
      res.on("end", () => {
        fs.writeFileSync(__dirname + "/" + name, Buffer.concat(chunks));
        console.log(`wrote ${name}`);
        resolve();
      });
    }).on("error", reject);
  });
}

Promise.all(Object.entries(GRAMMARS).map(([name, url]) => fetch(name, url))).catch((err) => {
  console.error(err);
  process.exit(1);
});
