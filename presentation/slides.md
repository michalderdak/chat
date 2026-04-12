## Slide 1: Title

# gRPC in Practice: From Protobuf to Production

- Building real-time streaming services with gRPC
- Patterns for bidirectional communication, observability, and deployment
- Live demos with a chat application backed by an LLM

---

## Slide 2: Agenda

- **Foundations** -- gRPC, HTTP/2, Protobuf, Buf toolchain
- **Streaming patterns** -- bidi streaming, human-in-the-loop control, cancel/context injection
- **Cross-cutting concerns** -- interceptors, auth, context management, error handling
- **Deployment** -- gRPC Gateway, Envoy proxy, Kubernetes scaling
- **Observability** -- Prometheus metrics, OpenTelemetry traces, Jaeger

---

## Slide 3: What is gRPC

- Open-source RPC framework from Google, built on HTTP/2 and Protocol Buffers
- Strongly typed contracts: define your API once, generate client and server code in any language
- Native streaming support -- not bolted on like WebSockets over HTTP/1.1
- First-class support for deadlines, cancellation, and metadata propagation
- Ecosystem: health checking, reflection, load balancing, interceptors all standardized

---

## Slide 4: The Four RPC Types

```
Unary                  Server Streaming
Client ──Req──> Server   Client ──Req──> Server
Client <──Res── Server   Client <──Res── Server
                         Client <──Res── Server
                         Client <──Res── Server

Client Streaming       Bidirectional Streaming
Client ──Req──> Server   Client ──Req──> Server
Client ──Req──> Server   Client <──Res── Server
Client ──Req──> Server   Client ──Req──> Server
Client <──Res── Server   Client <──Res── Server
```

- **Unary**: one request, one response -- the classic RPC call
- **Server streaming**: one request, server sends N responses (feeds, log tailing)
- **Client streaming**: client sends N requests, server responds once (uploads, aggregation)
- **Bidi streaming**: both sides read and write independently on a single HTTP/2 stream

---

## Slide 5: HTTP/2 Under the Hood

```
TCP Connection
 +-- Stream 1 (Unary RPC: SendMessage)
 |    HEADERS frame -> DATA frame -> HEADERS frame (trailers)
 +-- Stream 3 (Bidi RPC: Chat)
 |    HEADERS -> DATA -> DATA -> DATA -> ... -> HEADERS (trailers)
 +-- Stream 5 (Health Check)
```

- **Binary framing**: every message is a length-prefixed frame, no chunked-encoding hacks
- **Multiplexing**: multiple RPCs share one TCP connection without head-of-line blocking
- **Flow control**: per-stream and per-connection back-pressure prevents fast producers from overwhelming slow consumers
- **Header compression (HPACK)**: repeated metadata (method, path, auth) compressed across frames

---

## Slide 6: Protobuf as Contract

```protobuf
message ChatRequest {
  string conversation_id = 1;   // Field 1 -- never reuse
  oneof action {
    UserMessage user_message = 2;
    CancelGeneration cancel = 3;
    ContextInjection add_context = 4;
  }
}
```

- Schema is the source of truth: field numbers are permanent, names can change
- **Backward compatible changes**: add fields, add enum values, deprecate (never remove) fields
- **Breaking changes**: changing field numbers, changing types, removing fields
- `oneof` enforces exactly-one semantics -- ideal for multiplexed message types on a single stream
- Protobuf wire format is compact and fast: no field names on the wire, just tag + value

---

## Slide 7: Buf Ecosystem

```yaml
# buf.yaml -- lint + breaking change rules
lint:
  use: [STANDARD]
breaking:
  use: [FILE]

# buf.gen.yaml -- multi-language codegen
plugins:
  - local: protoc-gen-go         # Go stubs
  - local: protoc-gen-go-grpc    # Go gRPC stubs
  - local: protoc-gen-grpc-gateway # HTTP gateway
  - protoc_builtin: python       # Python stubs
```

- **buf lint**: enforces naming conventions, field numbering, package structure in CI
- **buf breaking**: detects backward-incompatible changes against a baseline (e.g., git main)
- **buf generate**: single command produces Go, Python, and gateway code from one `.proto` file
- Replaces fragile protoc + plugin scripts with a reproducible, dependency-aware toolchain

---

## Slide 8: Demo -- Proto Definition

<!-- Demo: Show proto/chat/v1/chat.proto in editor -->

