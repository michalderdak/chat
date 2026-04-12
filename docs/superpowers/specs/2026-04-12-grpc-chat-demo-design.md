# gRPC Chat Demo — Design Spec

## Overview

A consultant demo project showcasing gRPC and its ecosystem through a working chat application. A Go terminal client (Bubble Tea) communicates with a Python gRPC server via bidirectional streaming. The server calls Ollama (Qwen3-0.6B) running on the host Mac for LLM inference. Deployed on a local Kind cluster with two namespaces: pure gRPC and Envoy-proxied — demonstrating the contrast between application-level and proxy-level concerns.

Accompanied by a slide deck presentation covering gRPC fundamentals through production patterns.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Host Mac (M2)                                                      │
│                                                                     │
│  ┌──────────────┐         ┌─────────────────────────────────────┐   │
│  │ Go Bubble Tea│  gRPC   │  Kind Cluster                       │   │
│  │ Client (TUI) │────────►│                                     │   │
│  │              │  bidi   │  ┌─────────────┐  ┌──────────────┐  │   │
│  └──────────────┘  stream │  │ chat-grpc   │  │ chat-envoy   │  │   │
│         │                 │  │             │  │              │  │   │
│         │ curl/           │  │ Gateway     │  │ Envoy Proxy  │  │   │
│         │ grpcurl         │  │ (HTTP/JSON) │  │ (mTLS, LB)   │  │   │
│         │                 │  │      │      │  │      │       │  │   │
│         │                 │  │ Server x3   │  │ Server x3    │  │   │
│         │                 │  │ (Python)    │  │ (Python)     │  │   │
│         │                 │  └──────┬──────┘  └──────┬───────┘  │   │
│         │                 │         │                │          │   │
│         │                 │  ┌──────────────────────────────┐   │   │
│         │                 │  │ observability                │   │   │
│         │                 │  │ Prometheus / Jaeger / OTEL   │   │   │
│         │                 │  └──────────────────────────────┘   │   │
│         │                 └─────────────────┬───────────────────┘   │
│         │                                   │ host.docker.internal  │
│         │                    ┌───────────────┴──────┐               │
│         │                    │ Ollama (Qwen3-0.6B)  │               │
│         │                    │ Metal-accelerated     │               │
│         │                    └──────────────────────┘               │
└─────────────────────────────────────────────────────────────────────┘
```

## Proto Definition & Buf Setup

### Proto structure

```
proto/
├── buf.yaml              # Module config, default lint + breaking rules
└── chat/
    └── v1/
        └── chat.proto    # Single service definition
```

### ChatService

Two RPCs:

- `SendMessage` (unary) — simple request/response. Exists for gRPC-Gateway HTTP/JSON transcoding demo. Has `google.api.http` annotation for REST mapping (`POST /v1/chat/send`).
- `Chat` (bidirectional streaming) — the core of the demo. Both sides read and write independently on the same connection.

### Message design

Uses `oneof` for multiplexing message types on the bidi stream:

**Client → Server (`ClientMessage`):**
- `UserMessage` — text from the user
- `CancelGeneration` — stop current generation
- `ContextInjection` — add info mid-stream

**Server → Client (`ServerEvent`):**
- `Token` — streamed token text
- `StatusUpdate` — generation phase (thinking, generating, done)
- `Error` — error with gRPC status code
- `Heartbeat` — keep-alive for long-lived streams
- `Acknowledgement` — explicit ack of client signals

`oneof` chosen over separate RPCs because it preserves message ordering on the stream — a cancel and a token are ordered relative to each other. Separate RPCs would be separate HTTP/2 streams with no ordering guarantee between them.

### Buf configuration

- `buf.gen.yaml` at repo root generates:
  - Go stubs (protoc-gen-go, protoc-gen-go-grpc, protoc-gen-grpc-gateway, protoc-gen-openapiv2) into `gen/go/`
  - Python stubs (protoc-gen-python, protoc-gen-grpc-python) into `gen/python/`
- `buf lint` — default rules (Google API design guide)
- `buf breaking --against .git#branch=main` — breaking change detection against main branch

## Python gRPC Server

### Structure

```
server/
├── pyproject.toml          # uv, dependencies
├── Dockerfile
└── src/
    └── chat_server/
        ├── __init__.py
        ├── main.py         # Server entrypoint
        ├── service.py      # ChatService implementation
        ├── ollama.py       # Ollama streaming HTTP client
        ├── config.py       # Settings
        └── interceptors/
            ├── __init__.py
            ├── auth.py
            ├── logging.py
            ├── otel.py
            └── prometheus.py
```

### Core behavior

- Uses `grpc.aio` (async gRPC) for natural streaming + async HTTP
- `Chat` bidi handler: reads client messages in a loop. For `UserMessage`, calls Ollama streaming endpoint, yields `Token` events. Listens for `CancelGeneration` to abort via asyncio cancellation.
- `SendMessage` unary handler: simple wrapper for gateway transcoding demo
- Server reflection enabled for `grpcurl` discovery
- gRPC health checking protocol for Kubernetes probes
- Imports generated stubs from `gen/python/`

