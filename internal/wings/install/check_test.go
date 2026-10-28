package install

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
)

func TestRunFailedDownload(t *testing.T) {
	var job strings.Builder
	rt := &fakeRuntime{output: "curl: (92) HTTP/2 stream 1 reset\n", failed: []containers.FailedDownload{
		{URL: "https://api.purpurmc.org/v2/purpur/1.21/latest/download", Code: 92},
		{URL: "https://example.com/b", Code: 6},
	}}
	p := params(t, testEgg())
	p.Output = &job
	res, err := Run(context.Background(), rt, p)
	var f *Failure
	want := "A download failed: https://api.purpurmc.org/v2/purpur/1.21/latest/download (curl exit 92; 1 more failed too). Reinstall to try again."
	if !errors.As(err, &f) || f.Reason != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	// The reason ends both the kept log and the job's.
	if !strings.HasSuffix(string(res.Log), want+"\n") || !strings.HasSuffix(job.String(), want+"\n") {
		t.Fatalf("log %q, job %q", res.Log, job.String())
	}

	// No URL recorded: still a failure.
	rt = &fakeRuntime{failed: []containers.FailedDownload{{Code: 7}}}
	if _, err := Run(context.Background(), rt, params(t, testEgg())); err == nil || err.Error() != "A download failed (curl exit 7). Reinstall to try again." {
		t.Fatalf("got %v", err)
	}

	// A Docker error is reported as it is.
	rt = &fakeRuntime{err: errors.New("start install container: boom"), failed: []containers.FailedDownload{{Code: 7}}}
	if _, err := Run(context.Background(), rt, params(t, testEgg())); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}

func jarEgg() *eggs.Egg {
	e := testEgg()
	e.Startup = []eggs.StartupCommand{{Name: "Default", Command: "java -Xms128M -jar {{SERVER_JARFILE}}"}}
	e.Variables = append(e.Variables, eggs.Variable{Env: "SERVER_JARFILE", Default: "server.jar", Rules: []string{"required", "string"}})
	return e
}

func zipFile(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	zw := zip.NewWriter(&b)
	w, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "Manifest-Version: 1.0\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRunChecksJar(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		vars  map[string]string
		want  string // the start of the error, "" for none
	}{
		{"valid", map[string]string{"server.jar": zipFile(t)}, nil, ""},
		{"absent", nil, nil, ""},
		{"empty", map[string]string{"server.jar": ""}, nil, "server.jar is empty, so the server can't start. Reinstall to try again."},
		{"corrupt", map[string]string{"server.jar": "<html>rate limited</html>"}, nil, "server.jar isn't a valid jar file (it's corrupt or not a jar), so the server can't start. Reinstall to try again."},
		{"variable", map[string]string{"purpur.jar": "x"}, map[string]string{"SERVER_JARFILE": "purpur.jar"}, "purpur.jar isn't a valid jar file"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := &fakeRuntime{files: c.files, exit: 1} // a non-zero exit is still only a warning
			p := params(t, jarEgg())
			p.Variables = c.vars
			_, err := Run(context.Background(), rt, p)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var f *Failure
			if !errors.As(err, &f) || !strings.HasPrefix(f.Reason, c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}

	// The server's own startup command decides, not the egg's default.
	rt := &fakeRuntime{files: map[string]string{"server.jar": "junk"}}
	p := params(t, jarEgg())
	p.Env.Startup = "./run.sh"
	if _, err := Run(context.Background(), rt, p); err != nil {
		t.Fatal(err)
	}
}

func TestStartupJar(t *testing.T) {
	vars := map[string]string{"SERVER_JARFILE": "server.jar", "EVIL": "../../etc/passwd", "SPACE": "a b.jar"}
	for _, c := range []struct{ startup, want string }{
		{"java -Xms128M -XX:MaxRAMPercentage=95.0 -jar {{SERVER_JARFILE}}", "server.jar"},
		{"java -jar {{SERVER_JARFILE}} nogui", "server.jar"},
		{"java -jar Lavalink.jar", "Lavalink.jar"},
		{"java -jar /home/container/libs/app.jar", "libs/app.jar"},
		{`java $( [[ ! -f unix_args.txt ]] && printf %s "-jar {{SERVER_JARFILE}}" || printf %s "@unix_args.txt" )`, ""},
		{"java -jar `cat .serverjar`", ""},
		{"java -jar {{UNKNOWN}}", ""},
		{"java -jar {{EVIL}}", ""},
		{"java -jar {{SPACE}}", ""},
		{"java -jar /opt/app.jar", ""},
		{"java -jar", ""},
		{"./bedrock_server", ""},
		{"", ""},
	} {
		if got := startupJar(c.startup, vars); got != c.want {
			t.Errorf("startupJar(%q) = %q, want %q", c.startup, got, c.want)
		}
	}
}

func TestCheckJarStaysInDir(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jar")
	if err := os.WriteFile(outside, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "server.jar")); err != nil {
		t.Fatal(err)
	}
	if err := checkJar(dir, "server.jar"); err != nil {
		t.Fatalf("followed a link out of the directory: %v", err)
	}
}
