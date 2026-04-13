# Graceful Shutdown & Redis History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add graceful pod shutdown with Redis-backed conversation history persistence and automatic client reconnect, so rolling K8s deployments don't disrupt active chat sessions.

**Architecture:** Server catches SIGTERM, waits for active generation to finish, saves conversation history to Redis as protobuf bytes, sends ServerShutdown event to clients. Client auto-reconnects to a new pod which loads history from Redis. Event log no longer shows token events.

**Tech Stack:** Protocol Buffers, Python grpc.aio, redis.asyncio, Go Bubble Tea, Kubernetes PDB

---

## File Structure

```
Modified:
  proto/chat/v1/chat.proto                    — ServerShutdown, ConversationMessage, ConversationHistory
  server/pyproject.toml                       — add redis[hiredis] dependency
  server/src/chat_server/config.py            — REDIS_URL setting
  server/src/chat_server/main.py              — SIGTERM handler, graceful shutdown
  server/src/chat_server/service.py           — drain, Redis load/save, stream tracking
  client/tui/commands.go                      — ShutdownMsg, remove Token from event log
  client/tui/model.go                         — reconnect flow, store client ref
  client/tui/events.go                        — ServerShutdown + Reconnected styles
  client/main.go                              — pass ChatServiceClient to model
  deploy/base/server-deployment.yaml          — terminationGracePeriod, preStop, REDIS_URL
  deploy/base/kustomization.yaml              — add redis + pdb resources

New:
  server/src/chat_server/history.py           — Redis history store with protobuf serialization
  deploy/base/redis-deployment.yaml
  deploy/base/redis-service.yaml
  deploy/base/pdb.yaml
```

---

### Task 1: Proto Changes & Regeneration

**Files:**
- Modify: `proto/chat/v1/chat.proto`

- [ ] **Step 1: Add ServerShutdown and storage messages to chat.proto**

Add `ServerShutdown` as field 8 in the `ChatResponse` oneof. Add after the closing brace of `UsageInfo`:

```protobuf
message ServerShutdown {
  string reason = 1;
}
```

Update `ChatResponse` oneof to include field 8:

```protobuf
message ChatResponse {
  string conversation_id = 1;
  oneof event {
    Token token = 2;
    StatusUpdate status = 3;
    Error error = 4;
    Heartbeat heartbeat = 5;
    Acknowledgement ack = 6;
    UsageInfo usage = 7;
    ServerShutdown shutdown = 8;
  }
}
```

Add storage messages at the end of the file (not used in RPCs, only for Redis serialization):

```protobuf
// --- Storage messages (Redis serialization) ---

message ConversationMessage {
  string role = 1;
  string content = 2;
}

message ConversationHistory {
  repeated ConversationMessage messages = 1;
}
```

- [ ] **Step 2: Regenerate stubs**

```bash
make docker
```

- [ ] **Step 3: Verify**

```bash
buf lint
go build ./client/... ./gateway/...
```

- [ ] **Step 4: Commit**

```bash
git add proto/ gen/
git commit -m "feat: add ServerShutdown and ConversationHistory to proto"
```

---

### Task 2: Redis K8s Manifests & Server Deployment Updates

**Files:**
- Create: `deploy/base/redis-deployment.yaml`
- Create: `deploy/base/redis-service.yaml`
- Create: `deploy/base/pdb.yaml`
- Modify: `deploy/base/kustomization.yaml`
- Modify: `deploy/base/server-deployment.yaml`

- [ ] **Step 1: Create deploy/base/redis-deployment.yaml**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
        - name: redis
          image: redis:7-alpine
          ports:
            - containerPort: 6379
              name: redis
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "128Mi"
              cpu: "100m"
```

- [ ] **Step 2: Create deploy/base/redis-service.yaml**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: redis
spec:
  selector:
    app: redis
  ports:
    - port: 6379
      targetPort: 6379
```

- [ ] **Step 3: Create deploy/base/pdb.yaml**

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: chat-server
spec:
  maxUnavailable: 1
  selector:
    matchLabels:
      app: chat-server
```

- [ ] **Step 4: Update deploy/base/kustomization.yaml**

Replace entirely:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ollama-service.yaml
  - server-deployment.yaml
  - server-service.yaml
  - gateway-deployment.yaml
  - gateway-service.yaml
  - redis-deployment.yaml
  - redis-service.yaml
  - pdb.yaml
```

- [ ] **Step 5: Update deploy/base/server-deployment.yaml**

