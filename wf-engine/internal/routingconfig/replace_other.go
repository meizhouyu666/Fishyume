//go:build !windows

package routingconfig

import "os"

func replaceRoutingFile(source, destination string) error { return os.Rename(source, destination) }
