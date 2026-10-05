package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// releaseSums is a checksums.txt as scripts/build-release.sh writes it.
const releaseSums = `1111111111111111111111111111111111111111111111111111111111111111  turnback_darwin_amd64.tar.gz
2222222222222222222222222222222222222222222222222222222222222222  turnback_darwin_arm64.tar.gz
3333333333333333333333333333333333333333333333333333333333333333  turnback_freebsd_amd64.tar.gz
4444444444444444444444444444444444444444444444444444444444444444  turnback_linux_amd64.tar.gz
5555555555555555555555555555555555555555555555555555555555555555  turnback_linux_arm64.tar.gz
6666666666666666666666666666666666666666666666666666666666666666  turnback_windows_amd64.zip
7777777777777777777777777777777777777777777777777777777777777777  turnback_windows_arm64.zip
`

// cask runs scripts/homebrew-cask.sh for tag with sums as the release's
// checksums.txt.
func cask(t *testing.T, tag, sums string) (stdout, stderr string, err error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the release scripts run on Unix")
	}
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "scripts/homebrew-cask.sh", tag, path)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// stanza returns the quoted values of the first line in rb that starts
// with name, such as `arch arm: "arm64", intel: "amd64"`, by their keys.
func stanza(t *testing.T, rb, name string) map[string]string {
	t.Helper()
	line := regexp.MustCompile(`(?m)^\s*` + name + ` (.*)$`).FindStringSubmatch(rb)
	if line == nil {
		t.Fatalf("the cask has no %s stanza:\n%s", name, rb)
	}
	values := map[string]string{}
	for _, m := range regexp.MustCompile(`(?:(\w+): )?"([^"]*)"`).FindAllStringSubmatch(line[1], -1) {
		values[m[1]] = m[2]
	}
	return values
}

func TestHomebrewCaskInstallsEachPlatformsOwnArchive(t *testing.T) {
	rb, stderr, err := cask(t, "v1.2.3", releaseSums)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	arch, osName := stanza(t, rb, "arch"), stanza(t, rb, "os")
	version, url, binary := stanza(t, rb, "version")[""], stanza(t, rb, "url")[""], stanza(t, rb, "binary")[""]
	// The sha256 stanza runs over several lines, one platform per line.
	sums := map[string]string{}
	block := regexp.MustCompile(`(?s)sha256 (.*?)\n\n`).FindStringSubmatch(rb)
	if block == nil {
		t.Fatalf("the cask has no sha256 stanza:\n%s", rb)
	}
	for _, m := range regexp.MustCompile(`(\w+):\s*"([0-9a-f]+)"`).FindAllStringSubmatch(block[1], -1) {
		sums[m[1]] = m[2]
	}

	for _, p := range []struct {
		name, cpu, system, archive, sum string
	}{
		{"arm", "arm", "macos", "turnback_darwin_arm64", strings.Repeat("2", 64)},
		{"intel", "intel", "macos", "turnback_darwin_amd64", strings.Repeat("1", 64)},
		{"arm64_linux", "arm", "linux", "turnback_linux_arm64", strings.Repeat("5", 64)},
		{"x86_64_linux", "intel", "linux", "turnback_linux_amd64", strings.Repeat("4", 64)},
	} {
		expand := strings.NewReplacer("#{version}", version, "#{os}", osName[p.system], "#{arch}", arch[p.cpu])
		wantURL := "https://github.com/Waiivyy/turnback/releases/download/v1.2.3/" + p.archive + ".tar.gz"
		if got := expand.Replace(url); got != wantURL {
			t.Errorf("%s downloads %s, want %s", p.name, got, wantURL)
		}
		if got := expand.Replace(binary); got != p.archive+"/turnback" {
			t.Errorf("%s installs %s, want %s/turnback", p.name, got, p.archive)
		}
		if sums[p.name] != p.sum {
			t.Errorf("%s checks sha256 %q, want %q", p.name, sums[p.name], p.sum)
		}
	}
	if len(sums) != 4 {
		t.Errorf("the cask lists %d checksums, want 4: %v", len(sums), sums)
	}
}

func TestHomebrewCaskClearsTheQuarantineOnMacOS(t *testing.T) {
	// The binaries are not signed by Apple, so a copy that Homebrew marks
	// as downloaded would be refused by Gatekeeper.
	rb, stderr, err := cask(t, "v1.2.3", releaseSums)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	step := regexp.MustCompile(`(?s)postflight_steps do\s+on_macos do\s+run "/usr/bin/xattr", args: \["-dr", "com\.apple\.quarantine", "\{\{staged_path\}\}"\]\s+end\s+end`)
	if !step.MatchString(rb) {
		t.Errorf("the cask does not clear the quarantine on macOS:\n%s", rb)
	}
}

func TestHomebrewCaskIsValidRuby(t *testing.T) {
	rb, stderr, err := cask(t, "v1.2.3", releaseSums)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	ruby, lookErr := exec.LookPath("ruby")
	if lookErr != nil {
		t.Skip("ruby is not installed")
	}
	path := filepath.Join(t.TempDir(), "turnback.rb")
	if err := os.WriteFile(path, []byte(rb), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ruby, "-c", path).CombinedOutput(); err != nil {
		t.Errorf("ruby -c: %v\n%s", err, out)
	}
}

func TestHomebrewCaskNeedsEveryArchiveItInstalls(t *testing.T) {
	var sums []string
	for _, line := range strings.Split(strings.TrimSpace(releaseSums), "\n") {
		if !strings.Contains(line, "linux_arm64") {
			sums = append(sums, line)
		}
	}
	rb, stderr, err := cask(t, "v1.2.3", strings.Join(sums, "\n")+"\n")
	if err == nil || rb != "" || !strings.Contains(stderr, "turnback_linux_arm64.tar.gz") {
		t.Errorf("err %v, stdout %q, stderr %q; want a failure naming the missing archive and no cask", err, rb, stderr)
	}
}

func TestHomebrewCaskNeedsAVersionTag(t *testing.T) {
	for _, tag := range []string{"main", "1.2.3", "v1.2", ""} {
		rb, _, err := cask(t, tag, releaseSums)
		if err == nil || rb != "" {
			t.Errorf("tag %q: err %v, stdout %q; want a failure and no cask", tag, err, rb)
		}
	}
}
