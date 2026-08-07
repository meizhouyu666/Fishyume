//go:build windows

package controlplane

import "os"

func replaceOwnerFile(source, target string) error {
	_ = os.Remove(target)
	return os.Rename(source, target)
}
