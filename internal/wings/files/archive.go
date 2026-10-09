package files

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// ErrNoSpace is returned when an archive operation would take more disk
// than the server has left.
var ErrNoSpace = errors.New("not enough disk space left for this server")

// ErrUnsupported is returned for archive formats Wings can't extract.
var ErrUnsupported = errors.New("unsupported archive format (supported: .zip, .tar, .tar.gz/.tgz, .tar.bz2, .tar.zst, .gz)")

// Limits on extraction. The disk limit bounds the bytes; these bound what
// the disk limit doesn't.
const (
	maxEntries    = 1_000_000
	maxLinkTarget = 4096
	zstdMaxWindow = 128 << 20
)

// ArchiveResult says what an archive operation did.
type ArchiveResult struct {
	Archive string `json:"archive,omitempty"` // the archive created
	Files   int    `json:"files"`             // entries written
	Bytes   int64  `json:"bytes"`             // bytes written
	Skipped int    `json:"skipped,omitempty"` // denied, unsafe, or special files left out
}

// budget counts bytes written and refuses to go past left (< 0: unlimited).
type budget struct {
	w    io.Writer
	left int64
	n    int64
}

func (b *budget) Write(p []byte) (int, error) {
	if b.left >= 0 && int64(len(p)) > b.left-b.n {
		return 0, ErrNoSpace
	}
	n, err := b.w.Write(p)
	b.n += int64(n)
	return n, err
}

// Archive formats Compress can write.
const (
	FormatTarGz  = "tar.gz"
	FormatZip    = "zip"
	FormatTarZst = "tar.zst"
	FormatTar    = "tar"
)

// Formats are the archive formats Compress can write.
var Formats = []string{FormatTarGz, FormatZip, FormatTarZst, FormatTar}

// CompressOptions say what archive to make.
type CompressOptions struct {
	Format string // one of Formats; "" is FormatTarGz
	// Name is the archive's name without its extension; "" is
	// archive-<UTC time>. A taken name gets -2, -3, ….
	Name string
}

// Compress writes the named files and directories (relative to dir) into a
// new archive in dir and returns its name. Denied files, and FIFOs, device
// nodes, and sockets, are left out; symlinks are stored as links. The
// archive may use at most space bytes (< 0: unlimited).
func (f *FS) Compress(ctx context.Context, dir string, names []string, space int64, now time.Time, opts CompressOptions) (ArchiveResult, error) {
	dir = Rel(dir)
	var res ArchiveResult
	if len(names) == 0 {
		return res, errors.New("nothing to compress")
	}
	format := opts.Format
	if format == "" {
		format = FormatTarGz
	}
	if !slices.Contains(Formats, format) {
		return res, fmt.Errorf("unknown archive format %q (one of %s)", format, strings.Join(Formats, ", "))
	}
	base := opts.Name
	if base == "" {
		base = "archive-" + now.UTC().Format("2006-01-02T150405Z")
	}
	if strings.ContainsAny(base, "/\\\x00") || base == "." || base == ".." || len(base) > 200 {
		return res, fmt.Errorf("an archive's name can't contain a slash, or be . or ..")
	}
	if fi, err := f.root.Stat(dir); err != nil {
		return res, err
	} else if !fi.IsDir() {
		return res, &fs.PathError{Op: "compress", Path: dir, Err: errors.New("not a directory")}
	}
	targets := make([]string, 0, len(names))
	for _, n := range names {
		t := path.Join(dir, Rel(n))
		if t == dir {
			return res, &fs.PathError{Op: "compress", Path: n, Err: errors.New("name a file or directory inside it")}
		}
		targets = append(targets, t)
	}

	out := ""
	for i := 1; i <= 100; i++ {
		name := base + "." + format
		if i > 1 {
			name = fmt.Sprintf("%s-%d.%s", base, i, format)
		}
		if _, err := f.root.Lstat(path.Join(dir, name)); errors.Is(err, fs.ErrNotExist) {
			out = path.Join(dir, name)
			break
		}
	}
	if out == "" {
		return res, &fs.PathError{Op: "compress", Path: base, Err: fs.ErrExist}
	}
	part := path.Join(dir, "."+path.Base(out)+".part")
	file, err := f.Open(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return res, err
	}
	done := false
	defer func() {
		_ = file.Close()
		if !done {
			_ = f.root.Remove(part)
		}
	}()
	counted := &budget{w: file, left: space}
	aw, err := newArchiveWriter(counted, format)
	if err != nil {
		return res, err
	}
	visit := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if p == part || p == out || IsStaging(p) {
			return nil
		}
		if f.Denied(p, d.IsDir()) {
			res.Skipped++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		added, err := f.addEntry(aw, p, strings.TrimPrefix(strings.TrimPrefix(p, dir), "/"), d)
		if err != nil {
			return err
		}
		if added {
			res.Files++
		} else {
			res.Skipped++
		}
		return nil
	}
	for _, t := range targets {
		// WalkDir would follow a symlink it starts at: a link is added as
		// a link, like the ones inside directories.
		fi, err := f.root.Lstat(t)
		if err != nil {
			return res, err
		}
		if fi.IsDir() {
			err = fs.WalkDir(f.root.FS(), t, visit)
		} else if err = visit(t, fs.FileInfoToDirEntry(fi), nil); errors.Is(err, fs.SkipDir) {
			err = nil
		}
		if err != nil {
			return res, err
		}
	}
	if err := aw.Close(); err != nil {
		return res, err
	}
	if err := file.Sync(); err != nil {
		return res, err
	}
	if err := f.Rename(part, out, false); err != nil {
		return res, err
	}
	done = true
	res.Archive, res.Bytes = out, counted.n
	return res, nil
}

