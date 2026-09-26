// Package configfile applies an egg's config file replacements (config.files)
// before a server starts, like Pterodactyl Wings' parsers (docs/EGGS.md).
//
// Unlike Pterodactyl, which re-serializes whole files, the parsers edit only
// what they change: comments, key order, formatting, and duplicate keys
// survive. Files are read and written only through an os.Root on the
// server's directory, replaced atomically, and a file that can't be parsed is
// left untouched.
package configfile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/xena-studios/raptor/internal/eggs"
)

// MaxSize is the largest config file Wings will edit.
const MaxSize = 16 << 20

// Rule is one resolved replacement.
type Rule struct {
	Key     string
	IfValue string // "" = always; "regex:<re>" = replace the matched part; else exact match
	Value   any    // string, bool, int, or float64, placeholders resolved
}

// String returns the value as text.
func (r Rule) String() string {
	switch v := r.Value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// typed returns the value for structured formats (JSON, YAML): booleans
// stay booleans, and strings that are integers become numbers, as in
// Pterodactyl (so "{{server.build.default.port}}" writes 25565, not "25565").
func (r Rule) typed() any {
	if s, ok := r.Value.(string); ok {
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
	}
	return r.Value
}

// match reports whether current satisfies IfValue, and for regex conditions
// returns the replacement text.
func (r Rule) match(current string) (ok bool, replaced string) {
	if r.IfValue == "" {
		return true, ""
	}
	if pat, isRe := strings.CutPrefix(r.IfValue, "regex:"); isRe {
		re, err := regexp.Compile(pat)
		if err != nil || !re.MatchString(current) {
			return false, ""
		}
		return true, re.ReplaceAllString(current, r.String())
	}
	return current == r.IfValue, ""
}

// Rules resolves an egg config file's replacements.
func Rules(f eggs.ConfigFile, vals eggs.Values) []Rule {
	out := make([]Rule, 0, len(f.Find))
	for _, fr := range f.Find {
		r := Rule{Key: fr.Key, IfValue: fr.IfValue, Value: fr.Value}
		if s, ok := fr.Value.(string); ok {
			r.Value = vals.Resolve(s)
		}
		out = append(out, r)
	}
	return out
}

// Edit applies rules to a file's contents with the given parser. data is
// empty for a file that doesn't exist yet.
func Edit(parser string, data []byte, rules []Rule) ([]byte, error) {
	switch parser {
	case "properties":
		return editProperties(data, rules), nil
	case "file":
		return editText(data, rules), nil
	case "ini":
		return editINI(data, rules), nil
	case "json":
		return editJSON(data, rules)
	case "yaml", "yml":
		return editYAML(data, rules)
	case "xml":
		return editXML(data, rules)
	}
	return nil, fmt.Errorf("unknown parser %q", parser)
}

// Owner is who new files and directories belong to (the server's container
// user). Existing files keep their owner and mode.
type Owner struct{ UID, GID int }

// Apply edits every config file. Each file is independent: an error in one
// (a parse error, an unknown parser) is returned but doesn't stop the others,
// and the failing file is left as it was. Like Pterodactyl, missing files are
// created (with their parent directories).
func Apply(root *os.Root, files []eggs.ConfigFile, vals eggs.Values, owner Owner) error {
	var errs []error
	for _, f := range files {
		if err := applyFile(root, f, vals, owner); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Path, err))
		}
	}
	return errors.Join(errs...)
}

func applyFile(root *os.Root, f eggs.ConfigFile, vals eggs.Values, owner Owner) error {
	name, err := cleanPath(f.Path)
	if err != nil {
		return err
	}
	data, info, err := read(root, name)
	if err != nil {
		return err
	}
	out, err := Edit(f.Parser, data, Rules(f, vals))
	if err != nil {
		return err
	}
	if info != nil && bytes.Equal(out, data) {
		return nil
	}
	return write(root, name, out, info, owner)
}

// cleanPath turns an egg's file path into a path relative to the server root.
// The root itself enforces that it can't escape; this rejects obviously
// invalid paths with a clear error.
func cleanPath(p string) (string, error) {
	c := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
	if c == "" || c == "." {
		return "", errors.New("invalid path")
	}
	return c, nil
}

func read(root *os.Root, name string) ([]byte, fs.FileInfo, error) {
	info, err := root.Stat(name) // follows symlinks, but never out of the root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("not a regular file")
	}
	if info.Size() > MaxSize {
		return nil, nil, fmt.Errorf("larger than %d MiB", MaxSize>>20)
	}
	data, err := root.ReadFile(name)
	return data, info, err
}

// write replaces the file atomically (temp file + rename), keeping the
// existing file's mode and owner. A symlink (inside the root) is written
// through in place instead, so the link itself is preserved.
func write(root *os.Root, name string, data []byte, info fs.FileInfo, owner Owner) error {
	mode := fs.FileMode(0o644)
	uid, gid := owner.UID, owner.GID
	if info != nil {
		mode = info.Mode().Perm()
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
		if li, err := root.Lstat(name); err == nil && li.Mode()&fs.ModeSymlink != 0 {
			return root.WriteFile(name, data, mode)
		}
	} else if err := mkdirs(root, path.Dir(name), owner); err != nil {
		return err
	}

	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	tmp := path.Join(path.Dir(name), ".raptor-"+hex.EncodeToString(suffix[:])+".tmp")
	fh, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = fh.Write(data)
	if err == nil {
		err = fh.Sync()
	}
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Chmod(tmp, mode) // undo the umask
	}
	if err == nil {
		err = root.Lchown(tmp, uid, gid)
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp)
	}
	return err
}

// mkdirs creates missing parent directories, owned by owner.
func mkdirs(root *os.Root, dir string, owner Owner) error {
	if dir == "." || dir == "/" {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(dir, "/") {
		cur = path.Join(cur, part)
		err := root.Mkdir(cur, 0o755)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := root.Lchown(cur, owner.UID, owner.GID); err != nil {
			return err
		}
	}
	return nil
}

// lineEnding returns "\r\n" for files that use it, else "\n".
func lineEnding(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}
