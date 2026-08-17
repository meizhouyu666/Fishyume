package application

import (
	"context"
	"errors"

	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/store"
)

type MemoryBackend interface {
	CreateMemory(store.MemoryCreateInput) (store.MemoryMutationResult, error)
	GetMemory(string, string) (contextcompiler.MemoryRecordV1, uint64, error)
	ListMemory(store.MemoryListInput) (store.MemoryListResult, error)
	SupersedeMemory(store.MemorySupersedeInput) (store.MemoryMutationResult, error)
	DeleteMemory(store.MemoryDeleteInput) (store.MemoryMutationResult, error)
}

func (s *Service) MemoryCreate(_ context.Context, request MemoryCreateRequest) (MemoryMutationResponse, *Error) {
	return s.memoryCreate(request, contextcompiler.MemoryWriterUser)
}

func (s *Service) MemoryCreateHost(_ context.Context, request MemoryCreateRequest) (MemoryMutationResponse, *Error) {
	return s.memoryCreate(request, contextcompiler.MemoryWriterHostAgent)
}

func (s *Service) memoryCreate(request MemoryCreateRequest, writer contextcompiler.MemoryWriter) (MemoryMutationResponse, *Error) {
	if s.memory == nil {
		return MemoryMutationResponse{}, NewError(CodeInternal, "Memory store is unavailable", nil)
	}
	result, err := s.memory.CreateMemory(store.MemoryCreateInput{Project: request.Project, MutationID: request.MutationID, Type: request.Type, Content: request.Content, Sensitivity: request.Sensitivity, Writer: writer, Reason: request.Reason, ExpiresAt: request.ExpiresAt, MaxUses: request.MaxUses})
	if err != nil {
		return MemoryMutationResponse{}, mapMemoryError(err)
	}
	response := memoryMutationResponse(result)
	if err := ensureResponseBound(response, MaxResponseBytes); err != nil {
		return MemoryMutationResponse{}, internalError(err)
	}
	return response, nil
}

func (s *Service) MemoryGet(_ context.Context, request MemoryGetRequest) (MemoryGetResponse, *Error) {
	if s.memory == nil {
		return MemoryGetResponse{}, NewError(CodeInternal, "Memory store is unavailable", nil)
	}
	record, revision, err := s.memory.GetMemory(request.Project, request.RecordID)
	if err != nil {
		return MemoryGetResponse{}, mapMemoryError(err)
	}
	response := MemoryGetResponse{APIVersion: APIVersion, Revision: revision, Record: record}
	if err := ensureResponseBound(response, MaxResponseBytes); err != nil {
		return MemoryGetResponse{}, internalError(err)
	}
	return response, nil
}

func (s *Service) MemoryList(_ context.Context, request MemoryListRequest) (MemoryListResponse, *Error) {
	if s.memory == nil {
		return MemoryListResponse{}, NewError(CodeInternal, "Memory store is unavailable", nil)
	}
	result, err := s.memory.ListMemory(store.MemoryListInput{Project: request.Project, Filter: store.MemoryListFilter{Type: request.Filter.Type, State: request.Filter.State, Sensitivity: request.Filter.Sensitivity, Writer: request.Filter.Writer}, Cursor: request.Cursor, Limit: request.Limit})
	if err != nil {
		return MemoryListResponse{}, mapMemoryError(err)
	}
	response := MemoryListResponse{APIVersion: APIVersion, Revision: result.Revision, Items: result.Items, NextCursor: result.NextCursor}
	if err := ensureResponseBound(response, MaxResponseBytes); err != nil {
		return MemoryListResponse{}, internalError(err)
	}
	return response, nil
}

func (s *Service) MemorySupersede(_ context.Context, request MemorySupersedeRequest) (MemoryMutationResponse, *Error) {
	return s.memorySupersede(request, contextcompiler.MemoryWriterUser)
}

func (s *Service) MemorySupersedeHost(_ context.Context, request MemorySupersedeRequest) (MemoryMutationResponse, *Error) {
	return s.memorySupersede(request, contextcompiler.MemoryWriterHostAgent)
}

func (s *Service) memorySupersede(request MemorySupersedeRequest, writer contextcompiler.MemoryWriter) (MemoryMutationResponse, *Error) {
	if s.memory == nil {
		return MemoryMutationResponse{}, NewError(CodeInternal, "Memory store is unavailable", nil)
	}
	result, err := s.memory.SupersedeMemory(store.MemorySupersedeInput{Project: request.Project, MutationID: request.MutationID, Supersedes: request.Supersedes, Type: request.Type, Content: request.Content, Sensitivity: request.Sensitivity, Writer: writer, Reason: request.Reason, ExpiresAt: request.ExpiresAt, MaxUses: request.MaxUses})
	if err != nil {
		return MemoryMutationResponse{}, mapMemoryError(err)
	}
	return memoryMutationResponse(result), nil
}

func (s *Service) MemoryDelete(_ context.Context, request MemoryDeleteRequest) (MemoryMutationResponse, *Error) {
	return s.memoryDelete(request, contextcompiler.MemoryWriterUser)
}

func (s *Service) MemoryDeleteHost(_ context.Context, request MemoryDeleteRequest) (MemoryMutationResponse, *Error) {
	return s.memoryDelete(request, contextcompiler.MemoryWriterHostAgent)
}

func (s *Service) memoryDelete(request MemoryDeleteRequest, writer contextcompiler.MemoryWriter) (MemoryMutationResponse, *Error) {
	if s.memory == nil {
		return MemoryMutationResponse{}, NewError(CodeInternal, "Memory store is unavailable", nil)
	}
	result, err := s.memory.DeleteMemory(store.MemoryDeleteInput{Project: request.Project, MutationID: request.MutationID, RecordID: request.RecordID, Writer: writer, Reason: request.Reason})
	if err != nil {
		return MemoryMutationResponse{}, mapMemoryError(err)
	}
	return memoryMutationResponse(result), nil
}

func memoryMutationResponse(result store.MemoryMutationResult) MemoryMutationResponse {
	return MemoryMutationResponse{APIVersion: APIVersion, Revision: result.Revision, RecordID: result.RecordID, AffectedIDs: append([]string{}, result.AffectedIDs...), Replayed: result.Replayed}
}

func mapMemoryError(err error) *Error {
	var storeErr *store.MemoryStoreError
	if !errors.As(err, &storeErr) {
		return NewError(CodeInternal, "Memory store operation failed", nil)
	}
	switch storeErr.Code {
	case store.MemoryStoreInvalid:
		return NewError(CodeInvalidArgument, storeErr.Message, map[string]any{"storeCode": storeErr.Code})
	case store.MemoryStoreConflict:
		return NewError(CodeConflict, storeErr.Message, map[string]any{"storeCode": storeErr.Code})
	case store.MemoryStoreNotFound:
		return NewError(CodeNotFound, storeErr.Message, map[string]any{"storeCode": storeErr.Code})
	default:
		return NewError(CodeInternal, "Memory catalog is unavailable or invalid", map[string]any{"storeCode": storeErr.Code})
	}
}