// archiveWriter writes one archive's entries.
type archiveWriter interface {
	dir(name string, fi fs.FileInfo) error
	symlink(name, target string, fi fs.FileInfo) error
	file(name string, fi fs.FileInfo, size int64, r io.Reader) error
	Close() error
}

func newArchiveWriter(w io.Writer, format string) (archiveWriter, error) {
	switch format {
	case FormatZip:
		return &zipArchive{zw: zip.NewWriter(w)}, nil
	case FormatTar:
		return &tarArchive{tw: tar.NewWriter(w)}, nil
	case FormatTarZst:
		zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			return nil, err
		}
		return &tarArchive{tw: tar.NewWriter(zw), comp: zw}, nil
	default:
		zw, err := gzip.NewWriterLevel(w, gzip.DefaultCompression)
		if err != nil {
			return nil, err
		}
		return &tarArchive{tw: tar.NewWriter(zw), comp: zw}, nil
	}
}

type tarArchive struct {
	tw   *tar.Writer
	comp io.Closer // the compressor around the file, if any
}

func (a *tarArchive) header(name string, fi fs.FileInfo) *tar.Header {
	return &tar.Header{Name: name, Mode: int64(fi.Mode().Perm()), ModTime: fi.ModTime(), Format: tar.FormatPAX}
}

func (a *tarArchive) dir(name string, fi fs.FileInfo) error {
	h := a.header(name+"/", fi)
	h.Typeflag = tar.TypeDir
	return a.tw.WriteHeader(h)
}

func (a *tarArchive) symlink(name, target string, fi fs.FileInfo) error {
	h := a.header(name, fi)
	h.Typeflag, h.Linkname = tar.TypeSymlink, target
	return a.tw.WriteHeader(h)
}

func (a *tarArchive) file(name string, fi fs.FileInfo, size int64, r io.Reader) error {
	h := a.header(name, fi)
	h.Typeflag, h.Size = tar.TypeReg, size
	if err := a.tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := io.Copy(a.tw, r)
	return err
}

func (a *tarArchive) Close() error {
	err := a.tw.Close()
	if a.comp != nil {
		err = errors.Join(err, a.comp.Close())
	}
	return err
}

// zipArchive stores links as Info-ZIP does: the target as the content, with
// the link mode, which the extractor reads back.
type zipArchive struct{ zw *zip.Writer }

func (a *zipArchive) header(name string, fi fs.FileInfo, mode fs.FileMode, method uint16) *zip.FileHeader {
	h := &zip.FileHeader{Name: name, Method: method, Modified: fi.ModTime()}
	h.SetMode(mode)
	return h
}

func (a *zipArchive) dir(name string, fi fs.FileInfo) error {
	_, err := a.zw.CreateHeader(a.header(name+"/", fi, fs.ModeDir|fi.Mode().Perm(), zip.Store))
	return err
}

