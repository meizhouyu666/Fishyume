package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/team"
	"wf.local/wf-engine/internal/teamcontract"
)

const (
	EngineVersion  = "0.2.1-alpha.1"
	MaxMessageSize = 1 << 20
)

var supportedMethods = append(append([]string{"engine.hello"}, application.StableMethods...), teamcontract.StableMethods...)

type Server struct {
	reader               *bufio.Reader
	writer               io.Writer
	service              *run.Service
	application          *application.Service
	teams                *team.Service
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
	return newServer(input, output, service, selectApplication(service, applications), nil, nil, true)
}

func NewServerWithTeam(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, teamService *team.Service) *Server {
	return newServer(input, output, service, applicationService, teamService, nil, true)
}

func NewConnectionServer(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, mutationMu *sync.Mutex, teams ...*team.Service) *Server {
	var teamService *team.Service
	if len(teams) > 0 {
		teamService = teams[0]
	}
	return newServer(input, output, service, applicationService, teamService, mutationMu, false)
}

func selectApplication(service *run.Service, applications []*application.Service) *application.Service {
	if len(applications) > 0 && applications[0] != nil {
		return applications[0]
	}
	return application.NewService(service, "codex", service.ApplicationJournal())
}

func newServer(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, teamService *team.Service, mutationMu *sync.Mutex, waitControllersOnEOF bool) *Server {
	server := &Server{reader: bufio.NewReaderSize(input, 64*1024), writer: output, service: service, application: applicationService, teams: teamService, mutationMu: mutationMu, waitControllersOnEOF: waitControllersOnEOF}
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
				if s.teams != nil {
					teamWaitContext, cancelTeamWait := context.WithTimeout(context.Background(), time.Second)
					_ = s.teams.Wait(teamWaitContext)
					cancelTeamWait()
				}
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
	case "routing.catalog":
		invokeApplication(ctx, s, id, request.Params, application.RoutingCatalogRequest{}, s.application.RoutingCatalog)
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
		invokeApplication(ctx, s, id, request.Params, application.RunStartRequest{}, s.application.RunStart)
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
	case "memory.create":
		invokeApplication(ctx, s, id, request.Params, application.MemoryCreateRequest{}, s.application.MemoryCreate)
	case "memory.get":
		invokeApplication(ctx, s, id, request.Params, application.MemoryGetRequest{}, s.application.MemoryGet)
	case "memory.list":
		invokeApplication(ctx, s, id, request.Params, application.MemoryListRequest{}, s.application.MemoryList)
	case "memory.supersede":
		invokeApplication(ctx, s, id, request.Params, application.MemorySupersedeRequest{}, s.application.MemorySupersede)
	case "memory.delete":
		invokeApplication(ctx, s, id, request.Params, application.MemoryDeleteRequest{}, s.application.MemoryDelete)
	case "memory.host.create":
		invokeApplication(ctx, s, id, request.Params, application.MemoryCreateRequest{}, s.application.MemoryCreateHost)
	case "memory.host.supersede":
		invokeApplication(ctx, s, id, request.Params, application.MemorySupersedeRequest{}, s.application.MemorySupersedeHost)
	case "memory.host.delete":
		invokeApplication(ctx, s, id, request.Params, application.MemoryDeleteRequest{}, s.application.MemoryDeleteHost)
	case "team.capabilities":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamCapabilitiesRequestV1{}, func(context.Context, teamcontract.TeamCapabilitiesRequestV1) (teamcontract.TeamCapabilitiesV1, error) {
			return s.teams.Capabilities()
		})
	case "team.start":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamStartRequestV1{}, func(ctx context.Context, value teamcontract.TeamStartRequestV1) (teamcontract.TeamStartResponseV1, error) {
			result, err := s.teams.Begin(ctx, value)
			return teamcontract.TeamStartResponseV1{SchemaVersion: teamcontract.SchemaVersion, Team: result.Team, Replayed: result.Replayed}, err
		})
	case "team.list":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamListRequestV1{}, func(_ context.Context, value teamcontract.TeamListRequestV1) (teamcontract.TeamListResponseV1, error) {
			return s.teams.List(value)
		})
	case "team.get":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamGetRequestV1{}, func(_ context.Context, value teamcontract.TeamGetRequestV1) (teamcontract.TeamGetResponseV1, error) {
			return s.teams.GetView(value)
		})
	case "team.events":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamEventsRequestV1{}, s.teams.Events)
	case "team.messages":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamMessagesRequestV1{}, func(_ context.Context, value teamcontract.TeamMessagesRequestV1) (teamcontract.TeamMessagesResponseV1, error) {
			return s.teams.Messages(value)
		})
	case "team.action":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamActionV1{}, s.teams.Action)
	case "team.handoff.create":
		invokeTeam(ctx, s, id, request.Params, teamcontract.HandoffCreateRequestV1{}, func(_ context.Context, value teamcontract.HandoffCreateRequestV1) (teamcontract.HandoffCreateResponseV1, error) {
			return s.teams.HandoffCreate(value)
		})
	case "team.handoff.get":
		invokeTeam(ctx, s, id, request.Params, teamcontract.HandoffGetRequestV1{}, func(_ context.Context, value teamcontract.HandoffGetRequestV1) (teamcontract.HandoffGetResponseV1, error) {
			return s.teams.HandoffGet(value)
		})
	case "team.handoff.list":
		invokeTeam(ctx, s, id, request.Params, teamcontract.HandoffListRequestV1{}, func(_ context.Context, value teamcontract.HandoffListRequestV1) (teamcontract.HandoffListResponseV1, error) {
			return s.teams.HandoffList(value)
		})
	case "team.handoff.bindRun":
		invokeTeam(ctx, s, id, request.Params, teamcontract.HandoffBindRunRequestV1{}, func(_ context.Context, value teamcontract.HandoffBindRunRequestV1) (teamcontract.HandoffBindRunResponseV1, error) {
			return s.teams.HandoffBindRun(value)
		})
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
	default:
		s.writeError(id, -32601, "method not found", request.Method)
	}
}

