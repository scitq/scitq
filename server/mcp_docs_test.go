package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func getDocText(t *testing.T, page string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"page": page})
	res, rpcErr := (&mcpHandler{}).toolGetDoc(context.Background(), args)
	if rpcErr != nil {
		t.Fatalf("get_doc %q: %v", page, rpcErr.Message)
	}
	return res.(*toolResult).Content[0].Text
}

// Every page get_doc advertises is served — including those the MCP
// instructions point agents to (optimization, recruitment).
func TestGetDocServesEveryAdvertisedPage(t *testing.T) {
	pages := docPages()
	for _, want := range []string{"optimization", "recruitment", "compatibility", "cli"} {
		found := false
		for _, p := range pages {
			found = found || p == want
		}
		if !found {
			t.Fatalf("page %q not embedded (run make copy-docs); have %v", want, pages)
		}
	}
	for _, p := range pages {
		if strings.HasPrefix(getDocText(t, p), "Unknown page") {
			t.Errorf("advertised page %q is refused", p)
		}
	}
}

func TestGetDocRefusesUnknownPages(t *testing.T) {
	for _, p := range []string{"nope", "../migrations/000001_init_schema.up", "docs/cli"} {
		if !strings.HasPrefix(getDocText(t, p), "Unknown page") {
			t.Errorf("page %q should be refused", p)
		}
	}
}
