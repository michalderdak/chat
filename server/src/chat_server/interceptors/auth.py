import grpc
import structlog

log = structlog.get_logger()


_SKIP_AUTH_PREFIXES = (
    "/grpc.health.v1.Health/",
    "/grpc.reflection.v1alpha.ServerReflection/",
    "/grpc.reflection.v1.ServerReflection/",
)


class AuthInterceptor(grpc.aio.ServerInterceptor):
    def __init__(self, token: str):
        self._token = token

    async def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        if any(method.startswith(prefix) for prefix in _SKIP_AUTH_PREFIXES):
            return await continuation(handler_call_details)

        metadata = dict(handler_call_details.invocation_metadata or [])
        auth_header = metadata.get("authorization", "")

        handler = await continuation(handler_call_details)
        if handler is None:
            return None

        if auth_header != f"Bearer {self._token}":
            log.warning("auth.rejected", method=handler_call_details.method)

            if handler.unary_unary:
                async def deny_unary(request, context):
                    await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Invalid or missing bearer token")
                return handler._replace(unary_unary=deny_unary)

            if handler.stream_stream:
                async def deny_stream(request_iterator, context):
                    await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Invalid or missing bearer token")
                return handler._replace(stream_stream=deny_stream)

        return handler