Add `terminationGracePeriodSeconds`, `preStop` lifecycle hook, and `REDIS_URL` env var. Replace entirely:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chat-server
spec:
  replicas: 3
  selector:
    matchLabels:
      app: chat-server
  template:
    metadata:
      labels:
        app: chat-server
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
    spec:
      terminationGracePeriodSeconds: 30
      containers:
        - name: chat-server
          image: chat-server:latest
          imagePullPolicy: Never
          ports:
            - containerPort: 50051
              name: grpc
            - containerPort: 9090
              name: metrics
          lifecycle:
            preStop:
              exec:
                command: ["sleep", "5"]
          env:
            - name: GRPC_PORT
              value: "50051"
            - name: METRICS_PORT
              value: "9090"
            - name: OLLAMA_URL
              value: "http://ollama:11434"
            - name: OLLAMA_MODEL
              value: "qwen3:0.6b"
            - name: AUTH_ENABLED
              value: "true"
            - name: AUTH_TOKEN
              value: "demo-token"
            - name: OTEL_ENABLED
              value: "true"
            - name: OTEL_ENDPOINT
              value: "otel-collector.observability.svc.cluster.local:4317"
            - name: REDIS_URL
              value: "redis://redis:6379"
          readinessProbe:
            grpc:
              port: 50051
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            grpc:
              port: 50051
            initialDelaySeconds: 10
            periodSeconds: 30
          resources:
            requests:
              memory: "128Mi"
              cpu: "100m"
            limits:
              memory: "512Mi"
              cpu: "500m"
```

- [ ] **Step 6: Validate kustomize**

```bash
kubectl kustomize deploy/grpc/
kubectl kustomize deploy/envoy/
```

Expected: renders with Redis deployment, service, and PDB included.

- [ ] **Step 7: Commit**

```bash
git add deploy/base/
git commit -m "feat: add Redis, PDB, and graceful shutdown config to K8s manifests"
```

---

### Task 3: Python Redis History Store

**Files:**
- Create: `server/src/chat_server/history.py`
- Modify: `server/pyproject.toml`
- Modify: `server/src/chat_server/config.py`

- [ ] **Step 1: Add redis dependency**

Add `"redis[hiredis]>=5.0.0"` to the dependencies list in `server/pyproject.toml`. Then:

```bash
cd server && uv sync
```

- [ ] **Step 2: Add REDIS_URL to config.py**

Add this field to the `Settings` dataclass in `server/src/chat_server/config.py`:

```python
    redis_url: str = os.getenv("REDIS_URL", "redis://redis:6379")
```

- [ ] **Step 3: Create server/src/chat_server/history.py**

```python
"""Redis-backed conversation history using protobuf serialization."""

import redis.asyncio as aioredis
from chat.v1 import chat_pb2

HISTORY_TTL = 3600  # 1 hour


class HistoryStore:
    def __init__(self, redis_url: str):
        self._redis = aioredis.from_url(redis_url)

    def _key(self, conversation_id: str) -> str:
        return f"chat:history:{conversation_id}"

    async def save(self, conversation_id: str, history: list[dict]) -> None:
        """Save conversation history to Redis as protobuf bytes."""
        proto = chat_pb2.ConversationHistory()
        for msg in history:
            proto.messages.append(
                chat_pb2.ConversationMessage(
                    role=msg["role"],
                    content=msg["content"],
                )
            )
        await self._redis.set(
            self._key(conversation_id),
            proto.SerializeToString(),
            ex=HISTORY_TTL,
        )

    async def load(self, conversation_id: str) -> list[dict]:
        """Load conversation history from Redis. Returns empty list if not found."""
        data = await self._redis.get(self._key(conversation_id))
        if data is None:
            return []
        proto = chat_pb2.ConversationHistory()
        proto.ParseFromString(data)
        return [
            {"role": msg.role, "content": msg.content}
            for msg in proto.messages
        ]

    async def close(self) -> None:
        await self._redis.aclose()
```

- [ ] **Step 4: Run existing tests to verify nothing broke**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/ -v
```

Expected: all pass (history.py is standalone, not yet used).

- [ ] **Step 5: Commit**

```bash
git add server/pyproject.toml server/uv.lock server/src/chat_server/config.py server/src/chat_server/history.py
git commit -m "feat: Redis history store with protobuf serialization"
```

---

### Task 4: Server Graceful Shutdown & Redis Integration

**Files:**
- Modify: `server/src/chat_server/service.py`
- Modify: `server/src/chat_server/main.py`

