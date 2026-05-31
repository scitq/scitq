package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

// Workflow chain CLI subcommands — see specs/workflow_chain.md.

// chainEntrySummary formats one entry as a single line for list/show output.
func chainEntrySummary(e *pb.ChainEntry) string {
	tmpl := e.TemplateName
	if e.TemplateVersion != nil && *e.TemplateVersion != "" {
		tmpl += "@" + *e.TemplateVersion
	}
	child := "-"
	if e.ChildWorkflowId != nil {
		child = fmt.Sprintf("%d", *e.ChildWorkflowId)
	}
	when := e.WhenExpr
	if when == "" || when == "true" {
		when = "-"
	}
	return fmt.Sprintf("entry=%d parent=%d idx=%d %-9s on=%-9s when=%s template=%s child=%s",
		e.ChainEntryId, e.ParentWorkflowId, e.Idx, e.Status, e.OnStatus, when, tmpl, child)
}

// chainEntryDetail prints every field of one entry. Used by `show` and as a
// trailing block after lifecycle mutations so the user sees the new state.
func (c *CLI) chainEntryDetail(e *pb.ChainEntry) {
	if c.jsonOut(e) {
		return
	}
	fmt.Printf("🔗 Chain entry %d\n", e.ChainEntryId)
	fmt.Printf("    parent_workflow_id: %d\n", e.ParentWorkflowId)
	fmt.Printf("    idx:                %d\n", e.Idx)
	fmt.Printf("    status:             %s\n", e.Status)
	fmt.Printf("    on_status:          %s\n", e.OnStatus)
	if e.WhenExpr != "" && e.WhenExpr != "true" {
		fmt.Printf("    when:               %s\n", e.WhenExpr)
	}
	fmt.Printf("    template:           %s", e.TemplateName)
	if e.TemplateVersion != nil && *e.TemplateVersion != "" {
		fmt.Printf("@%s", *e.TemplateVersion)
	}
	fmt.Println()
	fmt.Printf("    always_new:         %v\n", e.AlwaysNew)
	if e.ChildWorkflowId != nil {
		fmt.Printf("    child_workflow_id:  %d\n", *e.ChildWorkflowId)
	}
	if e.ErrorMessage != nil && *e.ErrorMessage != "" {
		fmt.Printf("    error_message:      %s\n", *e.ErrorMessage)
	}
	fmt.Printf("    created_at:         %s\n", e.CreatedAt)
	if e.FiredAt != nil && *e.FiredAt != "" {
		fmt.Printf("    fired_at:           %s\n", *e.FiredAt)
	}
	if e.LastFiredAt != nil && *e.LastFiredAt != "" {
		fmt.Printf("    last_fired_at:      %s\n", *e.LastFiredAt)
	}
	fmt.Printf("    params_template:    %s\n", e.ParamsTemplateJson)
}

func (c *CLI) WorkflowChainList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.ListChainEntriesRequest{}
	if c.Attr.Workflow.Chain.List.WorkflowId != nil {
		req.ParentWorkflowId = c.Attr.Workflow.Chain.List.WorkflowId
	}
	if c.Attr.Workflow.Chain.List.Status != "" {
		s := c.Attr.Workflow.Chain.List.Status
		req.StatusFilter = &s
	}
	res, err := c.QC.Client.ListChainEntries(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to list chain entries: %w", err)
	}
	if c.jsonOut(res.Entries) {
		return nil
	}
	if len(res.Entries) == 0 {
		fmt.Println("No chain entries match.")
		return nil
	}
	fmt.Printf("🔗 Chain entries (%d):\n", len(res.Entries))
	for _, e := range res.Entries {
		fmt.Println("  " + chainEntrySummary(e))
	}
	return nil
}

func (c *CLI) WorkflowChainShow() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()
	id := c.Attr.Workflow.Chain.Show.EntryId
	e, err := c.QC.Client.GetChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: id})
	if err != nil {
		return fmt.Errorf("failed to get chain entry: %w", err)
	}
	c.chainEntryDetail(e)
	return nil
}

