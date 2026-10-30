package config

import "testing"

func TestUIColorsAreValidated(t *testing.T) {
	for _, ok := range []string{"#0b5d3b", "#fff", "teal", "rgb(10, 20, 30)", "hsl(120 50% 40%)"} {
		if err := (UIConfig{Colors: UIColors{Primary: ok}}).validate(); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"red;}body{display:none", "#0b5d3b</style>", "url(x)"} {
		if err := (UIConfig{Colors: UIColors{Accent: bad}}).validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := (UIConfig{Logo: "/nonexistent/logo.png"}).validate(); err == nil {
		t.Error("a missing logo file is accepted")
	}
	if err := (UIConfig{Colors: UIColors{Sidebar: "#f4f5fb", SidebarDark: "#123"}}).validate(); err != nil {
		t.Errorf("hex sidebar colors refused: %v", err)
	}
	if err := (UIConfig{Colors: UIColors{Sidebar: "white"}}).validate(); err == nil {
		t.Error("a sidebar color that is not #hex is accepted")
	}
	logo := t.TempDir()
	if err := (UIConfig{Logo: logo, LogoDark: "/nonexistent/logo.png"}).validate(); err == nil {
		t.Error("a missing dark logo file is accepted")
	}
	if err := (UIConfig{LogoDark: logo}).validate(); err == nil {
		t.Error("ui.logo_dark is accepted without ui.logo")
	}
}
