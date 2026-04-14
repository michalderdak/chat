---
theme: dark
author: gRPC Session
date: MMMM dd, YYYY
paging: Slide %d / %d
---

# gRPC in Practice: From Protobuf to Production

Building real-time streaming services with gRPC

Live demos with a Go TUI client, Python async server, and Qwen3 LLM

Deployed on Kubernetes with Envoy proxy

---

# About Me

Working at **Visma** for 5 years, leading teams of data engineers, frontend developers and data admins. 

**Smartscan** -- Document scanning & extraction API
- ~20 million document scans per month
- State of the art LLM-based extraction models

**Autosuggest** -- Generic classifier for transactional data
- ~26 million prediction calls per month
- Hosts up to 700,000 models

Both services:
- Hosted on **GKE** inside **Istio** service mesh
- gRPC APIs in Go and Python
- BigQuery, Cloud Storage, and Spanner

---

# Agenda

1. gRPC Fundamentals -- what, why, and how it works under the hood
2. Protobuf & Buf -- schema-first design with tooling
3. Bidirectional Streaming -- real-time human-in-the-loop patterns
4. Interceptors -- auth, logging, metrics, traces without touching handlers
5. Context Management -- deadlines, cancellation, propagation
6. gRPC Gateway -- REST/JSON transcoding from the same proto
7. Envoy Proxy -- mTLS, per-RPC load balancing, stream management
8. Graceful Shutdown -- draining streams, Redis history, client reconnect
9. Observability -- Prometheus, OpenTelemetry, Jaeger
10. Live Demos throughout

---

# What is gRPC

- Open-source RPC framework from Google, built on **HTTP/2** and **Protocol Buffers**
- Define your API once in `.proto`, generate client + server code in any language
- Native streaming support -- not bolted on like WebSockets over HTTP/1.1
- First-class deadlines, cancellation, and metadata propagation
- Standardized ecosystem: health checking, reflection, load balancing, interceptors
  - **grpc-ecosystem** (github.com/grpc-ecosystem): community tools -- grpc-gateway, grpc-health-probe, Go middleware

---

# The Four RPC Types

```
Unary                     Server Streaming
Client ──Req──> Server    Client ──Req──> Server
Client <──Res── Server    Client <──Res── Server
                          Client <──Res── Server

Client Streaming          Bidirectional Streaming
Client ──Req──> Server    Client ──Req──> Server
Client ──Req──> Server    Client <──Res── Server
Client <──Res── Server    Client ──Req──> Server
                          Client <──Res── Server
```

**Unary**: request-response (classic RPC)
**Server streaming**: server sends N responses (feeds, LLM token streaming)
**Client streaming**: client sends N messages, server responds once (uploads)
**Bidi streaming**: both sides read/write independently on one HTTP/2 stream

---

# HTTP/2 Under the Hood

```
TCP Connection
 +-- Stream 1 (Unary RPC: SendMessage)
 |    HEADERS -> DATA -> HEADERS (trailers)
 +-- Stream 3 (Bidi RPC: Chat)
 |    HEADERS -> DATA -> DATA -> DATA -> ... -> HEADERS (trailers)
 +-- Stream 5 (Health Check)
```

- **Binary framing**: length-prefixed frames, no chunked-encoding hacks
- **Multiplexing**: multiple RPCs share one TCP connection
- **Flow control**: per-stream back-pressure prevents fast producers overwhelming slow consumers
- **Header compression (HPACK)**: repeated metadata compressed across frames
- Why this matters: 100 concurrent users = 100 streams on ONE connection

---

# Proto as Contract

```protobuf
syntax = "proto3";
package chat.v1;

message SendMessageRequest {
  string conversation_id = 1;  // Field 1 -- permanent wire identity
  string text = 2;             // Field 2 -- name can change, number cannot
}
```

- Field numbers are the wire identity -- names can change, numbers cannot
- **Safe changes**: add fields, add enum values, deprecate fields
- **Breaking changes**: reuse numbers, change types, remove fields
- Both sides need the `.proto` schema to decode -- trade-off: readability vs compact + fast