- [ ] **Step 1: Rewrite service.py with drain, stream tracking, and Redis**

Replace `server/src/chat_server/service.py` entirely:

```python
import asyncio
import grpc
import structlog
from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.ollama import OllamaClient
from chat_server.history import HistoryStore

log = structlog.get_logger()


class ActiveStream:
    """Tracks state for one active bidi stream."""

    def __init__(self, conversation_id: str, send_queue: asyncio.Queue):
        self.conversation_id = conversation_id
        self.send_queue = send_queue
        self.conversation_history: list[dict] = []
        self.generation_task: asyncio.Task | None = None


class ChatServiceServicer(chat_pb2_grpc.ChatServiceServicer):
    def __init__(self, ollama_url: str, ollama_model: str, history_store: HistoryStore):
        self._ollama = OllamaClient(base_url=ollama_url, model=ollama_model)
        self._history = history_store
        self._context_length: int = 0
        self._active_streams: set[ActiveStream] = set()
        self._draining = False

    async def initialize(self):
        """Call once at startup to cache model info."""
        self._context_length = await self._ollama.get_model_context_length()

    async def drain(self):
        """Graceful drain: wait for generations, save history, notify clients."""
        self._draining = True
        log.info("drain.start", active_streams=len(self._active_streams))

        # Wait for active generations to finish (up to 20s safety limit)
        for stream_state in list(self._active_streams):
            if stream_state.generation_task and not stream_state.generation_task.done():
                try:
                    await asyncio.wait_for(stream_state.generation_task, timeout=20.0)
                except (asyncio.TimeoutError, asyncio.CancelledError):
                    stream_state.generation_task.cancel()
                    log.warning("drain.generation_timeout", conversation_id=stream_state.conversation_id)

        # Save history and send shutdown signal to each stream
        for stream_state in list(self._active_streams):
            if stream_state.conversation_history:
                await self._history.save(
                    stream_state.conversation_id,
                    stream_state.conversation_history,
                )
                log.info("drain.history_saved", conversation_id=stream_state.conversation_id)

            await stream_state.send_queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=stream_state.conversation_id,
                    shutdown=chat_pb2.ServerShutdown(reason="pod draining"),
                )
            )
            await stream_state.send_queue.put(None)

        log.info("drain.complete")

    async def SendMessage(self, request, context):
        """Unary RPC: send message, get complete response."""
        try:
            full_response = ""
            async for token in self._ollama.chat(request.text):
                full_response += token
            return chat_pb2.SendMessageResponse(
                conversation_id=request.conversation_id,
                text=full_response,
            )
        except Exception as e:
            await context.abort(grpc.StatusCode.INTERNAL, str(e))

    async def Chat(self, request_iterator, context):
        """Bidirectional streaming RPC with cancel, history, usage, and heartbeat."""
        send_queue: asyncio.Queue = asyncio.Queue()
        cancel_event = asyncio.Event()

        # Create stream state and register it
        stream_state = ActiveStream(conversation_id="", send_queue=send_queue)
        self._active_streams.add(stream_state)

        async def heartbeat_loop():
            try:
                while True:
                    await asyncio.sleep(15)
                    word = await self._ollama.generate_heartbeat_word()
                    await send_queue.put(
                        chat_pb2.ChatResponse(
                            conversation_id=stream_state.conversation_id,
                            heartbeat=chat_pb2.Heartbeat(beat=word),
                        )
                    )
            except asyncio.CancelledError:
                pass

        heartbeat_task = asyncio.create_task(heartbeat_loop())

        async def read_client_messages():
            try:
                async for msg in request_iterator:
                    # Update conversation_id from first message
                    if stream_state.conversation_id == "":
                        stream_state.conversation_id = msg.conversation_id
                        # Load history from Redis on first message
                        stream_state.conversation_history = await self._history.load(
                            msg.conversation_id
                        )
                        if stream_state.conversation_history:
                            log.info(
                                "history.loaded",
                                conversation_id=msg.conversation_id,
                                messages=len(stream_state.conversation_history),
                            )

                    action = msg.WhichOneof("action")

                    if action == "user_message":
                        if self._draining:
                            continue

                        if stream_state.generation_task and not stream_state.generation_task.done():
                            cancel_event.set()
                            stream_state.generation_task.cancel()
                            try:
                                await stream_state.generation_task
                            except asyncio.CancelledError:
                                pass

                        cancel_event.clear()
                        stream_state.conversation_history.append(
                            {"role": "user", "content": msg.user_message.text}
                        )
                        stream_state.generation_task = asyncio.create_task(
                            self._generate(
                                msg.conversation_id,
                                msg.user_message.text,
                                send_queue,
                                cancel_event,
                                stream_state,
                            )
                        )

                    elif action == "cancel":
                        cancel_event.set()
                        if stream_state.generation_task and not stream_state.generation_task.done():
                            stream_state.generation_task.cancel()
                            try:
                                await stream_state.generation_task
                            except asyncio.CancelledError:
                                pass
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(acknowledged_type="cancel"),
                            )
                        )

                    elif action == "add_context":
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(acknowledged_type="context_injection"),
                            )
                        )
            finally:
                if not self._draining:
                    await send_queue.put(None)

        reader_task = asyncio.create_task(read_client_messages())

        try:
            while True:
                response = await send_queue.get()
                if response is None:
                    break
                yield response
        finally:
            self._active_streams.discard(stream_state)
            heartbeat_task.cancel()
            reader_task.cancel()
            if stream_state.generation_task and not stream_state.generation_task.done():
                stream_state.generation_task.cancel()

    async def _generate(
        self,
        conversation_id: str,
        text: str,
        queue: asyncio.Queue,
        cancel_event: asyncio.Event,
        stream_state: ActiveStream,
    ):
        """Stream tokens from Ollama into the send queue, then save history and report usage."""
        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_THINKING),
            )
        )
        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_GENERATING),
            )
        )

        accumulated_response = ""
        try:
            async for token_text in self._ollama.chat(
                text, conversation_history=stream_state.conversation_history[:-1]
            ):
                if cancel_event.is_set():
                    break
                accumulated_response += token_text
                await queue.put(
                    chat_pb2.ChatResponse(
                        conversation_id=conversation_id,
                        token=chat_pb2.Token(text=token_text),
                    )
                )
        except asyncio.CancelledError:
            pass
        except Exception as e:
            await queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=conversation_id,
                    error=chat_pb2.Error(code=13, message=str(e)),
                )
            )

        # Record assistant response in history
        if accumulated_response:
            stream_state.conversation_history.append(
                {"role": "assistant", "content": accumulated_response}
            )

        # Save history to Redis after every generation
        await self._history.save(conversation_id, stream_state.conversation_history)

        # Send usage info
        usage = self._ollama.last_usage
        if usage:
            await queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=conversation_id,
                    usage=chat_pb2.UsageInfo(
                        prompt_tokens=usage.get("prompt_eval_count", 0),
                        completion_tokens=usage.get("eval_count", 0),
                        context_length=self._context_length,
                    ),
                )
            )

        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_DONE),
            )
        )
```

