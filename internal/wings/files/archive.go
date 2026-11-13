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
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
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
	// Level is the deflate level of tar.gz and zip archives, 1 (fastest)
	// to 9 (smallest); 0 is DefaultLevel. Node-wide, from config.yml.
	Level int
	// Threads is how many blocks are compressed at once (0: Threads(0)).
	Threads int
}

// DefaultLevel is the deflate level used when none is configured.
const DefaultLevel = 2

// MaxThreads caps the cores one archive job uses by default, so a job
// can't take over the node (docs/WINGS.md#archives).
const MaxThreads = 4

// Threads returns how many cores one archive job uses: n if set, else
// GOMAXPROCS (which follows the service's CPU quota) up to MaxThreads.
func Threads(n int) int {
	if n > 0 {
		return n
	}
	return min(runtime.GOMAXPROCS(0), MaxThreads)
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
		return res, errors.New(`an archive's name can't contain a slash, or be "." or ".."`)
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
	level := opts.Level
	if level == 0 {
		level = DefaultLevel
	}
	aw, err := newArchiveWriter(counted, format, level, Threads(opts.Threads))
	if err != nil {
		return res, err
	}
	closed := false
	defer func() {
		// Stops the compressor's goroutines when the archive is abandoned.
		if !closed {
			_ = aw.Close()
		}
	}()
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
	closed = true
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

func newArchiveWriter(w io.Writer, format string, level, threads int) (archiveWriter, error) {
	switch format {
	case FormatZip:
		return newZipArchive(w, level)
	case FormatTar:
		return &tarArchive{tw: tar.NewWriter(w)}, nil
	case FormatTarZst:
		zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			return nil, err
		}
		return &tarArchive{tw: tar.NewWriter(zw), comp: zw}, nil
	default:
		zw, err := newParallelGzip(w, level, threads)
		if err != nil {
			return nil, err
		}
		return &tarArchive{tw: tar.NewWriter(zw), comp: zw}, nil
	}
}

// gzipBlock is how much input each of parallelGzip's threads compresses at
// a time. Each block is primed with the end of the one before, so the
// output is barely larger than a single stream's.
const gzipBlock = 1 << 20

// parallelGzip writes one gzip stream, compressing blocks on several
// threads with pgzip. pgzip's writer goroutine only stops when Close gets
// to the end, and Close gives up early once a write to the file has
// failed, which would leak the goroutine and its buffers. So pgzip never
// sees a failure: the first one is kept here, and returned to the next
// Write and to Close.
type parallelGzip struct {
	z   *pgzip.Writer
	out *firstError
}

func newParallelGzip(w io.Writer, level, threads int) (*parallelGzip, error) {
	out := &firstError{w: w}
	z, err := pgzip.NewWriterLevel(out, level)
	if err != nil {
		return nil, err
	}
	if err := z.SetConcurrency(gzipBlock, threads); err != nil {
		return nil, err
	}
	return &parallelGzip{z: z, out: out}, nil
}

func (g *parallelGzip) Write(p []byte) (int, error) {
	if err := g.out.Err(); err != nil {
		return 0, err
	}
	return g.z.Write(p)
}

// Close writes what's left and waits for it: when it returns, nothing
// writes to the file anymore.
func (g *parallelGzip) Close() error {
	err := g.z.Close()
	if werr := g.out.Err(); werr != nil {
		return werr
	}
	return err
}

// firstError passes writes on until one fails, then drops the rest and
// keeps that error.
type firstError struct {
	w   io.Writer
	mu  sync.Mutex
	err error
}

func (e *firstError) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err == nil {
		n, err := e.w.Write(p)
		if err == nil && n < len(p) {
			err = io.ErrShortWrite
		}
		e.err = err
	}
	return len(p), nil
}

func (e *firstError) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
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

// newZipArchive writes a zip with klauspost's deflate, which is several
// times faster than compress/flate's. zip writes one entry at a time, so
// one compressor is reused for all of them.
func newZipArchive(w io.Writer, level int) (*zipArchive, error) {
	fw, err := flate.NewWriter(nil, level)
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		fw.Reset(w)
		return fw, nil
	})
	return &zipArchive{zw: zw}, nil
}

// flateReaders are klauspost's inflaters for zip entries, reused across
// entries and archives, as archive/zip does with its own.
var flateReaders sync.Pool

