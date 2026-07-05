// A classic (non-module) service worker script has no DOM to attach a
// <script> tag to, so wasm_exec.js is loaded via importScripts instead;
// everything else is identical to loading it on a normal page.
importScripts("wasm_exec.js");

const go = new Go();
WebAssembly.instantiateStreaming(fetch("sw.wasm"), go.importObject).then((result) => {
  go.run(result.instance);
});