- [ ] **Step 2: Rewrite main.py with SIGTERM handler**

Replace `server/src/chat_server/main.py` entirely:

```python
import asyncio
import os
import signal
import grpc
import structlog
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from grpc_reflection.v1alpha import reflection
from prometheus_client import start_http_server

from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.config import settings
from chat_server.service import ChatServiceServicer
from chat_server.history import HistoryStore
from chat_server.interceptors.logging import LoggingInterceptor
from chat_server.interceptors.auth import AuthInterceptor
from chat_server.interceptors.otel import OTelInterceptor
from chat_server.interceptors.prometheus import PrometheusInterceptor

log = structlog.get_logger()


def _setup_otel():
    if not settings.otel_enabled:
        return
    from opentelemetry import trace
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor
    from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
    from opentelemetry.sdk.resources import Resource

    resource = Resource.create({"service.name": "chat-server"})
    provider = TracerProvider(resource=resource)
    exporter = OTLPSpanExporter(endpoint=settings.otel_endpoint, insecure=True)
    provider.add_span_processor(BatchSpanProcessor(exporter))
    trace.set_tracer_provider(provider)


async def serve():
    _setup_otel()

    interceptors = [LoggingInterceptor()]
    if settings.auth_enabled:
        interceptors.append(AuthInterceptor(token=settings.auth_token))
    if settings.otel_enabled:
        interceptors.append(OTelInterceptor())
    interceptors.append(PrometheusInterceptor())

    server = grpc.aio.server(interceptors=interceptors)

    history_store = HistoryStore(redis_url=settings.redis_url)

    servicer = ChatServiceServicer(
        ollama_url=settings.ollama_url,
        ollama_model=settings.ollama_model,
        history_store=history_store,
    )
    await servicer.initialize()
    chat_pb2_grpc.add_ChatServiceServicer_to_server(servicer, server)

    health_servicer = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)
    health_servicer.set("chat.v1.ChatService", health_pb2.HealthCheckResponse.SERVING)
    health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)

    service_names = [
        chat_pb2.DESCRIPTOR.services_by_name["ChatService"].full_name,
        health_pb2.DESCRIPTOR.services_by_name["Health"].full_name,
        reflection.SERVICE_NAME,
    ]
    reflection.enable_server_reflection(service_names, server)

    start_http_server(settings.metrics_port)

    listen_addr = f"[::]:{settings.grpc_port}"
    server.add_insecure_port(listen_addr)
    hostname = os.getenv("HOSTNAME", "local")
    log.info("server.start", hostname=hostname, addr=listen_addr, metrics_port=settings.metrics_port)

    await server.start()

    # SIGTERM handler for graceful shutdown
    shutdown_event = asyncio.Event()

    def on_sigterm():
        log.info("server.sigterm")
        shutdown_event.set()

    loop = asyncio.get_running_loop()
    loop.add_signal_handler(signal.SIGTERM, on_sigterm)
    loop.add_signal_handler(signal.SIGINT, on_sigterm)

    # Wait for shutdown signal
    await shutdown_event.wait()

    # Graceful drain
    log.info("server.draining")
    health_servicer.set("chat.v1.ChatService", health_pb2.HealthCheckResponse.NOT_SERVING)
    health_servicer.set("", health_pb2.HealthCheckResponse.NOT_SERVING)

    await servicer.drain()
    await server.stop(grace=5)
    await history_store.close()
    log.info("server.stopped")


if __name__ == "__main__":
    asyncio.run(serve())
```

