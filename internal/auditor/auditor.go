package auditor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/gtm"
)

// LoadSnapshot collects a complete entity snapshot from either a GTM workspace or the live version.
func LoadSnapshot(ctx context.Context, client *gtm.Client, accountID, containerID, workspaceID string, live bool) (*Snapshot, error) {
	if client == nil {
		return nil, errors.New("gtm client is required")
	}
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(containerID) == "" {
		return nil, fmt.Errorf("%w: accountId and containerId are required", gtm.ErrValidation)
	}
	if !live && strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("%w: workspaceId is required unless live=true", gtm.ErrValidation)
	}

	snap := &Snapshot{
		AccountID:   accountID,
		ContainerID: containerID,
		WorkspaceID: workspaceID,
	}

	// Best-effort container metadata (workspaces & version headers) for governance checks.
	if ws, err := client.ListWorkspaces(ctx, accountID, containerID, gtm.ListOptions{Fresh: true}); err == nil {
		snap.Workspaces = ws
	}
	if vh, err := client.ListVersionHeaders(ctx, accountID, containerID, false, gtm.ListOptions{Fresh: true}); err == nil {
		snap.VersionHeaders = vh
	}

	if live {
		ver, err := client.GetLiveVersion(ctx, accountID, containerID)
		if err != nil {
			return nil, err
		}
		snap.LiveVersionID = ver.ContainerVersionId
		snap.TargetLabel = fmt.Sprintf("live version %s (%s)", ver.ContainerVersionId, firstNonEmpty(ver.Name, "unnamed"))
		snap.Tags = ver.Tag
		snap.Triggers = ver.Trigger
		snap.Variables = ver.Variable
		snap.BuiltInVariables = ver.BuiltInVariable
		snap.Folders = ver.Folder
		snap.Templates = ver.CustomTemplate
		snap.Clients = ver.Client
		snap.Transformations = ver.Transformation
		return snap, nil
	}

	snap.TargetLabel = fmt.Sprintf("workspace %s", workspaceID)
	var err error
	if snap.Tags, err = client.ListTags(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}
	if snap.Triggers, err = client.ListTriggers(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}
	if snap.Variables, err = client.ListVariables(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}
	if snap.BuiltInVariables, err = client.ListBuiltInVariables(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}
	if snap.Folders, err = client.ListFolders(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}
	if snap.Templates, err = client.ListTemplates(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}
	if snap.Clients, err = client.ListClients(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}
	if snap.Transformations, err = client.ListTransformations(ctx, accountID, containerID, workspaceID, gtm.ListOptions{Fresh: true}); err != nil {
		return nil, err
	}

	return snap, nil
}

// Run loads the target container snapshot, runs all 15 deterministic rules, and returns a Report.
func Run(ctx context.Context, client *gtm.Client, input Input) (*Report, error) {
	minSev, err := ParseSeverity(string(input.MinSeverity))
	if err != nil {
		return nil, err
	}
	input.MinSeverity = minSev

	snap, err := LoadSnapshot(ctx, client, input.AccountID, input.ContainerID, input.WorkspaceID, input.Live)
	if err != nil {
		return nil, err
	}
	return EvaluateSnapshot(input, snap), nil
}

// EvaluateSnapshot runs all audit rules on an in-memory Snapshot and builds a scored Report.
func EvaluateSnapshot(input Input, snap *Snapshot) *Report {
	allFindings := Evaluate(snap)

	bySeverity := map[Severity]int{
		SeverityCritical: 0,
		SeverityHigh:     0,
		SeverityMedium:   0,
		SeverityLow:      0,
	}
	for _, f := range allFindings {
		bySeverity[f.Severity]++
	}

	score := 100 -
		20*bySeverity[SeverityCritical] -
		10*bySeverity[SeverityHigh] -
		4*bySeverity[SeverityMedium] -
		1*bySeverity[SeverityLow]
	if score < 0 {
		score = 0
	}

	minRank := SeverityRank(input.MinSeverity)
	if minRank == 0 {
		minRank = SeverityRank(SeverityLow)
	}
	filtered := make([]Finding, 0, len(allFindings))
	for _, f := range allFindings {
		if SeverityRank(f.Severity) >= minRank {
			filtered = append(filtered, f)
		}
	}

	return &Report{
		AccountID:     snap.AccountID,
		ContainerID:   snap.ContainerID,
		WorkspaceID:   snap.WorkspaceID,
		LiveVersionID: snap.LiveVersionID,
		Target:        snap.TargetLabel,
		Summary: Summary{
			Score:         score,
			Grade:         gradeForScore(score),
			TotalFindings: len(allFindings),
			BySeverity:    bySeverity,
			Counts: map[string]int{
				"tags":       len(snap.Tags),
				"triggers":   len(snap.Triggers),
				"variables":  len(snap.Variables),
				"folders":    len(snap.Folders),
				"templates":  len(snap.Templates),
				"workspaces": len(snap.Workspaces),
			},
		},
		Findings: filtered,
	}
}

// HasFindingsAtOrAbove returns true when at least one finding meets or exceeds threshold.
func HasFindingsAtOrAbove(findings []Finding, threshold Severity) bool {
	minRank := SeverityRank(threshold)
	for _, f := range findings {
		if SeverityRank(f.Severity) >= minRank {
			return true
		}
	}
	return false
}

// RenderMarkdown renders the audit report as structured Markdown.
func (r *Report) RenderMarkdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# GTM Container Audit Report\n\n")
	fmt.Fprintf(&sb, "- **Account / Container:** `%s` / `%s`\n", r.AccountID, r.ContainerID)
	fmt.Fprintf(&sb, "- **Target:** %s\n", r.Target)
	fmt.Fprintf(&sb, "- **Health Score:** **%d / 100 (Grade %s)**\n\n", r.Summary.Score, r.Summary.Grade)

	fmt.Fprintf(&sb, "## Summary\n\n")
	fmt.Fprintf(&sb, "| Severity | Count |\n|---|---|\n")
	for _, sev := range []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow} {
		fmt.Fprintf(&sb, "| %s | %d |\n", sev, r.Summary.BySeverity[sev])
	}
	fmt.Fprintf(&sb, "\n")

	if len(r.Findings) == 0 {
		fmt.Fprintf(&sb, "No findings at or above the selected severity threshold.\n")
		return sb.String()
	}

	fmt.Fprintf(&sb, "## Findings (%d)\n\n", len(r.Findings))
	for _, f := range r.Findings {
		idSuffix := ""
		if f.EntityID != "" {
			idSuffix = fmt.Sprintf(" (ID `%s`)", f.EntityID)
		}
		fmt.Fprintf(&sb, "### [%s] %s — `%s`\n\n", strings.ToUpper(string(f.Severity)), f.Title, f.RuleID)
		fmt.Fprintf(&sb, "- **Category:** %s\n", f.Category)
		fmt.Fprintf(&sb, "- **Entity:** %s `%s`%s\n", f.EntityKind, f.EntityName, idSuffix)
		fmt.Fprintf(&sb, "- **Detail:** %s\n", f.Message)
		fmt.Fprintf(&sb, "- **Remediation:** %s\n\n", f.Remediation)
	}
	return sb.String()
}

func gradeForScore(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 75:
		return "B"
	case score >= 60:
		return "C"
	case score >= 40:
		return "D"
	default:
		return "F"
	}
}
