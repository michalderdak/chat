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


@pytest.mark.asyncio
async def test_chat_returns_usage_stats(client):
    """The final done:true chunk should yield usage stats."""
    lines = [
        json.dumps({"message": {"content": "Hi"}, "done": False}),
        json.dumps({
            "message": {"content": ""},
            "done": True,
            "prompt_eval_count": 11,
            "eval_count": 5,
        }),
    ]
    mock_response = AsyncMock()
    mock_response.raise_for_status = MagicMock()

    async def fake_aiter_lines():
        for line in lines:
            yield line

    mock_response.aiter_lines = fake_aiter_lines
    mock_response.__aenter__ = AsyncMock(return_value=mock_response)
    mock_response.__aexit__ = AsyncMock(return_value=False)

    with patch.object(client._client, "stream", return_value=mock_response):
        tokens = []
        async for token in client.chat("test"):
            tokens.append(token)

    assert tokens == ["Hi"]
    assert client.last_usage == {"prompt_eval_count": 11, "eval_count": 5}