```protobuf
service ChatService {
  rpc SendMessage(SendMessageRequest)
      returns (SendMessageResponse) {
    option (google.api.http) = {
      post: "/v1/chat/send" body: "*"
    };
  }
  rpc Chat(stream ChatRequest)
      returns (stream ChatResponse);
}
```

- Two RPCs: **SendMessage** (unary, gateway-friendly) and **Chat** (bidi streaming, full-featured)
- `google.api.http` annotation enables automatic REST transcoding
- `ChatRequest.action` uses `oneof` to multiplex: user messages, cancellation, and context injection
- `ChatResponse.event` uses `oneof` for tokens, status updates, errors, heartbeats, and acks
- Clean separation: the proto is both the API contract and the documentation

---

## Slide 9: Bidirectional Streaming Deep Dive

```
Client goroutine(s)            Server coroutines
  Send(UserMessage) ───>  ─── read_client_messages()
  Send(Cancel) ─────────>       |
                                v
  <─── Recv(Token)         _generate() -> send_queue
  <─── Recv(Token)               |
  <─── Recv(StatusDone)    <─────+
```

- Both sides read and write independently -- no request/response lock-step
- Ordering guarantee: messages arrive in send order within a single stream
- No ordering across streams: two concurrent bidi streams are independent
- Server uses an async queue: reader task feeds control signals, generator task feeds tokens
- Stream lifetime is bounded by context deadline, not individual message timeouts

---

## Slide 10: Human-in-the-Loop Over Bidi

```protobuf
oneof action {
  UserMessage user_message = 2;  // Start generation
  CancelGeneration cancel = 3;   // Stop mid-stream
  ContextInjection add_context = 4; // Inject context
}
```

- **Cancel mid-stream**: client sends `CancelGeneration`, server sets event flag, generation task stops
- **Context injection**: inject system prompts or tool results without closing the stream
- **Multiplexed message types**: `oneof` turns one stream into a typed control channel
- Server acknowledges every control message with an `Acknowledgement` response
- Pattern works for any human-in-the-loop scenario: code review, approval workflows, collaborative editing

---

## Slide 11: Demo -- Live Chat

<!-- Demo: Start server and client -->
<!-- Demo: cd server && uv run python -m chat_server.main -->
<!-- Demo: cd client && go run . -addr localhost:50051 -token changeme -->

- Go TUI client (Bubble Tea) connects to Python async server via bidi streaming
- Type a message: tokens stream back in real-time with a blinking cursor
- Press Ctrl+C mid-generation: sends `CancelGeneration`, server stops immediately, acks back
- Status bar shows connection state, streaming phase, and cancellation feedback
- One persistent HTTP/2 stream handles the entire conversation session

---

## Slide 12: Interceptors

```python
# Interceptor chain: Logging -> Auth -> OTEL -> Prometheus -> Handler
interceptors = [LoggingInterceptor()]
if settings.auth_enabled:
    interceptors.append(AuthInterceptor(token=...))
if settings.otel_enabled:
    interceptors.append(OTelInterceptor())
interceptors.append(PrometheusInterceptor())

server = grpc.aio.server(interceptors=interceptors)
```

- Cross-cutting concerns without touching handler code -- same pattern as HTTP middleware
- Each interceptor wraps `continuation` and can inspect/modify metadata, request, or response
- Works for both unary and streaming RPCs (wrap the generator for streams)
- Composable: add or remove interceptors via configuration, no code changes to handlers

---

## Slide 13: Authentication

```python
class AuthInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, handler_call_details):
        metadata = dict(handler_call_details.invocation_metadata)
        auth = metadata.get("authorization", "")
        if auth != f"Bearer {self._token}":
            async def deny(request, context):
                await context.abort(
                    grpc.StatusCode.UNAUTHENTICATED,
                    "Invalid or missing bearer token")
            return handler._replace(unary_unary=deny)
        return await continuation(handler_call_details)
```

- gRPC metadata is the equivalent of HTTP headers -- key-value pairs sent with every RPC
- Bearer tokens flow in the `authorization` metadata key, same pattern as REST APIs
- Interceptor rejects before the handler is invoked -- zero wasted compute
- Client attaches metadata via `call_credentials` or per-call metadata tuples

---

## Slide 14: Context Management

```go
// Client: set a 30-minute deadline for a long chat session
ctx, cancel := context.WithTimeout(
    context.Background(), 30*time.Minute)
defer cancel()
stream, err := client.Chat(ctx)
```

