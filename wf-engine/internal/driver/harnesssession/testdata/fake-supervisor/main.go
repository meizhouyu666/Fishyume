package main

import (
	"os"

	"wf.local/wf-engine/internal/driver/codexprocess"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "direct-supervisor" {
		os.Exit(codexprocess.RunSupervisor(os.Args[2]))
	}
	os.Exit(2)
}
