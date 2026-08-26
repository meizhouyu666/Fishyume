package routing

import (
	"context"
	"fmt"
	"sync"
)

// CatalogProvider supplies the active catalog used for new decisions and
// resolves the immutable catalog hash captured by historical decisions.
type CatalogProvider interface {
	ActiveCatalog() (CapabilityCatalogV1, string, error)
	CatalogByHash(string) (CapabilityCatalogV1, bool)
}

// TargetAvailabilityGate is implemented by dynamic providers that can prove a
// selected route before any Attempt is persisted or dispatched.
type TargetAvailabilityGate interface {
	EnsureTargetAvailable(context.Context, Target) error
}

// CatalogRegistry keeps immutable, hash-addressed catalog snapshots. Catalogs
// are copied on both input and output so callers cannot mutate trusted state.
type CatalogRegistry struct {
	mu         sync.RWMutex
	activeHash string
	catalogs   map[string]CapabilityCatalogV1
}

func NewCatalogRegistry(active CapabilityCatalogV1, historical ...CapabilityCatalogV1) (*CatalogRegistry, error) {
	registry := &CatalogRegistry{catalogs: make(map[string]CapabilityCatalogV1)}
	for _, catalog := range historical {
		if _, err := registry.register(catalog); err != nil {
			return nil, err
		}
	}
	hash, err := registry.register(active)
	if err != nil {
		return nil, err
	}
	registry.activeHash = hash
	return registry, nil
}

// BuiltinCatalogRegistry returns the compatibility registry used when no
// dynamic configuration has been composed. It always recognizes the frozen
// M6 catalog hash.
func BuiltinCatalogRegistry() *CatalogRegistry {
	registry, err := NewCatalogRegistry(BuiltinCatalogV1())
	if err != nil {
		panic(fmt.Sprintf("construct built-in routing catalog registry: %v", err))
	}
	return registry
}

func (r *CatalogRegistry) Register(catalog CapabilityCatalogV1) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.register(catalog)
}

func (r *CatalogRegistry) SetActive(catalog CapabilityCatalogV1) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hash, err := r.register(catalog)
	if err != nil {
		return "", err
	}
	r.activeHash = hash
	return hash, nil
}

func (r *CatalogRegistry) ActiveCatalog() (CapabilityCatalogV1, string, error) {
	if r == nil {
		return CapabilityCatalogV1{}, "", fmt.Errorf("routing catalog registry is unavailable")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	catalog, ok := r.catalogs[r.activeHash]
	if !ok {
		return CapabilityCatalogV1{}, "", fmt.Errorf("active routing catalog %q is unavailable", r.activeHash)
	}
	return cloneCatalog(catalog), r.activeHash, nil
}

func (r *CatalogRegistry) CatalogByHash(hash string) (CapabilityCatalogV1, bool) {
	if r == nil {
		return CapabilityCatalogV1{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	catalog, ok := r.catalogs[hash]
	if !ok {
		return CapabilityCatalogV1{}, false
	}
	return cloneCatalog(catalog), true
}

func (r *CatalogRegistry) register(catalog CapabilityCatalogV1) (string, error) {
	catalog = CanonicalCatalogV1(catalog)
	if err := ValidateCatalog(catalog); err != nil {
		return "", fmt.Errorf("validate routing catalog: %w", err)
	}
	hash, err := CatalogHash(catalog)
	if err != nil {
		return "", fmt.Errorf("hash routing catalog: %w", err)
	}
	if existing, ok := r.catalogs[hash]; ok {
		existingHash, hashErr := CatalogHash(existing)
		if hashErr != nil || existingHash != hash {
			return "", fmt.Errorf("routing catalog registry is inconsistent for hash %q", hash)
		}
		return hash, nil
	}
	r.catalogs[hash] = cloneCatalog(catalog)
	return hash, nil
}
