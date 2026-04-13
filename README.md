# gRPC Chat Demo

A gRPC demo application with a Go terminal client, Python async server, and Qwen3 LLM — deployed on Kind with Envoy proxy.

## Architecture

```
Go TUI Client (Bubble Tea)
  |
  +-- gRPC (plaintext) --> chat-grpc namespace (3 replicas, bearer token auth)
  +-- gRPC (mTLS)      --> chat-envoy namespace (3 replicas, Envoy proxy)
                              |
                              +-- Round-robin LB, mTLS, stream management
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
- Go 1.23+
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
make docker          # Generate proto stubs via Docker
make lint            # buf lint
make breaking        # buf breaking change detection

make cluster         # Create Kind cluster
make deploy-all      # Build, load, deploy everything
make client          # Unified TUI (Tab to switch modes)

make grpcurl-list    # Service discovery via reflection
make grpcurl-health  # Health check
make curl-send       # HTTP/JSON via gateway

make logs-grpc       # Server logs (chat-grpc namespace)
make logs-envoy      # Server logs (chat-envoy namespace)
make cluster-clean   # Delete Kind cluster
```

## Presentation

```bash
brew install slides
slides presentation/slides.md
```

## Project Structure

```
proto/chat/v1/chat.proto    — Service definition (SendMessage + Chat bidi)
client/                     — Go Bubble Tea TUI with unified 4-tab model
server/                     — Python async gRPC server with interceptors
gateway/                    — gRPC-Gateway HTTP/JSON reverse proxy
deploy/base/                — K8s base manifests (server, gateway, Redis, PDB)
deploy/grpc/                — chat-grpc namespace (bearer token auth)
deploy/envoy/               — chat-envoy namespace (Envoy mTLS + headless LB)
deploy/observability/       — Prometheus, Jaeger, OTEL Collector
presentation/slides.md      — Terminal slide deck (slides CLI)
```
