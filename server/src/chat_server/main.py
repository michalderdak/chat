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

    # Build interceptor chain: Logging -> Auth -> OTEL -> Prometheus -> Handler
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
    await servicer.initialize()
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
