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
	for _, path := range []string{brandingLogoPath, brandingLogoDarkPath} {
		if code, _ := serve(t, config.UIConfig{}, path); code != 404 {
			t.Fatalf("%s without a configured logo: %d, want 404", path, code)
		}
	}
}

func TestUIBrandedPage(t *testing.T) {
	dir := t.TempDir()
	logo, logoDark := filepath.Join(dir, "logo.png"), filepath.Join(dir, "logo-dark.png")
	for file, content := range map[string]string{logo: "png", logoDark: "dark png"} {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ui := config.UIConfig{
		Name:     "GMT SciTQ",
		Logo:     logo,
		LogoDark: logoDark,
		Colors:   config.UIColors{Primary: "#0b5d3b", PrimaryDark: "#2f8f66", Accent: "rgb(200, 80, 0)"},
	}
	_, page := serve(t, ui, "/")
	for _, want := range []string{
		"<title>GMT SciTQ</title>",
		`<link rel="icon" href="/branding/logo" /><link rel="icon" href="/branding/logo-dark" media="(prefers-color-scheme: dark)" />`,
		`window.SCITQ_BRANDING={"logo":"/branding/logo","logoDark":"/branding/logo-dark","name":"GMT SciTQ"}`,
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
	if code, body := serve(t, ui, brandingLogoDarkPath); code != 200 || body != "dark png" {
		t.Errorf("dark logo: %d %q", code, body)
	}
}

func TestUILightSidebar(t *testing.T) {
	ui := config.UIConfig{Colors: config.UIColors{Sidebar: "#f4f5fb", SidebarDark: "#1f1e64"}}
	_, page := serve(t, ui, "/")
	for _, want := range []string{
		`:root:not([data-theme="dark"]){--sidebar-bg:#f4f5fb;--sidebar-heading:var(--brand-color);`,
		`[data-theme="dark"]{--sidebar-bg:#1f1e64;}`,
		`"lightSidebar":["light"]`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
}

func TestIsLight(t *testing.T) {
	for color, light := range map[string]bool{"#fff": true, "#f4f5fb": true, "#44c2d6": true, "#192245": false, "#1f1e64": false, "#666": false} {
		if isLight(color) != light {
			t.Errorf("isLight(%s) = %v", color, !light)
		}
	}
}

func TestUIBrandNameIsEscaped(t *testing.T) {
	_, page := serve(t, config.UIConfig{Name: `</title><script>x()</script>`}, "/")
	if strings.Contains(page, "<script>x()") {
		t.Fatalf("name not escaped:\n%s", page)
	}
}