**JSON**: `{"conversation_id":"abc","text":"hello"}` -- field names every time

**Protobuf**: `[0x0A 03 "abc"][0x12 05 "hello"]` -- tag + value only

---

# Demo: Let's Look at our Proto Definition

![](../proto/chat/v1/chat.proto)

---

# Buf Ecosystem

https://buf.build/

```yaml
# buf.yaml
lint:
  use: [STANDARD]     # Google API design guide
breaking:
  use: [FILE]          # Detect breaking changes

# buf.gen.yaml -- one command, multiple languages
plugins:
  - local: protoc-gen-go          # Go stubs
  - local: protoc-gen-go-grpc     # Go gRPC stubs
  - local: protoc-gen-grpc-gateway # HTTP transcoding
  - protoc_builtin: python         # Python stubs
```

- `buf lint` -- enforces naming, numbering, package structure in CI
- `buf breaking --against .git#branch=main` -- catches breaking changes before merge
- `buf generate` -- one command produces Go + Python + Gateway code

---

# Demo: buf in Action

![](../buf.yaml)

*`make generate`* - fails, why?

*`make docker`* - succeeds, why?

lint & breaking changes

---

# Any Questions So Far?

---

# The Setup

- **Chat Service**: Python async gRPC server with Ollama LLM backend
- **Go TUI Client**: Chat / demo interface for the gRPC endpoints
- **KinD**: local Kubernetes cluster for deploying the server
- **Ollama**: local LLM inference engine (Qwen3 0.6b)

---

# Unary RPC: `SendMessage` -- one request, one response

```
Client (Go TUI)              Server (Python async)
  SendMessageRequest ──>  ── SendMessage handler ──>
  <── SendMessageResponse  <── Response with AI reply
```

# Bidirectional Streaming `Chat` -- independent read/write, real-time tokens

How our `Chat` RPC works: `server/src/chat_server/service.py`

```
Client (Go TUI)              Server (Python async)
  Send(UserMessage) ───>  ── read_client_messages()
  Send(Cancel) ──────────>      |
                                v
  <─── Recv(Token)         _generate() -> send_queue
  <─── Recv(Token)               |
  <─── Recv(StatusDone)    <─────+
```

- Both sides read/write independently -- no request/response lock-step
- **Ordering guarantee**: messages arrive in send order
  -  This is where `oneof` shines

---

# Demo: The Chat App - gRPC

- **Cancel mid-stream**: client sends cancel, server sets event flag, generation stops
- **Mid-generation interrupt**: type a new message while AI is streaming -- auto-cancels + sends new
- **Context injection**: send a control message with extra context (e.g. "remember this fact") that the server can use in generation
- **Multiplexing in action - one TCP connection, multiple streams**: chat stream + unary RPCs
- **Connected to a single server pod** -- no load balancing

---

# Context Management: Deadlines & Cancellation

```go
// client/main.go -- 30-minute deadline for long chat sessions
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
stream, err := client.Chat(ctx)
```

- **Deadlines propagate**: client sets timeout, server sees remaining time
- **Cancellation propagates**: cancel on client closes server-side context
- Server checks `cancel_event.is_set()` to stop expensive Ollama calls early
- gRPC translates expired deadlines to `DEADLINE_EXCEEDED` automatically

```go
// client/tui/unified_model.go -- Ctrl+C sends CancelGeneration
case "ctrl+c":
    if mode.IsStream && mode.Streaming {
        return m, withMode(m.activeMode, SendCancel(...))
    }
```

---

# Context Management: Metadata Propagation

gRPC metadata = key-value pairs that travel alongside the RPC (like HTTP headers)

**Three examples from our demo:**

```
Client ──[authorization: Bearer demo-token]──> Auth Interceptor
         metadata set by client interceptor    read by server interceptor

Client ──[traceparent: 00-abc123...]──────────> OTEL Interceptor
         W3C trace context                      creates child span

Client <──[x-served-by: chat-server-5md2x]─── Server
         trailing metadata (response headers)   read by client for pod name
```

