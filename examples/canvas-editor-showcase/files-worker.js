// The web worker of the showcase's file formats: it runs files.wasm (see files/main.go). Messages that arrive
// before the program is up are kept and handed to it when it starts.
importScripts("wasm_exec.js");
const queue = [];
self.onmessage = (e) => queue.push(e.data);
self.__filesStart = (handler) => {
  self.onmessage = (e) => handler(e.data);
  for (const m of queue.splice(0)) handler(m);
};
const go = new Go();
WebAssembly.instantiateStreaming(fetch("files.wasm"), go.importObject).then((r) => go.run(r.instance));
