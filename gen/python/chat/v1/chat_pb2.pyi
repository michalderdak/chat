from google.api import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Phase(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PHASE_UNSPECIFIED: _ClassVar[Phase]
    PHASE_THINKING: _ClassVar[Phase]
    PHASE_GENERATING: _ClassVar[Phase]
    PHASE_DONE: _ClassVar[Phase]
PHASE_UNSPECIFIED: Phase
PHASE_THINKING: Phase
PHASE_GENERATING: Phase
PHASE_DONE: Phase

class SendMessageRequest(_message.Message):
    __slots__ = ("conversation_id", "text")
    CONVERSATION_ID_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    conversation_id: str
    text: str
    def __init__(self, conversation_id: _Optional[str] = ..., text: _Optional[str] = ...) -> None: ...

class SendMessageResponse(_message.Message):
    __slots__ = ("conversation_id", "text")
    CONVERSATION_ID_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    conversation_id: str
    text: str
    def __init__(self, conversation_id: _Optional[str] = ..., text: _Optional[str] = ...) -> None: ...

class ChatRequest(_message.Message):
    __slots__ = ("conversation_id", "user_message", "cancel", "add_context")
    CONVERSATION_ID_FIELD_NUMBER: _ClassVar[int]
    USER_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CANCEL_FIELD_NUMBER: _ClassVar[int]
    ADD_CONTEXT_FIELD_NUMBER: _ClassVar[int]
    conversation_id: str
    user_message: UserMessage
    cancel: CancelGeneration
    add_context: ContextInjection
    def __init__(self, conversation_id: _Optional[str] = ..., user_message: _Optional[_Union[UserMessage, _Mapping]] = ..., cancel: _Optional[_Union[CancelGeneration, _Mapping]] = ..., add_context: _Optional[_Union[ContextInjection, _Mapping]] = ...) -> None: ...

class UserMessage(_message.Message):
    __slots__ = ("text",)
    TEXT_FIELD_NUMBER: _ClassVar[int]
    text: str
    def __init__(self, text: _Optional[str] = ...) -> None: ...

class CancelGeneration(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ContextInjection(_message.Message):
    __slots__ = ("text",)
    TEXT_FIELD_NUMBER: _ClassVar[int]
    text: str
    def __init__(self, text: _Optional[str] = ...) -> None: ...

class ChatResponse(_message.Message):
    __slots__ = ("conversation_id", "token", "status", "error", "heartbeat", "ack", "usage", "shutdown")
    CONVERSATION_ID_FIELD_NUMBER: _ClassVar[int]
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    HEARTBEAT_FIELD_NUMBER: _ClassVar[int]
    ACK_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    SHUTDOWN_FIELD_NUMBER: _ClassVar[int]
    conversation_id: str
    token: Token
    status: StatusUpdate
    error: Error
    heartbeat: Heartbeat
    ack: Acknowledgement
    usage: UsageInfo
    shutdown: ServerShutdown
    def __init__(self, conversation_id: _Optional[str] = ..., token: _Optional[_Union[Token, _Mapping]] = ..., status: _Optional[_Union[StatusUpdate, _Mapping]] = ..., error: _Optional[_Union[Error, _Mapping]] = ..., heartbeat: _Optional[_Union[Heartbeat, _Mapping]] = ..., ack: _Optional[_Union[Acknowledgement, _Mapping]] = ..., usage: _Optional[_Union[UsageInfo, _Mapping]] = ..., shutdown: _Optional[_Union[ServerShutdown, _Mapping]] = ...) -> None: ...

class Token(_message.Message):
    __slots__ = ("text",)
    TEXT_FIELD_NUMBER: _ClassVar[int]
    text: str
    def __init__(self, text: _Optional[str] = ...) -> None: ...

class StatusUpdate(_message.Message):
    __slots__ = ("phase",)
    PHASE_FIELD_NUMBER: _ClassVar[int]
    phase: Phase
    def __init__(self, phase: _Optional[_Union[Phase, str]] = ...) -> None: ...

class Error(_message.Message):
    __slots__ = ("code", "message")
    CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    code: int
    message: str
    def __init__(self, code: _Optional[int] = ..., message: _Optional[str] = ...) -> None: ...

class Heartbeat(_message.Message):
    __slots__ = ("beat",)
    BEAT_FIELD_NUMBER: _ClassVar[int]
    beat: str
    def __init__(self, beat: _Optional[str] = ...) -> None: ...

class Acknowledgement(_message.Message):
    __slots__ = ("acknowledged_type",)
    ACKNOWLEDGED_TYPE_FIELD_NUMBER: _ClassVar[int]
    acknowledged_type: str
    def __init__(self, acknowledged_type: _Optional[str] = ...) -> None: ...

class UsageInfo(_message.Message):
    __slots__ = ("prompt_tokens", "completion_tokens", "context_length")
    PROMPT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    COMPLETION_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CONTEXT_LENGTH_FIELD_NUMBER: _ClassVar[int]
    prompt_tokens: int
    completion_tokens: int
    context_length: int
    def __init__(self, prompt_tokens: _Optional[int] = ..., completion_tokens: _Optional[int] = ..., context_length: _Optional[int] = ...) -> None: ...

class ServerShutdown(_message.Message):
    __slots__ = ("reason",)
    REASON_FIELD_NUMBER: _ClassVar[int]
    reason: str
    def __init__(self, reason: _Optional[str] = ...) -> None: ...

class ConversationMessage(_message.Message):
    __slots__ = ("role", "content")
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    role: str
    content: str
    def __init__(self, role: _Optional[str] = ..., content: _Optional[str] = ...) -> None: ...

class ConversationHistory(_message.Message):
    __slots__ = ("messages",)
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    messages: _containers.RepeatedCompositeFieldContainer[ConversationMessage]
    def __init__(self, messages: _Optional[_Iterable[_Union[ConversationMessage, _Mapping]]] = ...) -> None: ...
