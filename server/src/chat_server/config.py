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
    redis_url: str = os.getenv("REDIS_URL", "redis://redis:6379")


settings = Settings()
