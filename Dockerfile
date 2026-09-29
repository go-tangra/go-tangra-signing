# syntax=docker/dockerfile:1
# go-tangra-signing (document signing service, go-tangra v4) - standalone image.
# Build context: the repository root.
#
#   docker buildx build --secret id=npm_token,env=NODE_AUTH_TOKEN \
#     --build-arg APP_VERSION=4.0.0 --build-arg VCS_REF=$(git rev-parse HEAD) -t go-tangra-signing:dev .
#
# npm_token is a GitHub token with read:packages for @go-tangra/ui on npm.pkg.github.com.
# It is mounted only for the npm ci step and written to a tmpfs, so it never lands in a layer.

FROM node:22-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN --mount=type=secret,id=npm_token,required=true \
    --mount=type=tmpfs,target=/run/npmrc \
    --mount=type=cache,target=/root/.npm \
    set -eu; \
    printf '@go-tangra:registry=https://npm.pkg.github.com\n//npm.pkg.github.com/:_authToken=%s\nignore-scripts=true\nfund=false\naudit=false\n' \
      "$(cat /run/secrets/npm_token)" > /run/npmrc/.npmrc; \
    NPM_CONFIG_USERCONFIG=/run/npmrc/.npmrc npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
# GOWORK=off: service repositories never use a go.work.
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false GOWORK=off
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=ui /src/ui/dist ./ui/dist
ARG APP_VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -tags "ui" -ldflags "-s -w -X main.version=${APP_VERSION}" -o /out/signingsvc ./cmd/signingsvc

FROM alpine:3.20
ARG APP_VERSION=dev
ARG VCS_REF=unknown
LABEL org.opencontainers.image.source="https://github.com/go-tangra/go-tangra-signing" \
      org.opencontainers.image.title="go-tangra-signing" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}"
# tzdata: dates on signatures and the audit trail are shown in local time zones.
# The PDF fonts (DejaVu) are embedded in the binary.
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
COPY --from=build /out/signingsvc /usr/local/bin/
COPY deploy /app/deploy
WORKDIR /app
USER app
ENTRYPOINT ["signingsvc"]
CMD ["-config","deploy/container.yaml"]
