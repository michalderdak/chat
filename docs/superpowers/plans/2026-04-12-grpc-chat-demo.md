# gRPC Chat Demo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a working gRPC chat demo with Go TUI client, Python bidi-streaming server backed by Ollama, deployed on Kind with pure-gRPC and Envoy namespaces, plus a slide deck presentation.

**Architecture:** Go Bubble Tea client communicates via gRPC bidi streaming with a Python async server. Server calls Ollama on the host Mac for LLM inference. Two K8s namespaces contrast app-level gRPC (auth interceptor, pick-first LB) vs Envoy proxy (mTLS, round-robin LB, stream management). A gRPC-Gateway provides HTTP/JSON transcoding for the unary RPC.

**Tech Stack:** Go 1.23, Python 3.12, Buf v2, gRPC, Protocol Buffers, Bubble Tea, grpc.aio, httpx, Envoy, Kind, Prometheus, Jaeger, OpenTelemetry, Kustomize

---

## File Structure

```
chat/
├── proto/
│   └── chat/v1/chat.proto
├── buf.yaml
├── buf.gen.yaml
├── gen/
│   ├── go/chat/v1/           # Generated Go stubs
│   └── python/chat/v1/       # Generated Python stubs
├── client/
│   ├── main.go
│   ├── tui/
│   │   ├── model.go
│   │   ├── commands.go
│   │   └── styles.go
│   └── grpcclient/
│       ├── client.go
│       └── stream.go
├── server/
│   ├── pyproject.toml
│   ├── Dockerfile
│   └── src/chat_server/
│       ├── __init__.py
│       ├── main.py
│       ├── service.py
│       ├── ollama.py
│       ├── config.py
│       └── interceptors/
│           ├── __init__.py
│           ├── auth.py
│           ├── logging.py
│           ├── otel.py
│           └── prometheus.py
├── gateway/
│   ├── main.go
│   └── Dockerfile
├── deploy/
│   ├── kind-config.yaml
│   ├── base/
│   ├── grpc/
│   ├── envoy/
│   └── observability/
├── go.mod
├── go.sum
├── Makefile
└── presentation/
    └── slides.md
```

---

### Task 1: Proto Definition & Buf Setup

**Files:**
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Create: `proto/chat/v1/chat.proto`

- [ ] **Step 1: Install prerequisites**

Run:
```bash
# Install buf
brew install bufbuild/buf/buf

# Install Go protoc plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
```

Verify: `buf --version` prints a version, `which protoc-gen-go` returns a path.

- [ ] **Step 2: Create buf.yaml**

```yaml
# buf.yaml
version: v2
modules:
  - path: proto
deps:
  - buf.build/googleapis/googleapis
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

- [ ] **Step 3: Create buf.gen.yaml**

```yaml
# buf.gen.yaml
version: v2
inputs:
  - directory: proto
plugins:
  - local: protoc-gen-go
    out: gen/go
    opt: paths=source_relative
  - local: protoc-gen-go-grpc
    out: gen/go
    opt: paths=source_relative
  - local: protoc-gen-grpc-gateway
    out: gen/go
    opt: paths=source_relative
  - local: protoc-gen-openapiv2
    out: gen/openapiv2
  - protoc_builtin: python
    out: gen/python
  - protoc_builtin: pyi
    out: gen/python
```

- [ ] **Step 4: Create proto/chat/v1/chat.proto**

```protobuf
syntax = "proto3";

package chat.v1;

option go_package = "github.com/michal-derdak/chat/gen/go/chat/v1;chatv1";

import "google/api/annotations.proto";

service ChatService {
  // Unary RPC — used for gRPC-Gateway HTTP/JSON transcoding demo
  rpc SendMessage(SendMessageRequest) returns (SendMessageResponse) {
    option (google.api.http) = {
      post: "/v1/chat/send"
      body: "*"
    };
  }

  // Bidirectional streaming RPC — core of the demo
  rpc Chat(stream ChatRequest) returns (stream ChatResponse);
}

// --- Unary messages ---

message SendMessageRequest {
  string conversation_id = 1;
  string text = 2;
}

message SendMessageResponse {
  string conversation_id = 1;
  string text = 2;
}

// --- Bidi streaming messages ---

message ChatRequest {
  string conversation_id = 1;
  oneof action {
    UserMessage user_message = 2;
    CancelGeneration cancel = 3;
    ContextInjection add_context = 4;
  }
}

message UserMessage {
  string text = 1;
}

message CancelGeneration {}

message ContextInjection {
  string text = 1;
}

message ChatResponse {
  string conversation_id = 1;
  oneof event {
    Token token = 2;
    StatusUpdate status = 3;
    Error error = 4;
    Heartbeat heartbeat = 5;
    Acknowledgement ack = 6;
  }
}

message Token {
  string text = 1;
}

enum Phase {
  PHASE_UNSPECIFIED = 0;
  PHASE_THINKING = 1;
  PHASE_GENERATING = 2;
  PHASE_DONE = 3;
}

message StatusUpdate {
  Phase phase = 1;
}

message Error {
  int32 code = 1;
  string message = 2;
}

message Heartbeat {}

message Acknowledgement {
  string acknowledged_type = 1;
}
```

- [ ] **Step 5: Run buf dep update and lint**

```bash
buf dep update
buf lint
```

Expected: no errors. `buf.lock` is created.

- [ ] **Step 6: Generate code**

```bash
mkdir -p gen/go gen/python gen/openapiv2
buf generate
```

Expected: files appear in `gen/go/chat/v1/` (`.pb.go`, `_grpc.pb.go`, `.pb.gw.go`) and `gen/python/chat/v1/` (`chat_pb2.py`, `chat_pb2.pyi`).

- [ ] **Step 7: Generate Python gRPC stubs**

buf's `protoc_builtin` doesn't cover gRPC Python stubs. Generate them with `grpcio-tools`:

```bash
pip install grpcio-tools
python -m grpc_tools.protoc \
  -I proto \
  -I $(buf config ls-modules -q 2>/dev/null || echo "proto") \
  --grpc_python_out=gen/python \
  proto/chat/v1/chat.proto
```

If the googleapis import path causes issues, download the dep cache:
```bash
python -m grpc_tools.protoc \
  -I proto \
  -I $(buf dep resolve -q 2>/dev/null && echo ".buf/cache" || echo "proto") \
  --grpc_python_out=gen/python \
  proto/chat/v1/chat.proto
```

Fallback: copy `google/api/annotations.proto` and `google/api/http.proto` from googleapis into `proto/` and run without the dep path.

- [ ] **Step 8: Create Python __init__.py files and verify**

```bash
touch gen/__init__.py gen/python/__init__.py gen/python/chat/__init__.py gen/python/chat/v1/__init__.py
```

Verify:
```bash
PYTHONPATH=gen/python python -c "from chat.v1 import chat_pb2; print(chat_pb2.DESCRIPTOR.name)"
```

Expected: prints `chat/v1/chat.proto`

- [ ] **Step 9: Commit**

```bash
git add proto/ buf.yaml buf.gen.yaml buf.lock gen/
git commit -m "feat: add proto definition and buf code generation"
```

---

### Task 2: Python Server Project Setup

**Files:**
- Create: `server/pyproject.toml`
- Create: `server/src/chat_server/__init__.py`
- Create: `server/src/chat_server/config.py`
- Create: `server/src/chat_server/interceptors/__init__.py`

- [ ] **Step 1: Initialize Python project with uv**

```bash
cd server
uv init --lib --name chat-server
```

If `uv init` creates files we don't want, clean up and create manually.

- [ ] **Step 2: Create server/pyproject.toml**

```toml
[project]
name = "chat-server"
version = "0.1.0"
requires-python = ">=3.12"
dependencies = [
    "grpcio>=1.68.0",
    "grpcio-reflection>=1.68.0",
    "grpcio-health-checking>=1.68.0",
    "grpcio-tools>=1.68.0",
    "httpx>=0.27.0",
    "structlog>=24.0.0",
    "opentelemetry-api>=1.28.0",
    "opentelemetry-sdk>=1.28.0",
    "opentelemetry-exporter-otlp-proto-grpc>=1.28.0",
    "prometheus-client>=0.21.0",
]

