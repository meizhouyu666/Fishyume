package routingconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wf.local/wf-engine/internal/routing"
)

const teamDiscoveryVersion = "fishyume.team-driver-discovery/v1"

type TeamRouteSource string

const (
	TeamRouteBuiltin      TeamRouteSource = "builtin"
	TeamRouteUser         TeamRouteSource = "user"
	TeamRouteLegacyImport TeamRouteSource = "legacy_import"
)

type TeamRouteSetting struct {
	RouteID  string          `json:"routeId"`
	Driver   string          `json:"driver"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Enabled  bool            `json:"enabled"`
	Source   TeamRouteSource `json:"source"`
}

type TeamRouteConfig struct {
	SchemaVersion string             `json:"schemaVersion"`
	Revision      int                `json:"revision"`
	Routes        []TeamRouteSetting `json:"routes"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}

type teamConfigFile struct {
	TeamRouteConfig
	Mutations []mutationReceipt `json:"mutations,omitempty"`
}

type TeamDriverStatus struct {
	Driver     string    `json:"driver"`
	Available  bool      `json:"available"`
	Executable string    `json:"executable,omitempty"`
	Diagnostic string    `json:"diagnostic,omitempty"`
	ObservedAt time.Time `json:"observedAt"`
}

type teamDriverDiscoveryFile struct {
	SchemaVersion string             `json:"schemaVersion"`
	Drivers       []TeamDriverStatus `json:"drivers"`
}

type TeamRouteView struct {
	TeamRouteSetting
	DriverAvailable bool   `json:"driverAvailable"`
	Effective       bool   `json:"effective"`
	Diagnostic      string `json:"diagnostic,omitempty"`
}

type TeamRoutesResponse struct {
	SchemaVersion string             `json:"schemaVersion"`
	Config        TeamRouteConfig    `json:"config"`
	Drivers       []TeamDriverStatus `json:"drivers"`
	Routes        []TeamRouteView    `json:"routes"`
	CatalogHash   string             `json:"catalogHash,omitempty"`
}

