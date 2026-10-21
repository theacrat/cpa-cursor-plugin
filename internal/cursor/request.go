package cursor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	maxFrame       = 16 << 20
	maxBlobs       = 64 << 20
	maxBlobEntries = 8192
	toolProvider   = "cpa"
)

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}
type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []toolCall      `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
	Reasoning  string          `json:"reasoning_content"`
	IsError    bool            `json:"is_error"`
}
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice      json.RawMessage `json:"tool_choice"`
	N               int             `json:"n"`
	MaxMode         bool            `json:"max_mode"`
	ModelParameters []struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	} `json:"cursor_model_parameters"`
}
type blobStore struct {
	data map[string][]byte
	size int
}

func newBlobs() *blobStore { return &blobStore{data: make(map[string][]byte)} }
func (s *blobStore) set(id, data []byte) error {
	if len(id) != 32 {
		return errors.New("Cursor blob ID must be SHA-256")
	}
	key := hex.EncodeToString(id)
	old, exists := s.data[key]
	if len(data) > maxFrame || s.size-len(old)+len(data) > maxBlobs || (!exists && len(s.data) >= maxBlobEntries) {
		return errors.New("Cursor blob store limit exceeded")
	}
	s.data[key] = append([]byte(nil), data...)
	s.size += len(data) - len(old)
	return nil
}
func (s *blobStore) put(data []byte) ([]byte, error) {
	sum := sha256.Sum256(data)
	if err := s.set(sum[:], data); err != nil {
		return nil, err
	}
	return sum[:], nil
}
func (s *blobStore) message(m proto.Message) ([]byte, error) {
	b, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	return s.put(b)
}
func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type builtRequest struct {
	message   *pb.AgentClientMessage
	blobs     *blobStore
	tools     []*pb.McpToolDefinition
	wire      map[string]string
	resolve   map[string]string
	callIDs   map[string]bool
	id, model string
}

func content(raw json.RawMessage) (string, []*pb.SelectedImage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, errors.New("unsupported message content")
	}
	var images []*pb.SelectedImage
	var texts []string
	for _, p := range parts {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "image_url":
			uri := p.ImageURL.URL
			head, encoded, ok := strings.Cut(uri, ",")
			if !ok || !strings.HasPrefix(head, "data:image/") || !strings.HasSuffix(head, ";base64") {
				return "", nil, errors.New("Cursor images require base64 image data URIs; remote URLs are unsupported")
			}
			if len(encoded) > maxFrame*4/3+4 {
				return "", nil, errors.New("Cursor image exceeds limit")
			}
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return "", nil, errors.New("invalid base64 image")
			}
			images = append(images, &pb.SelectedImage{Uuid: uuid(), MimeType: strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), DataOrBlobId: &pb.SelectedImage_Data{Data: data}})
		default:
			return "", nil, fmt.Errorf("unsupported content type %q", p.Type)
		}
	}
	return strings.Join(texts, "\n"), images, nil
}

func buildRequest(req pluginapi.ExecutorRequest) (*builtRequest, error) {
	if req.Format != "" && req.Format != "openai" && req.Format != "chat-completions" {
		return nil, errors.New("Cursor executor requires chat-completions payload")
	}
	if len(req.Payload) > maxFrame {
		return nil, errors.New("Cursor request exceeds limit")
	}
	var chat chatRequest
	if err := json.Unmarshal(req.Payload, &chat); err != nil {
		return nil, errors.New("invalid chat-completions payload")
	}
	if chat.N > 1 {
		return nil, errors.New("Cursor supports only one completion")
	}
	if len(chat.ToolChoice) > 0 && string(chat.ToolChoice) != "\"auto\"" && string(chat.ToolChoice) != "\"none\"" && string(chat.ToolChoice) != "null" {
		return nil, errors.New("Cursor forced tool_choice is unsupported")
	}
	if len(chat.Messages) == 0 {
		return nil, errors.New("Cursor requires messages")
	}
	b := &builtRequest{blobs: newBlobs(), wire: make(map[string]string), resolve: make(map[string]string), id: "chatcmpl-" + uuid(), model: req.Model}
	if b.model == "" {
		b.model = chat.Model
	}
	if b.model == "" {
		return nil, errors.New("Cursor model is required")
	}
	names := make([]string, 0, len(chat.Tools))
	seen := map[string]bool{}
	for _, t := range chat.Tools {
		if t.Type != "function" || t.Function.Name == "" || seen[t.Function.Name] {
			return nil, errors.New("Cursor requires unique named function tools")
		}
		seen[t.Function.Name] = true
		names = append(names, t.Function.Name)
	}
	b.callIDs = make(map[string]bool)
	for _, message := range chat.Messages {
		if message.Role != "assistant" {
			continue
		}
		for _, call := range message.ToolCalls {
			b.callIDs[call.ID] = true
			if call.Function.Name == "" {
				return nil, errors.New("invalid transcript tool call")
			}
			if !seen[call.Function.Name] {
				seen[call.Function.Name] = true
				names = append(names, call.Function.Name)
			}
		}
	}
	reserved := map[string]bool{}
	for _, prefixed := range []bool{false, true} {
		for _, name := range names {
			if strings.HasPrefix(name, "mcp_"+toolProvider+"_") != prefixed {
				continue
			}
			base := name
			for strings.HasPrefix(base, "mcp_"+toolProvider+"_") {
				base = strings.TrimPrefix(base, "mcp_"+toolProvider+"_")
			}
			if strings.EqualFold(base, "task") {
				base = "client_task"
			}
			if base == "" {
				base = "tool"
			}
			wire := base
			for index := 2; ; index++ {
				collision := false
				for _, alias := range clientToolAliases(wire) {
					collision = collision || reserved[alias]
				}
				if !collision {
					break
				}
				wire = fmt.Sprintf("%s_%d", base, index)
			}
			b.wire[name] = wire
			for _, alias := range clientToolAliases(wire) {
				reserved[alias] = true
			}
		}
	}
	if string(chat.ToolChoice) != "\"none\"" {
		for _, t := range chat.Tools {
			for _, alias := range clientToolAliases(b.wire[t.Function.Name]) {
				b.resolve[alias] = t.Function.Name
			}
			schema := t.Function.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			v, err := structpb.NewValue(schema)
			if err != nil {
				return nil, errors.New("invalid tool schema")
			}
			js, _ := json.Marshal(schema)
			b.tools = append(b.tools, &pb.McpToolDefinition{Name: b.wire[t.Function.Name], ToolName: clientToolQualifiedName(b.wire[t.Function.Name]), ProviderIdentifier: toolProvider, Description: t.Function.Description, InputSchema: v, InputSchemaJson: proto.String(string(js))})
		}
	}
	last := chat.Messages[len(chat.Messages)-1]
	if last.Role != "user" && last.Role != "tool" {
		return nil, errors.New("Cursor transcript must end in a user message or tool result")
	}
	system := []string{}
	for _, m := range chat.Messages {
		if m.Role == "system" || m.Role == "developer" {
			text, imgs, err := content(m.Content)
			if err != nil {
				return nil, err
			}
			if len(imgs) > 0 {
				return nil, errors.New("system images unsupported")
			}
			system = append(system, text)
		}
	}
	policy := clientToolPolicy(b.tools)
	systemText := strings.Join(system, "\n\n")
	state := &pb.ConversationStateStructure{Mode: proto.Int32(1), ClientName: "cpa-cursor"}
	addRoot := func(value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		id, err := b.blobs.put(raw)
		if err == nil {
			state.RootPromptMessagesJson = append(state.RootPromptMessagesJson, id)
		}
		return err
	}
	if err := addRoot(map[string]any{"role": "system", "content": systemText}); err != nil {
		return nil, err
	}
	if err := addRoot(map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "<rules>\n" + systemText + "\n\n" + policy + "\n</rules>"}}}); err != nil {
		return nil, err
	}
	systemID := state.RootPromptMessagesJson[0]
	selectedWire := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), systemID)
	selectedWire = protowire.AppendString(protowire.AppendTag(selectedWire, 22, protowire.BytesType), "cpa-cursor")
	selectedID, err := b.blobs.put(selectedWire)
	if err != nil {
		return nil, err
	}
	var current *pb.AgentConversationTurnStructure
	calls := map[string]*pb.McpToolCall{}
	callNames := map[string]string{}
	historyEnd := len(chat.Messages)
	if last.Role == "user" {
		historyEnd--
	}
	flushTurn := func() error {
		if current == nil {
			return nil
		}
		id, err := b.blobs.message(&pb.ConversationTurnStructure{Turn: &pb.ConversationTurnStructure_AgentConversationTurn{AgentConversationTurn: current}})
		if err == nil {
			state.Turns = append(state.Turns, id)
		}
		current = nil
		return err
	}
	for i, m := range chat.Messages[:historyEnd] {
		text, imgs, err := content(m.Content)
		if err != nil {
			return nil, err
		}
		switch m.Role {
		case "system", "developer":
			continue
		case "user":
			if err := flushTurn(); err != nil {
				return nil, err
			}
			u := newUser(text, imgs, selectedID)
			id, err := b.blobs.message(u)
			if err != nil {
				return nil, err
			}
			current = &pb.AgentConversationTurnStructure{UserMessage: id, RequestId: proto.String(uuid())}
			parts := []any{map[string]any{"type": "text", "text": "<user_query>\n" + text + "\n</user_query>"}}
			for _, img := range imgs {
				parts = append(parts, map[string]any{"type": "image", "image": base64.StdEncoding.EncodeToString(img.GetData()), "mimeType": img.MimeType})
			}
			if err := addRoot(map[string]any{"role": "user", "content": parts}); err != nil {
				return nil, err
			}
		case "assistant":
			if current == nil {
				return nil, fmt.Errorf("assistant at message %d has no user turn", i)
			}
			var parts []any
			if m.Reasoning != "" {
				id, err := b.blobs.message(&pb.ConversationStep{Message: &pb.ConversationStep_ThinkingMessage{ThinkingMessage: &pb.ThinkingMessage{Text: m.Reasoning}}})
				if err != nil {
					return nil, err
				}
				current.Steps = append(current.Steps, id)
				parts = append(parts, map[string]any{"type": "reasoning", "text": m.Reasoning})
			}
			if text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
				id, err := b.blobs.message(&pb.ConversationStep{Message: &pb.ConversationStep_AssistantMessage{AssistantMessage: &pb.AssistantMessage{Text: text}}})
				if err != nil {
					return nil, err
				}
				current.Steps = append(current.Steps, id)
			}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				if tc.ID == "" || json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil || args == nil {
					return nil, errors.New("invalid transcript tool call")
				}
				encoded := map[string][]byte{}
				for key, value := range args {
					v, err := structpb.NewValue(value)
					if err != nil {
						return nil, err
					}
					encoded[key], _ = proto.Marshal(v)
				}
				wire := b.wire[tc.Function.Name]
				callNames[tc.ID] = wire
				mc := &pb.McpToolCall{Args: &pb.McpArgs{Name: wire, ToolName: wire, ProviderIdentifier: toolProvider, ToolCallId: tc.ID, Args: encoded}}
				calls[tc.ID] = mc
				parts = append(parts, map[string]any{"type": "tool-call", "toolCallId": tc.ID, "toolName": clientToolQualifiedName(wire), "args": args})
			}
			if len(parts) > 0 {
				if err := addRoot(map[string]any{"role": "assistant", "content": parts}); err != nil {
					return nil, err
				}
			}
		case "tool":
			mc := calls[m.ToolCallID]
			if mc == nil || current == nil {
				return nil, errors.New("tool result has no preceding tool call")
			}
			items := []*pb.McpToolResultContentItem{{Content: &pb.McpToolResultContentItem_Text{Text: &pb.McpTextContent{Text: text}}}}
			for _, img := range imgs {
				items = append(items, &pb.McpToolResultContentItem{Content: &pb.McpToolResultContentItem_Image{Image: &pb.McpImageContent{Data: img.GetData(), MimeType: img.MimeType}}})
			}
			if m.IsError {
				mc.Result = &pb.McpToolResult{Result: &pb.McpToolResult_Error{Error: &pb.McpToolError{Error: text}}}
			} else {
				mc.Result = &pb.McpToolResult{Result: &pb.McpToolResult_Success{Success: &pb.McpSuccess{Content: items}}}
			}
			id, err := b.blobs.message(&pb.ConversationStep{Message: &pb.ConversationStep_ToolCall{ToolCall: &pb.ToolCall{Tool: &pb.ToolCall_McpToolCall{McpToolCall: mc}}}})
			if err != nil {
				return nil, err
			}
			current.Steps = append(current.Steps, id)
			if err := addRoot(map[string]any{"role": "tool", "content": []any{map[string]any{"type": "tool-result", "toolCallId": m.ToolCallID, "toolName": clientToolQualifiedName(callNames[m.ToolCallID]), "result": text, "isError": m.IsError}}}); err != nil {
				return nil, err
			}
			delete(calls, m.ToolCallID)
		default:
			return nil, fmt.Errorf("unsupported role %q", m.Role)
		}
	}
	if len(calls) > 0 {
		return nil, errors.New("transcript contains tool calls without results")
	}
	if err := flushTurn(); err != nil {
		return nil, err
	}
	context := clientRequestContext(b.tools)
	action := &pb.ConversationAction{Action: &pb.ConversationAction_ResumeAction{ResumeAction: &pb.ResumeAction{RequestContext: context}}}
	if last.Role == "user" {
		text, images, err := content(last.Content)
		if err != nil {
			return nil, err
		}
		action.Action = &pb.ConversationAction_UserMessageAction{UserMessageAction: &pb.UserMessageAction{UserMessage: newUser(text, images, selectedID), RequestContext: context}}
	}
	run := &pb.AgentRunRequest{ConversationState: state, Action: action, RequestedModel: &pb.RequestedModel{ModelId: b.model}, ConversationId: proto.String(uuid()), McpTools: &pb.McpTools{McpTools: b.tools}, McpFileSystemOptions: &pb.McpFileSystemOptions{Enabled: false}, SkillOptions: &pb.SkillOptions{}}
	run.RequestedModel.MaxMode = chat.MaxMode
	for _, p := range chat.ModelParameters {
		if p.ID == "" {
			return nil, errors.New("Cursor model parameter ID is required")
		}
		run.RequestedModel.Parameters = append(run.RequestedModel.Parameters, &pb.RequestedModel_ModelParameterbytes{Id: p.ID, Value: p.Value})
	}
	b.message = &pb.AgentClientMessage{Message: &pb.AgentClientMessage_RunRequest{RunRequest: run}}
	return b, nil
}
func newUser(text string, images []*pb.SelectedImage, selected []byte) *pb.UserMessage {
	id := uuid()
	return &pb.UserMessage{Text: text, MessageId: id, CorrelationId: id, Mode: 1, SelectedContext: &pb.SelectedContext{SelectedImages: images}, SelectedContextBlob: selected}
}
