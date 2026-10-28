package server

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/scitq/scitq/server/config"
)

// brandingLogoPath serves the configured logo (ui.logo).
const brandingLogoPath = "/branding/logo"

var (
	titleTag   = regexp.MustCompile(`<title>[^<]*</title>`)
	faviconTag = regexp.MustCompile(`<link rel="icon"[^>]*>`)
)

// brandIndex writes the ui branding into the UI's index.html: the tab
// title and icon, the brand colors as CSS variables, and the name and logo
// URL the UI reads at startup (window.SCITQ_BRANDING). The page is branded
// from its first render, without a flash of the default look.
func brandIndex(index string, ui config.UIConfig) string {
	var head strings.Builder
	brand := map[string]string{}
	if ui.Name != "" {
		index = titleTag.ReplaceAllLiteralString(index, "<title>"+html.EscapeString(ui.Name)+"</title>")
		brand["name"] = ui.Name
	}
	if ui.Logo != "" {
		index = faviconTag.ReplaceAllLiteralString(index, `<link rel="icon" href="`+brandingLogoPath+`" />`)
		brand["logo"] = brandingLogoPath
	}
	if css := brandColors(ui.Colors); css != "" {
		head.WriteString("<style>" + css + "</style>")
	}
	if len(brand) > 0 {
		// json.Marshal escapes <, > and &, so the values cannot close the script.
		b, _ := json.Marshal(brand)
		head.WriteString("<script>window.SCITQ_BRANDING=" + string(b) + "</script>")
	}
	if head.Len() == 0 {
		return index
	}
	return strings.Replace(index, "</head>", head.String()+"</head>", 1)
}

// brandColors overrides the theme variables of ui/src/app.css. Colors are
// validated as CSS colors when the configuration loads.
func brandColors(c config.UIColors) string {
	var css strings.Builder
	block := func(selector, primary, accent, shade, extra string) {
		if primary == "" && accent == "" {
			return
		}
		css.WriteString(selector + "{" + extra)
		if primary != "" {
			fmt.Fprintf(&css, "--primary-color:%s;--primary-hover:color-mix(in srgb,%s 85%%,%s);", primary, primary, shade)
		}
		if accent != "" {
			fmt.Fprintf(&css, "--link-color:%s;--link-hover:color-mix(in srgb,%s 80%%,%s);", accent, accent, shade)
		}
		css.WriteString("}")
	}
	// The sidebar and login page (--brand-color) keep one color in both themes.
	var brand string
	if c.Primary != "" {
		brand = "--brand-color:" + c.Primary + ";"
	}
	block(":root", c.Primary, c.Accent, "black", brand)
	block(`[data-theme="dark"]`, c.PrimaryDark, c.AccentDark, "white", "")
	return css.String()
}

// uiHandler serves the embedded UI, with index.html branded, and the
// configured logo.
func uiHandler(public fs.FS, ui config.UIConfig) (http.Handler, error) {
	raw, err := fs.ReadFile(public, "index.html")
	if err != nil {
		return nil, fmt.Errorf("embedded UI has no index.html: %w", err)
	}
	index := []byte(brandIndex(string(raw), ui))
	files := http.FileServer(http.FS(public))

	mux := http.NewServeMux()
	mux.HandleFunc(brandingLogoPath, func(w http.ResponseWriter, r *http.Request) {
		if ui.Logo == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, ui.Logo)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(index)
			return
		}
		files.ServeHTTP(w, r)
	})
	return mux, nil
}
