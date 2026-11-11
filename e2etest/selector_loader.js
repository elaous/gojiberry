// Loads the selector helper (the Go program in ./selector, built to wasm) into a page and
// installs window.__e2e. wasm_exec.js is run here with the page's own Go class set aside
// and put back, so a page that runs a wasm program of its own is not disturbed; the
// module is small enough to be compiled and started synchronously, so window.__e2e is
// there before any script of the page runs.
(() => {
  if (window.__e2e) return;
  const had = 'Go' in globalThis, prev = globalThis.Go;
  // The WASM_EXEC marker below is replaced by the toolchain's own lib/wasm/wasm_exec.js
  // (Go >= 1.24, where it lives under lib/wasm; see selector.go). Do not write the marker
  // itself in a comment: only its first occurrence is replaced.
  /*WASM_EXEC*/
  const G = globalThis.Go;
  if (had) globalThis.Go = prev; else delete globalThis.Go;
  const bytes = Uint8Array.from(atob("/*WASM_B64*/"), (c) => c.charCodeAt(0));
  const go = new G();
  go.run(new WebAssembly.Instance(new WebAssembly.Module(bytes), go.importObject));
  const g = window.__e2eGo;
  delete window.__e2eGo;
  // A Go function cannot throw into the page: it returns the error as a string.
  const unwrap = (f) => (...a) => { const r = f(...a); if (typeof r === 'string') throw new Error(r); return r; };
  window.__e2e = { all: unwrap(g.all), first: unwrap(g.first), centre: unwrap(g.centre), visible: g.visible, norm: g.norm };
})();
