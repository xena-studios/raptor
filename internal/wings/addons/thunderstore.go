package addons

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/files"
)

// Thunderstore's packages (#410) are zips, unpacked one folder each into
// the add-on folder (BepInEx/plugins/Namespace-Name, as r2modman does;
// BepInEx loads plugins from every folder inside it). Thunderstore lists
// no hashes, so the Panel downloads the version, hashes it, and gives
// Wings its SHA-256. Beside the package's files Wings keeps a record of
// the version it unpacked and the zip's hashes, which names it in
// listings; removing the folder removes the package.
const (
	// KindThunderstore is the add-on kind.
	KindThunderstore = "thunderstore"
	// ThunderstoreHost is Thunderstore's CDN.
	ThunderstoreHost = "ccdn.thunderstore.io"
	// MaxUnpacked is the most a package unpacks to.
	MaxUnpacked = 1 << 30
	// MaxEntries is the most files and folders a package has.
	MaxEntries = 5000
	// RecordName is the record Wings keeps in a package's folder.
	RecordName = ".raptor-package.json"
)

// Limits bound what a package unpacks to.
type Limits struct {
	// Entries is the most files and folders (0: MaxEntries).
	Entries int
	// Bytes is the most bytes written (0: MaxUnpacked).
	Bytes int64
}

func (l Limits) entries() int {
	if l.Entries > 0 {
		return l.Entries
	}
	return MaxEntries
}

func (l Limits) bytes() int64 {
	if l.Bytes > 0 {
		return l.Bytes
	}
	return MaxUnpacked
}

// Errors unpacking.
var (
	ErrUnsafe        = errors.New("the package has a file outside its folder; nothing was installed")
	ErrTooManyFiles  = fmt.Errorf("packages have at most %d files; nothing was installed", MaxEntries)
	ErrUnpackTooLong = fmt.Errorf("packages unpack to at most %d MiB; nothing was installed", MaxUnpacked>>20)
	ErrNotZip        = errors.New("the download isn't a zip; nothing was installed")
)

// packageFile is a version's file name: Namespace-Name-1.2.3.zip.
var packageFile = regexp.MustCompile(`^([A-Za-z0-9_]{1,128}-[A-Za-z0-9_]{1,128})-([0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9})\.zip$`)

// ParsePackageFile is the folder (Namespace-Name) and full name
// (Namespace-Name-1.2.3) a package's file is for.
func ParsePackageFile(name string) (folder, full string, ok bool) {
	m := packageFile.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	return m[1], m[1] + "-" + m[2], true
}

// record is what Wings keeps in a package's folder.
type record struct {
	Package string `json:"package"` // Namespace-Name-1.2.3
	Sha512  string `json:"sha512"`
	Sha256  string `json:"sha256"`
	Size    int64  `json:"size"` // the zip's
}

