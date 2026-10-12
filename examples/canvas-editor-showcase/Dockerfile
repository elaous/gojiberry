# The canvas editor showcase, built to wasm and served by nginx. Build from the module root:
#   docker build -f examples/canvas-editor-showcase/Dockerfile .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
WORKDIR /src/examples/canvas-editor-showcase
RUN GOOS=js GOARCH=wasm go build -o /site/main.wasm . \
 && cp index.html /site/index.html \
 && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" /site/wasm_exec.js

FROM nginx:1.27-alpine
RUN printf 'server {\n  listen 80;\n  root /usr/share/nginx/html;\n  gzip on; gzip_types application/wasm application/javascript text/css;\n  types { application/wasm wasm; }\n  location / { try_files $uri /index.html; }\n}\n' > /etc/nginx/conf.d/default.conf
COPY --from=build /site /usr/share/nginx/html
