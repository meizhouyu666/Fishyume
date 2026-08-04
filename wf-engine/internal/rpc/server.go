package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"wf.local/wf-engine/internal/run"
)

const (
	EngineVersion  = "0.1.0"
	MaxMessageSize = 1 << 20
)

var supportedMethods = []string{"engine.hello", "run.start", "run.get", "run.detach", "run.cancel"}

type Server struct {
	reader  *bufio.Reader
	writer  io.Writer
	service *run.Service
	writeMu sync.Mutex
}

func NewServer(input io.Reader, output io.Writer, service *run.Service) *Server {
	server := &Server{reader: bufio.NewReaderSize(input, 64*1024), writer: output, service: service}
	service.SetEventSink(func(event run.RunEvent) {
		_ = server.write(Notification{JSONRPC: "2.0", ProtocolVersion: ProtocolVersion, Method: "run.event", Params: event})
	})
	return server
}

func (s *Server) Serve(ctx context.Context) error {
	for {
		line, err := readLine(s.reader, MaxMessageSize)
		if errors.Is(err, io.EOF) {
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
		s.handle(ctx, request)
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

	switch request.Method {
	case "engine.hello":
		var params helloParams
		if len(request.Params) > 0 && string(request.Params) != "null" {
			if err := json.Unmarshal(request.Params, &params); err != nil {
				s.writeError(id, -32602, "invalid engine.hello params", err.Error())
				return
			}
		}
		doctor := s.service.Doctor(ctx, params.Project)
		s.writeResult(id, HelloResult{EngineVersion: EngineVersion, ProtocolVersion: ProtocolVersion,
			SupportedMethods: supportedMethods, SupportedBackends: []string{"ccpanes"},
			BackendReady: doctor.BackendReady, BackendDiagnostic: doctor.BackendDiagnostic,
			ProjectChecked: doctor.ProjectChecked, ProjectReady: doctor.ProjectReady,
			ProjectDiagnostic: doctor.ProjectDiagnostic})
	case "run.start":
		var params run.StartRequest
		if err := decodeParams(request.Params, &params); err != nil {
			s.writeError(id, -32602, "invalid run.start params", err.Error())
			return
		}
		snapshot, err := s.service.Start(ctx, params)
		if err != nil {
			s.writeError(id, -32602, "could not start run", err.Error())
			return
		}
		s.writeResult(id, StartResult{ProtocolVersion: ProtocolVersion, RunID: snapshot.ID})
	case "run.get":
		params, ok := s.parseRunID(id, request.Params)
		if !ok {
			return
		}
		snapshot, err := s.service.Get(params.RunID)
		if err != nil {
			s.writeError(id, -32004, "run not found", err.Error())
			return
		}
		s.writeResult(id, snapshot)
	case "run.detach":
		params, ok := s.parseRunID(id, request.Params)
		if !ok {
			return
		}
		snapshot, err := s.service.Detach(params.RunID)
		if err != nil {
			s.writeError(id, -32000, "could not detach run", err.Error())
			return
		}
		s.writeResult(id, snapshot)
	case "run.cancel":
		params, ok := s.parseRunID(id, request.Params)
		if !ok {
			return
		}
		snapshot, err := s.service.Cancel(ctx, params.RunID)
		if err != nil {
			s.writeError(id, -32000, "could not cancel run", err.Error())
			return
		}
		s.writeResult(id, snapshot)
	default:
		s.writeError(id, -32601, "method not found", request.Method)
	}
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
	return json.Unmarshal(raw, target)
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
