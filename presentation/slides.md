---
theme: dark
author: gRPC Consultant Session
date: MMMM dd, YYYY
paging: Slide %d / %d
---

# gRPC in Practice: From Protobuf to Production

Building real-time streaming services with gRPC

Live demos with a Go TUI client, Python async server, and Qwen3 LLM

Deployed on Kubernetes with Envoy proxy

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
- **Multiplexing**: multiple RPCs share one TCP connection, no head-of-line blocking
- **Flow control**: per-stream back-pressure prevents fast producers overwhelming slow consumers
- **Header compression (HPACK)**: repeated metadata compressed across frames
- Why this matters: 100 concurrent users = 100 streams on ONE connection

---

# Protobuf as Contract

Our chat service proto: `proto/chat/v1/chat.proto`

```protobuf
message ChatRequest {
  string conversation_id = 1;  // Field numbers are permanent
  oneof action {
    UserMessage user_message = 2;
    CancelGeneration cancel = 3;
    ContextInjection add_context = 4;
  }
}
```

- Field numbers are the wire identity -- names can change, numbers cannot
- **Safe changes**: add fields, add enum values, deprecate fields
- **Breaking changes**: reuse numbers, change types, remove fields
- `oneof` enforces exactly-one -- ideal for multiplexing message types on a stream
- Compact wire format: no field names transmitted, just tag + value

---

# Buf Ecosystem

Our config: `buf.yaml` and `buf.gen.yaml`

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

# Demo: Our Proto Definition

```bash
cat proto/chat/v1/chat.proto
```

Two RPCs from one service:
- **SendMessage** (unary) -- with `google.api.http` annotation for REST transcoding
- **Chat** (bidi streaming) -- the core interactive experience

Message multiplexing via `oneof`:
- Client sends: `UserMessage`, `CancelGeneration`, `ContextInjection`
- Server sends: `Token`, `StatusUpdate`, `Error`, `Heartbeat`, `Acknowledgement`, `UsageInfo`, `ServerShutdown`

```bash
buf lint && echo "Lint passed"
```

---

# Bidirectional Streaming Deep Dive

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
- **Ordering guarantee**: messages arrive in send order within ONE stream
- Server uses `asyncio.Queue`: reader task feeds control signals, generator streams tokens
- This is why `oneof` beats separate RPCs -- cancel and tokens share ordering

---

# Why oneof Over Separate RPCs?

```
Option A: oneof on one stream        Option B: separate RPCs
─────────────────────────────         ──────────────────────────
Stream 1: UserMessage                 Stream 1: Chat (tokens)
Stream 1: Token, Token, Token...      Stream 2: Cancel (unary)
Stream 1: CancelGeneration
Stream 1: Ack("cancel")              Race condition! Cancel on
                                      Stream 2 may arrive after
All messages ordered.                 tokens already sent on
Cancel is guaranteed to be            Stream 1.
seen after the last token.
```

One stream = one HTTP/2 stream = **ordered delivery**
Separate RPCs = separate HTTP/2 streams = **no ordering between them**

---

# Human-in-the-Loop Patterns

```protobuf
// Client can send any of these at any time:
oneof action {
  UserMessage user_message = 2;     // Start generation
  CancelGeneration cancel = 3;      // Stop mid-stream
  ContextInjection add_context = 4; // Inject context
}
```

- **Cancel mid-stream**: client sends cancel, server sets event flag, generation stops
- **Mid-generation interrupt**: type a new message while AI is streaming -- auto-cancels + sends new
- **Server acknowledges** every control message with `Acknowledgement` response
- Pattern works for: code review, approval workflows, collaborative editing

---

# Demo: Live Bidi Chat

```bash
make client
```

Split-screen TUI:
- **Left panel**: chat messages with word wrapping
- **Right panel**: gRPC event log showing every protocol message
- **Status bar**: pod name, token usage, streaming state

Try:
1. Send a message -- watch StatusUpdate and Token events flow
2. Press **Ctrl+C** mid-generation -- see CancelGeneration -> Ack
3. Type new message while AI is streaming -- auto-cancel + new generation
4. Watch **Heartbeat** events with playful words every 15 seconds

---

# Interceptors: Cross-Cutting Concerns

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

---

# Interceptor: Logging

`server/src/chat_server/interceptors/logging.py`

```python
class LoggingInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        log.info("rpc.start", method=method)
        handler = await continuation(handler_call_details)
        # Wrap both unary and stream handlers
        # Log every stream.recv and stream.send with event type
```

