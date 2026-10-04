# syntax=docker/dockerfile:1

# ---- Compilation (100 % Go, SQLite embarqué : pas de compilateur C) ----
FROM golang:1.24-alpine AS build
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./... && \
    go build -trimpath -ldflags="-s -w" -o /out/candidatures .

# ---- Image finale ----
FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/candidatures /usr/local/bin/candidatures
ENV ADDR=:8080 \
    DATA_DIR=/data \
    TZ=Europe/Paris
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["candidatures", "healthcheck"]
ENTRYPOINT ["candidatures"]
