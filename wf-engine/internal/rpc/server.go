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
	"wf.local/wf-engine/internal/routingconfig"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/team"
	"wf.local/wf-engine/internal/teamcontract"
)

const (
	EngineVersion  = "0.2.1-alpha.1"
	MaxMessageSize = 1 << 20
)

var supportedMethods = append(append(append([]string{"engine.hello"}, application.StableMethods...), teamcontract.StableMethods...), routingconfig.StableMethods...)

type Server struct {
	reader               *bufio.Reader
	writer               io.Writer
	service              *run.Service
	application          *application.Service
	teams                *team.Service
	routingConfig        *routingconfig.Service
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
	return newServer(input, output, service, selectApplication(service, applications), nil, nil, nil, true)
}

func NewServerWithTeam(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, teamService *team.Service) *Server {
	return newServer(input, output, service, applicationService, teamService, nil, nil, true)
}

func NewServerWithTeamAndConfig(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, teamService *team.Service, config *routingconfig.Service) *Server {
	return newServer(input, output, service, applicationService, teamService, config, nil, true)
}

func NewConnectionServer(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, mutationMu *sync.Mutex, teams ...*team.Service) *Server {
	var teamService *team.Service
	if len(teams) > 0 {
		teamService = teams[0]
	}
	return newServer(input, output, service, applicationService, teamService, nil, mutationMu, false)
}

func NewConnectionServerWithConfig(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, mutationMu *sync.Mutex, teamService *team.Service, config *routingconfig.Service) *Server {
	return newServer(input, output, service, applicationService, teamService, config, mutationMu, false)
}

func selectApplication(service *run.Service, applications []*application.Service) *application.Service {
	if len(applications) > 0 && applications[0] != nil {
		return applications[0]
	}
	return application.NewService(service, "codex", service.ApplicationJournal())
}

func newServer(input io.Reader, output io.Writer, service *run.Service, applicationService *application.Service, teamService *team.Service, config *routingconfig.Service, mutationMu *sync.Mutex, waitControllersOnEOF bool) *Server {
	server := &Server{reader: bufio.NewReaderSize(input, 64*1024), writer: output, service: service, application: applicationService, teams: teamService, routingConfig: config, mutationMu: mutationMu, waitControllersOnEOF: waitControllersOnEOF}
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
	case "driver.list":
		s.invokeRoutingRead(id, request.Params, func() any { return s.routingConfig.DriverList() })
	case "driver.models.discover":
		s.invokeRoutingDiscover(ctx, id, request.Params)
	case "driver.models.probe":
		s.invokeRoutingProbe(ctx, id, request.Params)
	case "routing.config.get":
		s.invokeRoutingRead(id, request.Params, func() any { return s.routingConfig.ConfigGet() })
	case "routing.config.update":
		s.invokeRoutingConfigUpdate(id, request.Params)
	case "routing.availability":
		s.invokeRoutingRead(id, request.Params, func() any { return s.routingConfig.Availability() })
	case "routing.catalog.effective":
		s.invokeRoutingEffectiveCatalog(id, request.Params)
	case "team.routes.get":
		s.invokeTeamRoutesGet(id, request.Params)
	case "team.routes.refresh":
		s.invokeTeamRoutesRefresh(ctx, id, request.Params)
	case "team.routes.upsert":
		s.invokeTeamRouteUpsert(id, request.Params)
	case "team.routes.remove":
		s.invokeTeamRouteRemove(id, request.Params)
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
	case "team.template.list":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamTemplateListRequestV1{}, func(_ context.Context, value teamcontract.TeamTemplateListRequestV1) (teamcontract.TeamTemplateListResponseV1, error) {
			return s.teams.TemplateList(value)
		})
	case "team.template.get":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamTemplateGetRequestV1{}, func(_ context.Context, value teamcontract.TeamTemplateGetRequestV1) (teamcontract.TeamTemplateV1, error) {
			return s.teams.TemplateGet(value)
		})
	case "team.template.upsert":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamTemplateUpsertRequestV1{}, func(_ context.Context, value teamcontract.TeamTemplateUpsertRequestV1) (teamcontract.TeamTemplateUpsertResponseV1, error) {
			template, err := s.teams.TemplateUpsert(value)
			return teamcontract.TeamTemplateUpsertResponseV1{SchemaVersion: teamcontract.TemplateSchemaVersion, Template: template}, err
		})
	case "team.template.delete":
		invokeTeam(ctx, s, id, request.Params, teamcontract.TeamTemplateDeleteRequestV1{}, func(_ context.Context, value teamcontract.TeamTemplateDeleteRequestV1) (teamcontract.TeamTemplateDeleteResponseV1, error) {
			if err := s.teams.TemplateDelete(value); err != nil {
				return teamcontract.TeamTemplateDeleteResponseV1{}, err
			}
			return teamcontract.TeamTemplateDeleteResponseV1{SchemaVersion: teamcontract.TemplateSchemaVersion, TemplateID: value.TemplateID}, nil
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
	case "run.start", "run.action", "memory.create", "memory.supersede", "memory.delete", "memory.host.create", "memory.host.supersede", "memory.host.delete", "team.start", "team.action", "team.handoff.create", "team.handoff.bindRun", "team.template.upsert", "team.template.delete", "driver.models.discover", "driver.models.probe", "routing.config.update", "team.routes.refresh", "team.routes.upsert", "team.routes.remove":
		return true
	default:
		return false
	}
}