func (a *zipArchive) symlink(name, target string, fi fs.FileInfo) error {
	w, err := a.zw.CreateHeader(a.header(name, fi, fs.ModeSymlink|0o777, zip.Store))
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, target)
	return err
}

func (a *zipArchive) file(name string, fi fs.FileInfo, _ int64, r io.Reader) error {
	w, err := a.zw.CreateHeader(a.header(name, fi, fi.Mode().Perm(), zip.Deflate))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	return err
}

func (a *zipArchive) Close() error { return a.zw.Close() }

// addEntry adds one entry. It reports false for what it leaves out.
func (f *FS) addEntry(aw archiveWriter, p, name string, d fs.DirEntry) (bool, error) {
	if name == "" {
		return false, nil
	}
	fi, err := d.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil // removed while walking
	}
	if err != nil {
		return false, err
	}
	switch {
	case fi.IsDir():
		return true, aw.dir(name, fi)
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := f.root.Readlink(p)
		if err != nil {
			return false, err
		}
		return true, aw.symlink(name, target, fi)
	case !fi.Mode().IsRegular():
		return false, nil
	}
	src, err := f.Open(p, os.O_RDONLY)
	if errors.Is(err, ErrNotRegular) || errors.Is(err, fs.ErrNotExist) {
		return false, nil // swapped or removed while walking
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = src.Close() }()
	st, err := src.Stat()
	if err != nil {
		return false, err
	}
	// A file that grows while it's read is cut at the size it had.
	return true, aw.file(name, fi, st.Size(), io.LimitReader(src, st.Size()))
}

// Extract unpacks an archive into dest (default: the archive's directory).
// Every entry lands inside the server's directory: names are cleaned, and
// entries that would climb out, denied files, and anything but files,
// directories, and links are skipped. Existing files are replaced. At most
// space bytes are written (< 0: unlimited).
func (f *FS) Extract(ctx context.Context, archive, dest string, space int64) (ArchiveResult, error) {
	archive = Rel(archive)
	if dest == "" {
		dest = path.Dir(archive)
	}
	dest = Rel(dest)
	var res ArchiveResult
	src, err := f.Open(archive, os.O_RDONLY)
	if err != nil {
		return res, err
	}
	defer func() { _ = src.Close() }()
	st, err := src.Stat()
	if err != nil {
		return res, err
	}
	if err := f.MkdirAll(dest); err != nil {
		return res, err
	}
	x := &extractor{f: f, ctx: ctx, dest: dest, space: space, res: &res}

	head := make([]byte, 512)
	n, _ := src.ReadAt(head, 0)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		zr, err := zip.NewReader(src, st.Size())
		if err != nil {
			return res, err
		}
		return res, x.zip(zr)
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		zr, err := gzip.NewReader(src)
		if err != nil {
			return res, err
		}
		defer func() { _ = zr.Close() }()
		single := strings.TrimSuffix(path.Base(archive), ".gz")
		if single == path.Base(archive) {
			single += ".out"
		}
		return res, x.tarOrSingle(zr, single)
	case bytes.HasPrefix(head, []byte("BZh")):
		return res, x.tarOrSingle(bzip2.NewReader(src), "")
	case bytes.HasPrefix(head, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(src, zstd.WithDecoderMaxWindow(zstdMaxWindow), zstd.WithDecoderLowmem(true), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return res, err
		}
		defer zr.Close()
		return res, x.tarOrSingle(zr, "")
	case isTar(head):
		return res, x.tar(tar.NewReader(src))
	}
	return res, ErrUnsupported
}

func isTar(head []byte) bool {
	return len(head) >= 262 && string(head[257:262]) == "ustar"
}

type extractor struct {
	f       *FS
	ctx     context.Context
	dest    string
	space   int64
	written int64
	res     *ArchiveResult
}

// tarOrSingle extracts a compressed tar, or a single compressed file named
// single ("" = only tars).
func (x *extractor) tarOrSingle(r io.Reader, single string) error {
	br := bufio.NewReaderSize(r, 512)
	head, _ := br.Peek(512)
	if isTar(head) {
		return x.tar(tar.NewReader(br))
	}
	if single == "" {
		return ErrUnsupported
	}
	return x.entry(single, kindFile, 0, "", br, time.Time{})
}

