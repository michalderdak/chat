# gRPC Chat Demo

A gRPC demo application with a Go terminal client, Python async server, and Qwen3 LLM — deployed on Kind with Envoy proxy.

## Architecture

```
Go TUI Client (Bubble Tea)
  |
  +-- gRPC (plaintext) --> chat namespace (3 replicas, bearer token auth)
  +-- gRPC (mTLS)      --> Envoy proxy (round-robin LB, mTLS, stream management)
                              |
                              +-- headless service discovery --> chat-server pods
                              |
Python gRPC Server (grpc.aio)
  |
  +-- Ollama (Qwen3 0.6B, host Mac, Metal-accelerated)
  +-- Redis (conversation history, protobuf serialization)
```

## Features

- **Bidirectional streaming** with cancel, context injection, and `oneof` message multiplexing
- **Unary RPCs** with pod name in trailing metadata (round-robin demo)
- **Unified TUI** — 4 tabs (gRPC Unary / Stream, Envoy Unary / Stream) sharing 2 HTTP/2 connections
- **Split-screen** — chat on left, gRPC event log on right
- **Interceptors** — logging, auth, OpenTelemetry, Prometheus
- **gRPC Gateway** — HTTP/JSON transcoding from the same proto
- **Envoy proxy** — mTLS, per-RPC load balancing, headless service discovery
- **Graceful shutdown** — SIGTERM drain, Redis history persistence, client auto-reconnect
- **Observability** — Prometheus, Jaeger, OTEL Collector

## Prerequisites

- Docker Desktop
- Kind
- Go 1.24+
- Ollama with `qwen3:0.6b` (`ollama pull qwen3:0.6b`)
- `buf` (`brew install bufbuild/buf/buf`)

## Quick Start

```bash
# Start Ollama (must bind to all interfaces for Kind access)
OLLAMA_HOST=0.0.0.0 ollama serve

# Create cluster and deploy everything
make cluster
make deploy-all

# Launch the unified TUI
make client
```

## Make Targets

```
make generate        # Generate proto stubs via buf (remote plugins)
make docker          # Generate proto stubs via Docker
make lint            # buf lint
make breaking        # buf breaking change detection
make clean           # Remove generated files

make cluster         # Create Kind cluster
make build           # Build server and gateway Docker images
make load            # Build and load images into Kind
make certs           # Generate TLS certificates for mTLS
make deploy-chat     # Build, load, deploy chat services + Envoy
make deploy-observability  # Deploy Prometheus, Jaeger, OTEL Collector
make deploy-all      # Deploy everything (observability + chat)
make client          # Unified TUI (Tab to switch modes)

make grpcurl-list    # Service discovery via reflection
make grpcurl-health  # Health check
make grpcurl-send    # Unary RPC via grpcurl
make curl-send       # HTTP/JSON via gateway

make logs            # Server logs (chat namespace)
make logs-envoy-proxy  # Envoy proxy logs
make cluster-clean   # Delete Kind cluster
```

## Project Structure

```
proto/chat/v1/chat.proto    — Service definition (SendMessage + Chat bidi)
client/                     — Go Bubble Tea TUI with unified 4-tab model
server/                     — Python async gRPC server with interceptors
gateway/                    — gRPC-Gateway HTTP/JSON reverse proxy
deploy/base/                — K8s base manifests (server, gateway, Redis, PDB)
deploy/chat/                — chat namespace (server, gateway, Envoy mTLS + headless LB, certs)
deploy/observability/       — Prometheus, Jaeger, OTEL Collector
```
