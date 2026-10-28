package server

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/scitq/scitq/server/config"
)

const testIndex = `<html><head><title>SciTQ</title><link rel="icon" href="/assets/logo-abc.png" /></head><body></body></html>`

func serve(t *testing.T, ui config.UIConfig, path string) (int, string) {
	t.Helper()
	h, err := uiHandler(fstest.MapFS{"index.html": {Data: []byte(testIndex)}}, ui)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(body)
}

func TestUIUnbrandedPageIsUnchanged(t *testing.T) {
	if _, page := serve(t, config.UIConfig{}, "/"); page != testIndex {
		t.Fatalf("page changed without ui configuration:\n%s", page)
	}
	if code, _ := serve(t, config.UIConfig{}, brandingLogoPath); code != 404 {
		t.Fatalf("logo without ui.logo: %d, want 404", code)
	}
}

func TestUIBrandedPage(t *testing.T) {
	logo := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(logo, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui := config.UIConfig{
		Name:   "GMT SciTQ",
		Logo:   logo,
		Colors: config.UIColors{Primary: "#0b5d3b", PrimaryDark: "#2f8f66", Accent: "rgb(200, 80, 0)"},
	}
	_, page := serve(t, ui, "/")
	for _, want := range []string{
		"<title>GMT SciTQ</title>",
		`<link rel="icon" href="/branding/logo" />`,
		`window.SCITQ_BRANDING={"logo":"/branding/logo","name":"GMT SciTQ"}`,
		":root{--brand-color:#0b5d3b;--primary-color:#0b5d3b;",
		"--link-color:rgb(200, 80, 0);",
		`[data-theme="dark"]{--primary-color:#2f8f66;`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("branded page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "logo-abc.png") {
		t.Error("the default favicon is still referenced")
	}
	if code, body := serve(t, ui, brandingLogoPath); code != 200 || body != "png" {
		t.Errorf("logo: %d %q", code, body)
	}
}

func TestUIBrandNameIsEscaped(t *testing.T) {
	_, page := serve(t, config.UIConfig{Name: `</title><script>x()</script>`}, "/")
	if strings.Contains(page, "<script>x()") {
		t.Fatalf("name not escaped:\n%s", page)
	}
}