func (x *extractor) tar(tr *tar.Reader) error {
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		kind := kindOther
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // old archives still use TypeRegA
			kind = kindFile
		case tar.TypeDir:
			kind = kindDir
		case tar.TypeSymlink:
			kind = kindSymlink
		case tar.TypeLink:
			kind = kindHardlink
		case tar.TypeXGlobalHeader:
			continue
		}
		if err := x.entry(h.Name, kind, fs.FileMode(h.Mode).Perm(), h.Linkname, tr, h.ModTime); err != nil { //nolint:gosec // only the permission bits are kept
			return err
		}
	}
}

func (x *extractor) zip(zr *zip.Reader) error {
	for _, zf := range zr.File {
		if err := x.zipEntry(zf); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) zipEntry(zf *zip.File) error {
	mode := zf.Mode()
	kind := kindFile
	switch {
	case mode.IsDir() || strings.HasSuffix(zf.Name, "/"):
		kind = kindDir
	case mode&fs.ModeSymlink != 0:
		kind = kindSymlink
	case !mode.IsRegular():
		kind = kindOther
	}
	link := ""
	var r io.Reader
	if kind == kindFile || kind == kindSymlink {
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		r = rc
		if kind == kindSymlink {
			b, err := io.ReadAll(io.LimitReader(rc, maxLinkTarget))
			if err != nil {
				return err
			}
			link = string(b)
		}
	}
	return x.entry(zf.Name, kind, mode.Perm(), link, r, zf.Modified)
}

// Kinds of archive entries.
type entryKind int

const (
	kindFile entryKind = iota
	kindDir
	kindSymlink
	kindHardlink
	kindOther // FIFOs, devices, …: skipped
)

// entryName cleans an archive entry's name. ok is false for names that
// climb out of the destination.
func entryName(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/") // Windows-made zips
	name = path.Clean(strings.TrimLeft(name, "/"))
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	return name, true
}

// entry writes one archive entry.
func (x *extractor) entry(name string, kind entryKind, perm fs.FileMode, link string, r io.Reader, mtime time.Time) error {
	if err := x.ctx.Err(); err != nil {
		return err
	}
	if x.res.Files+x.res.Skipped >= maxEntries {
		return fmt.Errorf("archive has more than %d entries", maxEntries)
	}
	rel, ok := entryName(name)
	if !ok {
		x.res.Skipped++
		return nil
	}
	target := path.Join(x.dest, rel)
	f := x.f
	if f.Denied(target, kind == kindDir) {
		x.res.Skipped++
		return nil
	}
	switch kind {
	case kindDir:
		if err := f.MkdirAll(target); err != nil {
			return err
		}
		x.res.Files++
		return nil
	case kindOther:
		x.res.Skipped++
		return nil
	}
	if err := f.MkdirAll(path.Dir(target)); err != nil {
		return err
	}
	// Links in the way are replaced, not written through, as tar does.
	if fi, err := f.root.Lstat(target); err == nil && (kind != kindFile || fi.Mode()&fs.ModeSymlink != 0) {
		if fi.IsDir() {
			x.res.Skipped++
			return nil
		}
		if err := f.root.Remove(target); err != nil {
			return err
		}
	}
	switch kind {
	case kindSymlink:
		if len(link) == 0 || len(link) >= maxLinkTarget {
			x.res.Skipped++
			return nil
		}
		if err := f.Symlink(link, target); err != nil {
			return err
		}
		x.res.Files++
		return nil
	case kindHardlink:
		from, ok := entryName(link)
		if !ok {
			x.res.Skipped++
			return nil
		}
		if err := f.Link(path.Join(x.dest, from), target); err != nil {
			x.res.Skipped++ // the file it points at was skipped
			return nil      //nolint:nilerr // a missing link target doesn't stop the rest
		}
		x.res.Files++
		return nil
	}
	out, err := f.Open(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if errors.Is(err, ErrNotRegular) {
		x.res.Skipped++
		return nil
	}
	if err != nil {
		return err
	}
	w := &budget{w: out, left: -1}
	if x.space >= 0 {
		w.left = x.space - x.written
	}
	_, err = io.Copy(w, r)
	x.written += w.n
	x.res.Bytes += w.n
	if err == nil && perm != 0 {
		err = out.Chmod(perm)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if !mtime.IsZero() {
		_ = f.root.Chtimes(target, mtime, mtime)
	}
	x.res.Files++
	return nil
}