// listPackages lists a Thunderstore add-on folder's packages: each
// folder, named by its record when Wings unpacked it.
func (s *Service) listPackages(f *files.FS, dir string, out Listing) (Listing, error) {
	infos, err := f.List(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, fi := range infos {
		if !fi.IsDir() || strings.HasPrefix(fi.Name(), ".") {
			continue
		}
		if len(out.Files) == MaxList {
			break
		}
		file := File{Name: fi.Name(), Modified: fi.ModTime().UnixMilli()}
		if b, _, err := f.ReadFile(path.Join(dir, fi.Name(), RecordName), 4096); err == nil {
			var r record
			_ = json.Unmarshal(b, &r)
			folder, _, ok := ParsePackageFile(r.Package + ".zip")
			if ok && folder == fi.Name() && sha512Hex(r.Sha512) {
				file.Package, file.Sha512, file.Sha256, file.Size = r.Package, r.Sha512, r.Sha256, r.Size
			}
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

func sha512Hex(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha512.Size
}

// installPackage downloads a package and unpacks it into its folder,
// replacing the version there (which goes to the trash).
func (s *Service) installPackage(ctx context.Context, f *files.FS, id, user string, a eggs.Addon, p Install, download func(tmp string) error) (File, error) {
	folder, full, ok := ParsePackageFile(p.Filename)
	if !ok || (p.Replace != "" && p.Replace != folder) {
		return File{}, ErrName
	}
	if err := f.MkdirAll(a.Dir); err != nil {
		return File{}, err
	}
	tmp := path.Join(a.Dir, ".raptor-"+uuid.NewString()+".part")
	defer func() { _ = f.Remove(tmp) }()
	if err := download(tmp); err != nil {
		return File{}, err
	}
	sums, size, err := hashFile(f, tmp)
	if err != nil {
		return File{}, err
	}
	stage := path.Join(a.Dir, ".raptor-"+uuid.NewString())
	if err := f.Mkdir(stage); err != nil {
		return File{}, err
	}
	done := false
	defer func() {
		if !done {
			_ = f.RemoveAll(stage)
		}
	}()
	if err := Unpack(ctx, f, tmp, stage, s.Unpack); err != nil {
		return File{}, err
	}
	rec, err := json.Marshal(record{Package: full, Sha512: sums[0], Sha256: sums[1], Size: size})
	if err != nil {
		return File{}, err
	}
	if err := f.WriteFile(path.Join(stage, RecordName), rec); err != nil {
		return File{}, err
	}
	dst := path.Join(a.Dir, folder)
	if _, err := f.Lstat(dst); err == nil {
		if err := s.Files.Trash(ctx, id, user, a.Dir, []string{folder}); err != nil {
			return File{}, fmt.Errorf("the version installed can't be moved to the trash: %w", err)
		}
	}
	if err := f.Rename(stage, dst, false); err != nil {
		return File{}, err
	}
	done = true
	fi, err := f.Stat(dst)
	if err != nil {
		return File{}, err
	}
	return File{Name: folder, Size: size, Modified: fi.ModTime().UnixMilli(), Sha512: sums[0], Sha256: sums[1], Package: full}, nil
}

// hashFile is a file's SHA-512 and SHA-256, and its size.
func hashFile(f *files.FS, name string) ([2]string, int64, error) {
	file, err := f.Open(name, os.O_RDONLY)
	if err != nil {
		return [2]string{}, 0, err
	}
	defer func() { _ = file.Close() }()
	h512, h256 := sha512.New(), sha256.New()
	n, err := io.Copy(io.MultiWriter(h512, h256), io.LimitReader(file, MaxFile))
	if err != nil {
		return [2]string{}, 0, err
	}
	return [2]string{hex.EncodeToString(h512.Sum(nil)), hex.EncodeToString(h256.Sum(nil))}, n, nil
}

// entryName is a zip entry's path inside the package's folder: forward
// slashes (zips made on Windows often use backslashes), and nothing that
// climbs out, is absolute, or names Wings' record. ok is false for
// anything else.
func entryName(raw string) (string, bool) {
	name := strings.ReplaceAll(raw, `\`, "/")
	if name == "" || len(name) > 1024 || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	for seg := range strings.SplitSeq(strings.TrimSuffix(name, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
	}
	clean := path.Clean(name)
	if clean == RecordName {
		return "", false
	}
	return clean, true
}

// Unpack unpacks a zip (a path in f) into dest, a folder in f. Every
// entry must stay inside dest (else ErrUnsafe, and nothing is kept: dest
// is the caller's to remove); links and other special files are skipped;
// at most the limits' entries and bytes, counted as they're written, not
// as the zip claims.
func Unpack(ctx context.Context, f *files.FS, archive, dest string, limits Limits) error {
	src, err := f.Open(archive, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(src, st.Size())
	if err != nil {
		return ErrNotZip
	}
	if len(zr.File) > limits.entries() {
		return ErrTooManyFiles
	}
	// Every name is checked before anything is written.
	names := make([]string, len(zr.File))
	for i, zf := range zr.File {
		n, ok := entryName(zf.Name)
		if !ok {
			return ErrUnsafe
		}
		names[i] = n
	}
	var written int64
	for i, zf := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := path.Join(dest, names[i])
		mode := zf.Mode()
		switch {
		case mode.IsDir() || strings.HasSuffix(zf.Name, "/") || strings.HasSuffix(zf.Name, `\`):
			if err := f.MkdirAll(target); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			continue // a link, or a device: never made
		}
		if err := f.MkdirAll(path.Dir(target)); err != nil {
			return err
		}
		n, err := unpackFile(f, zf, target, limits.bytes()-written)
		written += n
		if err != nil {
			return err
		}
	}
	return nil
}

// unpackFile writes one entry, at most left bytes.
func unpackFile(f *files.FS, zf *zip.File, target string, left int64) (int64, error) {
	if zf.UncompressedSize64 > uint64(max(left, 0)) {
		return 0, ErrUnpackTooLong
	}
	rc, err := zf.Open()
	if err != nil {
		return 0, ErrNotZip
	}
	defer func() { _ = rc.Close() }()
	out, err := f.Open(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(rc, left+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case n > left:
		return n, ErrUnpackTooLong
	case errors.Is(err, zip.ErrChecksum), errors.Is(err, zip.ErrFormat):
		return n, ErrNotZip
	}
	return n, err
}
