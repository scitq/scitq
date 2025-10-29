package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"reflect"
	"strings"
)

func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", typeString(t.X), t.Sel.Name)
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", typeString(t.Key), typeString(t.Value))
	case *ast.StructType:
		return "struct"
	default:
		return fmt.Sprintf("%T", expr)
	}
}

func extractComment(field *ast.Field) string {
	var lines []string
	if field.Doc != nil {
		for _, c := range field.Doc.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			text = strings.TrimSpace(strings.TrimPrefix(text, "/*"))
			text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
			lines = append(lines, text)
		}
	} else if field.Comment != nil {
		for _, c := range field.Comment.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			text = strings.TrimSpace(strings.TrimPrefix(text, "/*"))
			text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, " ")
}

func walkStruct(section string, st *ast.StructType, depth string, printedSections map[string]bool) {
	if printedSections[section] {
		// Already printed header for this section
	} else {
		// print section header line (no field name, no type)
		fmt.Printf("| %s |  | `%s` |  |  |  |\n", section, depth)
		printedSections[section] = true
	}

	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		tag := reflect.StructTag(field.Tag.Value[1 : len(field.Tag.Value)-1])
		yamlTag := tag.Get("yaml")
		def := tag.Get("default")
		ftype := typeString(field.Type)

		key := yamlTag
		if key == "" && len(field.Names) > 0 {
			key = strings.ToLower(field.Names[0].Name)
		}
		full := key
		if depth != "" {
			full = depth + "." + key
		}

		desc := extractComment(field)

		// indent all fields under that section (no section name repetition)
		fmt.Printf("|  | %s | `%s` | `%s` | `%s` | %s |\n",
			field.Names[0].Name, full, def, ftype, desc)

		// Recurse into nested structs
		switch ft := field.Type.(type) {
		case *ast.Ident:
			if ts, ok := typeSpecs[ft.Name]; ok {
				if inner, ok := ts.Type.(*ast.StructType); ok {
					walkStruct(field.Names[0].Name, inner, full, printedSections)
				}
			}
		case *ast.StructType:
			walkStruct(field.Names[0].Name, ft, full, printedSections)
		}
	}
}

var typeSpecs = map[string]*ast.TypeSpec{}

func main() {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "server/config/config.go", nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}

	// Collect all type specs first
	for _, decl := range f.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					typeSpecs[ts.Name.Name] = ts
				}
			}
		}
	}

	fmt.Println("# Configuration reference\n")
	fmt.Println("| Field | YAML key | Default | Type | Description |")
	fmt.Println("|-------|-----------|----------|------|-------------|")

	printedSections := make(map[string]bool)

	// Find and expand Config struct
	if ts, ok := typeSpecs["Config"]; ok {
		if st, ok := ts.Type.(*ast.StructType); ok {
			// Don’t print Config itself — go directly into its fields
			for _, field := range st.Fields.List {
				if field.Type == nil || field.Tag == nil {
					continue
				}
				tag := reflect.StructTag(field.Tag.Value[1 : len(field.Tag.Value)-1])
				yamlTag := tag.Get("yaml")
				if yamlTag == "" && len(field.Names) > 0 {
					yamlTag = strings.ToLower(field.Names[0].Name)
				}

				desc := extractComment(field)

				// Print section heading only once per section
				section := field.Names[0].Name
				if !printedSections[section] {
					fmt.Printf("| %s |  | `%s` |  |  | %s |\n", section, yamlTag, desc)
					printedSections[section] = true
				}

				// Dive into the struct
				switch ft := field.Type.(type) {
				case *ast.Ident:
					if ts2, ok := typeSpecs[ft.Name]; ok {
						if inner, ok := ts2.Type.(*ast.StructType); ok {
							walkStruct(field.Names[0].Name, inner, yamlTag, printedSections)
						}
					}
				case *ast.StructType:
					walkStruct(field.Names[0].Name, ft, yamlTag, printedSections)
				}
			}
		}
	}
}