[build-system]
requires = ["setuptools>=75.0"]
build-backend = "setuptools.build_meta"

[tool.setuptools.packages.find]
where = ["src"]
```

- [ ] **Step 3: Install dependencies**

```bash
cd server && uv sync
```

Expected: `.venv` created, `uv.lock` generated.

- [ ] **Step 4: Create config.py**

```python
# server/src/chat_server/config.py
import os
from dataclasses import dataclass


@dataclass
class Settings:
    grpc_port: int = int(os.getenv("GRPC_PORT", "50051"))
    metrics_port: int = int(os.getenv("METRICS_PORT", "9090"))
    ollama_url: str = os.getenv("OLLAMA_URL", "http://localhost:11434")
    ollama_model: str = os.getenv("OLLAMA_MODEL", "qwen3:0.6b")
    auth_enabled: bool = os.getenv("AUTH_ENABLED", "true").lower() == "true"
    auth_token: str = os.getenv("AUTH_TOKEN", "demo-token")
    otel_endpoint: str = os.getenv("OTEL_ENDPOINT", "localhost:4317")
    otel_enabled: bool = os.getenv("OTEL_ENABLED", "false").lower() == "true"


settings = Settings()
```

- [ ] **Step 5: Create __init__.py files**

```bash
touch server/src/chat_server/__init__.py
mkdir -p server/src/chat_server/interceptors
touch server/src/chat_server/interceptors/__init__.py
```

- [ ] **Step 6: Commit**

```bash
git add server/
git commit -m "feat: python server project setup with uv"
```

---

### Task 3: Ollama Client

**Files:**
- Create: `server/src/chat_server/ollama.py`
- Create: `server/tests/test_ollama.py`

- [ ] **Step 1: Write failing test for Ollama streaming**

```python
# server/tests/test_ollama.py
import asyncio
import json
import pytest
from unittest.mock import AsyncMock, MagicMock, patch
from chat_server.ollama import OllamaClient


@pytest.fixture
def client():
    return OllamaClient(base_url="http://fake:11434", model="qwen3:0.6b")


def _make_sse_lines(tokens: list[str]) -> list[str]:
    lines = []
    for t in tokens:
        lines.append(json.dumps({"message": {"content": t}, "done": False}))
    lines.append(json.dumps({"message": {"content": ""}, "done": True}))
    return lines


@pytest.mark.asyncio
async def test_chat_streams_tokens(client):
    mock_response = AsyncMock()
    mock_response.raise_for_status = MagicMock()

    async def fake_aiter_lines():
        for line in _make_sse_lines(["Hello", " world", "!"]):
            yield line

    mock_response.aiter_lines = fake_aiter_lines
    mock_response.__aenter__ = AsyncMock(return_value=mock_response)
    mock_response.__aexit__ = AsyncMock(return_value=False)

    with patch.object(client._client, "stream", return_value=mock_response):
        tokens = []
        async for token in client.chat("test"):
            tokens.append(token)

    assert tokens == ["Hello", " world", "!"]
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/test_ollama.py -v
```

Expected: FAIL — `ModuleNotFoundError: No module named 'chat_server.ollama'`

- [ ] **Step 3: Implement ollama.py**

```python
# server/src/chat_server/ollama.py
import json
from typing import AsyncIterator

import httpx


class OllamaClient:
    def __init__(self, base_url: str = "http://localhost:11434", model: str = "qwen3:0.6b"):
        self._model = model
        self._client = httpx.AsyncClient(base_url=base_url, timeout=120.0)

    async def chat(self, message: str, conversation_history: list[dict] | None = None) -> AsyncIterator[str]:
        """Stream chat tokens from Ollama."""
        messages = conversation_history or []
        messages.append({"role": "user", "content": message})

        async with self._client.stream(
            "POST",
            "/api/chat",
            json={
                "model": self._model,
                "messages": messages,
                "stream": True,
            },
        ) as response:
            response.raise_for_status()
            async for line in response.aiter_lines():
                if not line:
                    continue
                data = json.loads(line)
                if "message" in data and "content" in data["message"]:
                    content = data["message"]["content"]
                    if content:
                        yield content
                if data.get("done"):
                    break

    async def close(self):
        await self._client.aclose()
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/test_ollama.py -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/src/chat_server/ollama.py server/tests/
git commit -m "feat: add Ollama streaming client"
```

---

### Task 4: ChatService Implementation

**Files:**
- Create: `server/src/chat_server/service.py`
- Create: `server/tests/test_service.py`

- [ ] **Step 1: Write failing test for SendMessage**

```python
# server/tests/test_service.py
import asyncio
import pytest
import grpc
from unittest.mock import AsyncMock, patch, MagicMock
from chat_server.service import ChatServiceServicer


@pytest.fixture
def servicer():
    return ChatServiceServicer(ollama_url="http://fake:11434", ollama_model="qwen3:0.6b")


@pytest.fixture
def mock_context():
    ctx = AsyncMock()
    ctx.abort = AsyncMock(side_effect=grpc.RpcError)
    return ctx


@pytest.mark.asyncio
async def test_send_message(servicer, mock_context):
    from chat.v1 import chat_pb2

    async def fake_chat(msg, history=None):
        for token in ["Hello", " from", " Ollama"]:
            yield token

    with patch.object(servicer._ollama, "chat", side_effect=fake_chat):
        request = chat_pb2.SendMessageRequest(conversation_id="test-1", text="hi")
        response = await servicer.SendMessage(request, mock_context)

    assert response.conversation_id == "test-1"
    assert response.text == "Hello from Ollama"
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/test_service.py::test_send_message -v
```

Expected: FAIL — `ModuleNotFoundError: No module named 'chat_server.service'`

- [ ] **Step 3: Implement service.py**

```python
# server/src/chat_server/service.py
import asyncio
import grpc
from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.ollama import OllamaClient


class ChatServiceServicer(chat_pb2_grpc.ChatServiceServicer):
    def __init__(self, ollama_url: str, ollama_model: str):
        self._ollama = OllamaClient(base_url=ollama_url, model=ollama_model)

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
        """Bidirectional streaming RPC with cancel support."""
        send_queue: asyncio.Queue = asyncio.Queue()
        cancel_event = asyncio.Event()
        generation_task: asyncio.Task | None = None

        async def read_client_messages():
            nonlocal generation_task
            try:
                async for msg in request_iterator:
                    action = msg.WhichOneof("action")

                    if action == "user_message":
                        # Cancel any in-flight generation
                        if generation_task and not generation_task.done():
                            cancel_event.set()
                            generation_task.cancel()
                            try:
                                await generation_task
                            except asyncio.CancelledError:
                                pass

                        cancel_event.clear()
                        generation_task = asyncio.create_task(
                            self._generate(
                                msg.conversation_id,
                                msg.user_message.text,
                                send_queue,
                                cancel_event,
                            )
                        )

                    elif action == "cancel":
                        cancel_event.set()
                        if generation_task and not generation_task.done():
                            generation_task.cancel()
                            try:
                                await generation_task
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
                                ack=chat_pb2.Acknowledgement(
                                    acknowledged_type="context_injection"
                                ),
                            )
                        )
            finally:
                # Client closed stream — signal the sender to stop
                await send_queue.put(None)

        reader_task = asyncio.create_task(read_client_messages())

        try:
            while True:
                response = await send_queue.get()
                if response is None:
                    break
                yield response
        finally:
            reader_task.cancel()
            if generation_task and not generation_task.done():
                generation_task.cancel()

    async def _generate(
        self,
        conversation_id: str,
        text: str,
        queue: asyncio.Queue,
        cancel_event: asyncio.Event,
    ):
        """Stream tokens from Ollama into the send queue."""
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

        try:
            async for token_text in self._ollama.chat(text):
                if cancel_event.is_set():
                    break
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

        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_DONE),
            )
        )
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/test_service.py -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/src/chat_server/service.py server/tests/test_service.py
git commit -m "feat: implement ChatService with bidi streaming and cancel"
```

---

### Task 5: Server Interceptors

**Files:**
- Create: `server/src/chat_server/interceptors/logging.py`
- Create: `server/src/chat_server/interceptors/auth.py`
- Create: `server/src/chat_server/interceptors/otel.py`
- Create: `server/src/chat_server/interceptors/prometheus.py`

- [ ] **Step 1: Create logging interceptor**

```python
# server/src/chat_server/interceptors/logging.py
import time
import grpc
import structlog

