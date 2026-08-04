package rpc

import "encoding/json"

const ProtocolVersion = 1

type Request struct {
	JSONRPC         string          `json:"jsonrpc"`
	ProtocolVersion int             `json:"protocolVersion"`
	ID              json.RawMessage `json:"id"`
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC         string    `json:"jsonrpc"`
	ProtocolVersion int       `json:"protocolVersion"`
	ID              any       `json:"id"`
	Result          any       `json:"result,omitempty"`
	Error           *RPCError `json:"error,omitempty"`
}

type Notification struct {
	JSONRPC         string `json:"jsonrpc"`
	ProtocolVersion int    `json:"protocolVersion"`
	Method          string `json:"method"`
	Params          any    `json:"params"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type HelloResult struct {
	EngineVersion     string   `json:"engineVersion"`
	ProtocolVersion   int      `json:"protocolVersion"`
	SupportedMethods  []string `json:"supportedMethods"`
	SupportedBackends []string `json:"supportedBackends"`
	BackendReady      bool     `json:"backendReady"`
	BackendDiagnostic string   `json:"backendDiagnostic"`
	ProjectChecked    bool     `json:"projectChecked"`
	ProjectReady      bool     `json:"projectReady"`
	ProjectDiagnostic string   `json:"projectDiagnostic,omitempty"`
}

type helloParams struct {
	Project string `json:"project"`
}

type StartResult struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RunID           string `json:"runId"`
}

type runIDParams struct {
	RunID string `json:"runId"`
}
