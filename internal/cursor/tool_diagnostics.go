package cursor

import (
	"encoding/json"

	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func (e *Executor) logToolRejection(b *builtRequest, message proto.Message, count int, outcome string) {
	if e.Log == nil || count > maxRejectedToolCalls+1 {
		return
	}
	event := struct {
		Event         string `json:"event"`
		RunID         string `json:"run_id"`
		Category      string `json:"category"`
		Subtype       string `json:"subtype"`
		FieldNumber   int    `json:"field_number"`
		Provider      string `json:"provider"`
		Resolution    string `json:"resolution"`
		ArgumentShape string `json:"argument_shape"`
		Count         int    `json:"count"`
		Limit         int    `json:"limit"`
		Outcome       string `json:"outcome"`
	}{Event: "cursor_tool_rejected", RunID: b.id, Provider: "none", Resolution: "not_applicable", ArgumentShape: "not_applicable", Count: count, Limit: maxRejectedToolCalls, Outcome: outcome}
	if count > maxRejectedToolCalls {
		event.Outcome = "limit"
	}
	switch request := message.(type) {
	case *pb.ExecServerMessage:
		event.Category = "native"
		event.Subtype, event.FieldNumber = rejectionSubtype(request, "message")
		if args := request.GetMcpArgs(); args != nil {
			event.Category = "mcp"
			event.Provider = "foreign"
			if args.ProviderIdentifier == "" {
				event.Provider = "empty"
			} else if args.ProviderIdentifier == toolProvider {
				event.Provider = "cpa"
			}
			toolName, name := b.resolve[args.ToolName], b.resolve[args.Name]
			event.Resolution = "missing"
			if toolName != "" || name != "" {
				event.Resolution = "recognised"
			}
			if toolName != "" && name != "" && toolName != name {
				event.Resolution = "conflicting"
			}
			event.ArgumentShape = "empty"
			if len(args.Args) > 0 {
				event.ArgumentShape = "flat"
			}
			if _, present := args.Args["mcpDetails"]; present {
				event.ArgumentShape = "dynamic_wrapper"
			}
		}
	case *pb.InteractionQuery:
		event.Category = "interaction"
		event.Subtype, event.FieldNumber = rejectionSubtype(request, "query")
		if unknown := request.ProtoReflect().GetUnknown(); len(unknown) > 0 {
			event.Subtype, event.FieldNumber = "unknown", 0
			field, kind, tagBytes := protowire.ConsumeTag(unknown)
			if request.Query == nil && tagBytes > 0 && field == 9 && kind == protowire.BytesType {
				_, valueBytes := protowire.ConsumeBytes(unknown[tagBytes:])
				if valueBytes >= 0 && tagBytes+valueBytes == len(unknown) {
					event.Subtype, event.FieldNumber = "web_fetch_field_9", 9
				}
			}
		}
	}
	raw, _ := json.Marshal(event)
	e.Log(string(raw))
}

func rejectionSubtype(message proto.Message, oneof protoreflect.Name) (string, int) {
	reflected := message.ProtoReflect()
	if field := reflected.WhichOneof(reflected.Descriptor().Oneofs().ByName(oneof)); field != nil {
		return string(field.Name()), int(field.Number())
	}
	if len(reflected.GetUnknown()) > 0 {
		return "unknown", 0
	}
	return "empty", 0
}
