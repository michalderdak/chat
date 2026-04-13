import asyncio
import os
import grpc
import structlog
from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.ollama import OllamaClient
from chat_server.history import HistoryStore

log = structlog.get_logger()


class ActiveStream:
    """Tracks state for one active bidi stream."""

    def __init__(self, conversation_id: str, send_queue: asyncio.Queue):
        self.conversation_id = conversation_id
        self.send_queue = send_queue
        self.conversation_history: list[dict] = []
        self.generation_task: asyncio.Task | None = None


class ChatServiceServicer(chat_pb2_grpc.ChatServiceServicer):
    def __init__(self, ollama_url: str, ollama_model: str, history_store: HistoryStore):
        self._ollama = OllamaClient(base_url=ollama_url, model=ollama_model)
        self._history = history_store
        self._context_length: int = 0
        self._active_streams: set[ActiveStream] = set()
        self._draining = False

    async def initialize(self):
        self._context_length = await self._ollama.get_model_context_length()

    async def drain(self):
        self._draining = True
        log.info("drain.start", active_streams=len(self._active_streams))

        for stream_state in list(self._active_streams):
            if stream_state.generation_task and not stream_state.generation_task.done():
                try:
                    await asyncio.wait_for(stream_state.generation_task, timeout=20.0)
                except (asyncio.TimeoutError, asyncio.CancelledError):
                    stream_state.generation_task.cancel()
                    log.warning("drain.generation_timeout", conversation_id=stream_state.conversation_id)

        for stream_state in list(self._active_streams):
            if stream_state.conversation_history:
                await self._history.save(
                    stream_state.conversation_id,
                    stream_state.conversation_history,
                )
                log.info("drain.history_saved", conversation_id=stream_state.conversation_id)

            await stream_state.send_queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=stream_state.conversation_id,
                    shutdown=chat_pb2.ServerShutdown(reason="pod draining"),
                )
            )
            await stream_state.send_queue.put(None)

        log.info("drain.complete")

    async def SendMessage(self, request, context):
        try:
            full_response = ""
            async for token in self._ollama.chat(request.text):
                full_response += token
            hostname = os.environ.get("HOSTNAME", "unknown")
            context.set_trailing_metadata([("x-served-by", hostname)])
            return chat_pb2.SendMessageResponse(
                conversation_id=request.conversation_id,
                text=full_response,
            )
        except Exception as e:
            await context.abort(grpc.StatusCode.INTERNAL, str(e))

    async def Chat(self, request_iterator, context):
        send_queue: asyncio.Queue = asyncio.Queue()
        cancel_event = asyncio.Event()

        stream_state = ActiveStream(conversation_id="", send_queue=send_queue)
        self._active_streams.add(stream_state)

        async def heartbeat_loop():
            try:
                while True:
                    await asyncio.sleep(15)
                    word = await self._ollama.generate_heartbeat_word()
                    await send_queue.put(
                        chat_pb2.ChatResponse(
                            conversation_id=stream_state.conversation_id,
                            heartbeat=chat_pb2.Heartbeat(beat=word),
                        )
                    )
            except asyncio.CancelledError:
                pass

        heartbeat_task = asyncio.create_task(heartbeat_loop())

        async def read_client_messages():
            try:
                async for msg in request_iterator:
                    if stream_state.conversation_id == "":
                        stream_state.conversation_id = msg.conversation_id
                        stream_state.conversation_history = await self._history.load(
                            msg.conversation_id
                        )
                        if stream_state.conversation_history:
                            log.info(
                                "history.loaded",
                                conversation_id=msg.conversation_id,
                                messages=len(stream_state.conversation_history),
                            )

                    action = msg.WhichOneof("action")

                    if action == "user_message":
                        if self._draining:
                            continue

                        if stream_state.generation_task and not stream_state.generation_task.done():
                            cancel_event.set()
                            stream_state.generation_task.cancel()
                            try:
                                await stream_state.generation_task
                            except asyncio.CancelledError:
                                pass

                        cancel_event.clear()
                        stream_state.conversation_history.append(
                            {"role": "user", "content": msg.user_message.text}
                        )
                        stream_state.generation_task = asyncio.create_task(
                            self._generate(
                                msg.conversation_id,
                                msg.user_message.text,
                                send_queue,
                                cancel_event,
                                stream_state,
                            )
                        )

                    elif action == "cancel":
                        cancel_event.set()
                        if stream_state.generation_task and not stream_state.generation_task.done():
                            stream_state.generation_task.cancel()
                            try:
                                await stream_state.generation_task
                            except asyncio.CancelledError:
                                pass
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(acknowledged_type="cancel"),
                            )
                        )

                    elif action == "add_context":
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(acknowledged_type="context_injection"),
                            )
                        )
            finally:
                if not self._draining:
                    await send_queue.put(None)

        reader_task = asyncio.create_task(read_client_messages())

        try:
            while True:
                response = await send_queue.get()
                if response is None:
                    break
                yield response
        finally:
            self._active_streams.discard(stream_state)
            heartbeat_task.cancel()
            reader_task.cancel()
            if stream_state.generation_task and not stream_state.generation_task.done():
                stream_state.generation_task.cancel()

    async def _generate(
        self,
        conversation_id: str,
        text: str,
        queue: asyncio.Queue,
        cancel_event: asyncio.Event,
        stream_state: ActiveStream,
    ):
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
                text, conversation_history=stream_state.conversation_history[:-1]
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

        if accumulated_response:
            stream_state.conversation_history.append(
                {"role": "assistant", "content": accumulated_response}
            )

        await self._history.save(conversation_id, stream_state.conversation_history)

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
