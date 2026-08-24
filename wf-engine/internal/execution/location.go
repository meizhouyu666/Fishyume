// Package execution contains domain-neutral process execution primitives.
// Workflow and Team layers may use these helpers without importing each
// other's state contracts.
package execution

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type ArtifactLocation struct {
	Namespace      string
	OwnerID        string
	ResourceKind   string
	ResourceID     string
	GenerationKind string
	Generation     int
}

func (location ArtifactLocation) RelativePath() (string, error) {
	if location.Generation < 1 {
		return "", fmt.Errorf("execution generation must be positive")
	}
	values := []string{location.Namespace, location.OwnerID, location.ResourceKind, location.ResourceID, location.GenerationKind, strconv.Itoa(location.Generation)}
	for _, value := range values[:len(values)-1] {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
			return "", fmt.Errorf("execution artifact path component is invalid")
		}
	}
	return filepath.Join(values...), nil
}