- **Deadlines** propagate automatically: client sets timeout, server sees remaining time
- **Cancellation** propagates through the call chain -- cancel on the client closes server-side context
- Server can check `context.cancelled()` to stop expensive work early
- gRPC translates expired deadlines to `DEADLINE_EXCEEDED` status automatically
- Best practice: always set deadlines -- an RPC without a deadline is a resource leak waiting to happen

---

## Slide 15: gRPC Gateway

```
            +----------+  HTTP/JSON  +---------+  gRPC  +--------+
  curl ---->| Gateway  |------------>| Server  |------->| Ollama |
            | (Go)     |  (unary)    | (Python)|        |        |
            +----------+             +---------+        +--------+
```

- Auto-generated HTTP/JSON reverse proxy from the same proto with `google.api.http` annotations
- Enables REST clients, browsers, and tools like `curl` to call gRPC services
- **Limitation**: only works for unary and server-streaming RPCs -- no bidi streaming support
- OpenAPI spec generated automatically for documentation and client generation
- Zero additional handler code: the gateway reads the proto annotations and does the translation

---

## Slide 16: Demo -- Three Access Paths

<!-- Demo: grpcurl -plaintext -d '{"conversation_id":"1","text":"Hello"}' localhost:50051 chat.v1.ChatService/SendMessage -->
<!-- Demo: curl -X POST http://localhost:8080/v1/chat/send -d '{"conversation_id":"1","text":"Hello"}' -->
<!-- Demo: go run ./client -addr localhost:50051 -token changeme -->

- **grpcurl**: native gRPC client, uses reflection to discover services, full proto fidelity
- **curl**: hits the gRPC Gateway over HTTP/JSON -- works for unary RPCs only
- **Go TUI**: full bidi streaming experience with real-time token display and cancellation
- Same server, same proto, three access patterns for different use cases
- Gateway is ideal for quick integration; native gRPC is required for streaming

---

## Slide 17: Health Checking and Reflection

```python
# Standard gRPC health check protocol
health_servicer = health.HealthServicer()
health_servicer.set("chat.v1.ChatService",
    HealthCheckResponse.SERVING)

# Enable reflection -- makes the service self-documenting
reflection.enable_server_reflection(service_names, server)
```

- **Health checking**: standardized protocol (`grpc.health.v1.Health`) with per-service status
- Kubernetes supports native gRPC health probes since v1.24 -- no HTTP sidecar needed
- **Reflection**: server exposes its proto schema at runtime -- enables grpcurl and other tools
- `grpcurl -plaintext localhost:50051 list` discovers services without having the proto file
- Reflection should be disabled in production if your API is not meant to be self-documenting

---

## Slide 18: Error Model and Retries

```
Common gRPC Status Codes:
  OK (0)                 -- success
  CANCELLED (1)          -- client cancelled the RPC
  DEADLINE_EXCEEDED (4)  -- timeout expired
  UNAUTHENTICATED (16)   -- missing/invalid credentials
  UNAVAILABLE (14)       -- transient failure, safe to retry
  INTERNAL (13)          -- server bug
```

