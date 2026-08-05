package directcli

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

type processIdentity struct {
	Fingerprint string
	Executable  string
	GroupID     int
}

type processMatch int

const (
	processGone processMatch = iota
	processMatched
	processMismatched
)

func inspectProcessRef(ref processRef) (processMatch, error) {
	if ref.PID <= 0 || ref.Fingerprint == "" {
		return processMismatched, nil
	}
	identity, exists, err := currentProcessIdentity(ref.PID)
	if err != nil {
		return processGone, fmt.Errorf("inspect PID %d: %w", ref.PID, err)
	}
	if !exists {
		return processGone, nil
	}
	if identity.Fingerprint != ref.Fingerprint || !sameExecutable(identity.Executable, ref.Executable) {
		return processMismatched, nil
	}
	return processMatched, nil
}

func sameExecutable(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
