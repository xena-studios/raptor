package addons

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/shared/nupkg"
	"github.com/xena-studios/raptor/internal/wings/files"
)

// OpenMod's plugins (#415) are NuGet packages: Wings downloads the
// .nupkg from NuGet only, keeps it only if it has the SHA-512 NuGet's
// catalog lists (which the Panel passes), and writes the plugin's one
// assembly from it into the add-on folder. Nothing else in the package
// is written, and the entry's own name is never used as a path, so a
// package can't place a file anywhere else.
const (
	NuGetHost = "api.nuget.org"
	// KindOpenMod is the add-on kind whose files come from NuGet.
	KindOpenMod = "openmod"
	// MaxAssembly is the largest plugin assembly taken out of a package.
	MaxAssembly = 64 << 20
)

// ErrPackage is a package Raptor doesn't install: it has no single
// plugin assembly for Unturned (nupkg.PluginDLL).
var ErrPackage = errors.New("this package doesn't have one plugin assembly for Unturned; install it with openmod install in the console")

// checkNuGetKind keeps NuGet's packages to the openmod kind, and the
// openmod kind to NuGet.
func checkNuGetKind(kind, host string) error {
	if (kind == KindOpenMod) != strings.EqualFold(host, NuGetHost) {
		return ErrSource
	}
	return nil
}

// downloadNuGet downloads a package next to tmp, checks its hash, and
// writes its plugin assembly to tmp.
func (s *Service) downloadNuGet(ctx context.Context, f *files.FS, tmp string, u *url.URL, want expected) error {
	pkg := path.Join(path.Dir(tmp), ".raptor-"+uuid.NewString()+".nupkg.part")
	defer func() { _ = f.Remove(pkg) }()
	if err := s.download(ctx, f, pkg, u, want); err != nil {
		return err
	}
	in, err := f.Open(pkg, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	return extractPlugin(in, fi.Size(), func() (io.WriteCloser, error) {
		return f.Open(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	})
}

// extractPlugin copies a package's plugin assembly to the file create
// makes, at most MaxAssembly bytes.
func extractPlugin(r io.ReaderAt, size int64, create func() (io.WriteCloser, error)) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("the package isn't a valid .nupkg: %w", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	entry, ok := nupkg.PluginDLL(names)
	if !ok {
		return ErrPackage
	}
	var zf *zip.File
	for _, c := range zr.File {
		if c.Name == entry {
			zf = c
		}
	}
	if zf.UncompressedSize64 > MaxAssembly {
		return ErrTooLarge
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	out, err := create()
	if err != nil {
		return err
	}
	// The header's size can lie: the copy is capped too.
	n, err := io.Copy(out, io.LimitReader(rc, MaxAssembly+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case n > MaxAssembly:
		return ErrTooLarge
	}
	return nil
}
