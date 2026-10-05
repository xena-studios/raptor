package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// Transfers through the web file manager (docs/ARCHITECTURE.md#files-and-sftp):
// a file of up to MaxTransfer bytes goes in chunks of up to MaxChunk, over a
// separate connection per transfer that Wings opens to the Panel, so a large
// transfer never slows the console. Both directions resume: an upload from
// what's been received (even after a Wings restart), a download from any
// offset. Larger files go through SFTP.
const (
	MaxTransfer = 1 << 30
	// MaxChunk keeps every chunk under Cloudflare's 100 MB request limit.
	MaxChunk = 64 << 20
	// Idle transfers expire: uploads (and their staging files) a day after
	// the last chunk, downloads after an hour.
	uploadIdle   = 24 * time.Hour
	downloadIdle = time.Hour
	// stagingPrefix names an upload's staging file, in the server's
	// directory so it counts toward the server's disk limit.
	stagingPrefix = ".raptor-upload-"
)

// Transfer errors.
var (
	ErrTransferNotFound = errors.New("transfer not found or expired")
	ErrOffset           = errors.New("chunk doesn't start where the upload is")
	ErrChunkTooLarge    = errors.New("chunk too large")
	ErrChanged          = errors.New("the file changed during the download; start it again")
)

// Upload is an upload in progress.
type Upload struct {
	ID       string `json:"upload_id"`
	ServerID string `json:"server_id"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Received int64  `json:"received"`
	Done     bool   `json:"done,omitempty"` // the file is in place
	// MaxChunk is the largest chunk accepted.
	MaxChunk int64 `json:"max_chunk"`
}

func staging(id string) string { return stagingPrefix + id }

// StartUpload begins an upload of size bytes to name, which it creates or
// replaces once every byte has arrived.
func (s *Service) StartUpload(ctx context.Context, id, user, name string, size int64) (Upload, error) {
	switch {
	case size < 0:
		return Upload{}, errors.New("size can't be negative")
	case size > MaxTransfer:
		return Upload{}, fmt.Errorf("%w: uploads through the Panel are limited to %d GiB; use SFTP for larger files", ErrTooLarge, MaxTransfer>>30)
	}
	f, err := s.open(ctx, id, true)
	if err != nil {
		return Upload{}, err
	}
	defer func() { _ = f.Close() }()
	name = Rel(name)
	if name == "." {
		return Upload{}, &fs.PathError{Op: "upload", Path: name, Err: ErrRoot}
	}
	if err := f.check(name, false); err != nil {
		return Upload{}, err
	}
	// Fail now, not after the last chunk, if the file can't go there: a
	// directory or special file in the way, or a parent that's a file or a
	// link out of the directory.
	if fi, err := f.Stat(name); err == nil && !fi.Mode().IsRegular() {
		return Upload{}, &fs.PathError{Op: "upload", Path: name, Err: ErrNotRegular}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Upload{}, err
	}
	for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
		fi, err := f.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Upload{}, err
		}
		if !fi.IsDir() {
			return Upload{}, &fs.PathError{Op: "upload", Path: dir, Err: syscall.ENOTDIR}
		}
		break
	}
	space, err := s.o.Servers.SpaceLeft(ctx, id)
	if err != nil {
		return Upload{}, err
	}
	if space >= 0 && size > space {
		return Upload{}, fmt.Errorf("%w: the file is %d bytes and the server has %d left", ErrNoSpace, size, space)
	}
	up := Upload{ID: uuid.NewString(), ServerID: id, Path: name, Size: size, MaxChunk: MaxChunk}
	file, err := f.Open(staging(up.ID), os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return Upload{}, err
	}
	_ = file.Close()
	now := s.o.Now().UnixMilli()
	if err := s.o.Store.Write.CreateUpload(ctx, store.CreateUploadParams{
		ID: up.ID, ServerID: id, UserID: user, Path: name, Size: size, CreatedAt: now, TouchedAt: now,
	}); err != nil {
		_ = f.root.Remove(staging(up.ID))
		return Upload{}, err
	}
	if size == 0 {
		return s.finishUpload(ctx, f, up)
	}
	return up, nil
}

// upload loads an upload that hasn't expired.
func (s *Service) upload(ctx context.Context, uploadID string) (store.FileUpload, error) {
	row, err := s.o.Store.Write.GetUpload(ctx, uploadID)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrTransferNotFound
	}
	if err != nil {
		return row, err
	}
	if s.o.Now().Sub(time.UnixMilli(row.TouchedAt)) > uploadIdle {
		return row, ErrTransferNotFound
	}
	return row, nil
}

// HasTransfer reports whether id is an upload or download in progress.
func (s *Service) HasTransfer(ctx context.Context, id string) bool {
	s.mu.Lock()
	_, ok := s.downloads[id]
	s.mu.Unlock()
	if ok {
		return true
	}
	_, err := s.upload(ctx, id)
	return err == nil
}

// UploadStatus returns how much of an upload has arrived, to resume it.
func (s *Service) UploadStatus(ctx context.Context, uploadID string) (Upload, error) {
	row, err := s.upload(ctx, uploadID)
	if err != nil {
		return Upload{}, err
	}
	f, err := s.FS(row.ServerID)
	if err != nil {
		return Upload{}, err
	}
	defer func() { _ = f.Close() }()
	up := uploadOf(row)
	fi, err := f.Lstat(staging(row.ID))
	if err != nil {
		return Upload{}, fmt.Errorf("upload staging file: %w", err)
	}
	up.Received = fi.Size()
	return up, nil
}

func uploadOf(r store.FileUpload) Upload {
	return Upload{ID: r.ID, ServerID: r.ServerID, Path: r.Path, Size: r.Size, MaxChunk: MaxChunk}
}

// WriteChunk appends a chunk at offset, which must be what has arrived so
// far (a mismatch returns ErrOffset with the upload's state, to resume
// from). The chunk that completes the upload moves the file into place.
func (s *Service) WriteChunk(ctx context.Context, uploadID string, offset int64, r io.Reader) (Upload, error) {
	row, err := s.upload(ctx, uploadID)
	if err != nil {
		return Upload{}, err
	}
	up := uploadOf(row)
	if err := s.o.Servers.CheckFiles(ctx, row.ServerID, false); err != nil {
		return up, err
	}
	f, err := s.FS(row.ServerID)
	if err != nil {
		return up, err
	}
	defer func() { _ = f.Close() }()
	file, err := f.Open(staging(row.ID), os.O_WRONLY)
	if err != nil {
		return up, fmt.Errorf("upload staging file: %w", err)
	}
	defer func() { _ = file.Close() }()
	st, err := file.Stat()
	if err != nil {
		return up, err
	}
	up.Received = st.Size()
	if offset != up.Received {
		return up, fmt.Errorf("%w: it's at %d, the chunk starts at %d", ErrOffset, up.Received, offset)
	}
	limit := min(int64(MaxChunk), up.Size-offset)
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return up, err
	}
	n, err := io.Copy(file, io.LimitReader(r, limit))
	if err == nil {
		var one [1]byte
		if m, _ := io.ReadFull(r, one[:]); m > 0 {
			err = fmt.Errorf("%w: more than %d bytes (the chunk limit or what's left of the file)", ErrChunkTooLarge, limit)
		}
	}
	if err != nil {
		// Keep what arrived before this chunk, so it can be sent again.
		if terr := file.Truncate(offset); terr != nil {
			return up, errors.Join(err, terr)
		}
		return up, err
	}
	up.Received = offset + n
	if err := s.o.Store.Write.TouchUpload(ctx, store.TouchUploadParams{TouchedAt: s.o.Now().UnixMilli(), ID: row.ID}); err != nil {
		return up, err
	}
	if up.Received < up.Size {
		return up, nil
	}
	if err := file.Sync(); err != nil {
		return up, err
	}
	return s.finishUpload(ctx, f, up)
}

// finishUpload moves a complete upload into place, replacing what's there.
func (s *Service) finishUpload(ctx context.Context, f *FS, up Upload) (Upload, error) {
	if err := f.MkdirAll(path.Dir(up.Path)); err != nil {
		return up, err
	}
	if fi, err := f.Lstat(up.Path); err == nil && fi.IsDir() {
		return up, &fs.PathError{Op: "upload", Path: up.Path, Err: syscall.EISDIR}
	}
	if err := f.Rename(staging(up.ID), up.Path, true); err != nil {
		return up, err
	}
	up.Received, up.Done = up.Size, true
	return up, s.o.Store.Write.DeleteUpload(ctx, up.ID)
}

// CancelUpload drops an upload and what arrived of it.
func (s *Service) CancelUpload(ctx context.Context, id, uploadID string) error {
	row, err := s.o.Store.Write.GetUpload(ctx, uploadID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row.ServerID != id) {
		return ErrTransferNotFound
	}
	if err != nil {
		return err
	}
	s.removeStaging(row)
	return s.o.Store.Write.DeleteUpload(ctx, row.ID)
}

func (s *Service) removeStaging(row store.FileUpload) {
	f, err := s.FS(row.ServerID)
	if err != nil {
		return // the server is gone, and its files with it
	}
	defer func() { _ = f.Close() }()
	if err := f.root.Remove(staging(row.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.o.Log.Warn("removing an upload's staging file failed", "server", row.ServerID, "upload", row.ID, "err", err)
	}
}

// Prune drops idle uploads (with their staging files) and downloads.
func (s *Service) Prune(ctx context.Context) (int, error) {
	now := s.o.Now()
	s.mu.Lock()
	n := 0
	for id, d := range s.downloads {
		if now.Sub(d.touched) > downloadIdle {
			delete(s.downloads, id)
			n++
		}
	}
	s.mu.Unlock()
	rows, err := s.o.Store.Write.ListIdleUploads(ctx, now.Add(-uploadIdle).UnixMilli())
	if err != nil {
		return n, err
	}
	for _, row := range rows {
		s.removeStaging(row)
		if err := s.o.Store.Write.DeleteUpload(ctx, row.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// IsStaging reports whether a name in a server's directory is an upload's
// staging file.
func IsStaging(name string) bool { return strings.HasPrefix(path.Base(name), stagingPrefix) }

// Download is a download in progress. It's kept in memory: after a Wings
// restart the Panel starts it again and resumes from its offset.
type Download struct {
	ID       string    `json:"download_id"`
	ServerID string    `json:"server_id"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified_at"`
	MaxChunk int64     `json:"max_chunk"`

	touched time.Time
}

