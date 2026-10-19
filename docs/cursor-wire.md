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

Production calls require a host client. A test-injected `http.Client` exercises
native TLS HTTP/2 and `io.Pipe` at `/agent.v1.AgentService/Run` in local protocol
fixtures; it is not a production fallback. Host callers always use host transport.

Model discovery collapses effort variants into one row per base model and the
executor resolves the upstream variant per request from the `model(level)`
suffix, payload `reasoning_effort`, or native client thinking fields
(see [thinking switcher](thinking-switcher.md)).

Initial and stateless requests rebuild the full caller transcript. Completed turn
structures and model-visible JSON root prompts are
both content-addressed into a bounded per-run blob store. System instructions
are also published in a user `<rules>` message, because Cursor discards root
system-role entries. A latest user message goes in UserMessageAction; a
tool-result-ended transcript goes in ResumeAction. Compaction after a model switch
replays complete historical exchanges before the final user summary prompt, whether
the current tool catalogue is empty, disabled or different. Assistant prefills and
incomplete tool exchanges remain rejected. The current `requested_model`
field is used without legacy model_details or custom_system_prompt.

On hosts without retained HTTP support, MCP execution yields OpenAI `tool_calls`
and closes the upstream run immediately. The caller executes the tool, supplies
its result in the next request, and the executor republishes it in both turn state
and root prompts. This stateless fallback returns one MCP call per run.

With negotiated retained HTTP version 1, primary requests at
`/v1/chat/completions` collect a 250 ms burst and prepare the host scope for parking.
The host commits only at successful terminal response write/flush. The plugin keeps
the actual RunSSE transport and BidiAppend sequence for the following tool result
request. Stored public IDs map each result to its exact upstream execution ID pair.
The complete newest batch is validated before any append. Full-history replay
never resends old completed results into a pending execution. A missing final
assistant call list can be restored only from the existing owned pending batch.

The actual OpenCode session and host-derived caller/account scope own retained
state. Model, credential, system rules and exact original client catalogue are
compatibility boundaries. Shared affinity and parent headers do not identify a
live run. Missing identity/purpose, non-primary requests and unverified client
protocols remain stateless. A new user instruction retires pending work and starts
from the completed caller history.

Native
filesystem, shell and subagent exec requests get typed rejection/error replies with
the registered client alternatives and schema guidance. Unknown native operations
get an explicit ExecClientThrow. Repeated native or unavailable client calls stop
after eight refused replies rather than looping until the execution timeout.
Supported native interaction queries receive correlated typed rejections and the
run can continue through genuine registered client tools. Search, fetch, mode
changes, questions and planning are refused without inventing answers, consent or
success. These replies share the same eight-refusal bound. VM setup, absent query
types and unrecognised protocol shapes fail closed. No native operation is executed.

Only caller-supplied function tools are executable. The request policy and MCP
server instructions list their advertised names. Cursor's GetDynamicTools and
CallDynamicTool are catalogue controls, not OpenCode executable tool names. Raw,
advertised and provider-qualified names map back to the same client function with
collision checks. Tool-free requests advertise no executable client entries and
disable optional native web search. Cursor may still show upstream-native tools
that cannot be hidden by this protocol, so a native refusal explicitly distinguishes
that limitation from the availability of client tools.

The same executable-client policy is delivered in root rules, MCP instructions
and the native RequestContext global-rule field, including requested context
refreshes. Native global rules are protocol data, not a file written on the host.
They do not enable filesystem access, native tools or interactive approvals.

With the existing host debug logger enabled, rejected calls produce bounded
`cursor_tool_rejected` JSON events. A locally generated run ID links at most eight
refused replies and one terminal event. Fields classify the compiled protocol
subtype, provider as empty/cpa/foreign, name resolution and argument shape.
Unknown protocol fields collapse to an unknown category, except the verified
field-9 fetch case. Names, arbitrary argument keys, values, upstream IDs, prompts,
skill text, paths and URLs are excluded. Diagnostics do not alter dispatch,
unwrap dynamic payloads or retry tools.

Frames and decompressed messages are limited to 16 MiB, blob stores to 64 MiB
and 8192 entries. Retained runs are limited to 24 with a 256 MiB global retained
blob budget. Parked runs expire after one hour without requiring another request.
Missing blobs fail rather than silently erase history.
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

Retained fixtures additionally verify multiple tool rounds on one Run, full skill
results and actual errors, late-call batching, omitted-call ownership, deterministic
expiry, cancellation during acquisition, shutdown, admission pressure and bounded
recovery. Recovery uses complete validated caller history, not an unverified
checkpoint. It retains the absolute step deadline, refuses permission/quota errors
and divergent output, suppresses already streamed prefixes, and allows at most
three recoveries with one after visible text. The local fixtures do not establish
live upstream compatibility or production deployment.