func isMutation(method string) bool {
	switch method {
	case "run.start", "run.action", "memory.create", "memory.supersede", "memory.delete", "memory.host.create", "memory.host.supersede", "memory.host.delete", "team.start", "team.action", "team.handoff.create", "team.handoff.bindRun":
		return true
	default:
		return false
	}
}

func invokeTeam[Request any, Response any](ctx context.Context, s *Server, id any, raw json.RawMessage, request Request, call func(context.Context, Request) (Response, error)) {
	if s.teams == nil {
		s.writeTeamError(id, teamcontract.ErrorCapabilityUnavailable, "Team service is unavailable")
		return
	}
	if err := decodeParams(raw, &request); err != nil {
		s.writeTeamError(id, teamcontract.ErrorInvalidArgument, "invalid Team request: "+err.Error())
		return
	}
	if err := validateTeamRequest(request); err != nil {
		s.writeTeamError(id, teamcontract.ErrorInvalidArgument, err.Error())
		return
	}
	response, err := call(ctx, request)
	if err != nil {
		s.writeMappedTeamError(id, err)
		return
	}
	s.writeResult(id, response)
}

func validateTeamRequest(value any) error {
	switch request := value.(type) {
	case teamcontract.TeamCapabilitiesRequestV1:
		return teamcontract.ValidateCapabilitiesRequest(request)
	case teamcontract.TeamStartRequestV1:
		return teamcontract.ValidateStartRequest(request)
	case teamcontract.TeamListRequestV1:
		return teamcontract.ValidateListRequest(request)
	case teamcontract.TeamGetRequestV1:
		return teamcontract.ValidateGetRequest(request)
	case teamcontract.TeamEventsRequestV1:
		return teamcontract.ValidateEventsRequest(request)
	case teamcontract.TeamMessagesRequestV1:
		return teamcontract.ValidateMessagesRequest(request)
	case teamcontract.TeamActionV1:
		return teamcontract.ValidateActionRequest(request)
	case teamcontract.HandoffCreateRequestV1:
		return teamcontract.ValidateHandoffCreateRequest(request)
	case teamcontract.HandoffGetRequestV1:
		return teamcontract.ValidateHandoffGetRequest(request)
	case teamcontract.HandoffListRequestV1:
		return teamcontract.ValidateHandoffListRequest(request)
	case teamcontract.HandoffBindRunRequestV1:
		return teamcontract.ValidateHandoffBindRunRequest(request)
	default:
		return fmt.Errorf("unsupported Team request type")
	}
}

func (s *Server) writeMappedTeamError(id any, err error) {
	code := teamcontract.ErrorInternal
	switch {
	case errors.Is(err, team.ErrInvalidArgument):
		code = teamcontract.ErrorInvalidArgument
	case errors.Is(err, team.ErrConflict):
		code = teamcontract.ErrorConflict
	case errors.Is(err, team.ErrCapabilityUnavailable):
		code = teamcontract.ErrorCapabilityUnavailable
	case errors.Is(err, team.ErrQuotaExceeded):
		code = teamcontract.ErrorQuotaExceeded
	case errors.Is(err, os.ErrNotExist):
		code = teamcontract.ErrorNotFound
	}
	s.writeTeamError(id, code, err.Error())
}

func (s *Server) writeTeamError(id any, code teamcontract.ErrorCode, message string) {
	message = boundedTeamError(message)
	rpcCode := -32603
	switch code {
	case teamcontract.ErrorInvalidArgument:
		rpcCode = -32602
	case teamcontract.ErrorNotFound:
		rpcCode = -32004
	case teamcontract.ErrorConflict:
		rpcCode = -32009
	case teamcontract.ErrorCapabilityUnavailable:
		rpcCode = -32010
	case teamcontract.ErrorQuotaExceeded:
		rpcCode = -32013
	case teamcontract.ErrorNotReady:
		rpcCode = -32011
	case teamcontract.ErrorProtocolMismatch:
		rpcCode = -32012
	}
	s.writeError(id, rpcCode, message, teamcontract.TeamErrorV1{Code: code, Message: message})
}

func boundedTeamError(value string) string {
	data := []byte(value)
	if len(data) <= teamcontract.MaxMessageBytes {
		return value
	}
	data = data[:teamcontract.MaxMessageBytes]
	for !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data)
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