// StartDownload begins a download of a regular file of up to MaxTransfer
// bytes. Its chunks are read with ReadChunk.
func (s *Service) StartDownload(ctx context.Context, id, name string) (Download, error) {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return Download{}, err
	}
	defer func() { _ = f.Close() }()
	name = Rel(name)
	file, err := f.Open(name, os.O_RDONLY)
	if err != nil {
		return Download{}, err
	}
	fi, err := file.Stat()
	_ = file.Close()
	if err != nil {
		return Download{}, err
	}
	if fi.Size() > MaxTransfer {
		return Download{}, fmt.Errorf("%w: downloads through the Panel are limited to %d GiB; use SFTP for larger files", ErrTooLarge, MaxTransfer>>30)
	}
	d := &Download{ID: uuid.NewString(), ServerID: id, Path: name, Size: fi.Size(), Modified: fi.ModTime(), MaxChunk: MaxChunk, touched: s.o.Now()}
	s.mu.Lock()
	s.downloads[d.ID] = d
	s.mu.Unlock()
	return *d, nil
}

// ReadChunk writes up to n bytes (at most MaxChunk) of a download from
// offset to w and returns how many. The file must not have changed since
// the download started.
func (s *Service) ReadChunk(ctx context.Context, downloadID string, offset, n int64, w io.Writer) (int64, error) {
	s.mu.Lock()
	d, ok := s.downloads[downloadID]
	if ok && s.o.Now().Sub(d.touched) > downloadIdle {
		delete(s.downloads, downloadID)
		ok = false
	}
	if ok {
		d.touched = s.o.Now()
	}
	s.mu.Unlock()
	switch {
	case !ok:
		return 0, ErrTransferNotFound
	case n > MaxChunk:
		return 0, fmt.Errorf("%w: at most %d bytes per chunk", ErrChunkTooLarge, MaxChunk)
	case offset < 0 || n < 0 || offset > d.Size:
		return 0, fmt.Errorf("chunk %d+%d is outside the file (%d bytes)", offset, n, d.Size)
	}
	f, err := s.open(ctx, d.ServerID, false)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	file, err := f.Open(d.Path, os.O_RDONLY)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	fi, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if fi.Size() != d.Size || !fi.ModTime().Equal(d.Modified) {
		return 0, ErrChanged
	}
	return io.Copy(w, io.NewSectionReader(file, offset, min(n, d.Size-offset)))
}
