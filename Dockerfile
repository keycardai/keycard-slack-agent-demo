# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26.10

FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates git build-essential libsqlite3-dev && \
    rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOFLAGS="-tags=sqlite_fts5" go build -o /bin/slack-agent .

FROM debian:bookworm-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates libsqlite3-0 sqlite3 && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /bin/slack-agent /usr/local/bin/slack-agent

ENV PORT=8080
EXPOSE 8080

CMD ["/usr/local/bin/slack-agent"]
