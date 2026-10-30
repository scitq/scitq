package server

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/scitq/scitq/server/config"
)

// brandingLogoPath and brandingLogoDarkPath serve ui.logo and ui.logo_dark.
const (
	brandingLogoPath     = "/branding/logo"
	brandingLogoDarkPath = "/branding/logo-dark"
)

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
	brand := map[string]any{}
	if ui.Name != "" {
		index = titleTag.ReplaceAllLiteralString(index, "<title>"+html.EscapeString(ui.Name)+"</title>")
		brand["name"] = ui.Name
	}
	if ui.Logo != "" {
		icons := `<link rel="icon" href="` + brandingLogoPath + `" />`
		brand["logo"] = brandingLogoPath
		if ui.LogoDark != "" {
			// The tab icon follows the system theme: the page's own theme
			// is not known before the UI runs.
			icons += `<link rel="icon" href="` + brandingLogoDarkPath + `" media="(prefers-color-scheme: dark)" />`
			brand["logoDark"] = brandingLogoDarkPath
		}
		index = faviconTag.ReplaceAllLiteralString(index, icons)
	}
	var lightSidebar []string
	for theme, color := range map[string]string{"light": ui.Colors.Sidebar, "dark": ui.Colors.SidebarDark} {
		if color != "" && isLight(color) {
			lightSidebar = append(lightSidebar, theme)
		}
	}
	if lightSidebar != nil {
		sort.Strings(lightSidebar)
		brand["lightSidebar"] = lightSidebar
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
	sidebar := func(selector, color string) {
		if color == "" {
			return
		}
		css.WriteString(selector + "{--sidebar-bg:" + color + ";")
		if isLight(color) {
			css.WriteString(lightSidebarVars)
		}
		css.WriteString("}")
	}
	// The light theme's selector leaves the dark theme on the default dark
	// sidebar when only ui.colors.sidebar is set.
	sidebar(`:root:not([data-theme="dark"])`, c.Sidebar)
	sidebar(`[data-theme="dark"]`, c.SidebarDark)
	return css.String()
}

// lightSidebarVars restyles the sidebar's text for a light background
// (ui/src/app.css has the dark-background defaults).
const lightSidebarVars = "--sidebar-heading:var(--brand-color);--sidebar-link:var(--brand-color);" +
	"--sidebar-sub:var(--brand-color);--sidebar-icon:color-mix(in srgb,var(--brand-color) 65%,white);" +
	"--sidebar-subitem:color-mix(in srgb,var(--brand-color) 75%,white);" +
	"--sidebar-active:var(--link-color);--sidebar-active-sub:var(--link-color);--sidebar-active-item:var(--link-color);" +
	"--sidebar-hover-bg:rgba(0,0,0,0.05);--sidebar-logout:#dc2626;--sidebar-logout-hover:#b91c1c;" +
	"--sidebar-logout-hover-bg:rgba(0,0,0,0.06);"

// isLight tells whether dark text reads better than white text on a #hex
// color (validated at config load): its relative luminance is above the
// point where both contrasts are equal.
func isLight(hex string) bool {
	h := strings.TrimPrefix(hex, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	var lum float64
	for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
		v, _ := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		lum += weight * c
	}
	return lum > 0.179
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
	for path, file := range map[string]string{brandingLogoPath: ui.Logo, brandingLogoDarkPath: ui.LogoDark} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if file == "" {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, file)
		})
	}
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