type TeamRouteUpsertRequest struct {
	SchemaVersion    string `json:"schemaVersion"`
	MutationID       string `json:"mutationId"`
	ExpectedRevision int    `json:"expectedRevision"`
	RouteID          string `json:"routeId"`
	Driver           string `json:"driver"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Enabled          bool   `json:"enabled"`
}

type TeamRouteRemoveRequest struct {
	SchemaVersion    string `json:"schemaVersion"`
	MutationID       string `json:"mutationId"`
	ExpectedRevision int    `json:"expectedRevision"`
	RouteID          string `json:"routeId"`
}

type TeamRoutesMutationResponse struct {
	TeamRoutesResponse
	Replayed bool `json:"replayed"`
}

type TeamRouteApplier interface {
	ApplyTeamRoutes(routing.CapabilityCatalogV1, []TeamDriverStatus) error
}

func (s *Service) SetTeamRouteApplier(applier TeamRouteApplier) error {
	if applier == nil {
		return fmt.Errorf("Team route applier is required")
	}
	s.mu.Lock()
	s.teamApplier = applier
	catalog, err := s.effectiveTeamCatalogLocked()
	drivers := cloneTeamDrivers(s.teamDrivers.Drivers)
	s.mu.Unlock()
	if err != nil {
		return nil
	}
	return applier.ApplyTeamRoutes(catalog, drivers)
}

func (s *Service) TeamCatalog() (routing.CapabilityCatalogV1, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effectiveTeamCatalogLocked()
}

func (s *Service) TeamCatalogHistory() ([]routing.CapabilityCatalogV1, error) {
	entries, err := os.ReadDir(s.teamCatalogDir())
	if errors.Is(err, fs.ErrNotExist) {
		return []routing.CapabilityCatalogV1{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]routing.CapabilityCatalogV1, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var catalog routing.CapabilityCatalogV1
		if err := readJSONStrict(filepath.Join(s.teamCatalogDir(), entry.Name()), &catalog); err != nil {
			return nil, err
		}
		if _, err := routing.CatalogHash(catalog); err != nil {
			return nil, err
		}
		result = append(result, catalog)
	}
	return result, nil
}

func (s *Service) TeamRoutesGet() (TeamRoutesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.teamRoutesResponseLocked()
}

func (s *Service) TeamRoutesRefresh(_ context.Context) (TeamRoutesResponse, error) {
	s.mu.Lock()
	s.teamDrivers = discoverTeamDrivers(s.now().UTC())
	if err := writeJSONAtomic(s.teamDiscoveryPath(), s.teamDrivers); err != nil {
		s.mu.Unlock()
		return TeamRoutesResponse{}, err
	}
	response, err := s.teamRoutesResponseLocked()
	applier := s.teamApplier
	catalog, catalogErr := s.effectiveTeamCatalogLocked()
	if catalogErr == nil {
		catalogErr = s.persistTeamCatalogLocked(catalog)
	}
	drivers := cloneTeamDrivers(s.teamDrivers.Drivers)
	s.mu.Unlock()
	if err != nil {
		return TeamRoutesResponse{}, err
	}
	if catalogErr != nil {
		return response, nil
	}
	if applier != nil {
		if err := applier.ApplyTeamRoutes(catalog, drivers); err != nil {
			return TeamRoutesResponse{}, err
		}
	}
	return response, nil
}

func (s *Service) TeamRouteUpsert(request TeamRouteUpsertRequest) (TeamRoutesMutationResponse, error) {
	setting := TeamRouteSetting{RouteID: strings.TrimSpace(request.RouteID), Driver: strings.TrimSpace(request.Driver), Provider: strings.TrimSpace(request.Provider), Model: strings.TrimSpace(request.Model), Enabled: request.Enabled, Source: TeamRouteUser}
	if err := validateTeamMutation(request.SchemaVersion, request.MutationID, request.ExpectedRevision); err != nil {
		return TeamRoutesMutationResponse{}, err
	}
	if err := validateTeamRoute(setting); err != nil {
		return TeamRoutesMutationResponse{}, err
	}
	hash := teamMutationHash(request)
	return s.mutateTeamRoutes(request.MutationID, request.ExpectedRevision, hash, func(routes []TeamRouteSetting) ([]TeamRouteSetting, error) {
		updated := false
		for index := range routes {
			if routes[index].RouteID == setting.RouteID {
				if routes[index].Source == TeamRouteBuiltin {
					setting.Source = TeamRouteBuiltin
				}
				routes[index] = setting
				updated = true
				break
			}
		}
		if !updated {
			routes = append(routes, setting)
		}
		return routes, nil
	})
}

func (s *Service) TeamRouteRemove(request TeamRouteRemoveRequest) (TeamRoutesMutationResponse, error) {
	if err := validateTeamMutation(request.SchemaVersion, request.MutationID, request.ExpectedRevision); err != nil {
		return TeamRoutesMutationResponse{}, err
	}
	if strings.TrimSpace(request.RouteID) == "" || request.RouteID != strings.TrimSpace(request.RouteID) {
		return TeamRoutesMutationResponse{}, &ContractError{Code: "invalid_argument", Message: "routeId is required without surrounding whitespace"}
	}
	hash := teamMutationHash(request)
	return s.mutateTeamRoutes(request.MutationID, request.ExpectedRevision, hash, func(routes []TeamRouteSetting) ([]TeamRouteSetting, error) {
		result := make([]TeamRouteSetting, 0, len(routes))
		found := false
		for _, route := range routes {
			if route.RouteID == request.RouteID {
				found = true
				if route.Source == TeamRouteBuiltin {
					return nil, &ContractError{Code: "invalid_argument", Message: "built-in Team routes can be disabled but not removed"}
				}
				continue
			}
			result = append(result, route)
		}
		if !found {
			return routes, nil
		}
		return result, nil
	})
}

func (s *Service) mutateTeamRoutes(mutationID string, expectedRevision int, requestHash string, change func([]TeamRouteSetting) ([]TeamRouteSetting, error)) (TeamRoutesMutationResponse, error) {
	s.mu.Lock()
	for _, receipt := range s.teamConfig.Mutations {
		if receipt.MutationID == mutationID {
			if receipt.RequestHash != requestHash {
				s.mu.Unlock()
				return TeamRoutesMutationResponse{}, &ContractError{Code: "conflict", Message: "mutationId was already used with different content"}
			}
			response, err := s.teamRoutesResponseLocked()
			s.mu.Unlock()
			return TeamRoutesMutationResponse{TeamRoutesResponse: response, Replayed: true}, err
		}
	}
	if expectedRevision != s.teamConfig.Revision {
		current := s.teamConfig.Revision
		s.mu.Unlock()
		return TeamRoutesMutationResponse{}, &ContractError{Code: "conflict", Message: fmt.Sprintf("expected revision %d, current revision is %d", expectedRevision, current)}
	}
	previous := s.teamConfig
	routes, err := change(cloneTeamRoutes(s.teamConfig.Routes))
	if err != nil {
		s.mu.Unlock()
		return TeamRoutesMutationResponse{}, err
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].RouteID < routes[j].RouteID })
	if err := validateTeamRoutes(routes); err != nil {
		s.mu.Unlock()
		return TeamRoutesMutationResponse{}, err
	}
	s.teamConfig.Routes = routes
	s.teamConfig.Revision++
	s.teamConfig.UpdatedAt = s.now().UTC()
	s.teamConfig.Mutations = append(s.teamConfig.Mutations, mutationReceipt{MutationID: mutationID, RequestHash: requestHash, Revision: s.teamConfig.Revision})
	if len(s.teamConfig.Mutations) > maxMutationReceipts {
		s.teamConfig.Mutations = append([]mutationReceipt(nil), s.teamConfig.Mutations[len(s.teamConfig.Mutations)-maxMutationReceipts:]...)
	}
	if err := writeJSONAtomic(s.teamConfigPath(), s.teamConfig); err != nil {
		s.teamConfig = previous
		s.mu.Unlock()
		return TeamRoutesMutationResponse{}, err
	}
	response, err := s.teamRoutesResponseLocked()
	applier := s.teamApplier
	catalog, catalogErr := s.effectiveTeamCatalogLocked()
	if catalogErr == nil {
		catalogErr = s.persistTeamCatalogLocked(catalog)
	}
	drivers := cloneTeamDrivers(s.teamDrivers.Drivers)
	s.mu.Unlock()
	if err != nil {
		return TeamRoutesMutationResponse{}, err
	}
	if catalogErr != nil {
		return TeamRoutesMutationResponse{TeamRoutesResponse: response}, nil
	}
	if applier != nil {
		if err := applier.ApplyTeamRoutes(catalog, drivers); err != nil {
			return TeamRoutesMutationResponse{}, err
		}
	}
	return TeamRoutesMutationResponse{TeamRoutesResponse: response}, nil
}

func (s *Service) loadTeamRoutes() error {
	now := s.now().UTC()
	routes := defaultTeamRoutes()
	s.teamConfig = teamConfigFile{TeamRouteConfig: TeamRouteConfig{SchemaVersion: APIVersion, Revision: 1, Routes: routes, UpdatedAt: now}}
	err := readJSONStrict(s.teamConfigPath(), &s.teamConfig)
	if errors.Is(err, fs.ErrNotExist) {
		if legacyPath := strings.TrimSpace(os.Getenv(routing.AgentRoutesFileEnv)); legacyPath != "" {
			legacy, loadErr := routing.LoadCatalogFile(legacyPath)
			if loadErr != nil {
				return loadErr
			}
			s.teamConfig.Routes = make([]TeamRouteSetting, 0, len(legacy.Models))
			for _, model := range legacy.Models {
				s.teamConfig.Routes = append(s.teamConfig.Routes, TeamRouteSetting{RouteID: model.ID, Driver: model.Target.Driver, Provider: model.Target.Provider, Model: model.Target.Model, Enabled: true, Source: TeamRouteLegacyImport})
			}
		}
		sort.Slice(s.teamConfig.Routes, func(i, j int) bool { return s.teamConfig.Routes[i].RouteID < s.teamConfig.Routes[j].RouteID })
		if err := writeJSONAtomic(s.teamConfigPath(), s.teamConfig); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("read Team route configuration: %w", err)
	}
	if s.teamConfig.SchemaVersion != APIVersion || s.teamConfig.Revision < 1 {
		return &ContractError{Code: "invalid_config", Message: "Team route configuration version or revision is invalid"}
	}
	if err := validateTeamRoutes(s.teamConfig.Routes); err != nil {
		return err
	}
	s.teamDrivers = discoverTeamDrivers(now)
	if err := readJSONStrict(s.teamDiscoveryPath(), &s.teamDrivers); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read Team Driver discovery: %w", err)
	}
	// Executable availability is intentionally refreshed on every Engine start.
	s.teamDrivers = discoverTeamDrivers(now)
	if err := writeJSONAtomic(s.teamDiscoveryPath(), s.teamDrivers); err != nil {
		return err
	}
	if catalog, err := s.effectiveTeamCatalogLocked(); err == nil {
		return s.persistTeamCatalogLocked(catalog)
	}
	return nil
}

func (s *Service) effectiveTeamCatalogLocked() (routing.CapabilityCatalogV1, error) {
	available := map[string]bool{}
	for _, driver := range s.teamDrivers.Drivers {
		available[driver.Driver] = driver.Available
	}
	models := make([]routing.ModelCapabilityV1, 0, len(s.teamConfig.Routes))
	for _, route := range s.teamConfig.Routes {
		if route.Enabled && available[route.Driver] {
			models = append(models, teamCapability(route))
		}
	}
	if len(models) < 2 {
		return routing.CapabilityCatalogV1{}, &ContractError{Code: "capability_unavailable", Message: "at least two enabled Team routes across installed Agent Drivers are required"}
	}
	catalog := routing.CanonicalCatalogV1(routing.CapabilityCatalogV1{SchemaVersion: routing.CapabilityCatalogV1Version, PolicyVersion: routing.RoutingPolicyV1Version, Models: models})
	if err := routing.ValidateCatalog(catalog); err != nil {
		return routing.CapabilityCatalogV1{}, err
	}
	return catalog, nil
}

func (s *Service) teamRoutesResponseLocked() (TeamRoutesResponse, error) {
	catalog, catalogErr := s.effectiveTeamCatalogLocked()
	hash := ""
	if catalogErr == nil {
		var err error
		hash, err = routing.CatalogHash(catalog)
		if err != nil {
			return TeamRoutesResponse{}, err
		}
	}
	status := map[string]TeamDriverStatus{}
	for _, driver := range s.teamDrivers.Drivers {
		status[driver.Driver] = driver
	}
	views := make([]TeamRouteView, 0, len(s.teamConfig.Routes))
	for _, route := range s.teamConfig.Routes {
		driver := status[route.Driver]
		view := TeamRouteView{TeamRouteSetting: route, DriverAvailable: driver.Available, Effective: route.Enabled && driver.Available}
		if !driver.Available {
			view.Diagnostic = driver.Diagnostic
		} else if !route.Enabled {
			view.Diagnostic = "route is disabled"
		}
		views = append(views, view)
	}
	return TeamRoutesResponse{SchemaVersion: APIVersion, Config: cloneTeamConfig(s.teamConfig.TeamRouteConfig), Drivers: cloneTeamDrivers(s.teamDrivers.Drivers), Routes: views, CatalogHash: hash}, nil
}

func discoverTeamDrivers(now time.Time) teamDriverDiscoveryFile {
	drivers := []string{"claude", "codex", "opencode"}
	result := teamDriverDiscoveryFile{SchemaVersion: teamDiscoveryVersion, Drivers: make([]TeamDriverStatus, 0, len(drivers))}
	for _, name := range drivers {
		candidate := strings.TrimSpace(os.Getenv("FISHYUME_" + strings.ToUpper(name) + "_PATH"))
		if candidate == "" {
			candidate = name
		}
		path, err := exec.LookPath(candidate)
		status := TeamDriverStatus{Driver: name, ObservedAt: now}
		if err != nil {
			status.Diagnostic = name + " CLI was not found"
		} else {
			absolute, absErr := filepath.Abs(path)
			if absErr != nil {
				status.Diagnostic = boundedDiagnostic(absErr.Error())
			} else {
				status.Available, status.Executable = true, filepath.Clean(absolute)
			}
		}
		result.Drivers = append(result.Drivers, status)
	}
	return result
}

func defaultTeamRoutes() []TeamRouteSetting {
	return []TeamRouteSetting{
		{RouteID: "claude/default/default", Driver: "claude", Provider: "default", Model: "default", Enabled: true, Source: TeamRouteBuiltin},
		{RouteID: "codex/architect/gpt-5.6-sol", Driver: "codex", Provider: "local", Model: "gpt-5.6-sol", Enabled: true, Source: TeamRouteBuiltin},
		{RouteID: "codex/reviewer/gpt-5.6-sol", Driver: "codex", Provider: "local", Model: "gpt-5.6-sol", Enabled: true, Source: TeamRouteBuiltin},
		{RouteID: "opencode/default/default", Driver: "opencode", Provider: "default", Model: "default", Enabled: true, Source: TeamRouteBuiltin},
	}
}

func teamCapability(route TeamRouteSetting) routing.ModelCapabilityV1 {
	capabilities := routing.SortCapabilities([]routing.Capability{routing.CapabilityRepoRead, routing.CapabilityStreaming, routing.CapabilityToolUse})
	quality, cost, latency := routing.QualityBalanced, routing.CostMedium, routing.LatencyBalanced
	contextBytes, outputBytes := 128*1024, 32*1024
	if route.Driver == "codex" || route.Driver == "claude" {
		quality, cost, contextBytes, outputBytes = routing.QualityPremium, routing.CostHigh, 256*1024, 64*1024
	}
	return routing.ModelCapabilityV1{ID: route.RouteID, Target: routing.Target{Driver: route.Driver, Provider: route.Provider, Model: route.Model}, Capabilities: capabilities, ContextLimitBytes: contextBytes, MaxOutputBytes: outputBytes, Quality: quality, Cost: cost, Latency: latency, SupportsCancellation: true}
}

func validateTeamRoutes(routes []TeamRouteSetting) error {
	if len(routes) == 0 || len(routes) > routing.MaxCatalogModels {
		return &ContractError{Code: "invalid_config", Message: "Team routes count is out of bounds"}
	}
	seen := map[string]bool{}
	last := ""
	for _, route := range routes {
		if err := validateTeamRoute(route); err != nil {
			return err
		}
		if seen[route.RouteID] || last != "" && route.RouteID <= last {
			return &ContractError{Code: "invalid_config", Message: "Team routes must have unique canonical routeIds"}
		}
		seen[route.RouteID], last = true, route.RouteID
	}
	return nil
}

func validateTeamRoute(route TeamRouteSetting) error {
	if route.Driver != "codex" && route.Driver != "claude" && route.Driver != "opencode" {
		return &ContractError{Code: "invalid_argument", Message: "Team route Driver must be codex, claude, or opencode"}
	}
	if !strings.HasPrefix(route.RouteID, route.Driver+"/") {
		return &ContractError{Code: "invalid_argument", Message: "Team routeId must begin with its Driver name"}
	}
	if route.Source != TeamRouteBuiltin && route.Source != TeamRouteUser && route.Source != TeamRouteLegacyImport {
		return &ContractError{Code: "invalid_config", Message: "Team route source is invalid"}
	}
	model := teamCapability(route)
	if err := routing.ValidateModelCapability(model); err != nil {
		return &ContractError{Code: "invalid_argument", Message: err.Error()}
	}
	if model.Target.Driver != route.Driver || strings.TrimSpace(route.RouteID) != route.RouteID || strings.TrimSpace(route.Provider) != route.Provider || strings.TrimSpace(route.Model) != route.Model {
		return &ContractError{Code: "invalid_argument", Message: "Team route fields cannot contain surrounding whitespace"}
	}
	return nil
}

func validateTeamMutation(version, mutationID string, revision int) error {
	if version != APIVersion || strings.TrimSpace(mutationID) == "" || mutationID != strings.TrimSpace(mutationID) || len(mutationID) > 256 || revision < 1 {
		return &ContractError{Code: "invalid_argument", Message: "valid schemaVersion, mutationId, and expectedRevision are required"}
	}
	return nil
}

func teamMutationHash(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneTeamRoutes(values []TeamRouteSetting) []TeamRouteSetting {
	return append([]TeamRouteSetting(nil), values...)
}
func cloneTeamDrivers(values []TeamDriverStatus) []TeamDriverStatus {
	return append([]TeamDriverStatus(nil), values...)
}
func cloneTeamConfig(value TeamRouteConfig) TeamRouteConfig {
	value.Routes = cloneTeamRoutes(value.Routes)
	return value
}
func (s *Service) teamConfigPath() string {
	return filepath.Join(s.root, "config", "team-routing.json")
}
func (s *Service) teamDiscoveryPath() string {
	return filepath.Join(s.root, "routing", "team-drivers.json")
}
func (s *Service) teamCatalogDir() string { return filepath.Join(s.root, "routing", "team-catalogs") }

func (s *Service) persistTeamCatalogLocked(catalog routing.CapabilityCatalogV1) error {
	hash, err := routing.CatalogHash(catalog)
	if err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(s.teamCatalogDir(), hash+".json"), catalog)
}
