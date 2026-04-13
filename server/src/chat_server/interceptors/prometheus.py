import asyncio
import time
import grpc
from prometheus_client import Counter, Histogram, Gauge

GRPC_SERVER_HANDLED = Counter("grpc_server_handled_total", "Total RPCs completed", ["method", "status"])
GRPC_SERVER_DURATION = Histogram("grpc_server_handling_seconds", "RPC duration in seconds", ["method"])
GRPC_SERVER_ACTIVE_STREAMS = Gauge("grpc_server_active_streams", "Number of active streaming RPCs")


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
                    result = original(request, context)
                    response = await result if asyncio.iscoroutine(result) else result
                    GRPC_SERVER_HANDLED.labels(method=method, status="OK").inc()
                    GRPC_SERVER_DURATION.labels(method=method).observe(time.monotonic() - start)
                    return response
                except Exception as e:
                    GRPC_SERVER_HANDLED.labels(method=method, status="ERROR").inc()
                    GRPC_SERVER_DURATION.labels(method=method).observe(time.monotonic() - start)
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
                    GRPC_SERVER_DURATION.labels(method=method).observe(time.monotonic() - start)

            return handler._replace(stream_stream=metered_stream)

        return handler
