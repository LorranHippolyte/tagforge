package safety

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		raw     string
		want    Mode
		wantErr bool
	}{
		{raw: "", want: ModeWorkspaceOnly},
		{raw: "  ", want: ModeWorkspaceOnly},
		{raw: "read-only", want: ModeReadOnly},
		{raw: "READ-ONLY", want: ModeReadOnly},
		{raw: "workspace-only", want: ModeWorkspaceOnly},
		{raw: "full", want: ModeFull},
		{raw: "invalid", wantErr: true},
	}

	for _, tc := range tests {
		got, err := ParseMode(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseMode(%q) expected error, got %q", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseMode(%q) unexpected error: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("ParseMode(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestPolicyCheck(t *testing.T) {
	tests := []struct {
		name        string
		policy      Policy
		req         Request
		wantAllowed bool
		wantHintSub string
	}{
		{
			name:        "read-only allows reads",
			policy:      Policy{Mode: ModeReadOnly},
			req:         Request{Resource: ResourceTag, Action: ActionRead},
			wantAllowed: true,
		},
		{
			name:        "read-only allows preview",
			policy:      Policy{Mode: ModeReadOnly},
			req:         Request{Resource: ResourceWorkspace, Action: ActionPreview},
			wantAllowed: true,
		},
		{
			name:        "read-only blocks tag create",
			policy:      Policy{Mode: ModeReadOnly},
			req:         Request{Resource: ResourceTag, Action: ActionCreate},
			wantAllowed: false,
			wantHintSub: "workspace-only",
		},
		{
			name:        "read-only blocks delete even with confirm",
			policy:      Policy{Mode: ModeReadOnly},
			req:         Request{Resource: ResourceTag, Action: ActionDelete, Confirm: true},
			wantAllowed: false,
			wantHintSub: "read-only",
		},
		{
			name:        "workspace-only allows tag create",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceTag, Action: ActionCreate},
			wantAllowed: true,
		},
		{
			name:        "workspace-only allows bulkUpdate and createVersion",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceVersion, Action: ActionCreateVersion},
			wantAllowed: true,
		},
		{
			name:        "workspace-only allows workspace bulkUpdate",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceWorkspace, Action: ActionBulkUpdate},
			wantAllowed: true,
		},
		{
			name:        "workspace-only requires confirm to delete tag",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceTag, Action: ActionDelete, Confirm: false},
			wantAllowed: false,
			wantHintSub: "--confirm",
		},
		{
			name:        "workspace-only allows tag delete with confirm",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceTag, Action: ActionDelete, Confirm: true},
			wantAllowed: true,
		},
		{
			name:        "workspace-only requires confirm to disable builtin variable",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceBuiltInVariable, Action: ActionDisable, Confirm: false},
			wantAllowed: false,
			wantHintSub: "--confirm",
		},
		{
			name:        "workspace-only allows disable builtin variable with confirm",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceBuiltInVariable, Action: ActionDisable, Confirm: true},
			wantAllowed: true,
		},
		{
			name:        "workspace-only blocks publish even with confirm",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceVersion, Action: ActionPublish, Confirm: true},
			wantAllowed: false,
			wantHintSub: "--mode full",
		},
		{
			name:        "workspace-only blocks container delete even with confirm",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceContainer, Action: ActionDelete, Confirm: true},
			wantAllowed: false,
			wantHintSub: "--mode full",
		},
		{
			name:        "workspace-only blocks environment delete even with confirm",
			policy:      Policy{Mode: ModeWorkspaceOnly},
			req:         Request{Resource: ResourceEnvironment, Action: ActionDelete, Confirm: true},
			wantAllowed: false,
			wantHintSub: "--mode full",
		},
		{
			name:        "full blocks publish without confirm",
			policy:      Policy{Mode: ModeFull},
			req:         Request{Resource: ResourceVersion, Action: ActionPublish, Confirm: false},
			wantAllowed: false,
			wantHintSub: "--confirm",
		},
		{
			name:        "full allows publish with confirm",
			policy:      Policy{Mode: ModeFull},
			req:         Request{Resource: ResourceVersion, Action: ActionPublish, Confirm: true},
			wantAllowed: true,
		},
		{
			name:        "user permission read blocked by default",
			policy:      Policy{Mode: ModeFull, AllowUserPermissions: false},
			req:         Request{Resource: ResourceUserPermission, Action: ActionRead},
			wantAllowed: false,
			wantHintSub: "--allow-user-permissions",
		},
		{
			name:        "user permission read allowed when explicitly unlocked",
			policy:      Policy{Mode: ModeReadOnly, AllowUserPermissions: true},
			req:         Request{Resource: ResourceUserPermission, Action: ActionRead},
			wantAllowed: true,
		},
		{
			name:        "user permission delete requires confirm even when unlocked",
			policy:      Policy{Mode: ModeFull, AllowUserPermissions: true},
			req:         Request{Resource: ResourceUserPermission, Action: ActionDelete, Confirm: false},
			wantAllowed: false,
			wantHintSub: "--confirm",
		},
		{
			name:        "empty mode defaults to workspace-only",
			policy:      Policy{},
			req:         Request{Resource: ResourceVersion, Action: ActionPublish, Confirm: true},
			wantAllowed: false,
			wantHintSub: "--mode full",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Check(tc.req)
			if tc.wantAllowed {
				if err != nil {
					t.Fatalf("expected allowed, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("expected ErrBlocked, got %v", err)
			}
			if tc.wantHintSub != "" && !strings.Contains(err.Error(), tc.wantHintSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantHintSub)
			}
		})
	}
}

func TestCheckDuplicateName(t *testing.T) {
	existing := []string{"GA4 - Event - purchase", "CE - lead"}

	if err := CheckDuplicateName("tag", existing, "  ga4 - event - PURCHASE ", false); err == nil {
		t.Fatalf("expected duplicate error")
	} else if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}

	if err := CheckDuplicateName("tag", existing, "ga4 - event - purchase", true); err != nil {
		t.Fatalf("expected allowDuplicate=true to pass, got %v", err)
	}

	if err := CheckDuplicateName("tag", existing, "GA4 - Event - generate_lead", false); err != nil {
		t.Fatalf("expected unique name to pass, got %v", err)
	}
}
