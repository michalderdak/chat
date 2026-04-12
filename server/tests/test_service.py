import asyncio
import pytest
import grpc
from unittest.mock import AsyncMock, patch, MagicMock
from chat_server.service import ChatServiceServicer


@pytest.fixture
def servicer():
    return ChatServiceServicer(
        ollama_url="http://fake:11434", ollama_model="qwen3:0.6b"
    )


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


@pytest.mark.asyncio
async def test_send_message_error(servicer, mock_context):
    """SendMessage should call context.abort on Ollama errors."""
    from chat.v1 import chat_pb2

    async def failing_chat(msg, history=None):
        raise RuntimeError("connection refused")
        # Make it an async generator
        yield  # pragma: no cover

    with patch.object(servicer._ollama, "chat", side_effect=failing_chat):
        request = chat_pb2.SendMessageRequest(conversation_id="err-1", text="hi")
        with pytest.raises(grpc.RpcError):
            await servicer.SendMessage(request, mock_context)

    mock_context.abort.assert_called_once_with(
        grpc.StatusCode.INTERNAL, "connection refused"
    )


@pytest.mark.asyncio
async def test_chat_streaming(servicer, mock_context):
    """Chat bidi streaming should emit status updates and tokens."""
    from chat.v1 import chat_pb2

    async def fake_chat(msg, history=None):
        for token in ["Hi", " there"]:
            yield token

    async def fake_request_iterator():
        yield chat_pb2.ChatRequest(
            conversation_id="conv-1",
            user_message=chat_pb2.UserMessage(text="hello"),
        )
        # Give the generation task time to complete
        await asyncio.sleep(0.1)

    with patch.object(servicer._ollama, "chat", side_effect=fake_chat):
        responses = []
        async for resp in servicer.Chat(fake_request_iterator(), mock_context):
            responses.append(resp)

    # Expect: THINKING, GENERATING, token "Hi", token " there", DONE
    assert len(responses) == 5
    assert responses[0].status.phase == chat_pb2.PHASE_THINKING
    assert responses[1].status.phase == chat_pb2.PHASE_GENERATING
    assert responses[2].token.text == "Hi"
    assert responses[3].token.text == " there"
    assert responses[4].status.phase == chat_pb2.PHASE_DONE
    for r in responses:
        assert r.conversation_id == "conv-1"


@pytest.mark.asyncio
async def test_chat_cancel(servicer, mock_context):
    """Sending a cancel message should stop generation and send an ack."""
    from chat.v1 import chat_pb2

    generation_started = asyncio.Event()

    async def slow_chat(msg, history=None):
        generation_started.set()
        for token in ["a", "b", "c", "d", "e"]:
            await asyncio.sleep(0.05)
            yield token

    async def fake_request_iterator():
        yield chat_pb2.ChatRequest(
            conversation_id="conv-2",
            user_message=chat_pb2.UserMessage(text="hello"),
        )
        # Wait for generation to start, then cancel
        await generation_started.wait()
        await asyncio.sleep(0.05)
        yield chat_pb2.ChatRequest(
            conversation_id="conv-2",
            cancel=chat_pb2.CancelGeneration(),
        )

    with patch.object(servicer._ollama, "chat", side_effect=slow_chat):
        responses = []
        async for resp in servicer.Chat(fake_request_iterator(), mock_context):
            responses.append(resp)

    # Should have cancel ack somewhere in responses
    ack_responses = [r for r in responses if r.HasField("ack")]
    assert len(ack_responses) == 1
    assert ack_responses[0].ack.acknowledged_type == "cancel"


@pytest.mark.asyncio
async def test_chat_context_injection(servicer, mock_context):
    """Sending add_context should return a context_injection ack."""
    from chat.v1 import chat_pb2

    async def fake_request_iterator():
        yield chat_pb2.ChatRequest(
            conversation_id="conv-3",
            add_context=chat_pb2.ContextInjection(text="some context"),
        )

    responses = []
    async for resp in servicer.Chat(fake_request_iterator(), mock_context):
        responses.append(resp)

    assert len(responses) == 1
    assert responses[0].ack.acknowledged_type == "context_injection"
    assert responses[0].conversation_id == "conv-3"