- **Auth**: `authorization` in metadata, attached by client stream interceptor
- **Traces**: `traceparent` propagated via OTEL, enables end-to-end tracing
- **Pod identity**: `x-served-by` in trailing metadata, visible in TUI event log
- Metadata flows through interceptors without touching handler code

---

# Health Checking & Reflection

`server/src/chat_server/main.py`

```python
# Standard gRPC health protocol
health_servicer = health.HealthServicer()
health_servicer.set("chat.v1.ChatService", SERVING)
```

- Kubernetes 1.24+ supports native gRPC health probes

```yaml
# deploy/base/server-deployment.yaml -- K8s native gRPC probe
readinessProbe:
  grpc:
    port: 50051
```

*`make grpcurl-health`* -- uses gRPC health protocol to check server health

---

# Reflection

```python
# Self-documenting service
reflection.enable_server_reflection(service_names, server)
```

- Reflection lets `grpcurl` discover services at runtime

*`make grpcurl-list`* - lists services and methods via reflection

---

# Interceptors

Our interceptor chain: `server/src/chat_server/main.py`

```python
interceptors = [LoggingInterceptor()]      # Outermost: sees everything
if settings.auth_enabled:
    interceptors.append(AuthInterceptor())  # Rejects early
if settings.otel_enabled:
    interceptors.append(OTelInterceptor())  # Traces
interceptors.append(PrometheusInterceptor()) # Metrics
```

Each interceptor wraps the handler via `intercept_service`:
- Inspect metadata, request, response
- Works for both unary AND streaming RPCs
- Add/remove via config -- zero handler code changes

- Jaeger UI: http://localhost:16686
- Prometheus: http://localhost:9090

---

# gRPC Gateway: HTTP1/JSON

`gateway/main.go` -- auto-generated HTTP/JSON reverse proxy

```
                 HTTP/JSON                gRPC
curl ──────────> Gateway (Go) ──────────> Server (Python) ───> Ollama
POST /v1/chat/send                        SendMessage RPC
```

```protobuf
rpc SendMessage(SendMessageRequest) returns (SendMessageResponse) {
  option (google.api.http) = {
    post: "/v1/chat/send"
    body: "*"
  };
}
```

- One annotation in the proto could equal full REST endpoint
- **Limitation**: unary and server-streaming only -- NO bidi streaming
- Part of **grpc-ecosystem** (github.com/grpc-ecosystem/grpc-gateway)

*`make curl-send`* - sends HTTP/JSON request to gateway, which proxies to gRPC server

---

# Demo: Three Ways to Reach the Same Backend

```bash
# 1. Native gRPC (full proto fidelity)
grpcurl -plaintext -H 'authorization: Bearer demo-token' \
  -d '{"conversation_id":"1","text":"Hello"}' \
  localhost:50051 chat.v1.ChatService/SendMessage

# 2. HTTP/JSON via Gateway (curl-friendly)
curl -X POST http://localhost:8080/v1/chat/send \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer demo-token' \
  -d '{"conversation_id":"1","text":"Hello"}'

# 3. Go TUI (full bidi streaming)
make client
```

Same server, same proto, three access patterns

---

# gRPC Error Model & Status Codes

```
OK (0)                 -- success
CANCELLED (1)          -- client cancelled
DEADLINE_EXCEEDED (4)  -- timeout expired
UNAUTHENTICATED (16)   -- missing/invalid credentials
UNAVAILABLE (14)       -- transient, safe to retry
INTERNAL (13)          -- server bug
```

- **Retryable**: `UNAVAILABLE`, `RESOURCE_EXHAUSTED` (with backoff)
- **Not retryable**: `INVALID_ARGUMENT`, `NOT_FOUND` (client error)
- Our interceptors translate Ollama errors to gRPC status codes
- For streaming: errors mid-stream need application-level recovery

Our client auto-reconnects on `UNAVAILABLE` and `ServerShutdown`:

```go
if strings.Contains(err, "Unavailable") {
    return m, m.reconnectCmd()
}
```

---

# Pure gRPC Load Balancing

```
Client-side    Client ──────────────> Servers  (client decides)
```

