//go:build e2e

package server

import (
	"syscall"

	"github.com/xena-studios/raptor/internal/eggs"
)

func killStop() eggs.Stop { return eggs.Stop{Signal: syscall.SIGKILL} }