### Interceptors (stacking order)

```
Logging → Auth → OpenTelemetry → Prometheus → Handler
```

1. **Logging** — structured JSON (structlog). Logs RPC method, duration, status code. For streaming: stream open/close, message counts. Outermost to capture everything including auth failures.
2. **Auth** — extracts `authorization` bearer token from metadata. Returns `UNAUTHENTICATED` on failure. Disabled in Envoy namespace (proxy handles mTLS).
3. **OpenTelemetry** — wraps each RPC in a span, propagates trace context from gRPC metadata. Exports OTLP to collector.
4. **Prometheus** — RPC counts by method/status, latency histograms, active stream gauge. Exposes `/metrics` on a side HTTP port.

### Ollama integration

- `httpx` async client calling `http://host.docker.internal:11434/api/chat` with `stream: true`
- Reads NDJSON lines, extracts token text, yields through gRPC stream
- Translates Ollama errors to gRPC status codes

### Dependency management

- `uv` for dependency management
- `pyproject.toml` as single config
- Dockerfile uses `uv sync --frozen` for reproducible builds

## Go Bubble Tea Client

### Structure

```
client/
├── go.mod
├── go.sum
├── main.go              # Entrypoint, config flags
├── tui/
│   ├── model.go         # Bubble Tea model
│   ├── commands.go      # Bubble Tea commands (connect, send, receive)
│   └── styles.go        # Lipgloss styling
└── grpc/
    ├── client.go        # Connection setup, dial options
    └── stream.go        # Bidi stream wrapper
```

### TUI layout

- Chat history area (scrollable) — user messages and streamed AI responses with tokens appearing in real-time
- Input field at bottom (Bubbles text input component)
- Status bar — connection state, target server, active stream info
- Keybindings: `Enter` send, `Ctrl+C` cancel generation, `Esc` quit

### gRPC client behavior

- Config-driven target (`--target localhost:50051` for pure gRPC, `--target localhost:50052` for Envoy)
- Opens `Chat` bidi stream on first message
- Two goroutines per stream: one reading `ServerEvent`s → Bubble Tea messages, one sending `ClientMessage`s from user input
- `Ctrl+C` mid-generation sends `CancelGeneration` on the stream
- `--token` flag for bearer auth, `--tls` toggle

### Client-side interceptors

- Auth interceptor: attaches bearer token to outgoing metadata
- Logging interceptor: RPC lifecycle to debug log (not TUI)

### Context management

- Configurable per-RPC deadline (`--timeout` flag) to demo timeout behavior
- `Ctrl+C` propagates through gRPC context cancellation

## gRPC Gateway

### Structure

```
gateway/
├── go.mod
├── go.sum
├── main.go
└── Dockerfile
```

### Behavior

- Standalone Go service using `grpc-gateway` runtime library
- `POST /v1/chat/send` → transcodes to `SendMessage` unary RPC on the Python server
- gRPC-Web handler wrapping the gateway mux for browser access
- Serves OpenAPI/Swagger JSON generated by `buf` (protoc-gen-openapiv2)
- Does NOT support bidi streaming — this is a deliberate limitation that demonstrates why native gRPC is needed for full streaming

### Demo value

Three ways to reach the same backend from the same proto:
- `grpcurl` — native gRPC
- `curl` — HTTP/JSON via gateway
- Browser — gRPC-Web via gateway

## Kubernetes Deployment

### Kind cluster

```
deploy/
├── kind-config.yaml         # Extra port mappings
├── base/
│   ├── server-deployment.yaml    # 3 replicas
│   ├── server-service.yaml
│   ├── gateway-deployment.yaml
│   └── gateway-service.yaml
├── grpc/
│   ├── namespace.yaml           # chat-grpc
│   └── kustomization.yaml       # Auth interceptor enabled
├── envoy/
│   ├── namespace.yaml           # chat-envoy
│   ├── kustomization.yaml       # Auth interceptor disabled
│   ├── envoy-configmap.yaml     # Full Envoy config
│   ├── envoy-deployment.yaml
│   ├── envoy-service.yaml
│   └── certs/
│       └── generate-certs.sh    # Self-signed CA + certs
└── observability/
    ├── namespace.yaml           # observability
    ├── prometheus/
    ├── jaeger/
    └── otel-collector/
```

### Two-namespace comparison

| Concern | `chat-grpc` | `chat-envoy` |
|---|---|---|
| Auth | Bearer token interceptor | Envoy mTLS termination |
| Load balancing | gRPC default pick-first | Envoy per-RPC round-robin |
| Stream management | App-level timeouts | Envoy `max_stream_duration`, keep-alive, idle timeout |
| Observability | App interceptors only | App interceptors + Envoy access logs and stats |

