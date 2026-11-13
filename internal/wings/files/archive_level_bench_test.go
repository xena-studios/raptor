package files

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkCompressLevel compares deflate levels for tar.gz (threads:
// Threads(0), so they follow -cpu).
func BenchmarkCompressLevel(b *testing.B) {
	for _, fx := range benchFixtures {
		for _, level := range []int{1, 2, 3, 4, 6} {
			b.Run(fmt.Sprintf("%s/level=%d", fx.name, level), func(b *testing.B) {
				dir := benchDir(b)
				size := benchFixture(b, dir, fx.name)
				f := benchFS(b, filepath.Join(dir, "src"))
				b.SetBytes(size)
				b.ReportAllocs()
				var out int64
				for b.Loop() {
					res, err := f.Compress(context.Background(), ".", []string{fx.name}, -1, time.Now(), CompressOptions{Name: "bench", Level: level})
					if err != nil {
						b.Fatal(err)
					}
					out = res.Bytes
					b.StopTimer()
					if err := f.root.Remove(res.Archive); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
				b.ReportMetric(float64(out), "archive-B")
			})
		}
	}
}