log = structlog.get_logger()


class LoggingInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        start = time.monotonic()
        log.info("rpc.start", method=method)

        handler = await continuation(handler_call_details)
        if handler is None:
            return None

        if handler.unary_unary:
            original = handler.unary_unary

            async def logged_unary(request, context):
                try:
                    response = await original(request, context)
                    duration = round((time.monotonic() - start) * 1000, 2)
                    log.info("rpc.end", method=method, duration_ms=duration, status="OK")
                    return response
                except Exception as e:
                    duration = round((time.monotonic() - start) * 1000, 2)
                    log.error("rpc.error", method=method, duration_ms=duration, error=str(e))
                    raise

            return handler._replace(unary_unary=logged_unary)

        if handler.stream_stream:
            original = handler.stream_stream

            async def logged_stream(request_iterator, context):
                msg_count = 0
                try:
                    async for response in original(request_iterator, context):
                        msg_count += 1
                        yield response
                    duration = round((time.monotonic() - start) * 1000, 2)
                    log.info(
                        "rpc.end",
                        method=method,
                        duration_ms=duration,
                        status="OK",
                        server_messages=msg_count,
                    )
                except Exception as e:
                    duration = round((time.monotonic() - start) * 1000, 2)
                    log.error(
                        "rpc.error",
                        method=method,
                        duration_ms=duration,
                        error=str(e),
                        server_messages=msg_count,
                    )
                    raise

            return handler._replace(stream_stream=logged_stream)

        return handler
```

- [ ] **Step 2: Create auth interceptor**

```python
# server/src/chat_server/interceptors/auth.py
import grpc
import structlog

log = structlog.get_logger()


class AuthInterceptor(grpc.aio.ServerInterceptor):
    def __init__(self, token: str):
        self._token = token

    async def intercept_service(self, continuation, handler_call_details):
        metadata = dict(handler_call_details.invocation_metadata or [])
        auth_header = metadata.get("authorization", "")

        handler = await continuation(handler_call_details)
        if handler is None:
            return None

        if auth_header != f"Bearer {self._token}":
            log.warning("auth.rejected", method=handler_call_details.method)

            if handler.unary_unary:
                async def deny_unary(request, context):
                    await context.abort(
                        grpc.StatusCode.UNAUTHENTICATED, "Invalid or missing bearer token"
                    )

                return handler._replace(unary_unary=deny_unary)

            if handler.stream_stream:
                async def deny_stream(request_iterator, context):
                    await context.abort(
                        grpc.StatusCode.UNAUTHENTICATED, "Invalid or missing bearer token"
                    )

                return handler._replace(stream_stream=deny_stream)

        return handler
```

- [ ] **Step 3: Create OpenTelemetry interceptor**

```python
# server/src/chat_server/interceptors/otel.py
import grpc
from opentelemetry import trace
from opentelemetry.trace import StatusCode
from opentelemetry.context import attach, detach
from opentelemetry.propagate import extract

tracer = trace.get_tracer("chat_server")


class OTelInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        metadata = dict(handler_call_details.invocation_metadata or [])

        ctx = extract(metadata)
        token = attach(ctx)

        handler = await continuation(handler_call_details)
        if handler is None:
            detach(token)
            return None

        if handler.unary_unary:
            original = handler.unary_unary

            async def traced_unary(request, context):
                with tracer.start_as_current_span(method, kind=trace.SpanKind.SERVER) as span:
                    try:
                        response = await original(request, context)
                        span.set_status(StatusCode.OK)
                        return response
                    except Exception as e:
                        span.set_status(StatusCode.ERROR, str(e))
                        span.record_exception(e)
                        raise
                    finally:
                        detach(token)

            return handler._replace(unary_unary=traced_unary)

        if handler.stream_stream:
            original = handler.stream_stream

            async def traced_stream(request_iterator, context):
                with tracer.start_as_current_span(method, kind=trace.SpanKind.SERVER) as span:
                    try:
                        async for response in original(request_iterator, context):
                            yield response
                        span.set_status(StatusCode.OK)
                    except Exception as e:
                        span.set_status(StatusCode.ERROR, str(e))
                        span.record_exception(e)
                        raise
                    finally:
                        detach(token)

            return handler._replace(stream_stream=traced_stream)

        detach(token)
        return handler
```

- [ ] **Step 4: Create Prometheus interceptor**

```python
# server/src/chat_server/interceptors/prometheus.py
import time
import grpc
from prometheus_client import Counter, Histogram, Gauge

GRPC_SERVER_HANDLED = Counter(
    "grpc_server_handled_total", "Total RPCs completed", ["method", "status"]
)
GRPC_SERVER_DURATION = Histogram(
    "grpc_server_handling_seconds", "RPC duration in seconds", ["method"]
)
GRPC_SERVER_ACTIVE_STREAMS = Gauge(
    "grpc_server_active_streams", "Number of active streaming RPCs"
)


class PrometheusInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        start = time.monotonic()

        handler = await continuation(handler_call_details)
        if handler is None:
            return None

        if handler.unary_unary:
            original = handler.unary_unary

            async def metered_unary(request, context):
                try:
                    response = await original(request, context)
                    GRPC_SERVER_HANDLED.labels(method=method, status="OK").inc()
                    GRPC_SERVER_DURATION.labels(method=method).observe(
                        time.monotonic() - start
                    )
                    return response
                except Exception as e:
                    GRPC_SERVER_HANDLED.labels(method=method, status="ERROR").inc()
                    GRPC_SERVER_DURATION.labels(method=method).observe(
                        time.monotonic() - start
                    )
                    raise

            return handler._replace(unary_unary=metered_unary)

        if handler.stream_stream:
            original = handler.stream_stream

            async def metered_stream(request_iterator, context):
                GRPC_SERVER_ACTIVE_STREAMS.inc()
                try:
                    async for response in original(request_iterator, context):
                        yield response
                    GRPC_SERVER_HANDLED.labels(method=method, status="OK").inc()
                except Exception as e:
                    GRPC_SERVER_HANDLED.labels(method=method, status="ERROR").inc()
                    raise
                finally:
                    GRPC_SERVER_ACTIVE_STREAMS.dec()
                    GRPC_SERVER_DURATION.labels(method=method).observe(
                        time.monotonic() - start
                    )

            return handler._replace(stream_stream=metered_stream)

        return handler
```

- [ ] **Step 5: Verify interceptors import correctly**

```bash
cd server && PYTHONPATH=../gen/python uv run python -c "
from chat_server.interceptors.logging import LoggingInterceptor
from chat_server.interceptors.auth import AuthInterceptor
from chat_server.interceptors.otel import OTelInterceptor
from chat_server.interceptors.prometheus import PrometheusInterceptor
print('All interceptors imported successfully')
"
```

Expected: prints success message.

- [ ] **Step 6: Commit**

```bash
git add server/src/chat_server/interceptors/
git commit -m "feat: add gRPC server interceptors (logging, auth, otel, prometheus)"
```

---

### Task 6: Server Main, Health, Reflection & Dockerfile

**Files:**
- Create: `server/src/chat_server/main.py`
- Create: `server/Dockerfile`

- [ ] **Step 1: Create main.py**

```python
# server/src/chat_server/main.py
import asyncio
import os
import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from grpc_reflection.v1alpha import reflection
from prometheus_client import start_http_server

