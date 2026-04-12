import grpc
from opentelemetry import trace
from opentelemetry.trace import StatusCode
from opentelemetry.context import attach, detach
from opentelemetry.propagate import extract

tracer = trace.get_tracer("chat_server")


class OTelInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        metadata = dict(handler_call_details.invocation_metadata or [])

        ctx = extract(metadata)
        token = attach(ctx)

        handler = await continuation(handler_call_details)
        if handler is None:
            detach(token)
            return None

        if handler.unary_unary:
            original = handler.unary_unary

            async def traced_unary(request, context):
                with tracer.start_as_current_span(method, kind=trace.SpanKind.SERVER) as span:
                    try:
                        response = await original(request, context)
                        span.set_status(StatusCode.OK)
                        return response
                    except Exception as e:
                        span.set_status(StatusCode.ERROR, str(e))
                        span.record_exception(e)
                        raise
                    finally:
                        detach(token)

            return handler._replace(unary_unary=traced_unary)

        if handler.stream_stream:
            original = handler.stream_stream

            async def traced_stream(request_iterator, context):
                with tracer.start_as_current_span(method, kind=trace.SpanKind.SERVER) as span:
                    try:
                        async for response in original(request_iterator, context):
                            yield response
                        span.set_status(StatusCode.OK)
                    except Exception as e:
                        span.set_status(StatusCode.ERROR, str(e))
                        span.record_exception(e)
                        raise
                    finally:
                        detach(token)

            return handler._replace(stream_stream=traced_stream)

        detach(token)
        return handler
