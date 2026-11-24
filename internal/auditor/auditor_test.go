package auditor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"google.golang.org/api/option"
	"google.golang.org/api/tagmanager/v2"
)

func hasRule(findings []Finding, ruleID string) bool {
	for _, f := range findings {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}

func TestEvaluateAllFifteenRulesAndCleanPass(t *testing.T) {
	dirty := &Snapshot{
		AccountID:   "1",
		ContainerID: "2",
		WorkspaceID: "3",
		TargetLabel: "workspace 3",
		Templates: []*tagmanager.CustomTemplate{
			{
				TemplateId: "50",
				Name:       "Facebook Pixel by Stape",
				GalleryReference: &tagmanager.GalleryReference{
					Owner:      "stape-io",
					Repository: "fb-tag",
				},
			},
			{
				TemplateId: "60",
				Name:       "Facebook CAPI by Stape",
				GalleryReference: &tagmanager.GalleryReference{
					Owner:      "stape-io",
					Repository: "facebook-tag",
				},
			},
		},
		Triggers: []*tagmanager.Trigger{
			{TriggerId: "1", Name: "Init - All Pages", Type: "init"},
			{TriggerId: "2", Name: "Orphan Trigger", Type: "customEvent"},
		},
		Variables: []*tagmanager.Variable{
			{
				VariableId: "10",
				Name:       "DLV - user_email",
				Type:       "v",
				Parameter:  []*tagmanager.Parameter{{Key: "name", Type: "TEMPLATE", Value: "user_email"}},
			},
			{
				VariableId: "11",
				Name:       "Unused Variable",
				Type:       "c",
				Parameter:  []*tagmanager.Parameter{{Key: "value", Type: "TEMPLATE", Value: "unused"}},
			},
			{
				VariableId: "12",
				Name:       "CONST - Meta Test Event Code",
				Type:       "c",
				Parameter:  []*tagmanager.Parameter{{Key: "value", Type: "TEMPLATE", Value: "TEST12345"}},
			},
		},
		Tags: []*tagmanager.Tag{
			{
				TagId:           "101",
				Name:            "GA4 - PII Leak",
				Type:            "gaawe",
				FiringTriggerId: []string{"1"},
				Parameter: []*tagmanager.Parameter{
					{Key: "measurementIdOverride", Type: "TEMPLATE", Value: "G-ABC12345"}, // hardcoded
					{Key: "eventSettingsTable", Type: "LIST", List: []*tagmanager.Parameter{
						{Type: "MAP", Map: []*tagmanager.Parameter{
							{Key: "parameter", Type: "TEMPLATE", Value: "email"},
							{Key: "parameterValue", Type: "TEMPLATE", Value: "{{DLV - user_email}}"},
						}},
					}},
				},
			},
			{
				TagId:           "102",
				Name:            "HTML - Raw FB Loader",
				Type:            "html",
				FiringTriggerId: []string{"1"},
				Parameter: []*tagmanager.Parameter{
					{Key: "html", Type: "TEMPLATE", Value: `<script src="http://connect.facebook.net/en_US/fbevents.js"></script><script>eval("x");</script>`},
				},
			},
			{
				TagId:           "103",
				Name:            "Legacy UA Tag",
				Type:            "ua",
				FiringTriggerId: []string{"1"},
			},
			{
				TagId:           "104",
				Name:            "Meta Pixel No EventID",
				Type:            "cvt_2_50",
				FiringTriggerId: []string{"1"},
				Parameter:       []*tagmanager.Parameter{{Key: "pixelIds", Type: "TEMPLATE", Value: "12345678"}},
			},
			{
				TagId:           "105",
				Name:            "Meta CAPI Test Code",
				Type:            "cvt_2_60",
				FiringTriggerId: []string{"1"},
				ConsentSettings: &tagmanager.TagConsentSetting{ConsentStatus: "needed"},
				Parameter:       []*tagmanager.Parameter{{Key: "testId", Type: "TEMPLATE", Value: "TEST999"}},
			},
			{
				TagId:           "106",
				Name:            "Google Tag A",
				Type:            "googtag",
				FiringTriggerId: []string{"1"},
				Parameter:       []*tagmanager.Parameter{{Key: "tagId", Type: "TEMPLATE", Value: "G-DUP9999"}},
			},
			{
				TagId:           "107",
				Name:            "Google Tag B",
				Type:            "googtag",
				FiringTriggerId: []string{"1"},
				Parameter:       []*tagmanager.Parameter{{Key: "tagId", Type: "TEMPLATE", Value: "G-DUP9999"}},
			},
			{
				TagId:           "108",
				Name:            "Google Ads Purchase",
				Type:            "awct",
				FiringTriggerId: []string{"1"},
				Parameter:       []*tagmanager.Parameter{{Key: "conversionId", Type: "TEMPLATE", Value: "99887766"}},
			},
			{
				TagId: "109",
				Name:  "Untriggered GA4 Event",
				Type:  "gaawe",
			},
			{
				TagId:  "110",
				Name:   "Old Paused Tag",
				Type:   "gaawe",
				Paused: true,
			},
		},
		Workspaces: []*tagmanager.Workspace{
			{WorkspaceId: "1"}, {WorkspaceId: "2"}, {WorkspaceId: "3"},
		},
		VersionHeaders: []*tagmanager.ContainerVersionHeader{
			{ContainerVersionId: "1", Name: ""},
		},
	}

	report := EvaluateSnapshot(Input{MinSeverity: SeverityLow}, dirty)

	expectedRules := []string{
		"PII_EXPOSED_IN_PARAMS",
		"UNSAFE_CUSTOM_HTML",
		"CONSENT_MODE_MISSING_BASELINE",
		"CONSENT_NOT_SET_ON_MARKETING_TAG",
		"DEPRECATED_TAG_TYPE",
		"META_DEDUP_MISSING_EVENT_ID",
		"DUPLICATE_GOOGLE_TAG",
		"GADS_MISSING_CONVERSION_LINKER",
		"TEST_EVENT_CODE_ACTIVE",
		"TAG_WITHOUT_TRIGGER",
		"UNUSED_TRIGGER",
		"UNUSED_VARIABLE",
		"HARDCODED_TRACKING_ID",
		"PAUSED_TAGS_ACCUMULATION",
		"GOVERNANCE_HYGIENE",
	}
	for _, rule := range expectedRules {
		if !hasRule(report.Findings, rule) {
			t.Errorf("expected rule %s to fire on dirty container", rule)
		}
	}

	if report.Summary.Grade != "F" || report.Summary.Score != 0 {
		t.Fatalf("expected score=0 grade=F on heavily dirty container, got score=%d grade=%s", report.Summary.Score, report.Summary.Grade)
	}
	if !HasFindingsAtOrAbove(report.Findings, SeverityCritical) {
		t.Fatalf("expected critical finding present")
	}

	highOnly := EvaluateSnapshot(Input{MinSeverity: SeverityHigh}, dirty)
	if len(highOnly.Findings) >= len(report.Findings) {
		t.Fatalf("expected MinSeverity=high to filter out medium/low findings")
	}
	if highOnly.Summary.TotalFindings != report.Summary.TotalFindings {
		t.Fatalf("Summary.TotalFindings must reflect full container health even when filtered")
	}

	md := report.RenderMarkdown()
	if !strings.Contains(md, "PII_EXPOSED_IN_PARAMS") || !strings.Contains(md, "Grade F") {
		t.Fatalf("unexpected markdown output: %s", md)
	}
}

func TestCleanContainerScoresAWithEnhancedConversionsAllowed(t *testing.T) {
	clean := &Snapshot{
		AccountID:   "1",
		ContainerID: "2",
		WorkspaceID: "3",
		TargetLabel: "workspace 3",
		Templates: []*tagmanager.CustomTemplate{
			{TemplateId: "50", GalleryReference: &tagmanager.GalleryReference{Owner: "stape-io", Repository: "fb-tag"}},
			{TemplateId: "51", GalleryReference: &tagmanager.GalleryReference{Owner: "stape-io", Repository: "facebook-tag"}},
		},
		Folders: []*tagmanager.Folder{{FolderId: "f1", Name: "Core"}},
		Variables: []*tagmanager.Variable{
			{VariableId: "v1", Name: "CONST - GA4 ID", Type: "c", Parameter: []*tagmanager.Parameter{{Key: "value", Value: "G-CLEAN123"}}},
			{VariableId: "v2", Name: "CONST - Meta Pixel ID", Type: "c", Parameter: []*tagmanager.Parameter{{Key: "value", Value: "123456789"}}},
			{VariableId: "v3", Name: "VAR - Unique Event ID", Type: "cvt_2_99"},
			{VariableId: "v4", Name: "DLV - email", Type: "v", Parameter: []*tagmanager.Parameter{{Key: "name", Value: "user_data.email"}}},
			{VariableId: "v5", Name: "UPD - User Data", Type: "awec", Parameter: []*tagmanager.Parameter{{Key: "email", Value: "{{DLV - email}}"}}},
			{VariableId: "v6", Name: "CONST - Ads ID", Type: "c", Parameter: []*tagmanager.Parameter{{Key: "value", Value: "12345678"}}},
		},
		Triggers: []*tagmanager.Trigger{
			{TriggerId: "t0", Name: "Consent Init", Type: "consentInit"},
			{TriggerId: "t1", Name: "Init", Type: "init"},
			{TriggerId: "t2", Name: "PV", Type: "pageview"},
		},
		Tags: []*tagmanager.Tag{
			{
				TagId: "1", Name: "Consent Default", Type: "html", ParentFolderId: "f1", FiringTriggerId: []string{"t0"},
				ConsentSettings: &tagmanager.TagConsentSetting{ConsentStatus: "notNeeded"},
				Parameter:       []*tagmanager.Parameter{{Key: "html", Value: "<script>gtag('consent','default',{});</script>"}},
			},
			{
				TagId: "2", Name: "Google Tag", Type: "googtag", ParentFolderId: "f1", FiringTriggerId: []string{"t1"},
				Parameter: []*tagmanager.Parameter{{Key: "tagId", Value: "{{CONST - GA4 ID}}"}},
			},
			{
				TagId: "3", Name: "Linker", Type: "gclidw", ParentFolderId: "f1", FiringTriggerId: []string{"t1"},
			},
			{
				TagId: "4", Name: "Google Ads Conv", Type: "awct", ParentFolderId: "f1", FiringTriggerId: []string{"t2"},
				Parameter: []*tagmanager.Parameter{
					{Key: "conversionId", Value: "{{CONST - Ads ID}}"},
					{Key: "cssProvidedEnhancedConversionValue", Value: "{{UPD - User Data}}"},
				},
			},
			{
				TagId: "5", Name: "Meta Pixel", Type: "cvt_2_50", ParentFolderId: "f1", FiringTriggerId: []string{"t2"},
				ConsentSettings: &tagmanager.TagConsentSetting{ConsentStatus: "needed"},
				Parameter: []*tagmanager.Parameter{
					{Key: "pixelIds", Value: "{{CONST - Meta Pixel ID}}"},
					{Key: "eventId", Value: "{{VAR - Unique Event ID}}"},
				},
			},
			{
				TagId: "6", Name: "Meta CAPI", Type: "cvt_2_51", ParentFolderId: "f1", FiringTriggerId: []string{"t2"},
				ConsentSettings: &tagmanager.TagConsentSetting{ConsentStatus: "needed"},
				Parameter: []*tagmanager.Parameter{
					{Key: "pixelId", Value: "{{CONST - Meta Pixel ID}}"},
					{Key: "autoMapServerEventData", Value: "true"},
					{Key: "userDataList", Type: "LIST", List: []*tagmanager.Parameter{
						{Type: "MAP", Map: []*tagmanager.Parameter{{Key: "name", Value: "em"}, {Key: "value", Value: "{{DLV - email}}"}}},
					}},
				},
			},
		},
		Workspaces:     []*tagmanager.Workspace{{WorkspaceId: "3", Name: "Default"}},
		VersionHeaders: []*tagmanager.ContainerVersionHeader{{ContainerVersionId: "1", Name: "v1.0"}},
	}

	report := EvaluateSnapshot(Input{MinSeverity: SeverityLow}, clean)
	if report.Summary.Score != 100 || report.Summary.Grade != "A" || len(report.Findings) != 0 {
		t.Fatalf("expected 100/A with 0 findings on clean container, got score=%d grade=%s findings=%+v",
			report.Summary.Score, report.Summary.Grade, report.Findings)
	}
}

func TestRunAuditWorkspaceAndLiveAgainstMockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/workspaces"):
			_ = json.NewEncoder(w).Encode(map[string]any{"workspace": []*tagmanager.Workspace{{WorkspaceId: "3"}}})
		case strings.HasSuffix(r.URL.Path, "/version_headers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"containerVersionHeader": []*tagmanager.ContainerVersionHeader{{ContainerVersionId: "7", Name: "Release"}}})
		case strings.HasSuffix(r.URL.Path, "/versions:live"):
			_ = json.NewEncoder(w).Encode(&tagmanager.ContainerVersion{
				ContainerVersionId: "7",
				Name:               "Live Release",
				Tag:                []*tagmanager.Tag{{TagId: "1", Name: "Legacy UA", Type: "ua", FiringTriggerId: []string{"10"}}},
				Trigger:            []*tagmanager.Trigger{{TriggerId: "10", Name: "All Pages", Type: "pageview"}},
			})
		case strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": []*tagmanager.Tag{{TagId: "1", Name: "Google Tag", Type: "googtag", FiringTriggerId: []string{"10"}}}})
		case strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": []*tagmanager.Trigger{{TriggerId: "10", Name: "Init", Type: "init"}}})
		case strings.HasSuffix(r.URL.Path, "/variables"),
			strings.HasSuffix(r.URL.Path, "/built_in_variables"),
			strings.HasSuffix(r.URL.Path, "/folders"),
			strings.HasSuffix(r.URL.Path, "/templates"),
			strings.HasSuffix(r.URL.Path, "/clients"),
			strings.HasSuffix(r.URL.Path, "/transformations"):
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := gtm.NewClient(context.Background(), gtm.ClientConfig{
		ClientOptions: []option.ClientOption{
			option.WithHTTPClient(srv.Client()),
			option.WithEndpoint(srv.URL),
			option.WithoutAuthentication(),
		},
		Policy:    &safety.Policy{Mode: safety.ModeReadOnly},
		Scheduler: gtm.NewScheduler(gtm.SchedulerOptions{QPS: 100, Burst: 20, MaxAttempts: 2, BaseBackoff: time.Millisecond}),
		Cache:     gtm.NewSnapshotCache(false, time.Minute),
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	wsRep, err := Run(context.Background(), client, Input{AccountID: "1", ContainerID: "2", WorkspaceID: "3"})
	if err != nil {
		t.Fatalf("workspace Run failed: %v", err)
	}
	if wsRep.Target != "workspace 3" || wsRep.Summary.Counts["tags"] != 1 {
		t.Fatalf("unexpected workspace report: %+v", wsRep)
	}

	liveRep, err := Run(context.Background(), client, Input{AccountID: "1", ContainerID: "2", Live: true})
	if err != nil {
		t.Fatalf("live Run failed: %v", err)
	}
	if liveRep.LiveVersionID != "7" || !hasRule(liveRep.Findings, "DEPRECATED_TAG_TYPE") {
		t.Fatalf("unexpected live report: %+v", liveRep)
	}
}