- gRPC uses numeric status codes with string messages -- richer than HTTP but simpler than exceptions
- **Retryable**: `UNAVAILABLE` (server temporarily down), `RESOURCE_EXHAUSTED` (with backoff)
- **Not retryable**: `INVALID_ARGUMENT`, `NOT_FOUND`, `ALREADY_EXISTS` (client error, retry won't help)
- Retry policies can be configured per-method in service config or at the proxy layer (Envoy)
- For streaming RPCs: errors mid-stream require application-level recovery (reconnect + resume)

---

## Slide 19: Envoy as gRPC Proxy

```
               mTLS                    plaintext
Go Client ──────────> Envoy Proxy ──────────────> Chat Server (Python)
  |                      |
  |                      +-- Round-robin LB across replicas
  |                      +-- Health checks (gRPC native)
  |                      +-- Prometheus stats for free
  +-- Client cert required
```

- Envoy speaks HTTP/2 natively -- no protocol translation, no stream-breaking downgrades
- **Per-RPC load balancing**: unlike TCP-level LB, Envoy balances each RPC independently
- **mTLS**: mutual TLS terminates at Envoy, backend traffic can be plaintext within the cluster
- Built-in gRPC health checking and automatic endpoint draining
- Rich statistics emitted per-route, per-cluster with no application code changes

---

## Slide 20: Envoy Configuration Walkthrough

```yaml
listeners:
  - filter_chains:
      - transport_socket:  # mTLS termination
          require_client_certificate: true
        filters:
          - http_connection_manager:
              stream_idle_timeout: 1800s  # 30 min
clusters:
  - lb_policy: ROUND_ROBIN
    http2_protocol_options:
      connection_keepalive:
        interval: 30s    # Ping every 30s
        timeout: 5s      # Fail after 5s no pong
```

- **stream_idle_timeout**: must be tuned for long-lived streams (default 5 min would kill chat sessions)
- **max_stream_duration**: caps the absolute lifetime of a stream, prevents resource leaks
- **connection_keepalive**: HTTP/2 PING frames detect dead connections before TCP timeout (often minutes)
- **ROUND_ROBIN** at the cluster level gives per-RPC distribution across server pods
- gRPC health checks validate the actual service, not just TCP connectivity

---

## Slide 21: Demo -- Pure gRPC vs Envoy

<!-- Demo: kubectl port-forward -n chat-grpc svc/chat-server 50051:50051 -->
<!-- Demo: kubectl port-forward -n chat-envoy svc/envoy 50052:50051 -->
<!-- Demo: go run ./client -addr localhost:50051 -token changeme (direct) -->
<!-- Demo: go run ./client -addr localhost:50052 -token changeme -tls (via Envoy) -->

- **Direct gRPC (chat-grpc namespace)**: pick-first load balancing, single connection to one pod
- **Envoy gRPC (chat-envoy namespace)**: round-robin per-RPC, mTLS, health-checked backends
- Scale the server to 3 replicas: direct gRPC always hits the same pod; Envoy rotates
- Envoy admin UI (`localhost:9901`) shows per-upstream connection stats, active streams, error rates
- Takeaway: for >1 replica, you need a gRPC-aware proxy or client-side load balancing

---

## Slide 22: Scaling gRPC on Kubernetes

- **HPA metrics**: scale on `grpc_server_active_streams` (custom metric), not just CPU
- **Long-lived streams break naive HPA**: new pods get no streams; old pods stay overloaded
- **Graceful shutdown**: server calls `server.stop(grace_period)`, Envoy drains with `GOAWAY` frames
- **Connection draining**: Envoy health checks detect `NOT_SERVING`, stops routing new RPCs
- **Pod disruption budgets**: essential for streaming workloads -- losing a pod means losing all its streams

---

## Slide 23: Observability

```
Go Client --> gRPC Server --> Ollama
    |              |
    |     OTEL traces (W3C propagation)
    |              |
    v              v
OTEL Collector --> Jaeger (traces)
                   Prometheus (metrics)
                     - grpc_server_handled_total
                     - grpc_server_handling_seconds
                     - grpc_server_active_streams
```

- **Prometheus interceptor**: counters (total RPCs, status), histograms (latency), gauges (active streams)
- **OTEL interceptor**: creates spans per-RPC, propagates trace context via gRPC metadata
- **Envoy stats**: upstream connection counts, retry stats, latency histograms -- zero application code
- **Jaeger**: visualize end-to-end request flow across client, server, and LLM backend
- Three layers of observability: application interceptors, proxy stats, and Kubernetes metrics

---

## Slide 24: Benefits and Caveats

**Where gRPC shines:**
- Real-time streaming with back-pressure and cancellation
- Multi-language codegen from a single schema
- Performance: binary encoding, multiplexing, header compression
- Rich ecosystem: health checks, reflection, interceptors, load balancing

**Where gRPC adds friction:**
- Browser support requires grpc-web proxy or Connect protocol
- Debugging is harder -- binary wire format means no casual `curl` inspection
- Streaming RPCs need careful timeout, keepalive, and proxy configuration
- Proto schema evolution requires discipline and CI tooling (buf breaking)
- Operational complexity: HTTP/2-aware proxies, custom LB, stream-aware scaling

---

## Slide 25: Q&A

- **Source code**: all demos available in the companion repository
- **Key takeaways**:
  - Protobuf-first design gives you type safety, codegen, and documentation for free
  - Bidi streaming unlocks patterns impossible with REST (cancel, context injection, real-time tokens)
  - Interceptors keep handlers clean -- auth, metrics, and tracing are configuration, not code
  - Envoy is the natural proxy for gRPC -- HTTP/2 native, per-RPC LB, mTLS, observability
  - Always set deadlines, always tune keepalives, always plan for stream lifecycle

<!-- Questions? -->
