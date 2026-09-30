//go:build !linux

package backup

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }

// PrepareWorker does nothing outside Linux.
func PrepareWorker() {}
