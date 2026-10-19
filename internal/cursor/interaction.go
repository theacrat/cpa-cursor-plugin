package cursor

import (
	"errors"

	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
)

func interactionReply(query *pb.InteractionQuery, tools []*pb.McpToolDefinition) (*pb.AgentClientMessage, error) {
	if query == nil {
		return nil, errors.New("Cursor interaction query is empty; no approval or answer was provided")
	}
	reply := &pb.InteractionResponse{Id: query.Id}
	reason := "This Cursor interaction is unavailable in this bridge. No answer or consent was collected, and no operation was performed. Do not retry this interaction. " + nativeToolRefusal(tools)
	if unknown := query.ProtoReflect().GetUnknown(); len(unknown) > 0 {
		field, kind, tagBytes := protowire.ConsumeTag(unknown)
		if query.Query != nil || field != 9 || kind != protowire.BytesType || tagBytes < 0 {
			return nil, errors.New("Cursor interaction query contains unsupported protocol fields; no approval or answer was provided")
		}
		_, valueBytes := protowire.ConsumeBytes(unknown[tagBytes:])
		if valueBytes < 0 || tagBytes+valueBytes != len(unknown) {
			return nil, errors.New("Cursor interaction query contains an unsupported web-fetch shape; no approval was provided")
		}
		rejected := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), reason)
		result := protowire.AppendBytes(protowire.AppendTag(nil, 2, protowire.BytesType), rejected)
		reply.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 9, protowire.BytesType), result))
	} else {
		switch query.Query.(type) {
		case *pb.InteractionQuery_WebSearchRequestQuery:
			reply.Result = &pb.InteractionResponse_WebSearchRequestResponse{WebSearchRequestResponse: &pb.WebSearchRequestResponse{Result: &pb.WebSearchRequestResponse_Rejected{Rejected: &pb.WebSearchRequestResponse_RejectedMessage{Reason: reason}}}}
		case *pb.InteractionQuery_AskQuestionInteractionQuery:
			reply.Result = &pb.InteractionResponse_AskQuestionInteractionResponse{AskQuestionInteractionResponse: &pb.AskQuestionInteractionResponse{Result: &pb.AskQuestionResult{Result: &pb.AskQuestionResult_Rejected{Rejected: &pb.AskQuestionRejected{Reason: reason}}}}}
		case *pb.InteractionQuery_SwitchModeRequestQuery:
			reply.Result = &pb.InteractionResponse_SwitchModeRequestResponse{SwitchModeRequestResponse: &pb.SwitchModeRequestResponse{Result: &pb.SwitchModeRequestResponse_Rejected{Rejected: &pb.SwitchModeRequestResponse_RejectedMessage{Reason: reason}}}}
		case *pb.InteractionQuery_ExaSearchRequestQuery:
			reply.Result = &pb.InteractionResponse_ExaSearchRequestResponse{ExaSearchRequestResponse: &pb.ExaSearchRequestResponse{Result: &pb.ExaSearchRequestResponse_Rejected{Rejected: &pb.ExaSearchRequestResponse_RejectedMessage{Reason: reason}}}}
		case *pb.InteractionQuery_ExaFetchRequestQuery:
			reply.Result = &pb.InteractionResponse_ExaFetchRequestResponse{ExaFetchRequestResponse: &pb.ExaFetchRequestResponse{Result: &pb.ExaFetchRequestResponse_Rejected{Rejected: &pb.ExaFetchRequestResponse_RejectedMessage{Reason: reason}}}}
		case *pb.InteractionQuery_CreatePlanRequestQuery:
			reply.Result = &pb.InteractionResponse_CreatePlanRequestResponse{CreatePlanRequestResponse: &pb.CreatePlanRequestResponse{Result: &pb.CreatePlanResult{Result: &pb.CreatePlanResult_Error{Error: &pb.CreatePlanError{Error: reason}}}}}
		case *pb.InteractionQuery_SetupVmEnvironmentArgs:
			return nil, errors.New("Cursor VM setup interaction cannot be rejected by this protocol; no setup or approval was performed")
		default:
			return nil, errors.New("Cursor interaction query has no supported subtype; no approval or answer was provided")
		}
	}
	return &pb.AgentClientMessage{Message: &pb.AgentClientMessage_InteractionResponse{InteractionResponse: reply}}, nil
}
