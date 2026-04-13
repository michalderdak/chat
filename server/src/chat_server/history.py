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
