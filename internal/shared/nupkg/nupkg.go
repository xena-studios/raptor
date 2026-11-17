// Package nupkg is what Raptor reads of a NuGet package (#415): which of its
// files is the OpenMod plugin. The Panel uses it to offer only packages
// Wings can install, and Wings to pick the file out of a downloaded
// package, so the two agree.
package nupkg

import (
	"path"
	"regexp"
	"strings"
)

// Frameworks are the target frameworks Unturned's Mono runs, best first:
// a package's lib/<framework>/ folder is read from the first it has.
var Frameworks = []string{
	"netstandard2.1", "netstandard2.0",
	"net481", "net48", "net472", "net471", "net47", "net462", "net461",
}

// PluginDLL is the entry (of a package's file names) that holds its
// assembly: the one .dll in lib/<framework>/ for the best framework it
// has. A package with none, or with more than one there (an assembly and
// its own libraries), isn't one Raptor installs: ok is false.
func PluginDLL(names []string) (entry string, ok bool) {
	byFramework := map[string][]string{}
	for _, n := range names {
		if strings.Contains(n, `\`) || strings.Contains(n, "..") {
			continue
		}
		parts := strings.Split(n, "/")
		if len(parts) != 3 || !strings.EqualFold(parts[0], "lib") || !strings.EqualFold(path.Ext(parts[2]), ".dll") || parts[2] == ".dll" {
			continue
		}
		fw := strings.ToLower(parts[1])
		byFramework[fw] = append(byFramework[fw], n)
	}
	for _, fw := range Frameworks {
		if dlls := byFramework[fw]; len(dlls) > 0 {
			return dlls[0], len(dlls) == 1
		}
	}
	return "", false
}

var (
	id      = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\-]{0,99}$`)
	version = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.\-]+)?$`)
)

// ValidID reports whether s can be a NuGet package ID.
func ValidID(s string) bool { return id.MatchString(s) && !strings.Contains(s, "..") }

// ValidVersion reports whether s is a NuGet version (normalized, without
// build metadata).
func ValidVersion(s string) bool { return len(s) <= 64 && version.MatchString(s) }

// FileName is what Raptor names a package's plugin in OpenMod's plugins
// folder: ID@version.dll. OpenMod loads every .dll there whatever it's
// called, and the name says which package and version it came from, since
// the assembly's own hash isn't one NuGet publishes.
func FileName(pkg, ver string) string { return pkg + "@" + ver + ".dll" }

// ParseFileName is the package and version a plugin file is named for
// (".disabled" taken off).
func ParseFileName(name string) (pkg, ver string, ok bool) {
	name, _ = strings.CutSuffix(name, ".disabled")
	base, cut := strings.CutSuffix(name, ".dll")
	if !cut {
		return "", "", false
	}
	pkg, ver, ok = strings.Cut(base, "@")
	if !ok || !ValidID(pkg) || !ValidVersion(ver) {
		return "", "", false
	}
	return pkg, ver, true
}
