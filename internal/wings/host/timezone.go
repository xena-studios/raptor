package host

import (
	"os"
	"path/filepath"
	"strings"
)

// Timezone returns the host's IANA time zone ("Europe/Berlin"), for the TZ
// variable servers get. It falls back to UTC.
func Timezone() string {
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(b)); tz != "" {
			return tz
		}
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, tz, ok := strings.Cut(target, "zoneinfo/"); ok && tz != "" {
			return tz
		}
	}
	return "UTC"
}
