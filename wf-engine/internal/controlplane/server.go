package controlplane

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/rpc"
	"wf.local/wf-engine/internal/run"
)

const (
	agentFrameBufferSize = 64 * 1024
	handshakeTimeout     = 5 * time.Second
)

type Server struct {
	owner       *Owner
	listener    net.Listener
	service     *run.Service
	application *application.Service
	mutationMu  sync.Mutex
	connections atomic.Int64
	closed      chan struct{}
	closeOnce   sync.Once
}

func NewServer(owner *Owner, service *run.Service, applications ...*application.Service) (*Server, error) {
	if owner == nil || service == nil {
		return nil, errors.New("control plane owner and Run service are required")
	}
	listener, err := listenEndpoint(owner.Record())
	if err != nil {
		return nil, err
	}
	if err := owner.Publish(); err != nil {
		_ = listener.Close()
		_ = cleanupEndpoint(owner.Record())
		return nil, fmt.Errorf("publish control plane owner: %w", err)
	}
	applicationService := application.NewService(service, "codex", service.ApplicationJournal())
	if len(applications) > 0 && applications[0] != nil {
		applicationService = applications[0]
	}
	return &Server{owner: owner, listener: listener, service: service, application: applicationService, closed: make(chan struct{})}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.closed:
		}
	}()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return nil
			default:
				return fmt.Errorf("accept control plane connection: %w", err)
			}
		}
		s.connections.Add(1)
		go func() {
			defer s.connections.Add(-1)
			defer connection.Close()
			if err := s.handshake(connection); err != nil {
				return
			}
			server := rpc.NewConnectionServer(connection, connection, s.service, s.application, &s.mutationMu)
			_ = server.Serve(context.Background())
		}()
	}
}

func (s *Server) handshake(connection net.Conn) error {
	if err := connection.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(connection, agentFrameBufferSize)
	frame, err := readFrame(reader, agentFrameBufferSize)
	if err != nil {
		return err
	}
	var request HandshakeRequest
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return s.writeHandshakeError(connection, "invalid handshake: "+err.Error())
	}
	record := s.owner.Record()
	if request.ProtocolVersion != record.ProtocolVersion || request.RPCProtocolVersion != record.RPCProtocolVersion || request.StateSchema != record.StateSchema || request.EngineVersion != record.EngineVersion || request.OwnerID != record.OwnerID || request.StateDirHash != record.StateDirHash {
		return s.writeHandshakeError(connection, "control plane handshake identity mismatch")
	}
	response := HandshakeResponse{OK: true, EngineVersion: record.EngineVersion, Handshake: agent.IPCHandshake{
		ProtocolVersion: record.ProtocolVersion, StateSchema: record.StateSchema, OwnerID: record.OwnerID, StateDirHash: record.StateDirHash,
	}}
	if err := writeFrame(connection, response); err != nil {
		return err
	}
	return connection.SetDeadline(time.Time{})
}

func (s *Server) writeHandshakeError(connection net.Conn, message string) error {
	record := s.owner.Record()
	_ = writeFrame(connection, HandshakeResponse{OK: false, EngineVersion: record.EngineVersion, Error: message, Handshake: agent.IPCHandshake{
		ProtocolVersion: record.ProtocolVersion, StateSchema: record.StateSchema, OwnerID: record.OwnerID, StateDirHash: record.StateDirHash,
	}})
	return errors.New(message)
}

func writeFrame(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data)+1 > agentFrameBufferSize {
		return errors.New("control plane handshake frame is too large")
	}
	_, err = writer.Write(append(data, '\n'))
	return err
}

func readFrame(reader *bufio.Reader, limit int) ([]byte, error) {
	data := make([]byte, 0, 1024)
	for {
		part, err := reader.ReadSlice('\n')
		if len(data)+len(part) > limit {
			return nil, errors.New("control plane handshake frame is too large")
		}
		data = append(data, part...)
		if err == nil {
			return data, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return nil, err
	}
}

func (s *Server) Connections() int64 { return s.connections.Load() }

func (s *Server) Close() error {
	var result error
	s.closeOnce.Do(func() {
		close(s.closed)
		result = s.listener.Close()
		_ = cleanupEndpoint(s.owner.Record())
		if ownerErr := s.owner.Close(); result == nil {
			result = ownerErr
		}
	})
	return result
}

// Dial is used by integration tests and controlled embedded clients.
func Dial(record OwnerRecord, timeout time.Duration) (net.Conn, error) {
	connection, err := dialEndpoint(record.Endpoint, timeout)
	if err != nil {
		return nil, err
	}
	request := HandshakeRequest{ProtocolVersion: record.ProtocolVersion, RPCProtocolVersion: record.RPCProtocolVersion, StateSchema: record.StateSchema, EngineVersion: record.EngineVersion, OwnerID: record.OwnerID, StateDirHash: record.StateDirHash}
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		connection.Close()
		return nil, err
	}
	if err := writeFrame(connection, request); err != nil {
		connection.Close()
		return nil, err
	}
	frame, err := readFrame(bufio.NewReaderSize(connection, agentFrameBufferSize), agentFrameBufferSize)
	if err != nil {
		connection.Close()
		return nil, err
	}
	var response HandshakeResponse
	if err := json.Unmarshal(frame, &response); err != nil || !response.OK {
		connection.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New(response.Error)
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		connection.Close()
		return nil, err
	}
	return connection, nil
}