func newFlateReader(r io.Reader) io.ReadCloser {
	fr, ok := flateReaders.Get().(io.ReadCloser)
	if ok {
		if err := fr.(flate.Resetter).Reset(r, nil); err != nil {
			fr = flate.NewReader(r)
		}
	} else {
		fr = flate.NewReader(r)
	}
	return &pooledFlate{rc: fr}
}

type pooledFlate struct{ rc io.ReadCloser }

func (p *pooledFlate) Read(b []byte) (int, error) {
	if p.rc == nil {
		return 0, os.ErrClosed
	}
	return p.rc.Read(b)
}

func (p *pooledFlate) Close() error {
	if p.rc == nil {
		return nil
	}
	err := p.rc.Close()
	flateReaders.Put(p.rc)
	p.rc = nil
	return err
}

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
	// The walk checked p against the denylist, and fi is its lstat, just
	// read: a regular file. Open checks the open file again.
	src, st, err := f.open(p, os.O_RDONLY, false)
	if errors.Is(err, ErrNotRegular) || errors.Is(err, fs.ErrNotExist) {
		return false, nil // swapped or removed while walking
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = src.Close() }()
	// A file that grows while it's read is cut at the size it had.
	return true, aw.file(name, fi, st.Size(), io.LimitReader(src, st.Size()))
}

// ExtractOptions tune Extract.
type ExtractOptions struct {
	// Threads is how many files are written at once (0: Threads(0)). Only
	// zips are written in parallel.
	Threads int
}

// Extract unpacks an archive into dest (default: the archive's directory).
// Every entry lands inside the server's directory: names are cleaned, and
// entries that would climb out, denied files, and anything but files,
// directories, and links are skipped. Existing files are replaced. At most
// space bytes are written (< 0: unlimited).
func (f *FS) Extract(ctx context.Context, archive, dest string, space int64, opts ExtractOptions) (ArchiveResult, error) {
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
	x := &extractor{f: f, ctx: ctx, dest: dest, space: &spaceLeft{limit: space}, res: &res, dirs: map[string]bool{}}
	err = x.extract(src, st.Size(), path.Base(archive), Threads(opts.Threads))
	return res, err
}

func (x *extractor) extract(src *os.File, size int64, name string, threads int) error {
	head := make([]byte, 512)
	n, _ := src.ReadAt(head, 0)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		zr, err := zip.NewReader(src, size)
		if err != nil {
			return err
		}
		zr.RegisterDecompressor(zip.Deflate, newFlateReader)
		return x.zip(zr, threads)
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		zr, err := gzip.NewReader(src)
		if err != nil {
			return err
		}
		defer func() { _ = zr.Close() }()
		single := strings.TrimSuffix(name, ".gz")
		if single == name {
			single += ".out"
		}
		return x.tarOrSingle(zr, single)
	case bytes.HasPrefix(head, []byte("BZh")):
		return x.tarOrSingle(bzip2.NewReader(src), "")
	case bytes.HasPrefix(head, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(src, zstd.WithDecoderMaxWindow(zstdMaxWindow), zstd.WithDecoderLowmem(true), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return err
		}
		defer zr.Close()
		return x.tarOrSingle(zr, "")
	case isTar(head):
		return x.tar(tar.NewReader(src))
	}
	return ErrUnsupported
}

func isTar(head []byte) bool {
	return len(head) >= 262 && string(head[257:262]) == "ustar"
}

type extractor struct {
	f       *FS
	ctx     context.Context
	dest    string
	space   *spaceLeft
	entries int      // entries seen
	pool    *writers // nil: files are written one at a time
	// dirs are the directories this extraction made or found, so each is
	// checked once, not for every file in it. Only names are kept: files
	// are still opened through the root, which a directory swapped since
	// can't get them out of. Making a link forgets them all.
	dirs map[string]bool

	mu  sync.Mutex // res, for the writers
	res *ArchiveResult
}

func (x *extractor) count(files, skipped int, bytes int64) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.res.Files += files
	x.res.Skipped += skipped
	x.res.Bytes += bytes
}

// spaceLeft is what an extraction may still write, shared by its writers.
type spaceLeft struct {
	limit int64 // < 0: unlimited
	used  atomic.Int64
}

// reserve takes n bytes, or reports false if they don't fit.
func (s *spaceLeft) reserve(n int64) bool {
	if s.limit < 0 {
		return true
	}
	for {
		used := s.used.Load()
		if n > s.limit-used {
			return false
		}
		if s.used.CompareAndSwap(used, used+n) {
			return true
		}
	}
}

func (s *spaceLeft) release(n int64) {
	if s.limit >= 0 {
		s.used.Add(-n)
	}
}

