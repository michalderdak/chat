import json
from typing import AsyncIterator

import httpx


class OllamaClient:
    def __init__(self, base_url: str = "http://localhost:11434", model: str = "qwen3:0.6b"):
        self._model = model
        self._client = httpx.AsyncClient(
            base_url=base_url, timeout=120.0, headers={"Host": "localhost"}
        )
        self.last_usage: dict = {}
        self._context_length: int | None = None

    async def chat(self, message: str, conversation_history: list[dict] | None = None) -> AsyncIterator[str]:
        """Stream chat tokens from Ollama. After iteration, self.last_usage is set."""
        messages = list(conversation_history or [])
        messages.append({"role": "user", "content": message})
        self.last_usage = {}

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
                    self.last_usage = {
                        "prompt_eval_count": data.get("prompt_eval_count", 0),
                        "eval_count": data.get("eval_count", 0),
                    }
                    break

    async def get_model_context_length(self) -> int:
        """Get model context window size. Cached after first call."""
        if self._context_length is not None:
            return self._context_length

        response = await self._client.post("/api/show", json={"model": self._model})
        response.raise_for_status()
        data = response.json()
        model_info = data.get("model_info", {})
        for key, value in model_info.items():
            if "context_length" in key:
                self._context_length = int(value)
                return self._context_length
        self._context_length = 0
        return 0

    async def generate_heartbeat_word(self) -> str:
        """Generate a single playful gerund word via Ollama."""
        try:
            response = await self._client.post(
                "/api/generate",
                json={
                    "model": self._model,
                    "prompt": "Output only a single creative playful gerund word like shimmering, gallivanting, or percolating. Only the word, nothing else.",
                    "stream": False,
                },
                timeout=10.0,
            )
            response.raise_for_status()
            data = response.json()
            word = data.get("response", "...").strip().strip(".")
            return word.split()[0] if word else "..."
        except Exception:
            return "..."

    async def close(self):
        await self._client.aclose()
