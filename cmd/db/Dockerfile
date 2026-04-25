# docker.io/nvoi/db — the uniform database backup AND restore runner.
#
# Every DatabaseProvider's backup CronJob runs this image in MODE=backup
# (default). `nvoi database restore` and `nvoi database migrate` launch
# one-shot Jobs with MODE=restore. Same image, two pipelines — dispatches
# on ENGINE to pick between pg_dump/psql (postgres/neon) and
# mysqldump/mysql (mysql/planetscale); gzips dumps going out, gunzips
# coming back; uses the nvoi SigV4 client for S3-compatible I/O (no
# aws-cli dep).
#
# Multi-stage because the Go binary's compile-time deps are far heavier
# than the runtime image needs. Alpine for runtime keeps the image
# under 150 MB even with both postgres-client + mysql-client installed.
#
# Tag discipline: published on every `v*` git tag by
# .github/workflows/release.yml; the nvoi CLI pins
# `docker.io/nvoi/db:<utils.Version>` so the image version and the
# binary version always match.

ARG GO_VERSION=1.23
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG NVOI_VERSION=main
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X github.com/getnvoi/nvoi/pkg/utils.Version=${NVOI_VERSION}" \
    -o /out/nvoi-db ./cmd/db

FROM alpine:3.19
RUN apk add --no-cache \
      ca-certificates \
      postgresql16-client \
      mysql-client
COPY --from=build /out/nvoi-db /usr/local/bin/nvoi-db
# No shell entrypoint — the Go binary handles env validation, dispatch,
# dump, restore, and upload. Direct exec means failures propagate as
# the binary's exit code, which is what k8s' BackoffLimit keys on.
ENTRYPOINT ["/usr/local/bin/nvoi-db"]
