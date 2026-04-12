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
