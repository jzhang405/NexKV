package concurrency

import (
	"os"

	"go.uber.org/automaxprocs/maxprocs"
)

func init() {
	if os.Getenv("AUTOMAXPROCS_DISABLE") == "true" {
		return
	}

	undo, err := maxprocs.Set()
	if err != nil {
		return
	}

	_ = undo
}