- [ ] **Step 3: Run tests**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/ -v
```

Existing tests mock the ollama client. The `ChatServiceServicer` constructor now takes `history_store` — update test fixtures if tests create the servicer directly. Add `history_store=MagicMock()` to any test fixture that creates `ChatServiceServicer`.

- [ ] **Step 4: Commit**

```bash
git add server/src/chat_server/service.py server/src/chat_server/main.py
git commit -m "feat: server graceful shutdown with Redis history and SIGTERM handling"
```

---

### Task 5: Go Client Reconnect & Event Log Cleanup

**Files:**
- Modify: `client/tui/events.go`
- Modify: `client/tui/commands.go`
- Modify: `client/tui/model.go`
- Modify: `client/main.go`

- [ ] **Step 1: Add ServerShutdown and Reconnected styles to events.go**

Add to the style variables:

```go
	IncomingShutdownStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	OutgoingReconnectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
```

Add cases to `styleForType()`. In the `Outgoing` switch:

```go
		case "Reconnected":
			return OutgoingReconnectStyle
```

In the `Incoming` switch:

```go
		case "ServerShutdown":
			return IncomingShutdownStyle
```

- [ ] **Step 2: Add ShutdownMsg and ReconnectedMsg to commands.go, remove Token from event log**

Add new message types:

```go
type ShutdownMsg struct{ Reason string }
type ReconnectedMsg struct{}
```

In `WaitForEvent`, add the `ChatResponse_Shutdown` case:

```go
		case *chatv1.ChatResponse_Shutdown:
			return ShutdownMsg{Reason: evt.Shutdown.GetReason()}
```

- [ ] **Step 3: Rewrite model.go with reconnect and no Token events**

Key changes to `Model` struct — add `grpcClient` and `timeout` fields:

```go
type Model struct {
	chatViewport  viewport.Model
	eventViewport viewport.Model
	input         textinput.Model
	messages      []ChatMessage
	eventLog      []EventEntry

	grpcClient     chatv1.ChatServiceClient  // for reconnecting
	stream         *grpcclient.StreamClient
	conversationID string
	timeout        time.Duration
	streaming      bool
	status         string
	err            error

	promptTokens     int
	completionTokens int
	contextLength    int

	ready  bool
	width  int
	height int
}
```

Update `NewModel` to accept the gRPC client and timeout:

```go
func NewModel(grpcClient chatv1.ChatServiceClient, stream *grpcclient.StreamClient, conversationID string, timeout time.Duration) Model {
```

In `Update`, change the `TokenMsg` handler to NOT add to event log:

```go
	case TokenMsg:
		// Tokens render in chat only, not in event log
		if len(m.messages) > 0 {
```

Add `ShutdownMsg` handler:

```go
	case ShutdownMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "ServerShutdown", Payload: fmt.Sprintf("%q", msg.Reason)})
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "[Server restarting, reconnecting...]"})
		m.streaming = false
		m.status = "reconnecting..."
		m.refreshPanels()
		return m, m.reconnectCmd()

	case ReconnectedMsg:
		m.addEvent(EventEntry{Dir: Outgoing, Type: "Reconnected"})
		m.status = "reconnected"
		m.refreshPanels()
		return m, WaitForEvent(m.stream)