**Client-side** (built into gRPC):
- `pick_first` (default): one backend, failover on error -- our gRPC namespace
- `round_robin`: rotate across all resolved IPs -- needs headless service
- No extra hop, lowest latency, but every client needs service discovery

---

# Any Questions So Far?

---

# Demo: The Chat App - Envoy

Everything from pure gRPC, plus:

- **mTLS**: client cert + CA verification -- zero code in the Python server, Envoy terminates TLS
- **Per-RPC round-robin**: unary requests hit different pods -- visible in event log pod names
- **Headless service discovery**: Envoy resolves all pod IPs via DNS, not one virtual IP
- **Per-route timeouts**: unary gets 30s, bidi streaming gets 30 minutes -- configured in Envoy, not app code
- **HTTP/2 keepalive**: Envoy pings backends every 10s to detect dead connections
- **gRPC health checking**: Envoy probes each pod with the standard health protocol, removes unhealthy ones
- **Preconnect**: Envoy eagerly opens connections to all backends before the first request arrives
- **Observability for free**: Envoy stats (upstream_rq_total, latency histograms) without app changes

---

# Envoy as gRPC Proxy

```
               mTLS                      plaintext HTTP/2
Go Client ──────────> Envoy Proxy ──────────────────> Chat Server x3
  |                      |
  |                      +-- Round-robin per-RPC load balancing
  |                      +-- gRPC health checks per endpoint
  |                      +-- Headless service DNS for pod discovery
  +-- CA cert + client cert required
```

Our setup: `deploy/envoy/envoy-configmap.yaml`

- Envoy speaks HTTP/2 natively -- no protocol downgrade
- **Per-RPC load balancing**: each unary RPC can hit a different pod
- **mTLS**: client cert required, CA-signed, auto-generated
- A bidi stream stays on ONE pod for its lifetime (the stream IS the session)

---

# Envoy Config Walkthrough

`deploy/envoy/envoy-configmap.yaml`

```yaml
routes:
  # Unary RPC: 30s timeout
  - match: { path: "/chat.v1.ChatService/SendMessage" }
    route: { cluster: chat_service, timeout: 30s }
  # Bidi streaming: no timeout, 30min stream duration
  - match: { prefix: "/" }
    route:
      cluster: chat_service
      timeout: 0s
      max_stream_duration: { max_stream_duration: 1800s }

clusters:
  - type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    dns_lookup_family: V4_ONLY
    preconnect_policy: { per_upstream_preconnect_ratio: 3.0 }
```

Per-route timeouts: unary gets 30s, streaming gets 30 minutes.

---

# Headless Service for Envoy Discovery

```yaml
# deploy/envoy/server-service-headless.yaml
# Patches base service to clusterIP: None in envoy namespace
apiVersion: v1
kind: Service
metadata:
  name: chat-server
spec:
  clusterIP: None   # Returns all pod IPs, not one virtual IP
```

**Regular ClusterIP**: DNS returns 1 virtual IP -> Envoy "round-robins" across 1 address
**Headless (clusterIP: None)**: DNS returns all pod IPs -> real round-robin

The `chat-grpc` namespace keeps regular ClusterIP -- demonstrating pick-first behavior.

---

# Demo: Unary Load Balancing -- Envoy vs Direct

In the unified TUI (`make client`), switch tabs to compare:

**Tab: Envoy Unary** -- round-robin, different pod each request:
```
→ SendMessage "Hey"
← Response (chat-server-5md2x) 1.2s
→ SendMessage "Hello"
← Response (chat-server-9zstr) 0.9s   <-- different pod!
→ SendMessage "Hi"
← Response (chat-server-hp78l) 1.1s   <-- different pod!
```

**Tab: gRPC Unary** -- pick-first, same pod every time:
```
→ SendMessage "Hey"
← Response (chat-server-dbk28) 1.0s
→ SendMessage "Hello"
← Response (chat-server-dbk28) 0.8s   <-- same pod
```

Pod name returned via gRPC trailing metadata (`x-served-by`).
Same connection, same TUI -- just switch tabs to see the difference.

---

# mTLS with Envoy