// WorkflowChainLifecycle is shared by suspend / resume / cancel: each is a
// single-id RPC that returns the new entry state. Keeping one handler keeps
// the three subcommand branches honest about being only-an-ID operations.
func (c *CLI) WorkflowChainLifecycle(action string) error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	var id int32
	switch action {
	case "suspend":
		id = c.Attr.Workflow.Chain.Suspend.EntryId
	case "resume":
		id = c.Attr.Workflow.Chain.Resume.EntryId
	case "cancel":
		id = c.Attr.Workflow.Chain.Cancel.EntryId
	default:
		return fmt.Errorf("unknown chain lifecycle action: %s", action)
	}
	req := &pb.ChainEntryId{ChainEntryId: id}
	var (
		entry *pb.ChainEntry
		err   error
	)
	switch action {
	case "suspend":
		entry, err = c.QC.Client.SuspendChainEntry(ctx, req)
	case "resume":
		entry, err = c.QC.Client.ResumeChainEntry(ctx, req)
	case "cancel":
		entry, err = c.QC.Client.CancelChainEntry(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("chain %s: %w", action, err)
	}
	fmt.Printf("✅ chain entry %d → %s\n", entry.ChainEntryId, entry.Status)
	c.chainEntryDetail(entry)
	return nil
}

func (c *CLI) WorkflowChainEdit() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()
	args := c.Attr.Workflow.Chain.Edit
	req := &pb.EditChainEntryRequest{ChainEntryId: args.EntryId}
	if args.Template != nil {
		req.TemplateName = args.Template
	}
	if args.Version != nil {
		req.TemplateVersion = args.Version // empty string clears
	}
	if args.When != nil {
		req.WhenExpr = args.When
	}
	if args.On != nil {
		req.OnStatus = args.On
	}
	if args.AlwaysNew != nil {
		req.AlwaysNew = args.AlwaysNew
	}

	// Params handling. Three mutually-exclusive surfaces:
	//   (a) --params-json '{...}' replaces wholesale.
	//   (b) --param key=value (repeatable) and/or --param-clear key
	//       (repeatable) merge into the current entry's params.
	// (a) and (b) cannot both be specified.
	if args.ParamsJson != nil {
		if len(args.Param) > 0 || len(args.ParamClear) > 0 {
			return fmt.Errorf("--params-json is mutually exclusive with --param / --param-clear")
		}
		req.ParamsTemplateJson = args.ParamsJson
	} else if len(args.Param) > 0 || len(args.ParamClear) > 0 {
		// Fetch current params to merge.
		current, err := c.QC.Client.GetChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: args.EntryId})
		if err != nil {
			return fmt.Errorf("fetch entry for merge: %w", err)
		}
		merged := map[string]any{}
		if current.ParamsTemplateJson != "" {
			if err := json.Unmarshal([]byte(current.ParamsTemplateJson), &merged); err != nil {
				return fmt.Errorf("entry's current params is not a JSON object: %w", err)
			}
		}
		for _, kv := range args.Param {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid --param %q (expected key=value)", kv)
			}
			merged[parts[0]] = parts[1]
		}
		for _, k := range args.ParamClear {
			delete(merged, k)
		}
		// Marshal with sorted keys for stable output (and so canonical-JSON
		// continue_last reconcile gets a deterministic input).
		keys := make([]string, 0, len(merged))
		for k := range merged {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]any, len(merged))
		for _, k := range keys {
			ordered[k] = merged[k]
		}
		buf, err := json.Marshal(ordered)
		if err != nil {
			return fmt.Errorf("marshal merged params: %w", err)
		}
		s := string(buf)
		req.ParamsTemplateJson = &s
	}

	entry, err := c.QC.Client.EditChainEntry(ctx, req)
	if err != nil {
		return fmt.Errorf("edit chain entry: %w", err)
	}
	fmt.Printf("✅ chain entry %d edited\n", entry.ChainEntryId)
	c.chainEntryDetail(entry)
	return nil
}
