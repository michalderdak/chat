import asyncio
import grpc
from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.ollama import OllamaClient


class ChatServiceServicer(chat_pb2_grpc.ChatServiceServicer):
    def __init__(self, ollama_url: str, ollama_model: str):
        self._ollama = OllamaClient(base_url=ollama_url, model=ollama_model)
        self._context_length: int = 0

    async def initialize(self):
        """Call once at startup to cache model info."""
        self._context_length = await self._ollama.get_model_context_length()

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
        """Bidirectional streaming RPC with cancel, history, usage, and heartbeat."""
        send_queue: asyncio.Queue = asyncio.Queue()
        cancel_event = asyncio.Event()
        generation_task: asyncio.Task | None = None
        conversation_history: list[dict] = []

        async def heartbeat_loop():
            """Send a playful heartbeat word every 15 seconds."""
            try:
                while True:
                    await asyncio.sleep(15)
                    word = await self._ollama.generate_heartbeat_word()
                    await send_queue.put(
                        chat_pb2.ChatResponse(
                            conversation_id="",
                            heartbeat=chat_pb2.Heartbeat(beat=word),
                        )
                    )
            except asyncio.CancelledError:
                pass

        heartbeat_task = asyncio.create_task(heartbeat_loop())

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
                        conversation_history.append(
                            {"role": "user", "content": msg.user_message.text}
                        )
                        generation_task = asyncio.create_task(
                            self._generate(
                                msg.conversation_id,
                                msg.user_message.text,
                                send_queue,
                                cancel_event,
                                conversation_history,
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
                await send_queue.put(None)

        reader_task = asyncio.create_task(read_client_messages())

        try:
            while True:
                response = await send_queue.get()
                if response is None:
                    break
                yield response
        finally:
            heartbeat_task.cancel()
            reader_task.cancel()
            if generation_task and not generation_task.done():
                generation_task.cancel()

    async def _generate(
        self,
        conversation_id: str,
        text: str,
        queue: asyncio.Queue,
        cancel_event: asyncio.Event,
        conversation_history: list[dict],
    ):
        """Stream tokens from Ollama into the send queue, then report usage."""
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

        accumulated_response = ""
        try:
            async for token_text in self._ollama.chat(
                text, conversation_history=conversation_history[:-1]
            ):
                if cancel_event.is_set():
                    break
                accumulated_response += token_text
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

        # Record assistant response in history (even if partial/cancelled)
        if accumulated_response:
            conversation_history.append(
                {"role": "assistant", "content": accumulated_response}
            )

        # Send usage info
        usage = self._ollama.last_usage
        if usage:
            await queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=conversation_id,
                    usage=chat_pb2.UsageInfo(
                        prompt_tokens=usage.get("prompt_eval_count", 0),
                        completion_tokens=usage.get("eval_count", 0),
                        context_length=self._context_length,
                    ),
                )
            )

        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_DONE),
            )
        )