from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.config import settings
from chat_server.service import ChatServiceServicer
from chat_server.interceptors.logging import LoggingInterceptor
from chat_server.interceptors.auth import AuthInterceptor
from chat_server.interceptors.otel import OTelInterceptor
from chat_server.interceptors.prometheus import PrometheusInterceptor


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

    # Build interceptor chain: Logging → Auth → OTEL → Prometheus → Handler
    interceptors = [LoggingInterceptor()]
    if settings.auth_enabled:
        interceptors.append(AuthInterceptor(token=settings.auth_token))
    if settings.otel_enabled:
        interceptors.append(OTelInterceptor())
    interceptors.append(PrometheusInterceptor())

    server = grpc.aio.server(interceptors=interceptors)

    # Register ChatService
    servicer = ChatServiceServicer(
        ollama_url=settings.ollama_url,
        ollama_model=settings.ollama_model,
    )
    chat_pb2_grpc.add_ChatServiceServicer_to_server(servicer, server)

    # Register health service
    health_servicer = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)
    health_servicer.set("chat.v1.ChatService", health_pb2.HealthCheckResponse.SERVING)
    health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)

    # Enable reflection
    service_names = [
        chat_pb2.DESCRIPTOR.services_by_name["ChatService"].full_name,
        health_pb2.DESCRIPTOR.services_by_name["Health"].full_name,
        reflection.SERVICE_NAME,
    ]
    reflection.enable_server_reflection(service_names, server)

    # Start Prometheus metrics HTTP server (runs in a background thread)
    start_http_server(settings.metrics_port)

    listen_addr = f"[::]:{settings.grpc_port}"
    server.add_insecure_port(listen_addr)
    hostname = os.getenv("HOSTNAME", "local")
    print(f"Server {hostname} listening on {listen_addr}")
    print(f"Metrics on :{settings.metrics_port}")

    await server.start()
    await server.wait_for_termination()


if __name__ == "__main__":
    asyncio.run(serve())
```

- [ ] **Step 2: Test server starts locally (requires Ollama running)**

```bash
cd server && PYTHONPATH=../gen/python uv run python -m chat_server.main &
SERVER_PID=$!
sleep 2

# Test reflection
grpcurl -plaintext localhost:50051 list
# Expected output includes: chat.v1.ChatService, grpc.health.v1.Health

# Test health
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
# Expected: {"status":"SERVING"}

kill $SERVER_PID
```

- [ ] **Step 3: Create Dockerfile**

```dockerfile
# server/Dockerfile
FROM python:3.12-slim AS base

WORKDIR /app

# Install uv
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

# Install dependencies first (cache layer)
COPY server/pyproject.toml server/uv.lock* server/
RUN cd server && uv sync --frozen --no-dev

# Copy generated proto stubs
COPY gen/python gen/python
# Ensure __init__.py files exist
RUN touch gen/python/__init__.py gen/python/chat/__init__.py gen/python/chat/v1/__init__.py

# Copy server source
COPY server/src server/src

ENV PYTHONPATH=/app/gen/python
WORKDIR /app/server

CMD ["uv", "run", "--no-sync", "python", "-m", "chat_server.main"]
```

- [ ] **Step 4: Verify Docker build**

```bash
docker build -f server/Dockerfile -t chat-server:latest .
```

Expected: builds successfully.

- [ ] **Step 5: Commit**

```bash
git add server/src/chat_server/main.py server/Dockerfile
git commit -m "feat: server main with health check, reflection, and Dockerfile"
```

---

### Task 7: Go Project Setup & gRPC Client Layer

**Files:**
- Create: `go.mod`
- Create: `client/grpcclient/client.go`
- Create: `client/grpcclient/stream.go`

- [ ] **Step 1: Initialize Go module**

```bash
go mod init github.com/michal-derdak/chat
```

- [ ] **Step 2: Add dependencies**

```bash
go get google.golang.org/grpc
go get google.golang.org/protobuf
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/bubbles
go get github.com/charmbracelet/lipgloss
go get github.com/grpc-ecosystem/grpc-gateway/v2
```

- [ ] **Step 3: Create client/grpcclient/client.go**

```go
// client/grpcclient/client.go
package grpcclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
)

type Config struct {
	Target  string
	Token   string
	UseTLS  bool
	Timeout time.Duration
}

func NewChatClient(cfg Config) (chatv1.ChatServiceClient, *grpc.ClientConn, error) {
	var opts []grpc.DialOption

	if cfg.UseTLS {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if cfg.Token != "" {
		opts = append(opts,
			grpc.WithUnaryInterceptor(authUnaryInterceptor(cfg.Token)),
			grpc.WithStreamInterceptor(authStreamInterceptor(cfg.Token)),
		)
	}

	// Logging interceptors
	opts = append(opts,
		grpc.WithChainUnaryInterceptor(loggingUnaryInterceptor()),
		grpc.WithChainStreamInterceptor(loggingStreamInterceptor()),
	)

	conn, err := grpc.NewClient(cfg.Target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", cfg.Target, err)
	}

	return chatv1.NewChatServiceClient(conn), conn, nil
}

func authUnaryInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func authStreamInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

func loggingUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		err := invoker(ctx, method, req, reply, cc, opts...)
		log.Printf("[gRPC] %s %v (%s)", method, err, time.Since(start))
		return err
	}
}

func loggingStreamInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		log.Printf("[gRPC] stream open: %s", method)
		return streamer(ctx, desc, cc, method, opts...)
	}
}
```

- [ ] **Step 4: Create client/grpcclient/stream.go**

```go
// client/grpcclient/stream.go
package grpcclient

import (
	"context"
	"fmt"
	"io"
	"time"

	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
)

type StreamClient struct {
	stream chatv1.ChatService_ChatClient
	cancel context.CancelFunc
}

func OpenStream(client chatv1.ChatServiceClient, timeout time.Duration) (*StreamClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	stream, err := client.Chat(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open stream: %w", err)
	}

	return &StreamClient{stream: stream, cancel: cancel}, nil
}

func (s *StreamClient) Send(msg *chatv1.ChatRequest) error {
	return s.stream.Send(msg)
}

func (s *StreamClient) Recv() (*chatv1.ChatResponse, error) {
	return s.stream.Recv()
}

func (s *StreamClient) Close() {
	s.stream.CloseSend()
	s.cancel()
}

func (s *StreamClient) IsEOF(err error) bool {
	return err == io.EOF
}
```

- [ ] **Step 5: Tidy and verify compilation**

```bash
go mod tidy
go build ./client/grpcclient/
```

Expected: compiles without errors.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum client/grpcclient/
git commit -m "feat: Go gRPC client layer with auth and logging interceptors"
```

---

### Task 8: Go Bubble Tea TUI & Main

**Files:**
- Create: `client/tui/styles.go`
- Create: `client/tui/model.go`
- Create: `client/tui/commands.go`
- Create: `client/main.go`

- [ ] **Step 1: Create client/tui/styles.go**

```go
// client/tui/styles.go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	UserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	AssistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12"))

	StatusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("248")).
			Padding(0, 1)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)

	InputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("205"))
)
```

- [ ] **Step 2: Create client/tui/commands.go**