For streaming RPCs, wraps the request iterator AND response generator:
- `stream.recv` logs: method, message number, action type
- `stream.send` logs: method, message number, event type
- `rpc.end` logs: total duration, message count, status

---

# Interceptor: Authentication

`server/src/chat_server/interceptors/auth.py`

```python
# Skip auth for health checks and reflection
_SKIP_AUTH_PREFIXES = (
    "/grpc.health.v1.Health/",
    "/grpc.reflection.v1alpha.ServerReflection/",
)

class AuthInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, details):
        if any(details.method.startswith(p) for p in _SKIP_AUTH_PREFIXES):
            return await continuation(details)  # Let through
        # Check Bearer token in metadata
        # Reject with UNAUTHENTICATED if invalid
```

- gRPC metadata = HTTP headers (key-value pairs per RPC)
- Health probes don't send tokens -- must skip auth for them
- Client attaches token via stream interceptor on every RPC

---

# Context Management

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
// client/tui/model.go -- Ctrl+C sends CancelGeneration on the stream
case "ctrl+c":
    if m.streaming {
        return m, SendCancel(m.stream, m.conversationID)
    }
```

---

# gRPC Gateway: REST from the Same Proto

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

- One annotation in the proto = full REST endpoint
- **Limitation**: unary and server-streaming only -- NO bidi streaming
- OpenAPI spec generated automatically from the same proto

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
  -d '{"conversation_id":"1","text":"Hello"}'

# 3. Go TUI (full bidi streaming)
make client
```

Same server, same proto, three access patterns.
Gateway for quick integration; native gRPC for streaming.

---

# Health Checking & Reflection

`server/src/chat_server/main.py`

```python
# Standard gRPC health protocol
health_servicer = health.HealthServicer()
health_servicer.set("chat.v1.ChatService", SERVING)

# Self-documenting service
reflection.enable_server_reflection(service_names, server)
```

```yaml
# deploy/base/server-deployment.yaml -- K8s native gRPC probe
readinessProbe:
  grpc:
    port: 50051
```

- Kubernetes 1.24+ supports native gRPC health probes
- Reflection lets `grpcurl` discover services at runtime
- Health + reflection = observable, debuggable services out of the box

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

```bash
# Envoy namespace: round-robin -- different pod each request
make unary-envoy
# Send 3-4 messages, watch event log:
#   <- Response (chat-server-5md2x) 1.2s
#   <- Response (chat-server-9zstr) 0.9s   <-- different pod!
#   <- Response (chat-server-hp78l) 1.1s   <-- different pod!
```

```bash
# Pure gRPC namespace: pick-first -- same pod every time
make unary-grpc
# Send 3-4 messages, watch event log:
#   <- Response (chat-server-dbk28) 1.0s
#   <- Response (chat-server-dbk28) 0.8s   <-- same pod
#   <- Response (chat-server-dbk28) 1.2s   <-- same pod
```

The pod name is returned via gRPC trailing metadata (`x-served-by`).

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
# Client passes certs via flags:
make client-envoy  # --ca-cert, --client-cert, --client-key
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

`client/tui/model.go`

```go
case ShutdownMsg:
    // Server says "I'm going away"
    m.messages = append(m.messages,
        ChatMessage{Role: "system", Content: "[Server restarting, reconnecting...]"})
    m.reconnecting = true
    return m, m.reconnectCmd()

case ReconnectedMsg:
    // New stream opened to a different pod
    m.stream = msg.Stream
    m.podName = m.stream.PodName()
    m.status = fmt.Sprintf("reconnected to %s", m.podName)
    return m, WaitForEvent(m.stream)
```

Key insight: new stream carries the same `conversation_id`.
New pod loads history from Redis. AI remembers the conversation.

---

# Demo: Graceful Shutdown

Terminal 1:
```bash
make client  # Start bidi chat, send a few messages
```

Terminal 2:
```bash
kubectl -n chat-grpc rollout restart deployment/chat-server
```

Watch the TUI:
1. AI finishes its current response (not cut off!)
2. Event log: `<- ServerShutdown "pod draining"`
3. Chat: `[Server restarting, reconnecting...]`
4. Event log: `-> Reconnected chat-server-NEW-POD`
5. Send another message -- AI remembers the conversation

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

make client          # Bidi streaming TUI (chat-grpc)
make client-envoy    # Bidi streaming TUI via Envoy (mTLS)
make unary-grpc      # Unary TUI (chat-grpc, pick-first)
make unary-envoy     # Unary TUI via Envoy (round-robin)

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
