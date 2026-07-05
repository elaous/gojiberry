importScripts('wasm_exec.js');
const go = new Go();
WebAssembly.instantiateStreaming(fetch('worker.wasm'), go.importObject).then((result) => {
  go.run(result.instance);
});
