package cli

import (
	"flag"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/scitq/scitq/internal/compat"
)

var updateSurface = flag.Bool("update-surface", false, "rewrite compat/cli.txt from the current CLI")

// cliSurface lists every command and flag of the CLI, one per line:
//
//	cli <command path> <flag>:<type>
//	cli <command path> <flag> optional
//
// The second form is present only for optional flags, so making an optional
// flag required removes a line. See docs/reference/compatibility.md.
func cliSurface() []string {
	var lines []string
	var walk func(t reflect.Type, path []string)
	walk = func(t reflect.Type, path []string) {
		for t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag, ok := f.Tag.Lookup("arg")
			if !ok {
				continue
			}
			parts := strings.Split(tag, ",")
			if strings.HasPrefix(parts[0], "subcommand:") {
				cmd := strings.TrimPrefix(parts[0], "subcommand:")
				sub := append(append([]string{}, path...), cmd)
				lines = append(lines, "cli "+strings.Join(sub, " "))
				walk(f.Type, sub)
				continue
			}
			name, required := "", false
			for _, p := range parts {
				switch {
				case strings.HasPrefix(p, "--"):
					name = p
				case p == "positional":
					name = "<" + kebab(f.Name) + ">"
				case p == "required":
					required = true
				}
			}
			if name == "" {
				name = "--" + kebab(f.Name)
			}
			prefix := "cli " + strings.Join(append(append([]string{}, path...), name), " ")
			lines = append(lines, prefix+":"+flagType(f.Type))
			if !required {
				lines = append(lines, prefix+" optional")
			}
		}
	}
	walk(reflect.TypeOf(Attr{}), nil)
	sort.Strings(lines)
	return lines
}

func flagType(t reflect.Type) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice:
		return "list"
	default:
		return t.Kind().String()
	}
}

func kebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// TestCLISurfaceRecorded requires compat/cli.txt to match the CLI exactly, so
// every command or flag change shows up as a reviewed diff. Regenerate with
// `go test ./cli -run TestCLISurfaceRecorded -update-surface`.
func TestCLISurfaceRecorded(t *testing.T) {
	compat.CheckRecorded(t, "../compat/cli.txt", cliSurface(), *updateSurface)
}
