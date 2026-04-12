# server/Dockerfile
FROM python:3.12-slim AS base

WORKDIR /app

# Install uv
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

# Install dependencies first (cache layer)
COPY server/pyproject.toml server/uv.lock* server/
RUN cd server && uv sync --frozen --no-dev --no-install-project

# Copy generated proto stubs
COPY gen/python gen/python
# Ensure __init__.py files exist
RUN touch gen/python/__init__.py gen/python/chat/__init__.py gen/python/chat/v1/__init__.py

# Copy server source
COPY server/src server/src

ENV PYTHONPATH=/app/gen/python
WORKDIR /app/server

CMD ["uv", "run", "--no-sync", "python", "-m", "chat_server.main"]