// limitedWriter writes a file within the extraction's space, and stops
// when it's cancelled.
type limitedWriter struct {
	ctx   context.Context
	w     io.Writer
	space *spaceLeft
	n     int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if err := l.ctx.Err(); err != nil {
		return 0, err
	}
	if !l.space.reserve(int64(len(p))) {
		return 0, ErrNoSpace
	}
	n, err := l.w.Write(p)
	l.space.release(int64(len(p) - n))
	l.n += int64(n)
	return n, err
}

// copyBuffers are io.Copy's buffers, which it would otherwise allocate for
// every file.
var copyBuffers = sync.Pool{New: func() any { b := make([]byte, 32<<10); return &b }}

// writers writes regular files on several goroutines while the archive is
// read, and every entry checked, in order. Only paths that don't overlap
// are written at once: an entry at, inside, or above a path still being
// written waits for it, so the result is the same as one at a time.
type writers struct {
	ctx      context.Context
	cancel   context.CancelFunc
	slots    chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	inflight map[string]struct{}
	err      error // the first writer's error
}

func newWriters(ctx context.Context, n int) *writers {
	ctx, cancel := context.WithCancel(ctx)
	return &writers{ctx: ctx, cancel: cancel, slots: make(chan struct{}, n), inflight: map[string]struct{}{}}
}

// failed returns the first writer's error.
func (w *writers) failed() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// wait waits for every writer, and returns the first one's error.
func (w *writers) wait() error {
	if w == nil {
		return nil
	}
	w.wg.Wait()
	return w.failed()
}

// waitFor waits until nothing at, inside, or above target is being
// written.
func (w *writers) waitFor(target string) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	overlaps := false
	for p := range w.inflight {
		if p == target || strings.HasPrefix(p, target+"/") || strings.HasPrefix(target, p+"/") {
			overlaps = true
			break
		}
	}
	w.mu.Unlock()
	if overlaps {
		return w.wait()
	}
	return w.failed()
}

// start writes target on a goroutine, once one is free. The caller has
// waited for anything overlapping it.
func (w *writers) start(target string, write func(ctx context.Context) error) {
	w.slots <- struct{}{}
	w.mu.Lock()
	w.inflight[target] = struct{}{}
	w.mu.Unlock()
	w.wg.Go(func() {
		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("writing %s panicked: %v", target, p)
				}
			}()
			return write(w.ctx)
		}()
		w.mu.Lock()
		delete(w.inflight, target)
		if err != nil && w.err == nil {
			w.err = err
			w.cancel() // the others stop at their next write
		}
		w.mu.Unlock()
		<-w.slots
	})
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
	return x.entry(single, kindFile, 0, "", readerOf(br), time.Time{})
}

// readerOf opens what's next in a stream.
func readerOf(r io.Reader) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(r), nil }
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
		if err := x.entry(h.Name, kind, fs.FileMode(h.Mode).Perm(), h.Linkname, readerOf(tr), h.ModTime); err != nil { //nolint:gosec // only the permission bits are kept
			return err
		}
	}
}

// zip extracts a zip's entries in order, writing up to threads files at
// once: each entry is read on its own, so they're inflated in parallel.
func (x *extractor) zip(zr *zip.Reader, threads int) error {
	if threads > 1 {
		x.pool = newWriters(x.ctx, threads)
	}
	var err error
	for _, zf := range zr.File {
		if err = x.zipEntry(zf); err != nil {
			break
		}
	}
	// Nothing is written after Extract returns.
	if werr := x.pool.wait(); werr != nil {
		return werr
	}
	return err
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
	if kind == kindSymlink {
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxLinkTarget))
		_ = rc.Close()
		if err != nil {
			return err
		}
		link = string(b)
	}
	return x.entry(zf.Name, kind, mode.Perm(), link, zf.Open, zf.Modified)
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

