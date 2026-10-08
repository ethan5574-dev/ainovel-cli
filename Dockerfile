FROM --platform=$BUILDPLATFORM node:24-alpine AS webui-builder

WORKDIR /src/internal/web/webui-src

COPY internal/web/webui-src/package.json internal/web/webui-src/package-lock.json ./
RUN npm ci

COPY internal/web/webui-src/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

WORKDIR /src

ENV CGO_ENABLED=0 GOWORK=off

ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=webui-builder /src/internal/web/webui/ ./internal/web/webui/

RUN GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" \
    -o /out/ainovel-cli \
    ./cmd/ainovel-cli

RUN GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" \
    -o /out/ainovel-worker \
    ./cmd/ainovel-worker

FROM alpine:3.22

RUN apk add --no-cache \
    ca-certificates \
    tzdata

WORKDIR /workspace
ENV AINOVEL_HOME=/root/.ainovel

COPY --from=builder /out/ainovel-cli /usr/local/bin/ainovel-cli
COPY --from=builder /out/ainovel-worker /usr/local/bin/ainovel-worker

# Used by `ainovel-cli web --addr 0.0.0.0:5173`; CLI remains the default entry point.
EXPOSE 5173

ENTRYPOINT ["ainovel-cli"]
