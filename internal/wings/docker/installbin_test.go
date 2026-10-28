package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// fakeCurl is the image's "real" curl: it logs its arguments (except the
// wrapper's --version probes), prints a body, and exits with $FAKE_EXIT.
// With $FAKE_OLD it rejects --retry-all-errors as curl before 7.71 does;
// with $FAKE_ANCIENT it rejects --retry too.
const fakeCurl = `#!/bin/sh
case " $* " in
*" --version "*)
	case " $* " in
	*" --retry-all-errors "*) [ -n "$FAKE_OLD$FAKE_ANCIENT" ] && exit 2 ;;
	*" --retry "*) [ -n "$FAKE_ANCIENT" ] && exit 2 ;;
	esac
	exit 0 ;;
esac
printf '%s\n' "$*" >>"$FAKE_LOG"
printf 'body'
exit "${FAKE_EXIT:-0}"
`

type wrapperRun struct {
	stdout string
	code   int
	args   string // what the real curl got
	report string
}

func runWrapper(t *testing.T, shell string, env []string, withReal bool, args ...string) wrapperRun {
	t.Helper()
	dir := t.TempDir()
	if err := writeInstallBin(dir); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(dir, "real")
	if withReal {
		if err := os.Mkdir(realDir, 0o755); err != nil { //nolint:gosec // test
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(realDir, "curl"), []byte(fakeCurl), 0o755); err != nil { //nolint:gosec // test
			t.Fatal(err)
		}
	}
	logFile, report := filepath.Join(dir, "log"), filepath.Join(dir, "report")
	// The wrapper's own directory twice (with a trailing slash too) and an
	// empty entry: it must skip itself every time.
	bin := filepath.Join(dir, "bin")
	cmd := exec.Command(shell, append([]string{"-c", `curl "$@"`, "sh"}, args...)...) //nolint:gosec // test
	cmd.Env = append([]string{
		"PATH=" + bin + "::" + bin + "/:" + realDir, // no system curl
		"FAKE_LOG=" + logFile,
		"RAPTOR_INSTALL_REPORT=" + report,
	}, env...)
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns it unwrapped
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(logFile) //nolint:gosec // test
	rep, _ := os.ReadFile(report)     //nolint:gosec // test
	return wrapperRun{stdout: string(out), code: code, args: strings.TrimSpace(string(logged)), report: string(rep)}
}

// shells are the POSIX shells this machine has. BusyBox's is covered by the
// runtime e2e test, whose install image is BusyBox.
func shells(t *testing.T) []string {
	var out []string
	for _, s := range []string{"sh", "dash", "bash"} {
		if p, err := exec.LookPath(s); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Skip("no sh")
	}
	return out
}

func TestCurlWrapper(t *testing.T) {
	const retry = "--retry 5 --retry-delay 2 --retry-max-time 300"
	for _, sh := range shells(t) {
		t.Run(filepath.Base(sh), func(t *testing.T) {
			// Success: the script's arguments after the retry flags, the
			// body and exit code passed through, nothing recorded.
			r := runWrapper(t, sh, nil, true, "-sSL", "-o", "server.jar", "-w", "%{http_code}", "https://example.com/a b.jar")
			if r.code != 0 || r.stdout != "body" || r.report != "" {
				t.Fatalf("success: %+v", r)
			}
			if want := retry + " --retry-all-errors -sSL -o server.jar -w %{http_code} https://example.com/a b.jar"; r.args != want {
				t.Fatalf("args = %q, want %q", r.args, want)
			}

			// A network failure: recorded with the URL, curl's exit code kept.
			r = runWrapper(t, sh, []string{"FAKE_EXIT=92"}, true, "-sSL", "https://u:p@api.example.com/v2", "-o", "x")
			if r.code != 92 || r.stdout != "body" || r.report != "92 https://u:p@api.example.com/v2\n" {
				t.Fatalf("network failure: %+v", r)
			}

			// HTTP errors with --fail (a script probing a URL), a closed pipe,
			// and bad options are the script's business: not recorded.
			for _, code := range []string{"22", "23", "2", "3"} {
				r = runWrapper(t, sh, []string{"FAKE_EXIT=" + code}, true, "--head", "--fail", "https://example.com/missing")
				if r.code == 0 || r.report != "" {
					t.Fatalf("exit %s: %+v", code, r)
				}
			}

			// curl before 7.71: retries without --retry-all-errors.
			r = runWrapper(t, sh, []string{"FAKE_OLD=1"}, true, "-s", "https://example.com")
			if r.code != 0 || r.args != retry+" -s https://example.com" {
				t.Fatalf("old curl: %+v", r)
			}
			// A curl that knows neither: its arguments untouched.
			r = runWrapper(t, sh, []string{"FAKE_ANCIENT=1"}, true, "-s", "https://example.com")
			if r.code != 0 || r.args != "-s https://example.com" {
				t.Fatalf("ancient curl: %+v", r)
			}

			// No curl in the image: as if the wrapper weren't there.
			r = runWrapper(t, sh, nil, false, "https://example.com")
			if r.code != 127 || r.report != "" {
				t.Fatalf("no curl: %+v", r)
			}
		})
	}
}

func TestReadFailedDownloads(t *testing.T) {
	dir := t.TempDir()
	if got := readFailedDownloads(dir); got != nil {
		t.Fatalf("no report: %v", got)
	}
	report := "92 https://user:token@api.example.com/v2 https://other\nnonsense\n6\n\n"
	if err := os.WriteFile(filepath.Join(dir, installReportFile), []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []containers.FailedDownload{{URL: "https://api.example.com/v2", Code: 92}, {Code: 6}}
	if got := readFailedDownloads(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInstallEnv(t *testing.T) {
	image := []string{"PATH=/opt/java/bin:/usr/bin", "JAVA_HOME=/opt/java"}
	got := installEnv([]string{"TZ=UTC", "SERVER_PORT=1"}, image)
	if want := []string{"TZ=UTC", "SERVER_PORT=1", "PATH=/mnt/install/bin:/opt/java/bin:/usr/bin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("image PATH: %v", got)
	}
	if got := installEnv(nil, nil); !reflect.DeepEqual(got, []string{"PATH=/mnt/install/bin:" + dockerPath}) {
		t.Fatalf("no PATH: %v", got)
	}
	// An egg variable named PATH wins over the image's, as it would in Docker.
	if got := installEnv([]string{"PATH=/x"}, image); !reflect.DeepEqual(got, []string{"PATH=/mnt/install/bin:/x"}) {
		t.Fatalf("egg PATH: %v", got)
	}
}
