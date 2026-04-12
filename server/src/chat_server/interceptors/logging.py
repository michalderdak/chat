import asyncio
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
                    result = original(request, context)
                    response = await result if asyncio.iscoroutine(result) else result
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
                    log.info("rpc.end", method=method, duration_ms=duration, status="OK", server_messages=msg_count)
                except Exception as e:
                    duration = round((time.monotonic() - start) * 1000, 2)
                    log.error("rpc.error", method=method, duration_ms=duration, error=str(e), server_messages=msg_count)
                    raise

            return handler._replace(stream_stream=logged_stream)

        return handler
