package routingconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"wf.local/wf-engine/internal/driver/codexprocess"
	"wf.local/wf-engine/internal/routing"
)

const (
	APIVersion          = "fishyume.config/v1"
	availabilityVersion = "fishyume.routing-availability/v1"
	discoveryVersion    = "fishyume.driver-model-discovery/v1"
	maxMutationReceipts = 64
	availabilityTTL     = 24 * time.Hour
)

var StableMethods = []string{
	"driver.list", "driver.models.discover", "driver.models.probe",
	"routing.config.get", "routing.config.update", "routing.availability", "routing.catalog.effective",
}

type ModelInspector interface {
	DiscoverModels(context.Context) ([]codexprocess.ModelInfo, error)
	ProbeModel(context.Context, string, string) codexprocess.ProbeResult
}

type RouteSetting struct {
	RouteID string `json:"routeId"`
	Enabled bool   `json:"enabled"`
}

type Config struct {
	SchemaVersion string         `json:"schemaVersion"`
	Revision      int            `json:"revision"`
	Routes        []RouteSetting `json:"routes"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

type mutationReceipt struct {
	MutationID  string `json:"mutationId"`
	RequestHash string `json:"requestHash"`
	Revision    int    `json:"revision"`
}

type configFile struct {
	Config
	Mutations []mutationReceipt `json:"mutations,omitempty"`
}

type AvailabilityStatus string

const (
	AvailabilityUnknown     AvailabilityStatus = "unknown"
	AvailabilityAvailable   AvailabilityStatus = "available"
	AvailabilityUnavailable AvailabilityStatus = "unavailable"
)

type AvailabilityEntry struct {
	RouteID         string             `json:"routeId"`
	Model           string             `json:"model"`
	Status          AvailabilityStatus `json:"status"`
	ReasoningEffort string             `json:"reasoningEffort,omitempty"`
	ObservedAt      *time.Time         `json:"observedAt,omitempty"`
	ExpiresAt       *time.Time         `json:"expiresAt,omitempty"`
	Diagnostic      string             `json:"diagnostic,omitempty"`
}

type availabilityFile struct {
	SchemaVersion string              `json:"schemaVersion"`
	Entries       []AvailabilityEntry `json:"entries"`
}

type discoveryFile struct {
	SchemaVersion string                   `json:"schemaVersion"`
	ObservedAt    time.Time                `json:"observedAt"`
	Models        []codexprocess.ModelInfo `json:"models"`
}

type ProductProfile struct {
	RouteID       string   `json:"routeId"`
	Model         string   `json:"model"`
	Qualified     bool     `json:"qualified"`
	DefaultEffort string   `json:"defaultReasoningEffort"`
	Efforts       []string `json:"reasoningEfforts"`
	UseCases      []string `json:"recommendedUseCases"`
}

type RouteView struct {
	ProductProfile
	Discovered   bool               `json:"discovered"`
	Enabled      bool               `json:"enabled"`
	Availability AvailabilityStatus `json:"availability"`
	Routable     bool               `json:"routable"`
	Diagnostic   string             `json:"diagnostic,omitempty"`
}

type DriverListResponse struct {
	SchemaVersion string       `json:"schemaVersion"`
	Drivers       []DriverView `json:"drivers"`
}

type ReadRequest struct {
	SchemaVersion string `json:"schemaVersion"`
}

func ValidateReadRequest(request ReadRequest) error {
	if request.SchemaVersion != APIVersion {
		return &ContractError{Code: "unsupported_version", Message: "schemaVersion must be " + APIVersion}
	}
	return nil
}

type DriverView struct {
	Driver           string     `json:"driver"`
	Provider         string     `json:"provider"`
	WorkflowEligible bool       `json:"workflowEligible"`
	ModelCount       int        `json:"modelCount"`
	LastDiscoveredAt *time.Time `json:"lastDiscoveredAt,omitempty"`
}

type DiscoveryResponse struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Driver        string                   `json:"driver"`
	ObservedAt    time.Time                `json:"observedAt"`
	Models        []codexprocess.ModelInfo `json:"models"`
	Routes        []RouteView              `json:"routes"`
}

type ProbeRequest struct {
	SchemaVersion string   `json:"schemaVersion"`
	RouteIDs      []string `json:"routeIds,omitempty"`
}

type ProbeResponse struct {
	SchemaVersion string              `json:"schemaVersion"`
	Entries       []AvailabilityEntry `json:"entries"`
	CatalogHash   string              `json:"catalogHash"`
}

type ConfigGetResponse struct {
	SchemaVersion string `json:"schemaVersion"`
	Config        Config `json:"config"`
}

type ConfigUpdateRequest struct {
	SchemaVersion    string `json:"schemaVersion"`
	MutationID       string `json:"mutationId"`
	ExpectedRevision int    `json:"expectedRevision"`
	RouteID          string `json:"routeId"`
	Enabled          bool   `json:"enabled"`
}

type ConfigUpdateResponse struct {
	SchemaVersion string `json:"schemaVersion"`
	Config        Config `json:"config"`
	Replayed      bool   `json:"replayed"`
}

type AvailabilityResponse struct {
	SchemaVersion string              `json:"schemaVersion"`
	Entries       []AvailabilityEntry `json:"entries"`
}

type EffectiveCatalogResponse struct {
	SchemaVersion string                      `json:"schemaVersion"`
	Source        string                      `json:"source"`
	CatalogHash   string                      `json:"catalogHash"`
	Catalog       routing.CapabilityCatalogV1 `json:"catalog"`
	Routes        []RouteView                 `json:"routes"`
}

type ContractError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ContractError) Error() string { return e.Code + ": " + e.Message }

type Service struct {
	mu        sync.Mutex
	root      string
	inspector ModelInspector
	registry  *routing.CatalogRegistry
	now       func() time.Time
	config    configFile
	discovery discoveryFile
	available availabilityFile
}

func NewService(root string, inspector ModelInspector) (*Service, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("routing configuration root must be absolute")
	}
	service := &Service{root: root, inspector: inspector, now: time.Now}
	if err := service.load(); err != nil {
		return nil, err
	}
	historical, err := service.loadCatalogSnapshots()
	if err != nil {
		return nil, err
	}
	active, err := service.buildEffectiveCatalog()
	if err != nil {
		return nil, err
	}
	service.registry, err = routing.NewCatalogRegistry(active, append(historical, routing.BuiltinCatalogV1())...)
	if err != nil {
		return nil, err
	}
	if err := service.persistActiveCatalog(); err != nil {
		return nil, err
	}
	return service, nil
}

func ProductProfiles() []ProductProfile {
	return []ProductProfile{
		{RouteID: "codex/local/gpt-5.6-luna", Model: "gpt-5.6-luna", Qualified: true, DefaultEffort: "medium", Efforts: []string{"low", "medium", "high", "xhigh", "max"}, UseCases: []string{"bounded low-cost work", "independent comparison"}},
		{RouteID: "codex/local/gpt-5.6-sol", Model: "gpt-5.6-sol", Qualified: true, DefaultEffort: "medium", Efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, UseCases: []string{"general workflow", "repository implementation", "complex reasoning"}},
		{RouteID: "codex/local/gpt-5.6-terra", Model: "gpt-5.6-terra", Qualified: true, DefaultEffort: "medium", Efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, UseCases: []string{"quality-sensitive alternative", "deep review"}},
	}
}

func (s *Service) ActiveCatalog() (routing.CapabilityCatalogV1, string, error) {
	return s.registry.ActiveCatalog()
}

func (s *Service) CatalogByHash(hash string) (routing.CapabilityCatalogV1, bool) {
	return s.registry.CatalogByHash(hash)
}

func (s *Service) EnsureTargetAvailable(ctx context.Context, target routing.Target) error {
	if target.Driver != "codex" || target.Provider != "local" {
		return &ContractError{Code: "capability_unavailable", Message: "Workflow dynamic routing currently supports only codex/local"}
	}
	routeID := target.Driver + "/" + target.Provider + "/" + target.Model
	s.mu.Lock()
	profile, qualified := profileMap()[routeID]
	if !qualified || profile.Model != target.Model {
		s.mu.Unlock()
		return &ContractError{Code: "capability_unavailable", Message: "Codex route " + routeID + " is not product-qualified"}
	}
	enabled := false
	for _, setting := range s.config.Routes {
		if setting.RouteID == routeID {
			enabled = setting.Enabled
			break
		}
	}
	if !enabled {
		s.mu.Unlock()
		return &ContractError{Code: "model_unavailable", Message: "Codex route " + routeID + " is disabled"}
	}
	if len(s.discovery.Models) > 0 && !modelDiscovered(s.discovery.Models, target.Model) {
		s.mu.Unlock()
		return &ContractError{Code: "model_unavailable", Message: "Codex route " + routeID + " is not exposed by the installed Codex Agent"}
	}
	var status AvailabilityStatus
	for _, entry := range s.available.Entries {
		if entry.RouteID == routeID {
			status = effectiveAvailability(entry, s.now().UTC())
			break
		}
	}
	s.mu.Unlock()
	if status == AvailabilityAvailable {
		return nil
	}
	if status == AvailabilityUnavailable {
		return &ContractError{Code: "model_unavailable", Message: "Codex route " + routeID + " is unavailable"}
	}
	_, probeErr := s.Probe(ctx, ProbeRequest{SchemaVersion: APIVersion, RouteIDs: []string{routeID}})
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.available.Entries {
		if entry.RouteID == routeID && effectiveAvailability(entry, s.now().UTC()) == AvailabilityAvailable {
			return nil
		}
		if entry.RouteID == routeID && effectiveAvailability(entry, s.now().UTC()) == AvailabilityUnavailable {
			return &ContractError{Code: "model_unavailable", Message: "Codex route " + routeID + " failed its active probe"}
		}
	}
	if probeErr != nil {
		return probeErr
	}
	return &ContractError{Code: "model_unavailable", Message: "Codex route " + routeID + " failed its active probe"}
}

func (s *Service) DriverList() DriverListResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	var observed *time.Time
	if !s.discovery.ObservedAt.IsZero() {
		value := s.discovery.ObservedAt
		observed = &value
	}
	return DriverListResponse{SchemaVersion: APIVersion, Drivers: []DriverView{{Driver: "codex", Provider: "local", WorkflowEligible: true, ModelCount: len(s.discovery.Models), LastDiscoveredAt: observed}}}
}

func (s *Service) Discover(ctx context.Context) (DiscoveryResponse, error) {
	if s.inspector == nil {
		return DiscoveryResponse{}, &ContractError{Code: "capability_unavailable", Message: "Codex model inspector is unavailable"}
	}
	models, err := s.inspector.DiscoverModels(ctx)
	if err != nil {
		return DiscoveryResponse{}, &ContractError{Code: "discovery_failed", Message: boundedDiagnostic(err.Error())}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discovery = discoveryFile{SchemaVersion: discoveryVersion, ObservedAt: s.now().UTC(), Models: cloneModels(models)}
	if err := writeJSONAtomic(s.discoveryPath(), s.discovery); err != nil {
		return DiscoveryResponse{}, err
	}
	if err := s.rebuildActiveCatalog(); err != nil {
		return DiscoveryResponse{}, err
	}
	return DiscoveryResponse{SchemaVersion: APIVersion, Driver: "codex", ObservedAt: s.discovery.ObservedAt, Models: cloneModels(s.discovery.Models), Routes: s.routeViews()}, nil
}

func (s *Service) Probe(ctx context.Context, request ProbeRequest) (ProbeResponse, error) {
	if request.SchemaVersion != APIVersion {
		return ProbeResponse{}, &ContractError{Code: "unsupported_version", Message: "schemaVersion must be " + APIVersion}
	}
	if s.inspector == nil {
		return ProbeResponse{}, &ContractError{Code: "capability_unavailable", Message: "Codex model inspector is unavailable"}
	}
	s.mu.Lock()
	profiles, err := s.selectedProfiles(request.RouteIDs)
	s.mu.Unlock()
	if err != nil {
		return ProbeResponse{}, err
	}
	entries := make([]AvailabilityEntry, 0, len(profiles))
	for _, profile := range profiles {
		result := s.inspector.ProbeModel(ctx, profile.Model, profile.DefaultEffort)
		now := s.now().UTC()
		expires := now.Add(availabilityTTL)
		status := AvailabilityUnavailable
		if result.Available {
			status = AvailabilityAvailable
		}
		entries = append(entries, AvailabilityEntry{RouteID: profile.RouteID, Model: profile.Model, Status: status, ReasoningEffort: profile.DefaultEffort, ObservedAt: &now, ExpiresAt: &expires, Diagnostic: boundedDiagnostic(result.Diagnostic)})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		s.setAvailability(entry)
	}
	if err := writeJSONAtomic(s.availabilityPath(), s.available); err != nil {
		return ProbeResponse{}, err
	}
	if err := s.rebuildActiveCatalog(); err != nil {
		return ProbeResponse{}, err
	}
	_, hash, err := s.registry.ActiveCatalog()
	return ProbeResponse{SchemaVersion: APIVersion, Entries: cloneAvailability(entries), CatalogHash: hash}, err
}

func (s *Service) ConfigGet() ConfigGetResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ConfigGetResponse{SchemaVersion: APIVersion, Config: cloneConfig(s.config.Config)}
}

func (s *Service) ConfigUpdate(request ConfigUpdateRequest) (ConfigUpdateResponse, error) {
	if request.SchemaVersion != APIVersion || strings.TrimSpace(request.MutationID) == "" || len(request.MutationID) > 256 {
		return ConfigUpdateResponse{}, &ContractError{Code: "invalid_argument", Message: "valid schemaVersion and mutationId are required"}
	}
	profiles := profileMap()
	if _, ok := profiles[request.RouteID]; !ok {
		return ConfigUpdateResponse{}, &ContractError{Code: "invalid_argument", Message: "routeId is not product-qualified"}
	}
	hash := updateRequestHash(request)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, receipt := range s.config.Mutations {
		if receipt.MutationID == request.MutationID {
			if receipt.RequestHash != hash {
				return ConfigUpdateResponse{}, &ContractError{Code: "conflict", Message: "mutationId was already used with different content"}
			}
			return ConfigUpdateResponse{SchemaVersion: APIVersion, Config: cloneConfig(s.config.Config), Replayed: true}, nil
		}
	}
	if request.ExpectedRevision != s.config.Revision {
		return ConfigUpdateResponse{}, &ContractError{Code: "conflict", Message: fmt.Sprintf("expected revision %d, current revision is %d", request.ExpectedRevision, s.config.Revision)}
	}
	previous := s.config
	for index := range s.config.Routes {
		if s.config.Routes[index].RouteID == request.RouteID {
			s.config.Routes[index].Enabled = request.Enabled
		}
	}
	if !hasEnabledRoute(s.config.Routes) {
		s.config = previous
		return ConfigUpdateResponse{}, &ContractError{Code: "invalid_argument", Message: "at least one product-qualified route must remain enabled"}
	}
	s.config.Revision++
	s.config.UpdatedAt = s.now().UTC()
	s.config.Mutations = append(s.config.Mutations, mutationReceipt{MutationID: request.MutationID, RequestHash: hash, Revision: s.config.Revision})
	if len(s.config.Mutations) > maxMutationReceipts {
		s.config.Mutations = append([]mutationReceipt(nil), s.config.Mutations[len(s.config.Mutations)-maxMutationReceipts:]...)
	}
	if err := s.rebuildActiveCatalog(); err != nil {
		s.config = previous
		return ConfigUpdateResponse{}, err
	}
	if err := writeJSONAtomic(s.configPath(), s.config); err != nil {
		s.config = previous
		_ = s.rebuildActiveCatalog()
		return ConfigUpdateResponse{}, err
	}
	return ConfigUpdateResponse{SchemaVersion: APIVersion, Config: cloneConfig(s.config.Config)}, nil
}

func (s *Service) Availability() AvailabilityResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return AvailabilityResponse{SchemaVersion: APIVersion, Entries: s.allAvailability()}
}

func (s *Service) EffectiveCatalog() (EffectiveCatalogResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog, hash, err := s.registry.ActiveCatalog()
	if err != nil {
		return EffectiveCatalogResponse{}, err
	}
	return EffectiveCatalogResponse{SchemaVersion: APIVersion, Source: routing.DynamicCatalogSourceV1, CatalogHash: hash, Catalog: catalog, Routes: s.routeViews()}, nil
}

func (s *Service) load() error {
	now := s.now().UTC()
	defaults := make([]RouteSetting, 0, len(ProductProfiles()))
	for _, profile := range ProductProfiles() {
		defaults = append(defaults, RouteSetting{RouteID: profile.RouteID, Enabled: true})
	}
	s.config = configFile{Config: Config{SchemaVersion: APIVersion, Revision: 1, Routes: defaults, UpdatedAt: now}}
	if err := readJSONStrict(s.configPath(), &s.config); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read routing configuration: %w", err)
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := writeJSONAtomic(s.configPath(), s.config); err != nil {
			return err
		}
	}
	if err := validateConfig(s.config.Config); err != nil {
		return err
	}
	s.discovery = discoveryFile{SchemaVersion: discoveryVersion, Models: []codexprocess.ModelInfo{}}
	if err := readJSONStrict(s.discoveryPath(), &s.discovery); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read model discovery cache: %w", err)
	}
	s.available = availabilityFile{SchemaVersion: availabilityVersion, Entries: []AvailabilityEntry{}}
	if err := readJSONStrict(s.availabilityPath(), &s.available); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read routing availability cache: %w", err)
	}
	return nil
}

func (s *Service) buildEffectiveCatalog() (routing.CapabilityCatalogV1, error) {
	base := routing.BuiltinCodexCatalogV2()
	enabled := make(map[string]bool, len(s.config.Routes))
	for _, setting := range s.config.Routes {
		enabled[setting.RouteID] = setting.Enabled
	}
	discovered := make(map[string]bool, len(s.discovery.Models))
	for _, model := range s.discovery.Models {
		discovered[model.Model] = true
	}
	availability := make(map[string]AvailabilityStatus, len(s.available.Entries))
	for _, entry := range s.available.Entries {
		availability[entry.RouteID] = effectiveAvailability(entry, s.now().UTC())
	}
	models := make([]routing.ModelCapabilityV1, 0, len(base.Models))
	for _, model := range base.Models {
		if !enabled[model.ID] {
			continue
		}
		if len(discovered) > 0 && !discovered[model.Target.Model] {
			continue
		}
		if availability[model.ID] == AvailabilityUnavailable {
			continue
		}
		models = append(models, model)
	}
	if len(models) == 0 {
		return routing.CapabilityCatalogV1{}, &ContractError{Code: "capability_unavailable", Message: "no enabled Codex route is discovered and usable"}
	}
	base.Models = models
	return routing.CanonicalCatalogV1(base), nil
}

func (s *Service) rebuildActiveCatalog() error {
	catalog, err := s.buildEffectiveCatalog()
	if err != nil {
		return err
	}
	if _, err := s.registry.SetActive(catalog); err != nil {
		return err
	}
	return s.persistActiveCatalog()
}

func (s *Service) persistActiveCatalog() error {
	catalog, hash, err := s.registry.ActiveCatalog()
	if err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(s.catalogDir(), hash+".json"), catalog)
}

func (s *Service) loadCatalogSnapshots() ([]routing.CapabilityCatalogV1, error) {
	entries, err := os.ReadDir(s.catalogDir())
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
		if err := readJSONStrict(filepath.Join(s.catalogDir(), entry.Name()), &catalog); err != nil {
			return nil, err
		}
		hash, err := routing.CatalogHash(catalog)
		if err != nil || entry.Name() != hash+".json" {
			return nil, fmt.Errorf("routing catalog snapshot %q does not match its content hash", entry.Name())
		}
		result = append(result, catalog)
	}
	return result, nil
}

func (s *Service) selectedProfiles(routeIDs []string) ([]ProductProfile, error) {
	profiles := profileMap()
	if len(routeIDs) == 0 {
		for _, setting := range s.config.Routes {
			if setting.Enabled {
				routeIDs = append(routeIDs, setting.RouteID)
			}
		}
	}
	seen := map[string]bool{}
	result := make([]ProductProfile, 0, len(routeIDs))
	for _, id := range routeIDs {
		profile, ok := profiles[id]
		if !ok || seen[id] {
			return nil, &ContractError{Code: "invalid_argument", Message: "routeIds contain an unknown or duplicate route"}
		}
		seen[id] = true
		if len(s.discovery.Models) > 0 && !modelDiscovered(s.discovery.Models, profile.Model) {
			return nil, &ContractError{Code: "capability_unavailable", Message: "route " + id + " was not discovered by Codex"}
		}
		result = append(result, profile)
	}
	return result, nil
}

func (s *Service) routeViews() []RouteView {
	enabled := map[string]bool{}
	for _, setting := range s.config.Routes {
		enabled[setting.RouteID] = setting.Enabled
	}
	entries := map[string]AvailabilityEntry{}
	for _, entry := range s.available.Entries {
		entries[entry.RouteID] = entry
	}
	views := make([]RouteView, 0, len(ProductProfiles()))
	for _, profile := range ProductProfiles() {
		entry := entries[profile.RouteID]
		status := effectiveAvailability(entry, s.now().UTC())
		discovered := modelDiscovered(s.discovery.Models, profile.Model)
		views = append(views, RouteView{ProductProfile: profile, Discovered: discovered, Enabled: enabled[profile.RouteID], Availability: status, Routable: profile.Qualified && discovered && enabled[profile.RouteID] && status == AvailabilityAvailable, Diagnostic: entry.Diagnostic})
	}
	return views
}

func (s *Service) allAvailability() []AvailabilityEntry {
	entries := map[string]AvailabilityEntry{}
	for _, entry := range s.available.Entries {
		entries[entry.RouteID] = entry
	}
	result := make([]AvailabilityEntry, 0, len(ProductProfiles()))
	for _, profile := range ProductProfiles() {
		entry, ok := entries[profile.RouteID]
		if !ok {
			entry = AvailabilityEntry{RouteID: profile.RouteID, Model: profile.Model, Status: AvailabilityUnknown}
		} else {
			entry.Status = effectiveAvailability(entry, s.now().UTC())
		}
		result = append(result, entry)
	}
	return result
}

func (s *Service) setAvailability(entry AvailabilityEntry) {
	for index := range s.available.Entries {
		if s.available.Entries[index].RouteID == entry.RouteID {
			s.available.Entries[index] = entry
			return
		}
	}
	s.available.Entries = append(s.available.Entries, entry)
	sort.Slice(s.available.Entries, func(i, j int) bool { return s.available.Entries[i].RouteID < s.available.Entries[j].RouteID })
}

func effectiveAvailability(entry AvailabilityEntry, now time.Time) AvailabilityStatus {
	if entry.Status != AvailabilityAvailable && entry.Status != AvailabilityUnavailable {
		return AvailabilityUnknown
	}
	if entry.ExpiresAt == nil || !entry.ExpiresAt.After(now) {
		return AvailabilityUnknown
	}
	return entry.Status
}

func validateConfig(config Config) error {
	if config.SchemaVersion != APIVersion || config.Revision < 1 || config.UpdatedAt.IsZero() || len(config.Routes) != len(ProductProfiles()) {
		return &ContractError{Code: "invalid_config", Message: "routing configuration header or route count is invalid"}
	}
	profiles := profileMap()
	seen := map[string]bool{}
	for _, route := range config.Routes {
		if _, ok := profiles[route.RouteID]; !ok || seen[route.RouteID] {
			return &ContractError{Code: "invalid_config", Message: "routing configuration contains an unknown or duplicate route"}
		}
		seen[route.RouteID] = true
	}
	if !hasEnabledRoute(config.Routes) {
		return &ContractError{Code: "invalid_config", Message: "at least one route must be enabled"}
	}
	return nil
}

func profileMap() map[string]ProductProfile {
	result := map[string]ProductProfile{}
	for _, profile := range ProductProfiles() {
		result[profile.RouteID] = profile
	}
	return result
}

func modelDiscovered(models []codexprocess.ModelInfo, model string) bool {
	for _, candidate := range models {
		if candidate.Model == model {
			return true
		}
	}
	return false
}

func hasEnabledRoute(routes []RouteSetting) bool {
	for _, route := range routes {
		if route.Enabled {
			return true
		}
	}
	return false
}

func updateRequestHash(request ConfigUpdateRequest) string {
	data, _ := json.Marshal(struct {
		RouteID string `json:"routeId"`
		Enabled bool   `json:"enabled"`
	}{request.RouteID, request.Enabled})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneConfig(source Config) Config {
	result := source
	result.Routes = append([]RouteSetting(nil), source.Routes...)
	return result
}

func cloneModels(source []codexprocess.ModelInfo) []codexprocess.ModelInfo {
	data, _ := json.Marshal(source)
	var result []codexprocess.ModelInfo
	_ = json.Unmarshal(data, &result)
	return result
}

func cloneAvailability(source []AvailabilityEntry) []AvailabilityEntry {
	return append([]AvailabilityEntry(nil), source...)
}

func boundedDiagnostic(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}

func (s *Service) configPath() string    { return filepath.Join(s.root, "config", "routing.json") }
func (s *Service) discoveryPath() string { return filepath.Join(s.root, "routing", "discovery.json") }
func (s *Service) availabilityPath() string {
	return filepath.Join(s.root, "routing", "availability.json")
}
func (s *Service) catalogDir() string { return filepath.Join(s.root, "routing", "catalogs") }

func readJSONStrict(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("JSON file contains trailing data")
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".routing-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceRoutingFile(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
