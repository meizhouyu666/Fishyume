package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/run"
)

const (
	EngineVersion  = "0.2.1-alpha.1"
	MaxMessageSize = 1 << 20
)

var supportedMethods = []string{"engine.hello", "run.start", "run.startWorkflow", "run.status", "run.resume", "run.cancel", "run.detach", "system.capabilities", "workflow.validate", "workflow.explain", "run.list", "run.get", "run.events", "run.action", "run.result"}

type Server struct {
	reader               *bufio.Reader
	writer               io.Writer
	service              *run.Service
	application          *application.Service
	writeMu              sync.Mutex
	mutationMu           *sync.Mutex
	requestWG            sync.WaitGroup
	waitControllersOnEOF bool
	unsubscribe          func()
	notifications        chan run.WorkflowEvent
	notificationDone     chan struct{}
	notificationDoneOnce sync.Once
}

func NewServer(input io.Reader, output io.Writer, service *run.Service, applications ...*application.Service) *Server {
	return newServer(input, output, service, selectApplication(service, applications), nil, true)
}

func NewConnectionServer(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, mutationMu *sync.Mutex) *Server {
	return newServer(input, output, service, applicationService, mutationMu, false)
}

func selectApplication(service *run.Service, applications []*application.Service) *application.Service {
	if len(applications) > 0 && applications[0] != nil {
		return applications[0]
	}
	return application.NewService(service, "codex", service.ApplicationJournal())
}

func newServer(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, mutationMu *sync.Mutex, waitControllersOnEOF bool) *Server {
	server := &Server{reader: bufio.NewReaderSize(input, 64*1024), writer: output, service: service, application: applicationService, mutationMu: mutationMu, waitControllersOnEOF: waitControllersOnEOF}
	sink := func(event run.WorkflowEvent) {
		_ = server.write(Notification{JSONRPC: "2.0", ProtocolVersion: ProtocolVersion, Method: "run.event", Params: event})
	}
	if waitControllersOnEOF {
		service.SetEventSink(sink)
		server.unsubscribe = func() {}
	} else {
		server.notifications = make(chan run.WorkflowEvent, 128)
		server.notificationDone = make(chan struct{})
		sink = func(event run.WorkflowEvent) {
			select {
			case server.notifications <- event:
			default:
				// Durable state remains authoritative when a slow observer falls
				// behind this bounded best-effort notification stream.
			}
		}
		server.unsubscribe = service.AddEventSink(sink)
	}
	return server
}

func (s *Server) Serve(ctx context.Context) error {
	if s.notifications != nil {
		go s.writeNotifications()
	}
	defer func() {
		s.unsubscribe()
		s.notificationDoneOnce.Do(func() {
			if s.notificationDone != nil {
				close(s.notificationDone)
			}
		})
	}()
	for {
		line, err := readLine(s.reader, MaxMessageSize)
		if errors.Is(err, io.EOF) {
			s.requestWG.Wait()
			if s.waitControllersOnEOF {
				waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = s.application.CompatibilityWaitControllers(waitContext)
				cancel()
			}
			return nil
		}
		if errors.Is(err, errMessageTooLarge) {
			s.writeError(nil, -32600, "request is too large", nil)
			continue
		}
		if err != nil {
			return fmt.Errorf("read protocol input: %w", err)
		}
		if len(line) == 0 {
			continue
		}
		var request Request
		if err := json.Unmarshal(line, &request); err != nil {
			s.writeError(nil, -32700, "parse error", nil)
			continue
		}
		s.requestWG.Add(1)
		go func() {
			defer s.requestWG.Done()
			s.handle(ctx, request)
		}()
	}
}

func (s *Server) writeNotifications() {
	for {
		select {
		case event := <-s.notifications:
			_ = s.write(Notification{JSONRPC: "2.0", ProtocolVersion: ProtocolVersion, Method: "run.event", Params: event})
		case <-s.notificationDone:
			return
		}
	}
}

