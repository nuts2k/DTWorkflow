ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG LDFLAGS
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -ldflags "${LDFLAGS}" -o /out/dtworkflow ./cmd/dtworkflow

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/dtworkflow /usr/local/bin/

WORKDIR /app
RUN useradd --uid 1001 --create-home dtworkflow && \
    chown dtworkflow:dtworkflow /app && \
    # 添加 docker 组，使 dtworkflow 用户可访问挂载的 docker.sock
    groupadd -g 999 docker && usermod -aG docker dtworkflow

EXPOSE 8080

USER dtworkflow

ENTRYPOINT ["dtworkflow"]
CMD ["serve"]