```go
// client/tui/commands.go
package tui

import (
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
	"github.com/michal-derdak/chat/client/grpcclient"
)

// --- Bubble Tea messages ---

type TokenMsg struct{ Text string }
type StatusMsg struct{ Phase string }
type ErrorMsg struct{ Err error }
type StreamEndMsg struct{}
type AckMsg struct{ Type string }

// WaitForEvent returns a command that blocks on the next server event.
func WaitForEvent(sc *grpcclient.StreamClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := sc.Recv()
		if err != nil {
			if sc.IsEOF(err) {
				return StreamEndMsg{}
			}
			return ErrorMsg{Err: fmt.Errorf("recv: %w", err)}
		}

		switch evt := resp.Event.(type) {
		case *chatv1.ChatResponse_Token:
			return TokenMsg{Text: evt.Token.GetText()}
		case *chatv1.ChatResponse_Status:
			return StatusMsg{Phase: evt.Status.GetPhase().String()}
		case *chatv1.ChatResponse_Error:
			return ErrorMsg{Err: fmt.Errorf("server: %s", evt.Error.GetMessage())}
		case *chatv1.ChatResponse_Ack:
			return AckMsg{Type: evt.Ack.GetAcknowledgedType()}
		case *chatv1.ChatResponse_Heartbeat:
			return StatusMsg{Phase: "heartbeat"}
		default:
			return nil
		}
	}
}

// SendMessage sends a user message on the bidi stream.
func SendMessage(sc *grpcclient.StreamClient, conversationID, text string) tea.Cmd {
	return func() tea.Msg {
		err := sc.Send(&chatv1.ChatRequest{
			ConversationId: conversationID,
			Action: &chatv1.ChatRequest_UserMessage{
				UserMessage: &chatv1.UserMessage{Text: text},
			},
		})
		if err != nil && err != io.EOF {
			return ErrorMsg{Err: fmt.Errorf("send: %w", err)}
		}
		return nil
	}
}

// SendCancel sends a cancel signal on the bidi stream.
func SendCancel(sc *grpcclient.StreamClient, conversationID string) tea.Cmd {
	return func() tea.Msg {
		err := sc.Send(&chatv1.ChatRequest{
			ConversationId: conversationID,
			Action: &chatv1.ChatRequest_Cancel{
				Cancel: &chatv1.CancelGeneration{},
			},
		})
		if err != nil && err != io.EOF {
			return ErrorMsg{Err: fmt.Errorf("send cancel: %w", err)}
		}
		return nil
	}
}
```

- [ ] **Step 3: Create client/tui/model.go**

```go
// client/tui/model.go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/michal-derdak/chat/client/grpcclient"
)

type ChatMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

type Model struct {
	input          textinput.Model
	viewport       viewport.Model
	messages       []ChatMessage
	streaming      bool
	status         string
	err            error
	stream         *grpcclient.StreamClient
	conversationID string
	ready          bool
	width          int
	height         int
}

func NewModel(stream *grpcclient.StreamClient, conversationID string) Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message... (Enter to send, Ctrl+C to cancel, Esc to quit)"
	ti.Focus()
	ti.Width = 80

	return Model{
		input:          ti,
		messages:       []ChatMessage{},
		status:         "connected",
		stream:         stream,
		conversationID: conversationID,
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := 1 // status bar
		inputHeight := 1
		verticalMargin := headerHeight + inputHeight + 2

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMargin)
			m.viewport.SetContent(m.renderMessages())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMargin
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.streaming {
				m.status = "cancelling..."
				return m, SendCancel(m.stream, m.conversationID)
			}
			return m, tea.Quit
		case "esc":
			m.stream.Close()
			return m, tea.Quit
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.streaming {
				return m, nil
			}
			m.input.Reset()
			m.messages = append(m.messages, ChatMessage{Role: "user", Content: text})
			m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: ""})
			m.streaming = true
			m.status = "sending..."
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()

			return m, tea.Batch(
				SendMessage(m.stream, m.conversationID, text),
				WaitForEvent(m.stream),
			)
		}

	case TokenMsg:
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.Role == "assistant" {
				last.Content += msg.Text
			}
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, WaitForEvent(m.stream)

	case StatusMsg:
		m.status = msg.Phase
		if msg.Phase == "PHASE_DONE" {
			m.streaming = false
			m.status = "ready"
		}
		return m, WaitForEvent(m.stream)

	case AckMsg:
		m.status = fmt.Sprintf("ack: %s", msg.Type)
		if msg.Type == "cancel" {
			m.streaming = false
			m.status = "cancelled"
		}
		return m, WaitForEvent(m.stream)

	case ErrorMsg:
		m.err = msg.Err
		m.streaming = false
		m.status = fmt.Sprintf("error: %s", msg.Err)
		return m, nil

	case StreamEndMsg:
		m.streaming = false
		m.status = "stream ended"
		return m, nil
	}

	// Update text input
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	statusBar := StatusBarStyle.Width(m.width).Render(
		fmt.Sprintf(" %s | streaming: %v", m.status, m.streaming),
	)

	return fmt.Sprintf("%s\n%s\n%s",
		statusBar,
		m.viewport.View(),
		m.input.View(),
	)
}

func (m Model) renderMessages() string {
	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			b.WriteString(UserStyle.Render("You: "))
			b.WriteString(msg.Content)
		case "assistant":
			b.WriteString(AssistantStyle.Render("AI: "))
			b.WriteString(msg.Content)
			if m.streaming && &msg == &m.messages[len(m.messages)-1] {
				b.WriteString("▌")
			}
		}
		b.WriteString("\n\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Create client/main.go**

```go
// client/main.go
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/michal-derdak/chat/client/grpcclient"
	"github.com/michal-derdak/chat/client/tui"
)

