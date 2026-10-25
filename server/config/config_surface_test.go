package config

import (
	"flag"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scitq/scitq/internal/compat"
)

var updateSurface = flag.Bool("update-surface", false, "rewrite compat/config.txt from the current Config")

// configSurface lists every scitq.yaml key, one per line:
//
//	config <dotted.path>:<type>
//
// Map entries appear as `*` (e.g. providers.azure.*.username). See
// docs/reference/compatibility.md.
func configSurface() []string {
	var lines []string
	var walk func(t reflect.Type, path string)
	walk = func(t reflect.Type, path string) {
		for t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				name := strings.Split(f.Tag.Get("yaml"), ",")[0]
				if name == "-" || !f.IsExported() {
					continue
				}
				if name == "" {
					name = strings.ToLower(f.Name)
				}
				p := name
				if path != "" {
					p = path + "." + name
				}
				walk(f.Type, p)
			}
			return
		case reflect.Map:
			lines = append(lines, "config "+path+":map")
			walk(t.Elem(), path+".*")
			return
		case reflect.Slice:
			lines = append(lines, "config "+path+":list")
			if e := t.Elem(); e.Kind() == reflect.Struct || (e.Kind() == reflect.Ptr && e.Elem().Kind() == reflect.Struct) {
				walk(e, path+"[]")
			}
			return
		}
		lines = append(lines, "config "+path+":"+t.Kind().String())
	}
	walk(reflect.TypeOf(Config{}), "")
	sort.Strings(lines)
	return lines
}

// TestConfigSurfaceRecorded requires compat/config.txt to match the Config
// struct exactly, so every scitq.yaml key change shows up as a reviewed diff.
// Regenerate with `go test ./server/config -run TestConfigSurfaceRecorded -update-surface`.
func TestConfigSurfaceRecorded(t *testing.T) {
	compat.CheckRecorded(t, "../../compat/config.txt", configSurface(), *updateSurface)
}