// entry checks and writes one archive entry. A regular file's contents are
// read from open, possibly on another goroutine (x.pool).
func (x *extractor) entry(name string, kind entryKind, perm fs.FileMode, link string, open func() (io.ReadCloser, error), mtime time.Time) error {
	if err := x.pool.failed(); err != nil {
		return err
	}
	if err := x.ctx.Err(); err != nil {
		return err
	}
	if x.entries >= maxEntries {
		return fmt.Errorf("archive has more than %d entries", maxEntries)
	}
	x.entries++
	rel, ok := entryName(name)
	if !ok {
		x.count(0, 1, 0)
		return nil
	}
	target := path.Join(x.dest, rel)
	f := x.f
	if f.Denied(target, kind == kindDir) {
		x.count(0, 1, 0)
		return nil
	}
	if kind == kindOther {
		x.count(0, 1, 0)
		return nil
	}
	// Nothing below may race a file being written at, inside, or above
	// target; a hard link also needs the file it points at written.
	if kind == kindHardlink {
		if err := x.pool.wait(); err != nil {
			return err
		}
	} else if err := x.pool.waitFor(target); err != nil {
		return err
	}
	if kind == kindDir {
		if err := x.mkdirAll(target); err != nil {
			return err
		}
		x.count(1, 0, 0)
		return nil
	}
	if err := x.mkdirAll(path.Dir(target)); err != nil {
		return err
	}
	if kind == kindSymlink || kind == kindHardlink {
		clear(x.dirs)
	}
	switch kind {
	case kindSymlink:
		if skip, err := x.replaceLink(target, false); skip || err != nil {
			return err
		}
		if len(link) == 0 || len(link) >= maxLinkTarget {
			x.count(0, 1, 0)
			return nil
		}
		if err := f.Symlink(link, target); err != nil {
			return err
		}
		x.count(1, 0, 0)
		return nil
	case kindHardlink:
		if skip, err := x.replaceLink(target, false); skip || err != nil {
			return err
		}
		from, ok := entryName(link)
		if !ok {
			x.count(0, 1, 0)
			return nil
		}
		if err := f.Link(path.Join(x.dest, from), target); err != nil {
			x.count(0, 1, 0) // the file it points at was skipped
			return nil       //nolint:nilerr // a missing link target doesn't stop the rest
		}
		x.count(1, 0, 0)
		return nil
	}
	if x.pool == nil {
		return x.file(x.ctx, target, perm, open, mtime)
	}
	x.pool.start(target, func(ctx context.Context) error { return x.file(ctx, target, perm, open, mtime) })
	return nil
}

// mkdirAll is MkdirAll for directories not made or found already.
func (x *extractor) mkdirAll(dir string) error {
	if x.dirs[dir] {
		return nil
	}
	if err := x.f.MkdirAll(dir); err != nil {
		return err
	}
	for d := dir; d != "." && !x.dirs[d]; d = path.Dir(d) {
		x.dirs[d] = true
	}
	return nil
}

// replaceLink removes what's at target so a link can take its place, or,
// for a file (onlyLinks), a symlink, which is replaced, not written
// through, as tar does. skip is true for a directory in the way.
func (x *extractor) replaceLink(target string, onlyLinks bool) (skip bool, err error) {
	fi, err := x.f.root.Lstat(target)
	if err != nil || (onlyLinks && fi.Mode()&fs.ModeSymlink == 0) {
		return false, nil //nolint:nilerr // nothing in the way
	}
	if fi.IsDir() {
		x.count(0, 1, 0)
		return true, nil
	}
	return false, x.f.root.Remove(target)
}

// file writes one regular file. A new one is created with O_EXCL; only
// when something is in the way is it looked at (a link there is replaced,
// a directory or special file skipped), and the file opened as Open does.
func (x *extractor) file(ctx context.Context, target string, perm fs.FileMode, open func() (io.ReadCloser, error), mtime time.Time) error {
	r, err := open()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	f := x.f
	mode := perm
	if mode == 0 {
		mode = FileMode
	}
	out, err := f.create(target, mode)
	if errors.Is(err, fs.ErrNotExist) {
		// A directory made earlier was removed meanwhile.
		if err = f.MkdirAll(path.Dir(target)); err == nil {
			out, err = f.create(target, mode)
		}
	}
	if errors.Is(err, fs.ErrExist) {
		if _, err := x.replaceLink(target, true); err != nil {
			return err
		}
		out, err = f.Open(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if errors.Is(err, ErrNotRegular) {
			x.count(0, 1, 0)
			return nil
		}
		if err == nil && perm != 0 {
			err = out.Chmod(perm)
		}
	}
	if err != nil {
		if out != nil {
			_ = out.Close()
		}
		return err
	}
	w := &limitedWriter{ctx: ctx, w: out, space: x.space}
	buf := copyBuffers.Get().(*[]byte)
	_, err = io.CopyBuffer(w, r, *buf)
	copyBuffers.Put(buf)
	x.count(0, 0, w.n)
	if err == nil && !mtime.IsZero() {
		_ = setTimes(out, mtime, mtime)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	x.count(1, 0, 0)
	return nil
}
