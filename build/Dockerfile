ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG LDFLAGS
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags "${LDFLAGS}" -o /out/dtworkflow ./cmd/dtworkflow

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/dtworkflow /usr/local/bin/

RUN useradd --uid 1001 --create-home dtworkflow
USER dtworkflow

ENTRYPOINT ["dtworkflow"]
