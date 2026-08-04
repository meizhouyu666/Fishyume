//go:build !windows

package ccpanes

import "errors"

func discoverFromRunningProcess() (string, error) {
	return "", errors.New("running-process discovery is only available on Windows")
}