func (s *Server) invokeRoutingRead(id any, raw json.RawMessage, response func() any) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.ReadRequest
	if err := decodeParams(raw, &request); err != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "invalid routing request: " + err.Error()})
		return
	}
	if err := routingconfig.ValidateReadRequest(request); err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response())
}

func (s *Server) invokeRoutingDiscover(ctx context.Context, id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.ReadRequest
	if err := decodeParams(raw, &request); err != nil || routingconfig.ValidateReadRequest(request) != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "valid config schemaVersion is required"})
		return
	}
	response, err := s.routingConfig.Discover(ctx)
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) invokeRoutingProbe(ctx context.Context, id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.ProbeRequest
	if err := decodeParams(raw, &request); err != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "invalid probe request: " + err.Error()})
		return
	}
	response, err := s.routingConfig.Probe(ctx, request)
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) invokeRoutingConfigUpdate(id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.ConfigUpdateRequest
	if err := decodeParams(raw, &request); err != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "invalid routing config update: " + err.Error()})
		return
	}
	response, err := s.routingConfig.ConfigUpdate(request)
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) invokeRoutingEffectiveCatalog(id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.ReadRequest
	if err := decodeParams(raw, &request); err != nil || routingconfig.ValidateReadRequest(request) != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "valid config schemaVersion is required"})
		return
	}
	response, err := s.routingConfig.EffectiveCatalog()
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) invokeTeamRoutesRefresh(ctx context.Context, id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.ReadRequest
	if err := decodeParams(raw, &request); err != nil || routingconfig.ValidateReadRequest(request) != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "valid config schemaVersion is required"})
		return
	}
	response, err := s.routingConfig.TeamRoutesRefresh(ctx)
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) invokeTeamRoutesGet(id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.ReadRequest
	if err := decodeParams(raw, &request); err != nil || routingconfig.ValidateReadRequest(request) != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "valid config schemaVersion is required"})
		return
	}
	response, err := s.routingConfig.TeamRoutesGet()
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) invokeTeamRouteUpsert(id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.TeamRouteUpsertRequest
	if err := decodeParams(raw, &request); err != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "invalid Team route upsert: " + err.Error()})
		return
	}
	response, err := s.routingConfig.TeamRouteUpsert(request)
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) invokeTeamRouteRemove(id any, raw json.RawMessage) {
	if s.routingConfig == nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "capability_unavailable", Message: "routing configuration service is unavailable"})
		return
	}
	var request routingconfig.TeamRouteRemoveRequest
	if err := decodeParams(raw, &request); err != nil {
		s.writeRoutingError(id, &routingconfig.ContractError{Code: "invalid_argument", Message: "invalid Team route removal: " + err.Error()})
		return
	}
	response, err := s.routingConfig.TeamRouteRemove(request)
	if err != nil {
		s.writeRoutingError(id, err)
		return
	}
	s.writeResult(id, response)
}

func (s *Server) writeRoutingError(id any, err error) {
	code, message, rpcCode := "internal", err.Error(), -32603
	var contract *routingconfig.ContractError
	if errors.As(err, &contract) {
		code, message = contract.Code, contract.Message
		switch code {
		case "invalid_argument", "unsupported_version", "invalid_config":
			rpcCode = -32602
		case "conflict":
			rpcCode = -32009
		case "capability_unavailable", "model_unavailable":
			rpcCode = -32010
		}
	}
	s.writeError(id, rpcCode, message, map[string]string{"code": code})
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
	case teamcontract.TeamTemplateListRequestV1:
		return teamcontract.ValidateTemplateListRequest(request)
	case teamcontract.TeamTemplateGetRequestV1:
		return teamcontract.ValidateTemplateGetRequest(request)
	case teamcontract.TeamTemplateUpsertRequestV1:
		return teamcontract.ValidateTemplateUpsertRequest(request)
	case teamcontract.TeamTemplateDeleteRequestV1:
		return teamcontract.ValidateTemplateDeleteRequest(request)
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
	case errors.Is(err, team.ErrSessionLost):
		code = teamcontract.ErrorSessionLost
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