```

Change the `ErrorMsg` handler to auto-reconnect on stream errors:

```go
	case ErrorMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "Error", Payload: msg.Err.Error()})
		m.streaming = false
		// Auto-reconnect on connection errors
		if strings.Contains(msg.Err.Error(), "Unavailable") || strings.Contains(msg.Err.Error(), "EOF") {
			m.messages = append(m.messages, ChatMessage{Role: "system", Content: "[Connection lost, reconnecting...]"})
			m.status = "reconnecting..."
			m.refreshPanels()
			return m, m.reconnectCmd()
		}
		m.err = msg.Err
		m.status = fmt.Sprintf("error: %s", msg.Err)
		m.refreshPanels()
		return m, nil
```

Add the `reconnectCmd` method:

```go
func (m *Model) reconnectCmd() tea.Cmd {
	return func() tea.Msg {
		m.stream.Close()
		newStream, err := grpcclient.OpenStream(m.grpcClient, m.timeout)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("reconnect: %w", err)}
		}
		m.stream = newStream
		return ReconnectedMsg{}
	}
}
```

Add "system" role rendering in `renderMessages`:

```go
		case "system":
			prefix = ErrorStyle.Render("")
			content = ErrorStyle.Render(msg.Content)
```

- [ ] **Step 4: Update client/main.go to pass grpcClient to NewModel**

Change the model creation line:

```go
	model := tui.NewModel(client, stream, "conversation-1", *timeout)
```

This requires importing `chatv1` — but `client` is already a `chatv1.ChatServiceClient` from `grpcclient.NewChatClient()`. Also add `"time"` import if not present.

- [ ] **Step 5: Verify compilation**

```bash
go build ./client/...
```

- [ ] **Step 6: Commit**

```bash
git add client/
git commit -m "feat: client auto-reconnect on shutdown and connection loss, remove token events from log"
```

---

### Task 6: Rebuild, Deploy & Verify

**Files:** None (deployment only)

- [ ] **Step 1: Pre-pull Redis image and load into Kind**

```bash
docker pull redis:7-alpine
kind load docker-image redis:7-alpine --name chat-demo
```

If `kind load` fails with multi-platform error, use:

```bash
docker save redis:7-alpine | docker exec -i chat-demo-control-plane ctr --namespace=k8s.io images import --snapshotter=overlayfs -
```

- [ ] **Step 2: Rebuild and deploy**

```bash
make deploy-all
```

- [ ] **Step 3: Verify Redis is running**

```bash
kubectl -n chat-grpc get pods -l app=redis
kubectl -n chat-envoy get pods -l app=redis
```

Expected: 1/1 Running in each namespace.

- [ ] **Step 4: Verify PDB**

```bash
kubectl -n chat-grpc get pdb
```

Expected: `chat-server` PDB with `maxUnavailable: 1`.

- [ ] **Step 5: Test graceful shutdown**

In terminal 1:
```bash
make client
```

Send a message, wait for response to finish. Then in terminal 2:

```bash
kubectl -n chat-grpc rollout restart deployment/chat-server
```

Expected in the TUI:
1. Current response finishes completely
2. Event log shows `← ServerShutdown "pod draining"`
3. Chat shows `[Server restarting, reconnecting...]`
4. Event log shows `→ Reconnected`
5. Status returns to "reconnected"
6. Send another message — AI remembers the previous conversation (loaded from Redis)

- [ ] **Step 6: Verify event log has no token events**

Send a message and check the right panel. Should show StatusUpdate, Heartbeat, UsageInfo, etc. but no Token events.

- [ ] **Step 7: Commit any fixes**

If fixes are needed, commit them.

---

## Verification Checklist

1. `buf lint` passes
2. `go build ./client/... ./gateway/...` compiles
3. Python tests pass
4. Redis pods running in both namespaces
5. PDB exists and allows max 1 unavailable
6. Graceful shutdown: generation finishes, ServerShutdown sent, client reconnects
7. History persists across reconnect (AI remembers conversation)
8. Event log shows no Token events
9. Event log shows ServerShutdown (red) and Reconnected (green)
10. Error reconnect: kill a pod with `kubectl delete pod` — client auto-reconnects
