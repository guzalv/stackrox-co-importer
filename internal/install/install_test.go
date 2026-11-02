package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// IMP-IMG-007: install.sh downloads the release archive for the host's OS/arch,
// verifies its SHA-256 checksum against checksums.txt, and extracts the binary.
// These tests drive the real install.sh against a stub server standing in for
// GitHub releases (via the BASE_URL override), so they exercise the redirect
// based latest-version resolution, the checksum gate, and extraction without
// touching the network.

const (
	testTag     = "v9.9.9"
	testVersion = "9.9.9"
	sentinel    = "INSTALLED_OK_IMP_IMG_007"
	binaryName  = "compliance-operator-importer"
)

// assetName mirrors the name_template GoReleaser uses and the name install.sh
// builds. On the test host, runtime.GOOS/GOARCH match what `uname` resolves to
// in the script (linux/darwin, amd64/arm64).
func assetName() string {
	return fmt.Sprintf("stackrox-co-importer_%s_%s_%s.tar.gz", testVersion, runtime.GOOS, runtime.GOARCH)
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// makeTarball builds a .tar.gz whose compliance-operator-importer member is an
// executable shell script that prints the sentinel. LICENSE and README.md are
// added too, so the test also proves install.sh extracts only the named binary.
func makeTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := []struct {
		name string
		body string
		mode int64
	}{
		{"LICENSE", "fake license\n", 0o644},
		{"README.md", "fake readme\n", 0o644},
		{binaryName, "#!/bin/sh\necho " + sentinel + "\n", 0o755},
	}
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// startStubServer mimics the GitHub release endpoints install.sh uses:
//   - /releases/latest            -> 302 redirect to /releases/tag/<tag>
//   - /releases/tag/<tag>         -> 200 (HEAD target of the redirect)
//   - /releases/download/<tag>/*  -> the asset bytes and checksums.txt
//
// served is the bytes returned for the asset. hashOf is the content whose hash
// is recorded in checksums.txt. Passing different values simulates tampering.
func startStubServer(t *testing.T, served, hashOf []byte) *httptest.Server {
	t.Helper()
	asset := assetName()
	const repoPath = "/guzalv/stackrox-co-importer"
	checksums := fmt.Sprintf("%s  %s\n%s  %s\n",
		sha256hex(hashOf), asset,
		strings.Repeat("0", 64), "stackrox-co-importer_"+testVersion+"_other_arch.tar.gz", // decoy line
	)

	mux := http.NewServeMux()
	mux.HandleFunc(repoPath+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, repoPath+"/releases/tag/"+testTag, http.StatusFound)
	})
	mux.HandleFunc(repoPath+"/releases/tag/"+testTag, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(repoPath+"/releases/download/"+testTag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(served)
	})
	mux.HandleFunc(repoPath+"/releases/download/"+testTag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func installScriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatalf("resolve install.sh path: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("install.sh not found at %s: %v", p, err)
	}
	return p
}

// runInstall executes install.sh with the stub BASE_URL and a temp INSTALL_DIR.
// VERSION is left unset so the test drives the redirect-based latest-release
// resolution path.
func runInstall(t *testing.T, baseURL, installDir string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", installScriptPath(t))
	cmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "INSTALL_DIR="+installDir)
	// Drop any inherited VERSION so the redirect path is exercised.
	filtered := cmd.Env[:0]
	for _, e := range cmd.Env {
		if !strings.HasPrefix(e, "VERSION=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestIMP_IMG_007_InstallScriptDownloadsVerifiesAndExtracts(t *testing.T) {
	tarball := makeTarball(t)
	srv := startStubServer(t, tarball, tarball) // served == hashed: checksum matches
	installDir := t.TempDir()

	out, err := runInstall(t, srv.URL, installDir)
	if err != nil {
		t.Fatalf("install.sh failed: %v\noutput:\n%s", err, out)
	}

	if !strings.Contains(out, testTag) {
		t.Errorf("expected output to report resolved version %s; output:\n%s", testTag, out)
	}
	if !strings.Contains(out, "Checksum verified") {
		t.Errorf("expected output to confirm checksum verification; output:\n%s", out)
	}

	bin := filepath.Join(installDir, binaryName)
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("binary not installed at %s: %v", bin, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: mode %v", info.Mode())
	}

	ran, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("running installed binary failed: %v\noutput: %s", err, ran)
	}
	if !strings.Contains(string(ran), sentinel) {
		t.Errorf("installed binary did not print sentinel %q; got: %s", sentinel, ran)
	}
}

func TestIMP_IMG_007_InstallScriptRejectsChecksumMismatch(t *testing.T) {
	tarball := makeTarball(t)
	tampered := append(append([]byte{}, tarball...), 0x00) // extra byte -> different hash
	srv := startStubServer(t, tampered, tarball)           // checksums.txt holds the untampered hash
	installDir := t.TempDir()

	out, err := runInstall(t, srv.URL, installDir)
	if err == nil {
		t.Fatalf("expected install.sh to fail on checksum mismatch; output:\n%s", out)
	}
	if !strings.Contains(out, "checksum mismatch") {
		t.Errorf("expected a checksum-mismatch error; output:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(installDir, binaryName)); !os.IsNotExist(err) {
		t.Errorf("binary must NOT be installed when the checksum fails (stat err: %v)", err)
	}
}
