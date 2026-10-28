package install

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var varPlaceholder = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)

// startupJar returns the file a startup command runs with java -jar, with
// the server's variables filled in, or "" if it doesn't run one or the file
// can't be known before it runs (shell substitutions, unknown variables).
func startupJar(startup string, vars map[string]string) string {
	if strings.Contains(startup, "$(") || strings.Contains(startup, "`") {
		return ""
	}
	fields := strings.Fields(startup)
	for i, f := range fields {
		if f != "-jar" || i+1 >= len(fields) {
			continue
		}
		jar := varPlaceholder.ReplaceAllStringFunc(strings.Trim(fields[i+1], `"'`), func(m string) string {
			name := varPlaceholder.FindStringSubmatch(m)[1]
			if v, ok := vars[name]; ok {
				return v
			}
			if v, ok := vars[strings.ToUpper(name)]; ok {
				return v
			}
			return m
		})
		if jar == "" || strings.ContainsAny(jar, "{}$\"' ") {
			return ""
		}
		jar = filepath.Clean(strings.TrimPrefix(jar, "/home/container/"))
		if !filepath.IsLocal(jar) {
			return ""
		}
		return jar
	}
	return ""
}

var (
	errEmptyJar   = errors.New("is empty")
	errCorruptJar = errors.New("isn't a valid jar file (it's corrupt or not a jar)")
)

// checkJar says what's wrong with the jar at name in dir, if anything. A
// jar that isn't there is fine: some eggs download it when the server first
// starts. It reads through os.Root, so a link can't point it outside dir.
func checkJar(dir, name string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil //nolint:nilerr // nothing to check
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name)
	if err != nil {
		return nil //nolint:nilerr // absent (or unreadable): not ours to judge
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil //nolint:nilerr // a directory or a device: leave it
	}
	if st.Size() == 0 {
		return errEmptyJar
	}
	if _, err := zip.NewReader(f, st.Size()); err != nil {
		return errCorruptJar
	}
	return nil
}