func (s *Server) handle(ctx context.Context, request Request) {
	id := decodeID(request.ID)
	if request.JSONRPC != "2.0" {
		s.writeError(id, -32600, "unsupported JSON-RPC version", nil)
		return
	}
	if request.ProtocolVersion != ProtocolVersion {
		s.writeError(id, -32600, "unsupported protocol version", map[string]int{"supported": ProtocolVersion})
		return
	}
	if s.mutationMu != nil && isMutation(request.Method) {
		s.mutationMu.Lock()
		defer s.mutationMu.Unlock()
	}

	switch request.Method {
	case "system.capabilities":
		invokeApplication(ctx, s, id, request.Params, application.SystemCapabilitiesRequest{}, s.application.SystemCapabilities)
	case "workflow.validate":
		invokeApplication(ctx, s, id, request.Params, application.WorkflowValidateRequest{}, s.application.WorkflowValidate)
	case "workflow.explain":
		invokeApplication(ctx, s, id, request.Params, application.WorkflowExplainRequest{}, s.application.WorkflowExplain)
	case "engine.hello":
		var params helloParams
		if len(request.Params) > 0 && string(request.Params) != "null" {
			if err := json.Unmarshal(request.Params, &params); err != nil {
				s.writeError(id, -32602, "invalid engine.hello params", err.Error())
				return
			}
		}
		selectedDriver := params.Driver
		if selectedDriver == "" {
			selectedDriver = params.Backend
		}
		doctor := s.service.Doctor(ctx, params.Project, selectedDriver)
		s.writeResult(id, HelloResult{EngineVersion: EngineVersion, ProtocolVersion: ProtocolVersion,
			SupportedMethods: supportedMethods, SupportedDrivers: s.service.SupportedBackends(), SupportedBackends: s.service.SupportedBackends(),
			BackendReady: doctor.BackendReady, BackendDiagnostic: doctor.BackendDiagnostic,
			ProjectChecked: doctor.ProjectChecked, ProjectReady: doctor.ProjectReady,
			ProjectDiagnostic: doctor.ProjectDiagnostic})
	case "run.start":
		if isFormalStart(request.Params) {
			invokeApplication(ctx, s, id, request.Params, application.RunStartRequest{}, s.application.RunStart)
			return
		}
		s.compatibilityStart(ctx, id, request.Params)
	case "run.startWorkflow":
		s.compatibilityStartWorkflow(ctx, id, request.Params)
	case "run.list":
		invokeApplication(ctx, s, id, request.Params, application.RunListRequest{}, s.application.RunList)
	case "run.get":
		invokeApplication(ctx, s, id, request.Params, application.RunGetRequest{}, s.application.RunGet)
	case "run.events":
		invokeApplication(ctx, s, id, request.Params, application.RunEventsRequest{}, s.application.RunEvents)
	case "run.action":
		invokeApplication(ctx, s, id, request.Params, application.RunActionRequest{}, s.application.RunAction)
	case "run.result":
		invokeApplication(ctx, s, id, request.Params, application.RunResultRequest{}, s.application.RunResult)
	case "run.status":
		params, ok := s.parseRunID(id, request.Params)
		if !ok {
			return
		}
		snapshot, appErr := s.application.CompatibilityStatus(params.RunID)
		if appErr != nil {
			s.writeApplicationError(id, appErr)
			return
		}
		s.writeResult(id, snapshot)
	case "run.resume":
		var params run.ResumeRequest
		if err := decodeParams(request.Params, &params); err != nil || params.RunID == "" {
			s.writeError(id, -32602, "invalid run.resume params", errorText(err))
			return
		}
		if params.Action != nil && params.Action.Type != "approve" && params.Action.Type != "reject" && params.Action.Type != "retry" {
			s.writeError(id, -32602, "invalid run.resume action", params.Action.Type)
			return
		}
		s.compatibilityResume(ctx, id, params)
	case "run.detach":
		params, ok := s.parseRunID(id, request.Params)
		if !ok {
			return
		}
		snapshot, appErr := s.application.CompatibilityDetach(params.RunID)
		if appErr != nil {
			s.writeApplicationError(id, appErr)
			return
		}
		s.writeResult(id, snapshot)
	case "run.cancel":
		params, ok := s.parseRunID(id, request.Params)
		if !ok {
			return
		}
		s.compatibilityCancel(ctx, id, params)
	default:
		s.writeError(id, -32601, "method not found", request.Method)
	}
}

