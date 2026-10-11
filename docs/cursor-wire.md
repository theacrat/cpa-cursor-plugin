# Cursor agent wire

The executor implements the MIT-attributed schema in `proto/agent.proto`, pinned
to pi-cursor ef3cafb5c38081ce6e06df8e594241aabd9621e3. The official Cursor CLI
release `2026.10.01-e373342` independently confirms BidiAppend fields, RunSSE,
the current client version, and the AiService discovery route.

When CPA provides `HostHTTPClient`, inference uses Connect server streaming at
`APIBaseURL/agent.v1.AgentService/RunSSE`, and serial unary writes at
`APIBaseURL/aiserver.v1.BidiService/BidiAppend`. The request ID is shared across
both routes; append sequence numbers start at zero. This keeps host proxy,
request logging and cancellation policy. The streaming input is a single
Connect-framed BidiRequestId; appends are protobuf with hex client-message data.

Standalone calls without a host client use a native TLS HTTP/2 `http.Client`
and `io.Pipe` at `/agent.v1.AgentService/Run`. This is the only native transport
exception. Per-auth attribute proxy wins over auth metadata, then host metadata
`proxy_url`, then standard environment proxies. `direct` explicitly bypasses
proxies. Host callers always use host transport, never silent native fallback.

Requests rebuild the full caller transcript rather than retaining upstream
sessions. Completed turn structures and model-visible JSON root prompts are
both content-addressed into a bounded per-run blob store. System instructions
are also published in a user `<rules>` message, because Cursor discards root
system-role entries. A latest user message goes in UserMessageAction; a
tool-result-ended transcript goes in ResumeAction. The current `requested_model`
field is used without legacy model_details or custom_system_prompt.

MCP execution yields OpenAI `tool_calls` and closes the upstream run immediately.
The caller executes the tool, supplies its result in the next request, and the
executor republishes it in both turn state and root prompts. One MCP call is
returned per run; multiple sequential tool round trips are supported. Native
filesystem, shell, computer, network and subagent exec requests get an explicit
ExecClientThrow. Interactive approval queries fail explicitly. No native tool
operation is executed, and no consent or success is fabricated.

Frames and decompressed messages are limited to 16 MiB, blob stores to 64 MiB
and 8192 entries. Missing blobs fail rather than silently erase history.
Active inference and discovery calls share bounded concurrency and deadlines;
Close cancels admission and waits for active work. Exact token counting and
arbitrary HTTP forwarding are unsupported. Usage contains only upstream token
deltas/checkpoints, not heuristic estimates. Unknown token counts remain zero.
Forced tool choice, multiple completions, remote image URLs and non-chat input
are rejected. Inline base64 image data URIs are supported.

Deterministic local HTTP/2 TLS tests verify duplex KV exchanges, context and
native rejection, text/thinking/tokens, tool continuation, gzip and fragmented
frames, cancellation and drain. Host fixtures verify official route/body/ID/
sequence semantics. These are protocol fixture checks, not a real-account
end-to-end claim.
