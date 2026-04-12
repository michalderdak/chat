import asyncio
import grpc
from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.ollama import OllamaClient


class ChatServiceServicer(chat_pb2_grpc.ChatServiceServicer):
    def __init__(self, ollama_url: str, ollama_model: str):
        self._ollama = OllamaClient(base_url=ollama_url, model=ollama_model)

    async def SendMessage(self, request, context):
        """Unary RPC: send message, get complete response."""
        try:
            full_response = ""
            async for token in self._ollama.chat(request.text):
                full_response += token
            return chat_pb2.SendMessageResponse(
                conversation_id=request.conversation_id,
                text=full_response,
            )
        except Exception as e:
            await context.abort(grpc.StatusCode.INTERNAL, str(e))

    async def Chat(self, request_iterator, context):
        """Bidirectional streaming RPC with cancel support."""
        send_queue: asyncio.Queue = asyncio.Queue()
        cancel_event = asyncio.Event()
        generation_task: asyncio.Task | None = None

        async def read_client_messages():
            nonlocal generation_task
            try:
                async for msg in request_iterator:
                    action = msg.WhichOneof("action")

                    if action == "user_message":
                        # Cancel any in-flight generation
                        if generation_task and not generation_task.done():
                            cancel_event.set()
                            generation_task.cancel()
                            try:
                                await generation_task
                            except asyncio.CancelledError:
                                pass

                        cancel_event.clear()
                        generation_task = asyncio.create_task(
                            self._generate(
                                msg.conversation_id,
                                msg.user_message.text,
                                send_queue,
                                cancel_event,
                            )
                        )

                    elif action == "cancel":
                        cancel_event.set()
                        if generation_task and not generation_task.done():
                            generation_task.cancel()
                            try:
                                await generation_task
                            except asyncio.CancelledError:
                                pass
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(
                                    acknowledged_type="cancel"
                                ),
                            )
                        )

                    elif action == "add_context":
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(
                                    acknowledged_type="context_injection"
                                ),
                            )
                        )
            finally:
                # Client closed stream — signal the sender to stop
                await send_queue.put(None)

        reader_task = asyncio.create_task(read_client_messages())

        try:
            while True:
                response = await send_queue.get()
                if response is None:
                    break
                yield response
        finally:
            reader_task.cancel()
            if generation_task and not generation_task.done():
                generation_task.cancel()

    async def _generate(
        self,
        conversation_id: str,
        text: str,
        queue: asyncio.Queue,
        cancel_event: asyncio.Event,
    ):
        """Stream tokens from Ollama into the send queue."""
        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_THINKING),
            )
        )
        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_GENERATING),
            )
        )

        try:
            async for token_text in self._ollama.chat(text):
                if cancel_event.is_set():
                    break
                await queue.put(
                    chat_pb2.ChatResponse(
                        conversation_id=conversation_id,
                        token=chat_pb2.Token(text=token_text),
                    )
                )
        except asyncio.CancelledError:
            pass
        except Exception as e:
            await queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=conversation_id,
                    error=chat_pb2.Error(code=13, message=str(e)),
                )
            )

        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_DONE),
            )
        )
