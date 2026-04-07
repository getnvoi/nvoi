# -- CSS build ────────────────────────────────────────────────────────
FROM node:22-alpine AS css
WORKDIR /build
COPY cmd/web/package.json cmd/web/package-lock.json ./
RUN npm ci
COPY cmd/web/build.mjs build.mjs
COPY cmd/web/static static
COPY cmd/web/templates templates
RUN node build.mjs

# -- Go build ─────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS go
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=css /build/static/css static/css
COPY --from=css /build/static/manifest.json static/manifest.json
RUN CGO_ENABLED=0 go build -o /nvoi-web ./cmd/web

# -- Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app

# findRoot() walks up looking for go.mod -> root = /app
# then resolves cmd/web/templates, cmd/web/content, cmd/web/static
COPY go.mod .
COPY --from=go /nvoi-web .
COPY --from=css /build/static cmd/web/static
COPY cmd/web/templates cmd/web/templates
COPY cmd/web/content cmd/web/content

EXPOSE 3000
CMD ["./nvoi-web"]