### Server replicas

3 replicas in both namespaces to demonstrate load balancing contrast:
- `chat-grpc`: pick-first sends all requests to one pod (visible in logs)
- `chat-envoy`: Envoy round-robins across all 3 pods per-RPC

### External access

- Ollama: `ExternalName` or headless service pointing to `host.docker.internal:11434`
- Client access via port-forwards:
  - `chat-grpc` → `localhost:50051`
  - `chat-envoy` → `localhost:50052`
  - Gateway → `localhost:8080`
  - Jaeger UI → `localhost:16686`
  - Prometheus → `localhost:9090`

## Observability Stack

### Components (shared `observability` namespace)

- **OTEL Collector** — receives OTLP from Python server interceptors, exports to Jaeger
- **Jaeger** — all-in-one deployment (collector + query + UI), stores and visualizes traces
- **Prometheus** — scrapes Python server `/metrics` ports and Envoy admin stats

### Data flow

- **Traces:** Python OTEL interceptor → OTLP → Collector → Jaeger
- **Metrics:** Python Prometheus interceptor `/metrics` + Envoy stats → Prometheus scrape
- **Logs:** Python logging interceptor → structured JSON stdout → `kubectl logs`

### Demo moments

- Send message → Jaeger trace showing interceptor → handler → Ollama spans
- Multiple messages → Prometheus showing request distribution across pods
- Trigger timeout → DEADLINE_EXCEEDED in Prometheus error counters and Jaeger

## Presentation Slides

Format: Markdown, `---` separated, 3-5 bullet points per slide.

### Slide outline

1. Title — gRPC in Practice: From Protobuf to Production
2. Agenda
3. What is gRPC — RPC framework, HTTP/2, protobuf, language-agnostic
4. The four RPC types — unary, server-stream, client-stream, bidi (ASCII diagrams)
5. HTTP/2 under the hood — framing, multiplexing, flow control
6. Protobuf as contract — schema, field numbering, backwards compatibility
7. Buf ecosystem — lint, breaking change detection, multi-language codegen
8. Demo: proto definition — walk through `chat.proto`, the `oneof` pattern
9. Bidirectional streaming deep dive — independent read/write, ordering guarantees
10. Human-in-the-loop over bidi — cancel, context injection, multiplexed message types
11. Demo: live chat — Go client to Python server, streaming tokens, mid-stream cancel
12. Interceptors — logging, auth, OTEL, Prometheus as cross-cutting concerns
13. Authentication — bearer tokens in gRPC metadata
14. Context management — deadlines, timeouts, cancellation propagation
15. gRPC Gateway — HTTP/JSON transcoding, gRPC-Web, limitations (no bidi)
16. Demo: three access paths — `grpcurl`, `curl` via gateway, native client
17. Health checking & reflection — standard protocols, self-documenting services, K8s probes
18. Error model — gRPC status codes, rich error details, retry policies
19. Envoy as gRPC proxy — HTTP/2 native, per-RPC LB, mTLS, stream management
20. Envoy configuration walkthrough — listeners, clusters, timeouts, keep-alive
21. Demo: pure gRPC vs Envoy — mTLS, load balancing, stream timeouts
22. Scaling gRPC on Kubernetes — HPA, long-lived streams, graceful shutdown
23. Observability — Prometheus, OTEL traces, Jaeger, Envoy stats
24. Benefits & caveats — honest assessment
25. Q&A

## Makefile

Orchestration targets:

- `make generate` — `buf generate`
- `make lint` — `buf lint`
- `make breaking` — `buf breaking --against .git#branch=main`
- `make cluster` — create Kind cluster
- `make build` — Docker build server + gateway
- `make deploy-grpc` — deploy to `chat-grpc` namespace
- `make deploy-envoy` — deploy to `chat-envoy` namespace
- `make deploy-observability` — deploy Prometheus, Jaeger, OTEL collector
- `make port-forward` — all port-forwards
- `make client` — run Go Bubble Tea client
- `make clean` — tear everything down

## Key Design Decisions

1. **`oneof` for bidi message multiplexing** — preserves ordering on the stream; separate RPCs would lose ordering guarantees between HTTP/2 streams
2. **Ollama on host, not in Kind** — M2 Mac has no NVIDIA GPU; Ollama on host uses Metal acceleration; acts as external service (parallel to cross-cloud integration patterns)
3. **Standalone Envoy over Istio** — audience sees raw Envoy config, no abstraction hiding what the proxy does; simpler Kind setup
4. **Two namespaces** — side-by-side comparison of app-level vs proxy-level concerns using the same server code
5. **3 replicas** — makes load balancing behavior visible (pick-first vs round-robin)
6. **gRPC-Gateway as separate service** — clean separation; demonstrates that REST transcoding needs zero changes to the gRPC server
7. **uv for Python** — fast, reproducible dependency management