func main() {
	target := flag.String("target", "localhost:50051", "gRPC server address")
	token := flag.String("token", "demo-token", "Bearer token for auth")
	useTLS := flag.Bool("tls", false, "Use TLS")
	timeout := flag.Duration("timeout", 30*time.Minute, "Stream timeout")
	flag.Parse()

	// Redirect log output to a file so it doesn't interfere with TUI
	logFile, err := os.OpenFile("client.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	client, conn, err := grpcclient.NewChatClient(grpcclient.Config{
		Target:  *target,
		Token:   *token,
		UseTLS:  *useTLS,
		Timeout: *timeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	stream, err := grpcclient.OpenStream(client, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open stream: %v\n", err)
		os.Exit(1)
	}

	model := tui.NewModel(stream, "conversation-1")
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Verify compilation**

```bash
go mod tidy
go build ./client/
```

Expected: compiles without errors.

- [ ] **Step 6: Commit**

```bash
git add client/ go.mod go.sum
git commit -m "feat: Go Bubble Tea TUI client with bidi streaming"
```

---

### Task 9: gRPC Gateway & Dockerfile

**Files:**
- Create: `gateway/main.go`
- Create: `gateway/Dockerfile`

- [ ] **Step 1: Create gateway/main.go**

```go
// gateway/main.go
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
)

func main() {
	grpcAddr := flag.String("grpc-addr", "localhost:50051", "gRPC server address")
	httpAddr := flag.String("http-addr", ":8080", "HTTP listen address")
	flag.Parse()

	ctx := context.Background()
	mux := runtime.NewServeMux()

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	err := chatv1.RegisterChatServiceHandlerFromEndpoint(ctx, mux, *grpcAddr, opts)
	if err != nil {
		log.Fatalf("Failed to register gateway: %v", err)
	}

	log.Printf("Gateway listening on %s, proxying gRPC at %s", *httpAddr, *grpcAddr)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
```

- [ ] **Step 2: Create gateway/Dockerfile**

```dockerfile
# gateway/Dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY gen/go gen/go
COPY gateway gateway
RUN CGO_ENABLED=0 go build -o /gateway ./gateway

FROM alpine:3.20
COPY --from=build /gateway /gateway
ENTRYPOINT ["/gateway"]
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./gateway/
```

Expected: compiles without errors.

- [ ] **Step 4: Verify Docker build**

```bash
docker build -f gateway/Dockerfile -t chat-gateway:latest .
```

Expected: builds successfully.

- [ ] **Step 5: Commit**

```bash
git add gateway/
git commit -m "feat: gRPC-Gateway for HTTP/JSON transcoding"
```

---

### Task 10: Kind Cluster & Base Manifests

**Files:**
- Create: `deploy/kind-config.yaml`
- Create: `deploy/base/ollama-service.yaml`
- Create: `deploy/base/server-deployment.yaml`
- Create: `deploy/base/server-service.yaml`
- Create: `deploy/base/gateway-deployment.yaml`
- Create: `deploy/base/gateway-service.yaml`
- Create: `deploy/base/kustomization.yaml`

- [ ] **Step 1: Create deploy/kind-config.yaml**

```yaml
# deploy/kind-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      # chat-grpc namespace
      - containerPort: 30051
        hostPort: 50051
        protocol: TCP
      # chat-envoy namespace
      - containerPort: 30052
        hostPort: 50052
        protocol: TCP
      # gRPC-Gateway
      - containerPort: 30080
        hostPort: 8080
        protocol: TCP
      # Jaeger UI
      - containerPort: 30686
        hostPort: 16686
        protocol: TCP
      # Prometheus
      - containerPort: 30090
        hostPort: 9090
        protocol: TCP
```

- [ ] **Step 2: Create deploy/base/ollama-service.yaml**

```yaml
# deploy/base/ollama-service.yaml
apiVersion: v1
kind: Service
metadata:
  name: ollama
spec:
  type: ExternalName
  externalName: host.docker.internal
```

- [ ] **Step 3: Create deploy/base/server-deployment.yaml**

```yaml
# deploy/base/server-deployment.yaml
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
      containers:
        - name: chat-server
          image: chat-server:latest
          imagePullPolicy: Never
          ports:
            - containerPort: 50051
              name: grpc
            - containerPort: 9090
              name: metrics
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

- [ ] **Step 4: Create deploy/base/server-service.yaml**

```yaml
# deploy/base/server-service.yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-server
spec:
  selector:
    app: chat-server
  ports:
    - name: grpc
      port: 50051
      targetPort: 50051
    - name: metrics
      port: 9090
      targetPort: 9090
```

- [ ] **Step 5: Create deploy/base/gateway-deployment.yaml**

```yaml
# deploy/base/gateway-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chat-gateway
spec:
  replicas: 1
  selector:
    matchLabels:
      app: chat-gateway
  template:
    metadata:
      labels:
        app: chat-gateway
    spec:
      containers:
        - name: chat-gateway
          image: chat-gateway:latest
          imagePullPolicy: Never
          ports:
            - containerPort: 8080
              name: http
          args:
            - "--grpc-addr=chat-server:50051"
            - "--http-addr=:8080"
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "128Mi"
              cpu: "200m"
```

- [ ] **Step 6: Create deploy/base/gateway-service.yaml**

```yaml
# deploy/base/gateway-service.yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-gateway
spec:
  selector:
    app: chat-gateway
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

- [ ] **Step 7: Create deploy/base/kustomization.yaml**

```yaml
# deploy/base/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ollama-service.yaml
  - server-deployment.yaml
  - server-service.yaml
  - gateway-deployment.yaml
  - gateway-service.yaml
```

- [ ] **Step 8: Commit**

```bash
git add deploy/
git commit -m "feat: Kind cluster config and base K8s manifests"
```

---

### Task 11: chat-grpc Namespace Deployment

**Files:**
- Create: `deploy/grpc/namespace.yaml`
- Create: `deploy/grpc/server-service-nodeport.yaml`
- Create: `deploy/grpc/gateway-service-nodeport.yaml`
- Create: `deploy/grpc/kustomization.yaml`

- [ ] **Step 1: Create deploy/grpc/namespace.yaml**

```yaml
# deploy/grpc/namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: chat-grpc
```

- [ ] **Step 2: Create deploy/grpc/server-service-nodeport.yaml**

```yaml
# deploy/grpc/server-service-nodeport.yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-server
spec:
  type: NodePort
  selector:
    app: chat-server
  ports:
    - name: grpc
      port: 50051
      targetPort: 50051
      nodePort: 30051
    - name: metrics
      port: 9090
      targetPort: 9090
```

- [ ] **Step 3: Create deploy/grpc/gateway-service-nodeport.yaml**

```yaml
# deploy/grpc/gateway-service-nodeport.yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-gateway
spec:
  type: NodePort
  selector:
    app: chat-gateway
  ports:
    - name: http
      port: 8080
      targetPort: 8080
      nodePort: 30080
```

- [ ] **Step 4: Create deploy/grpc/kustomization.yaml**

```yaml
# deploy/grpc/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: chat-grpc

resources:
  - namespace.yaml
  - ../base

patches:
  - path: server-service-nodeport.yaml
  - path: gateway-service-nodeport.yaml
```

- [ ] **Step 5: Validate with dry-run**

```bash
kubectl kustomize deploy/grpc/
```

Expected: renders complete YAML with namespace set to `chat-grpc`.

- [ ] **Step 6: Commit**

```bash
git add deploy/grpc/
git commit -m "feat: chat-grpc namespace with NodePort services"
```

---

### Task 12: Envoy Namespace Deployment

**Files:**
- Create: `deploy/envoy/namespace.yaml`
- Create: `deploy/envoy/certs/generate-certs.sh`
- Create: `deploy/envoy/envoy-configmap.yaml`
- Create: `deploy/envoy/envoy-deployment.yaml`
- Create: `deploy/envoy/envoy-service.yaml`
- Create: `deploy/envoy/server-patch.yaml`
- Create: `deploy/envoy/kustomization.yaml`

- [ ] **Step 1: Create deploy/envoy/namespace.yaml**

```yaml
# deploy/envoy/namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: chat-envoy
```

- [ ] **Step 2: Create deploy/envoy/certs/generate-certs.sh**

```bash
#!/usr/bin/env bash
# deploy/envoy/certs/generate-certs.sh
# Generate self-signed CA and server/client certs for mTLS demo
set -euo pipefail

DIR="$(cd "$(dirname "$0")/generated" 2>/dev/null || mkdir -p "$(dirname "$0")/generated" && cd "$(dirname "$0")/generated" && pwd)"

echo "Generating certs in $DIR"

# CA
openssl req -x509 -newkey rsa:2048 -keyout "$DIR/ca.key" -out "$DIR/ca.crt" \
  -days 365 -nodes -subj "/CN=chat-demo-ca" 2>/dev/null

# Server cert
openssl req -newkey rsa:2048 -keyout "$DIR/server.key" -out "$DIR/server.csr" \
  -nodes -subj "/CN=chat-server" 2>/dev/null
openssl x509 -req -in "$DIR/server.csr" -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" \
  -CAcreateserial -out "$DIR/server.crt" -days 365 \
  -extfile <(printf "subjectAltName=DNS:chat-server,DNS:chat-server.chat-envoy.svc.cluster.local,DNS:envoy,DNS:localhost") 2>/dev/null

# Client cert
openssl req -newkey rsa:2048 -keyout "$DIR/client.key" -out "$DIR/client.csr" \
  -nodes -subj "/CN=chat-client" 2>/dev/null
openssl x509 -req -in "$DIR/client.csr" -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" \
  -CAcreateserial -out "$DIR/client.crt" -days 365 2>/dev/null

rm -f "$DIR"/*.csr "$DIR"/*.srl
echo "Done: ca.crt, server.crt, server.key, client.crt, client.key"
```

```bash
chmod +x deploy/envoy/certs/generate-certs.sh
```

- [ ] **Step 3: Create deploy/envoy/envoy-configmap.yaml**

```yaml
# deploy/envoy/envoy-configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: envoy-config
data:
  envoy.yaml: |
    admin:
      address:
        socket_address: { address: 0.0.0.0, port_value: 9901 }

    static_resources:
      listeners:
        - name: grpc_listener
          address:
            socket_address: { address: 0.0.0.0, port_value: 50051 }
          filter_chains:
            - transport_socket:
                name: envoy.transport_sockets.tls
                typed_config:
                  "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext
                  require_client_certificate: true
                  common_tls_context:
                    alpn_protocols: ["h2"]
                    tls_certificates:
                      - certificate_chain: { filename: /etc/envoy/certs/server.crt }
                        private_key: { filename: /etc/envoy/certs/server.key }
                    validation_context:
                      trusted_ca: { filename: /etc/envoy/certs/ca.crt }
              filters:
                - name: envoy.filters.network.http_connection_manager
                  typed_config:
                    "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                    stat_prefix: grpc
                    codec_type: AUTO
                    # Long-lived stream support
                    stream_idle_timeout: 1800s
                    route_config:
                      name: local_route
                      virtual_hosts:
                        - name: chat
                          domains: ["*"]
                          routes:
                            - match: { prefix: "/" }
                              route:
                                cluster: chat_service
                                timeout: 0s
                                max_stream_duration:
                                  max_stream_duration: 1800s
                    http_filters:
                      - name: envoy.filters.http.router
                        typed_config:
                          "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router

      clusters:
        - name: chat_service
          type: STRICT_DNS
          lb_policy: ROUND_ROBIN
          typed_extension_protocol_options:
            envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
              "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
              explicit_http_config:
                http2_protocol_options:
                  connection_keepalive:
                    interval: 30s
                    timeout: 5s
          load_assignment:
            cluster_name: chat_service
            endpoints:
              - lb_endpoints:
                  - endpoint:
                      address:
                        socket_address: { address: chat-server, port_value: 50051 }
          health_checks:
            - timeout: 5s
              interval: 10s
              unhealthy_threshold: 3
              healthy_threshold: 2
              grpc_health_check:
                service_name: "chat.v1.ChatService"
```

- [ ] **Step 4: Create deploy/envoy/envoy-deployment.yaml**

```yaml
# deploy/envoy/envoy-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: envoy
spec:
  replicas: 1
  selector:
    matchLabels:
      app: envoy
  template:
    metadata:
      labels:
        app: envoy
    spec:
      containers:
        - name: envoy
          image: envoyproxy/envoy:v1.31-latest
          ports:
            - containerPort: 50051
              name: grpc
            - containerPort: 9901
              name: admin
          volumeMounts:
            - name: config
              mountPath: /etc/envoy/envoy.yaml
              subPath: envoy.yaml
              readOnly: true
            - name: certs
              mountPath: /etc/envoy/certs
              readOnly: true
          command: ["envoy"]
          args: ["-c", "/etc/envoy/envoy.yaml"]
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "256Mi"
              cpu: "250m"
      volumes:
        - name: config
          configMap:
            name: envoy-config
        - name: certs
          secret:
            secretName: envoy-certs
```

- [ ] **Step 5: Create deploy/envoy/envoy-service.yaml**

```yaml
# deploy/envoy/envoy-service.yaml
apiVersion: v1
kind: Service
metadata:
  name: envoy
spec:
  type: NodePort
  selector:
    app: envoy
  ports:
    - name: grpc
      port: 50051
      targetPort: 50051
      nodePort: 30052
    - name: admin
      port: 9901
      targetPort: 9901
```

- [ ] **Step 6: Create deploy/envoy/server-patch.yaml**

This disables the auth interceptor in the Envoy namespace since Envoy handles mTLS:

```yaml
# deploy/envoy/server-patch.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chat-server
spec:
  template:
    spec:
      containers:
        - name: chat-server
          env:
            - name: AUTH_ENABLED
              value: "false"
```

- [ ] **Step 7: Create deploy/envoy/kustomization.yaml**

```yaml
# deploy/envoy/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: chat-envoy

resources:
  - namespace.yaml
  - ../base
  - envoy-configmap.yaml
  - envoy-deployment.yaml
  - envoy-service.yaml

patches:
  - path: server-patch.yaml
    target:
      kind: Deployment
      name: chat-server
```

- [ ] **Step 8: Validate with dry-run**

```bash
kubectl kustomize deploy/envoy/
```

Expected: renders complete YAML with namespace `chat-envoy` and auth disabled on server.

- [ ] **Step 9: Commit**

```bash
git add deploy/envoy/
git commit -m "feat: Envoy namespace with mTLS, round-robin LB, and stream management"
```

---

### Task 13: Observability Stack

**Files:**
- Create: `deploy/observability/namespace.yaml`
- Create: `deploy/observability/otel-collector/`
- Create: `deploy/observability/jaeger/`
- Create: `deploy/observability/prometheus/`
- Create: `deploy/observability/kustomization.yaml`

- [ ] **Step 1: Create namespace**

```yaml
# deploy/observability/namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: observability
```

- [ ] **Step 2: Create OTEL Collector**

```yaml
# deploy/observability/otel-collector/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: otel-collector-config
data:
  config.yaml: |
    receivers:
      otlp:
        protocols:
          grpc:
            endpoint: 0.0.0.0:4317
    exporters:
      otlp/jaeger:
        endpoint: jaeger:4317
        tls:
          insecure: true
    service:
      pipelines:
        traces:
          receivers: [otlp]
          exporters: [otlp/jaeger]
---
# deploy/observability/otel-collector/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: otel-collector
spec:
  replicas: 1
  selector:
    matchLabels:
      app: otel-collector
  template:
    metadata:
      labels:
        app: otel-collector
    spec:
      containers:
        - name: otel-collector
          image: otel/opentelemetry-collector-contrib:0.115.0
          args: ["--config=/etc/otel/config.yaml"]
          ports:
            - containerPort: 4317
              name: otlp-grpc
          volumeMounts:
            - name: config
              mountPath: /etc/otel
      volumes:
        - name: config
          configMap:
            name: otel-collector-config
---
# deploy/observability/otel-collector/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: otel-collector
spec:
  selector:
    app: otel-collector
  ports:
    - name: otlp-grpc
      port: 4317
      targetPort: 4317
```

- [ ] **Step 3: Create Jaeger**

```yaml
# deploy/observability/jaeger/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jaeger
spec:
  replicas: 1
  selector:
    matchLabels:
      app: jaeger
  template:
    metadata:
      labels:
        app: jaeger
    spec:
      containers:
        - name: jaeger
          image: jaegertracing/all-in-one:1.63
          ports:
            - containerPort: 4317
              name: otlp-grpc
            - containerPort: 16686
              name: ui
          env:
            - name: COLLECTOR_OTLP_ENABLED
              value: "true"
---
# deploy/observability/jaeger/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: jaeger
spec:
  type: NodePort
  selector:
    app: jaeger
  ports:
    - name: otlp-grpc
      port: 4317
      targetPort: 4317
    - name: ui
      port: 16686
      targetPort: 16686
      nodePort: 30686
```

- [ ] **Step 4: Create Prometheus**

```yaml
# deploy/observability/prometheus/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: prometheus-config
data:
  prometheus.yml: |
    global:
      scrape_interval: 15s
    scrape_configs:
      - job_name: 'chat-server-grpc'
        kubernetes_sd_configs:
          - role: pod
            namespaces:
              names: ['chat-grpc']
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
            action: keep
            regex: true
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_port]
            action: replace
            regex: (.+)
            target_label: __address__
            replacement: $1
          - source_labels: [__meta_kubernetes_pod_ip, __meta_kubernetes_pod_annotation_prometheus_io_port]
            action: replace
            regex: (.+);(.+)
            target_label: __address__
            replacement: $1:$2
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - source_labels: [__meta_kubernetes_pod_name]
            target_label: pod
      - job_name: 'chat-server-envoy'
        kubernetes_sd_configs:
          - role: pod
            namespaces:
              names: ['chat-envoy']
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
            action: keep
            regex: true
          - source_labels: [__meta_kubernetes_pod_ip, __meta_kubernetes_pod_annotation_prometheus_io_port]
            action: replace
            regex: (.+);(.+)
            target_label: __address__
            replacement: $1:$2
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - source_labels: [__meta_kubernetes_pod_name]
            target_label: pod
      - job_name: 'envoy-admin'
        kubernetes_sd_configs:
          - role: pod
            namespaces:
              names: ['chat-envoy']
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_label_app]
            action: keep
            regex: envoy
          - source_labels: [__meta_kubernetes_pod_ip]
            action: replace
            target_label: __address__
            replacement: $1:9901
        metrics_path: /stats/prometheus
---
# deploy/observability/prometheus/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prometheus
spec:
  replicas: 1
  selector:
    matchLabels:
      app: prometheus
  template:
    metadata:
      labels:
        app: prometheus
    spec:
      serviceAccountName: prometheus
      containers:
        - name: prometheus
          image: prom/prometheus:v3.0.1
          ports:
            - containerPort: 9090
          volumeMounts:
            - name: config
              mountPath: /etc/prometheus
      volumes:
        - name: config
          configMap:
            name: prometheus-config
---
# deploy/observability/prometheus/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: prometheus
spec:
  type: NodePort
  selector:
    app: prometheus
  ports:
    - port: 9090
      targetPort: 9090
      nodePort: 30090
---
# deploy/observability/prometheus/rbac.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: prometheus
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: prometheus
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: prometheus
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: prometheus
subjects:
  - kind: ServiceAccount
    name: prometheus
    namespace: observability
```

- [ ] **Step 5: Create deploy/observability/kustomization.yaml**

```yaml
# deploy/observability/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: observability

resources:
  - namespace.yaml
  - otel-collector/configmap.yaml
  - otel-collector/deployment.yaml
  - otel-collector/service.yaml
  - jaeger/deployment.yaml
  - jaeger/service.yaml
  - prometheus/configmap.yaml
  - prometheus/deployment.yaml
  - prometheus/service.yaml
  - prometheus/rbac.yaml
```

- [ ] **Step 6: Validate**

```bash
kubectl kustomize deploy/observability/
```

Expected: renders all observability manifests under `observability` namespace.

- [ ] **Step 7: Commit**

```bash
git add deploy/observability/
git commit -m "feat: observability stack (OTEL Collector, Jaeger, Prometheus)"
```

---

### Task 14: Makefile

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Create Makefile**

```makefile
# Makefile
.PHONY: generate lint breaking cluster build load deploy-grpc deploy-envoy \
        deploy-observability deploy-all port-forward client client-envoy \
        certs clean

# --- Code Generation ---
generate:
	buf dep update
	buf generate
	# gRPC Python stubs (not available as buf protoc_builtin)
	cd server && uv run python -m grpc_tools.protoc \
		-I ../proto \
		--grpc_python_out=../gen/python \
		../proto/chat/v1/chat.proto
	# Ensure Python __init__.py files exist
	touch gen/python/__init__.py gen/python/chat/__init__.py gen/python/chat/v1/__init__.py

lint:
	buf lint

breaking:
	buf breaking --against '.git#branch=main'

# --- Kind Cluster ---
cluster:
	kind create cluster --name chat-demo --config deploy/kind-config.yaml
	@echo "Cluster created. Loading images..."

clean:
	kind delete cluster --name chat-demo

# --- Docker Build ---
build:
	docker build -f server/Dockerfile -t chat-server:latest .
	docker build -f gateway/Dockerfile -t chat-gateway:latest .

load: build
	kind load docker-image chat-server:latest --name chat-demo
	kind load docker-image chat-gateway:latest --name chat-demo

# --- Deploy ---
deploy-observability:
	kubectl apply -k deploy/observability/

deploy-grpc: load
	kubectl apply -k deploy/grpc/
	@echo "Waiting for rollout..."
	kubectl -n chat-grpc rollout status deployment/chat-server --timeout=120s
	kubectl -n chat-grpc rollout status deployment/chat-gateway --timeout=120s

deploy-envoy: load certs
	# Create TLS secret from generated certs
	kubectl create namespace chat-envoy --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n chat-envoy create secret generic envoy-certs \
		--from-file=ca.crt=deploy/envoy/certs/generated/ca.crt \
		--from-file=server.crt=deploy/envoy/certs/generated/server.crt \
		--from-file=server.key=deploy/envoy/certs/generated/server.key \
		--dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -k deploy/envoy/
	@echo "Waiting for rollout..."
	kubectl -n chat-envoy rollout status deployment/chat-server --timeout=120s
	kubectl -n chat-envoy rollout status deployment/envoy --timeout=120s

deploy-all: deploy-observability deploy-grpc deploy-envoy
	@echo "All deployed."

certs:
	deploy/envoy/certs/generate-certs.sh

# --- Client ---
client:
	go run ./client/ --target localhost:50051 --token demo-token

client-envoy:
	go run ./client/ --target localhost:50052 --token demo-token --tls

# --- Demo Helpers ---
grpcurl-list:
	grpcurl -plaintext localhost:50051 list

grpcurl-health:
	grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check

grpcurl-send:
	grpcurl -plaintext -d '{"conversation_id":"test","text":"Hello"}' \
		localhost:50051 chat.v1.ChatService/SendMessage

curl-send:
	curl -X POST http://localhost:8080/v1/chat/send \
		-H 'Content-Type: application/json' \
		-d '{"conversation_id":"test","text":"Hello"}'

# --- Logs ---
logs-grpc:
	kubectl -n chat-grpc logs -l app=chat-server --all-containers -f

logs-envoy:
	kubectl -n chat-envoy logs -l app=chat-server --all-containers -f

logs-envoy-proxy:
	kubectl -n chat-envoy logs -l app=envoy -f
```

- [ ] **Step 2: Verify targets parse**

```bash
make -n generate
make -n lint
```

Expected: prints the commands without executing.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat: Makefile with all orchestration targets"
```

---

### Task 15: Update .gitignore

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Add project-specific ignores**

Append to `.gitignore`:

```
# Generated code (regenerate with `make generate`)
gen/

# Generated certs
deploy/envoy/certs/generated/

# Client debug log
client.log

# Kind cluster config
kubeconfig

# Go binary
client/client
gateway/gateway

# OpenAPI generated
gen/openapiv2/
```

- [ ] **Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: update gitignore for generated code and certs"
```

---

### Task 16: Presentation Slides

**Files:**
- Create: `presentation/slides.md`

- [ ] **Step 1: Write the full slide deck**

Create `presentation/slides.md` with one topic per slide, `---` separated, 3-5 bullet points per slide. Full content is in the presentation section of the spec. Key slides:

1. Title
2. Agenda
3-6. gRPC fundamentals (what, RPC types, HTTP/2, protobuf)
7-8. Buf ecosystem + demo
9-10. Bidi streaming + human-in-the-loop
11. Live demo: chat
12-14. Interceptors, auth, context management
15-16. Gateway + demo
17. Health checking & reflection
18. Error model & retries
19-20. Envoy + config walkthrough
21. Demo: pure gRPC vs Envoy
22. Scaling on K8s
23. Observability
24. Benefits & caveats
25. Q&A

Each slide should have speaker notes with demo commands where relevant (e.g., `grpcurl` commands, `kubectl` commands).

- [ ] **Step 2: Commit**

```bash
git add presentation/
git commit -m "feat: presentation slide deck"
```

---

## Verification Checklist

After all tasks are complete, run through this end-to-end:

1. `ollama pull qwen3:0.6b && ollama serve` — start Ollama on host
2. `make cluster` — create Kind cluster
3. `make generate` — generate stubs
4. `make deploy-all` — deploy everything
5. `make client` — run TUI, send a message, verify streaming tokens
6. `make client-envoy` — run against Envoy namespace, verify mTLS
7. `make grpcurl-list` — verify reflection works
8. `make grpcurl-health` — verify health check
9. `make curl-send` — verify gateway transcoding
10. Open `http://localhost:16686` — verify Jaeger traces
11. Open `http://localhost:9090` — verify Prometheus metrics
12. Send multiple messages → `make logs-grpc` — verify pick-first (one pod)
13. Send multiple messages → `make logs-envoy` — verify round-robin (all pods)
14. `buf lint` — passes
15. `buf breaking --against '.git#branch=main'` — passes
