package backup

import (
	"os"
	"syscall"
)

// sysProcAttr kills the worker if Wings dies: a resumed job would otherwise
// run alongside the orphan.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }

// PrepareWorker makes the worker the first thing the OOM killer picks.
// Wings' own -900 is inherited and must not protect it.
func PrepareWorker() { _ = os.WriteFile("/proc/self/oom_score_adj", []byte("1000"), 0) }