func isMutation(method string) bool {
	switch method {
	case "run.start", "run.startWorkflow", "run.resume", "run.cancel", "run.action":
		return true
	default:
		return false
	}
}

func isFormalStart(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	_, hasWorkflow := value["workflow"]
	_, hasRequestID := value["clientRequestId"]
	return hasWorkflow || hasRequestID
}

func invokeApplication[Request any, Response any](ctx context.Context, s *Server, id any, raw json.RawMessage, request Request, call func(context.Context, Request) (Response, *application.Error)) {
	if len(raw) > 0 && string(raw) != "null" {
		if err := decodeParams(raw, &request); err != nil {
			s.writeApplicationError(id, application.NewError(application.CodeInvalidArgument, "invalid application request", map[string]any{"detail": err.Error()}))
			return
		}
	}
	response, appErr := call(ctx, request)
	if appErr != nil {
		s.writeApplicationError(id, appErr)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) compatibilityStart(ctx context.Context, id any, raw json.RawMessage) {
	var params run.StartRequest
	if err := decodeParams(raw, &params); err != nil {
		s.writeError(id, -32602, "invalid run.start params", err.Error())
		return
	}
	document, err := json.Marshal(map[string]any{
		"apiVersion": "fishyume/v1", "name": "ad-hoc",
		"defaults":  map[string]any{"agent": map[string]any{"driver": firstNonEmpty(params.Driver, params.Backend, params.Tool, "codex"), "target": firstNonEmpty(params.Target, params.Runtime, "local")}},
		"execution": map[string]any{"maxConcurrency": 1},
		"nodes":     map[string]any{"agent-1": map[string]any{"type": "agent", "task": params.Task}},
	})
	if err != nil {
		s.writeError(id, -32603, "could not build compatibility workflow", nil)
		return
	}
	response, appErr := s.application.RunStart(ctx, application.RunStartRequest{Project: params.Project, Workflow: application.WorkflowInput{Document: document}, ClientRequestID: compatibilityID("start")})
	if appErr != nil {
		s.writeApplicationError(id, appErr)
		return
	}
	s.writeResult(id, StartResult{ProtocolVersion: ProtocolVersion, RunID: response.RunID})
}

func (s *Server) compatibilityStartWorkflow(ctx context.Context, id any, raw json.RawMessage) {
	var params run.StartWorkflowRequest
	if err := decodeParams(raw, &params); err != nil {
		s.writeError(id, -32602, "invalid run.startWorkflow params", err.Error())
		return
	}
	response, appErr := s.application.CompatibilityStartWorkflow(ctx, params, compatibilityID("start"))
	if appErr != nil {
		s.writeApplicationError(id, appErr)
		return
	}
	s.writeResult(id, StartResult{ProtocolVersion: ProtocolVersion, RunID: response.RunID})
}

func (s *Server) compatibilityResume(ctx context.Context, id any, params run.ResumeRequest) {
	view, appErr := s.application.CompatibilityStatus(params.RunID)
	if appErr != nil || view.Run == nil {
		if appErr == nil {
			appErr = application.NewError(application.CodeNotFound, "run not found", nil)
		}
		s.writeApplicationError(id, appErr)
		return
	}
	if params.Action == nil {
		snapshot, resumeErr := s.application.CompatibilityResume(ctx, params)
		if resumeErr != nil {
			s.writeApplicationError(id, resumeErr)
			return
		}
		s.writeResult(id, snapshot)
		return
	}
	expected := view.Run.StateVersion
	if params.ExpectedStateVersion != nil {
		expected = *params.ExpectedStateVersion
	}
	action := application.RunActionRequest{ActionID: compatibilityID("action"), RunID: params.RunID, Type: application.ActionType(params.Action.Type), ExpectedStateVersion: expected, NodeID: params.Action.NodeID, ExpectedAttempt: params.Action.ExpectedAttempt, Reason: params.Action.Reason, Answers: params.Action.Answers, AcknowledgeDuplicateRisk: params.Action.AcknowledgeDuplicateRisk}
	if action.Type == application.ActionRetry && action.ExpectedAttempt == nil {
		for _, node := range view.Nodes {
			if node.ID == action.NodeID {
				attempt := node.CurrentAttempt
				action.ExpectedAttempt = &attempt
				break
			}
		}
	}
	if _, appErr = s.application.RunAction(ctx, action); appErr != nil {
		s.writeApplicationError(id, appErr)
		return
	}
	updated, appErr := s.application.CompatibilityStatus(params.RunID)
	if appErr != nil || updated.Run == nil {
		s.writeApplicationError(id, appErr)
		return
	}
	s.writeResult(id, *updated.Run)
}

func (s *Server) compatibilityCancel(ctx context.Context, id any, params runIDParams) {
	view, appErr := s.application.CompatibilityStatus(params.RunID)
	if appErr != nil || view.Run == nil {
		s.writeApplicationError(id, appErr)
		return
	}
	expected := view.Run.StateVersion
	if params.ExpectedStateVersion != nil {
		expected = *params.ExpectedStateVersion
	}
	if _, appErr = s.application.RunAction(ctx, application.RunActionRequest{ActionID: compatibilityID("action"), RunID: params.RunID, Type: application.ActionCancel, ExpectedStateVersion: expected}); appErr != nil {
		s.writeApplicationError(id, appErr)
		return
	}
	updated, appErr := s.application.CompatibilityStatus(params.RunID)
	if appErr != nil || updated.Run == nil {
		s.writeApplicationError(id, appErr)
		return
	}
	s.writeResult(id, *updated.Run)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func compatibilityID(prefix string) string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("compat-%s-%d", prefix, time.Now().UnixNano())
	}
	return "compat-" + prefix + "-" + hex.EncodeToString(value)
}

