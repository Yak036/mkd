package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.7.0", "v0.6.0", true},
		{"0.7.0", "0.6.0", true},
		{"v0.6.0", "v0.6.0", false},
		{"v0.6.0", "v0.7.0", false},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.6.1", "v0.6.0", true},
		// pre-releases
		{"v0.7.0", "v0.7.0-rc1", true},
		{"v0.7.0-rc1", "v0.7.0", false},
		{"v0.7.0-rc2", "v0.7.0-rc1", true},
		// build de desarrollo: no avisar
		{"v0.7.0", "dev", false},
		{"v0.7.0", "0.6.0-dirty", false},
		// vacíos
		{"", "v0.6.0", false},
		{"v0.7.0", "", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.latest, tc.current); got != tc.want {
			t.Fatalf("IsNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	v, ok := parseSemver("1.2.3-rc1")
	if !ok || v.num != [3]int{1, 2, 3} || v.pre != "rc1" {
		t.Fatalf("parseSemver = %+v ok=%v", v, ok)
	}
	if _, ok := parseSemver("1.x.3"); ok {
		t.Fatal("no debía aceptar 1.x.3")
	}
	if _, ok := parseSemver("1.2.3.4"); ok {
		t.Fatal("no debía aceptar 4 segmentos")
	}
}

func TestNotice(t *testing.T) {
	if notice := (Check{}).Notice(); notice != "" {
		t.Fatalf("sin novedad el aviso debe estar vacío: %q", notice)
	}
	check := Check{Latest: "v0.7.0", Available: true}
	if notice := check.Notice(); notice == "" {
		t.Fatal("con novedad debe haber aviso")
	}
}

func TestAssetNamePerPlatform(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "mkd_linux_amd64.tar.gz"},
		{"linux", "arm64", "mkd_linux_arm64.tar.gz"},
		{"darwin", "arm64", "mkd_darwin_arm64.tar.gz"},
		{"windows", "amd64", "mkd_windows_amd64.zip"},
	}
	for _, tc := range cases {
		if got := AssetName(tc.goos, tc.goarch); got != tc.want {
			t.Fatalf("AssetName(%s,%s) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("MKD_NO_UPDATE_CHECK", "1")
	if !Disabled() {
		t.Fatal("MKD_NO_UPDATE_CHECK=1 debe desactivar")
	}
	t.Setenv("MKD_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "true")
	if !Disabled() {
		t.Fatal("CI debe desactivar")
	}
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	if Disabled() {
		t.Fatal("sin variables no debe estar desactivado")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := readCache(); err == nil {
		t.Fatal("caché inexistente no debe leerse")
	}

	entry := cacheEntry{Latest: "v0.7.0", URL: "https://example/rel"}
	if err := writeCache(entry); err != nil {
		t.Fatal(err)
	}

	got, err := readCache()
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest != entry.Latest {
		t.Fatalf("caché = %+v", got)
	}

	check := Cached("v0.6.0")
	if !check.Available || check.Latest != "v0.7.0" {
		t.Fatalf("Cached = %+v", check)
	}
	if Cached("v0.7.0").Available {
		t.Fatal("misma versión no debe marcar novedad")
	}
}

// TestExtractFromTarGz valida la extracción del binario del archivo real.
func TestExtractFromTarGz(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "mkd_linux_amd64.tar.gz")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho hola\n")
	if err := tw.WriteHeader(&tar.Header{
		Name: "mkd", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := extractFromTarGz(archive, dir, "mkd")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("contenido extraído = %q", data)
	}
}

func TestExtractFromZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "mkd_windows_amd64.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("mkd.exe")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("MZ fake exe")
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := extractFromZip(archive, dir, "mkd.exe")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("contenido = %q", data)
	}
}

func TestExtractMissingBinary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "x.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.Close()
	gz.Close()
	os.WriteFile(archive, buf.Bytes(), 0o600)

	if _, err := extractFromTarGz(archive, dir, "mkd"); err == nil {
		t.Fatal("debía fallar si falta el binario")
	}
}

// TestReplaceSelfKeepsExecutable valida el reemplazo real del binario en
// ejecución: debe funcionar (sin ETXTBSY) y dejar permisos 0755.
func TestReplaceSelfKeepsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("el binario en uso no se puede renombrar igual en Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "bin")
	if err := os.WriteFile(target, []byte("viejo"), 0o755); err != nil {
		t.Fatal(err)
	}

	nuevo := filepath.Join(dir, "nuevo")
	if err := os.WriteFile(nuevo, []byte("nuevo"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replacePath(nuevo, target); err != nil {
		t.Fatalf("replacePath: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nuevo" {
		t.Fatalf("contenido = %q", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("el binario quedó sin permiso de ejecución: %v", info.Mode())
	}
}
