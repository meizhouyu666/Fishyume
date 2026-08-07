//go:build !windows

package controlplane

import "os"

func replaceOwnerFile(source, target string) error { return os.Rename(source, target) }