func (s *Server) parseRunID(id any, raw json.RawMessage) (runIDParams, bool) {
	var params runIDParams
	if err := decodeParams(raw, &params); err != nil || params.RunID == "" {
		s.writeError(id, -32602, "runId is required", nil)
		return runIDParams{}, false
	}
	return params, true
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errors.New("params are required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("params contain multiple JSON values")
		}
		return err
	}
	return nil
}

func errorText(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func decodeID(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var id any
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil
	}
	return id
}

func (s *Server) writeResult(id, result any) {
	_ = s.write(Response{JSONRPC: "2.0", ProtocolVersion: ProtocolVersion, ID: id, Result: result})
}

func (s *Server) writeError(id any, code int, message string, data any) {
	_ = s.write(Response{JSONRPC: "2.0", ProtocolVersion: ProtocolVersion, ID: id,
		Error: &RPCError{Code: code, Message: message, Data: data}})
}

func (s *Server) writeApplicationError(id any, appErr *application.Error) {
	if appErr == nil {
		appErr = application.NewError(application.CodeInternal, "internal application error", nil)
	}
	_ = s.write(Response{JSONRPC: "2.0", ProtocolVersion: ProtocolVersion, ID: id,
		Error: &RPCError{Code: applicationRPCCode(appErr.Code), Message: appErr.Message, Data: appErr}})
}

func applicationRPCCode(code application.ErrorCode) int {
	switch code {
	case application.CodeInvalidArgument, application.CodeInvalidWorkflow:
		return -32602
	case application.CodeNotFound:
		return -32004
	case application.CodeConflict:
		return -32009
	case application.CodeCapabilityUnavailable:
		return -32010
	case application.CodeNotReady:
		return -32011
	case application.CodeProtocolMismatch:
		return -32012
	default:
		return -32603
	}
}

func (s *Server) write(message any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = s.writer.Write(data)
	return err
}
