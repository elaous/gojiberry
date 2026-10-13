# The canvas editor showcase, built to wasm, precompressed and served by nginx. Build from the module root:
#   docker build -f examples/canvas-editor-showcase/Dockerfile .
# docs/deploy.md explains the settings in nginx.conf and the other ways to serve the same directory.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN cd examples/canvas-editor-showcase \
 && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o /site/main.wasm . \
 && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o /site/files.wasm ./files \
 && cp index.html files-worker.js /site/ \
 && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" /site/wasm_exec.js
# A .gz beside each text-like file: nginx's gzip_static sends it as it is (about 6.9 MB for the wasm)
# instead of compressing 27 MB again on every request. For .br files as well, install the brotli tool
# here (apk add brotli) and use an nginx built with the brotli module.
RUN go run ./cmd/wasmdist -brotli off /site

FROM nginx:1.27-alpine
COPY examples/canvas-editor-showcase/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /site /usr/share/nginx/html
