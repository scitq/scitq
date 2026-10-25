package server

import (
	"flag"
	"sort"
	"testing"

	"github.com/scitq/scitq/internal/compat"
)

var updateSurface = flag.Bool("update-surface", false, "rewrite compat/mcp.txt from the current MCP tools")

// mcpSurface lists every MCP tool and parameter, one per line:
//
//	mcp <tool>
//	mcp <tool> <param>:<type>
//	mcp <tool> <param> optional
//
// The last form is present only for optional parameters, so making an
// optional parameter required removes a line. See
// docs/reference/compatibility.md.
func mcpSurface() []string {
	var lines []string
	for _, t := range (&mcpHandler{}).listTools() {
		lines = append(lines, "mcp "+t.Name)
		required := map[string]bool{}
		for _, r := range t.InputSchema.Required {
			required[r] = true
		}
		for name, p := range t.InputSchema.Properties {
			lines = append(lines, "mcp "+t.Name+" "+name+":"+p.Type)
			if !required[name] {
				lines = append(lines, "mcp "+t.Name+" "+name+" optional")
			}
		}
	}
	sort.Strings(lines)
	return lines
}

// TestMCPSurfaceRecorded requires compat/mcp.txt to match the MCP tools
// exactly, so every tool or parameter change shows up as a reviewed diff.
// Regenerate with `go test ./server -run TestMCPSurfaceRecorded -update-surface`.
func TestMCPSurfaceRecorded(t *testing.T) {
	compat.CheckRecorded(t, "../compat/mcp.txt", mcpSurface(), *updateSurface)
}