Certs generated by: `deploy/envoy/certs/generate-certs.sh`

```bash
# Self-signed CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -sha256 -key ca.key -out ca.crt

# Server cert with SANs for K8s service names
openssl x509 -req -extfile <(printf "subjectAltName=
  DNS:chat-server,
  DNS:chat-server.chat-envoy.svc.cluster.local,
  DNS:envoy,DNS:localhost")

# Client cert signed by same CA
openssl x509 -req -CA ca.crt -CAkey ca.key -out client.crt
```

```bash
# Unified client passes both connection configs:
make client  # --grpc-target + --envoy-target with certs
# Switch to Envoy tabs to use mTLS connection
```

---

# Graceful Shutdown: The Problem

What happens when you `kubectl rollout restart` during an active chat?

**Without graceful shutdown:**
1. K8s sends SIGTERM to pod
2. Pod dies immediately
3. Client gets `UNAVAILABLE` error
4. Conversation history lost
5. User has to start over

**With our graceful shutdown:**
1. SIGTERM received
2. Health set to `NOT_SERVING` (stop new traffic)
3. Wait for active generation to finish (don't cut off the AI)
4. Save conversation history to Redis
5. Send `ServerShutdown` message to client
6. Client auto-reconnects to a new pod
7. New pod loads history from Redis -- conversation continues

---

# Graceful Shutdown: Server Side

`server/src/chat_server/main.py`

```python
# SIGTERM handler
shutdown_event = asyncio.Event()
loop.add_signal_handler(signal.SIGTERM, lambda: shutdown_event.set())

await shutdown_event.wait()

# 1. Stop accepting new traffic
health_servicer.set("", NOT_SERVING)

# 2. Drain active streams
await servicer.drain()  # Waits for generation, saves Redis, sends shutdown

# 3. Stop server
await server.stop(grace=5)
```

Timing budget: `terminationGracePeriodSeconds: 30`

```
|5s preStop| SIGTERM -> drain (up to 20s) -> stop (5s) |
```

---

# Redis History: Protobuf Serialization

`server/src/chat_server/history.py`

```protobuf
// Storage messages (not used in RPCs, only Redis)
message ConversationMessage {
  string role = 1;
  string content = 2;
}
message ConversationHistory {
  repeated ConversationMessage messages = 1;
}
```

```python
class HistoryStore:
    async def save(self, conversation_id, history):
        proto = chat_pb2.ConversationHistory()
        for msg in history:
            proto.messages.append(ConversationMessage(**msg))
        await self._redis.set(key, proto.SerializeToString(), ex=3600)

    async def load(self, conversation_id):
        data = await self._redis.get(key)
        proto = ConversationHistory()
        proto.ParseFromString(data)
```

Protobuf for everything -- not just RPC, also storage.

---

# Client Auto-Reconnect

`client/tui/unified_model.go`

```go
case ShutdownMsg:
    mode.Messages = append(mode.Messages,
        ChatMessage{Role: "system",
            Content: "[Server restarting, reconnecting...]"})
    mode.Reconnecting = true
    return m, withMode(idx, m.reconnectCmd(idx))

case ReconnectedMsg:
    mode.Stream = inner.Stream
    mode.PodName = mode.Stream.PodName()
    return m, withMode(idx, WaitForEvent(mode.Stream))
```

Key insight: new stream carries the same `conversation_id`.
New pod loads history from Redis. AI remembers the conversation.

---

# Demo: Graceful Shutdown

Terminal 1:
```bash
make client  # Tab to "gRPC Stream", send a few messages
```

Terminal 2:
```bash
kubectl -n chat-grpc rollout restart deployment/chat-server
```

Watch the gRPC Stream tab:
1. AI finishes its current response (not cut off!)
2. Event log: `<- ServerShutdown "pod draining"`
3. Chat: `[Server restarting, reconnecting...]`
4. Event log: `-> Reconnected chat-server-NEW-POD`
5. Send another message -- AI remembers the conversation (Redis)

---

# Pod Disruption Budget

`deploy/base/pdb.yaml`

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: chat-server
spec:
  maxUnavailable: 1   # At most 1 of 3 pods down at a time
  selector:
    matchLabels:
      app: chat-server
```

- During rolling updates: K8s drains one pod at a time
- Combined with graceful shutdown: active streams migrate safely
- Essential for streaming workloads -- losing a pod = losing all its streams

---

# Scaling gRPC on Kubernetes

- **HPA**: scale on `grpc_server_active_streams` (custom metric), not just CPU
- **Long-lived streams break naive HPA**: new pods get no streams, old pods overloaded
- **Connection draining**: health -> `NOT_SERVING` -> Envoy stops routing -> drain streams
- **preStop hook**: 5s sleep before SIGTERM lets K8s update endpoints first

```yaml
# deploy/base/server-deployment.yaml
terminationGracePeriodSeconds: 30
lifecycle:
  preStop:
    exec:
      command: ["sleep", "5"]
```

The 30s budget: 5s preStop + 20s drain + 5s server.stop = safe margin.

---

# Observability Stack

```
Go Client --> gRPC Server --> Ollama
    |              |
    |     OTEL traces (W3C propagation)
    |              |
    v              v
OTEL Collector --> Jaeger (traces)     localhost:16686
                   Prometheus (metrics) localhost:9090
```

**Prometheus interceptor** (`server/src/chat_server/interceptors/prometheus.py`):
- `grpc_server_handled_total{method, status}` -- RPC counts
- `grpc_server_handling_seconds{method}` -- latency histograms
- `grpc_server_active_streams` -- gauge of open bidi streams

**OTEL interceptor**: spans per-RPC, trace context via gRPC metadata
**Envoy stats**: upstream connections, retries, latency -- zero app code

---

# Benefits and Caveats

**Where gRPC shines:**
- Real-time streaming with back-pressure and cancellation
- Multi-language codegen from a single schema
- Binary encoding + multiplexing + header compression = performance
- Rich ecosystem: health checks, reflection, interceptors, load balancing

**Where gRPC adds friction:**
- Browser support requires grpc-web proxy or Connect protocol
- Binary wire format means no casual `curl` inspection
- Streaming RPCs need careful timeout, keepalive, and proxy tuning
- Proto schema evolution requires discipline + CI tooling (`buf breaking`)
- Need HTTP/2-aware proxies (Envoy) -- nginx falls short for per-RPC LB

---

# Architecture Summary

```
                    Kind Cluster
+-------------------+-------------------+
| chat-grpc         | chat-envoy        |
|                   |                   |
| Gateway (HTTP)    | Envoy (mTLS)      |
|   |               |   |              |
| Server x3         | Server x3         |
| (bearer token)    | (no auth, Envoy   |
|                   |  handles mTLS)    |
| Redis             | Redis             |
+-------------------+-------------------+
|         observability                 |
|  Prometheus | Jaeger | OTEL Collector |
+---------------------------------------+
                  |
            host.docker.internal
                  |
              Ollama (Qwen3 0.6B, Metal-accelerated)
```

---

# Key Makefile Commands

```bash
make cluster         # Create Kind cluster
make deploy-all      # Deploy everything (builds, loads, restarts)

make client          # Unified TUI: 4 tabs, 2 connections
                     # Tab: gRPC Unary | gRPC Stream
                     #       Envoy Unary | Envoy Stream

make grpcurl-list    # Service discovery via reflection
make grpcurl-health  # Health check
make curl-send       # HTTP/JSON via gateway

make logs-grpc       # Server logs (chat-grpc)
make logs-envoy      # Server logs (chat-envoy)
```

---

# Q&A

**Source code**: all demos in this repository

**Key takeaways**:
- Protobuf-first design gives you type safety, codegen, and docs for free
- Bidi streaming unlocks patterns impossible with REST
- Interceptors keep handlers clean -- auth, metrics, tracing are config
- Envoy is the natural proxy for gRPC -- HTTP/2 native, per-RPC LB, mTLS
- Graceful shutdown with Redis history = zero-downtime rolling updates
- Always set deadlines, always tune keepalives, always plan for stream lifecycle

---

# Thank You

# war stories

PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python


