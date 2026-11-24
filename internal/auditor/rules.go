package auditor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"google.golang.org/api/tagmanager/v2"
)

var (
	emailLiteralRe   = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	cpfLiteralRe     = regexp.MustCompile(`\b\d{3}\.\d{3}\.\d{3}-\d{2}\b`)
	ga4LiteralRe     = regexp.MustCompile(`^G-[A-Z0-9]{4,}$`)
	awLiteralRe      = regexp.MustCompile(`^(AW-)?[0-9]{6,}$`)
	numericPixelRe   = regexp.MustCompile(`^[0-9]{6,}$`)
	varReferenceRe   = regexp.MustCompile(`\{\{([^}]+)\}\}`)
	piiParamKeyExact = map[string]bool{
		"email": true, "e_mail": true, "user_email": true, "customer_email": true,
		"cpf": true, "cnpj": true, "phone": true, "phone_number": true,
		"telefone": true, "celular": true, "whatsapp": true,
		"password": true, "senha": true, "credit_card": true, "ssn": true,
		"full_name": true, "nome_completo": true,
	}
	deprecatedTagTypes = map[string]string{
		"ua":           "Universal Analytics was sunset by Google; migrate to 'googtag' + 'gaawe'.",
		"gaawc":        "Legacy GA4 Configuration tag; replace with unified Google Tag ('googtag').",
		"gtagjs":       "Invalid GTM tag type 'gtagjs'; official Google Tag type is 'googtag'.",
		"gtagjs_event": "Invalid GTM tag type 'gtagjs_event'; official GA4 Event type is 'gaawe'.",
		"fbpixel":      "Non-existent GTM tag type 'fbpixel'; use sandboxed 'stape-io/fb-tag' gallery template.",
	}
)

// Evaluate executes all 15 deterministic audit rules over snap and returns sorted findings.
func Evaluate(snap *Snapshot) []Finding {
	if snap == nil {
		return nil
	}
	tplRepoByType := indexTemplateRepos(snap.ContainerID, snap.Templates)

	var findings []Finding
	findings = append(findings, checkPIIExposedInParams(snap, tplRepoByType)...)
	findings = append(findings, checkUnsafeCustomHTML(snap)...)
	findings = append(findings, checkConsentModeBaseline(snap, tplRepoByType)...)
	findings = append(findings, checkConsentNotSetOnMarketing(snap, tplRepoByType)...)
	findings = append(findings, checkDeprecatedTagTypes(snap)...)
	findings = append(findings, checkMetaDedupMissingEventID(snap, tplRepoByType)...)
	findings = append(findings, checkDuplicateGoogleTags(snap)...)
	findings = append(findings, checkGoogleAdsConversionLinker(snap)...)
	findings = append(findings, checkTestEventCodeActive(snap, tplRepoByType)...)
	findings = append(findings, checkTagWithoutTrigger(snap)...)
	findings = append(findings, checkUnusedTriggers(snap)...)
	findings = append(findings, checkUnusedVariables(snap)...)
	findings = append(findings, checkHardcodedTrackingIDs(snap)...)
	findings = append(findings, checkPausedTags(snap)...)
	findings = append(findings, checkGovernanceHygiene(snap)...)

	slices.SortStableFunc(findings, func(a, b Finding) int {
		if ra, rb := SeverityRank(a.Severity), SeverityRank(b.Severity); ra != rb {
			return rb - ra
		}
		if a.RuleID != b.RuleID {
			return strings.Compare(a.RuleID, b.RuleID)
		}
		return strings.Compare(a.EntityName, b.EntityName)
	})

	return findings
}

func indexTemplateRepos(containerID string, templates []*tagmanager.CustomTemplate) map[string]string {
	out := make(map[string]string, len(templates))
	for _, tpl := range templates {
		if tpl == nil || tpl.GalleryReference == nil {
			continue
		}
		repo := strings.ToLower(strings.TrimSpace(tpl.GalleryReference.Owner) + "/" + strings.TrimSpace(tpl.GalleryReference.Repository))
		if tpl.TemplateId != "" && containerID != "" {
			out["cvt_"+containerID+"_"+tpl.TemplateId] = repo
		}
		if tpl.GalleryReference.GalleryTemplateId != "" {
			out["cvt_temp_"+tpl.GalleryReference.GalleryTemplateId] = repo
		}
	}
	return out
}

func checkPIIExposedInParams(snap *Snapshot, tplRepoByType map[string]string) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		repo := tplRepoByType[tg.Type]
		isMetaTemplate := repo == "stape-io/facebook-tag" || repo == "stape-io/fb-tag"
		isGoogleAdsEnhanced := tg.Type == "awct" || tg.Type == "awec"

		var hits []string
		walkParameters(tg.Parameter, func(key, val string, inTable string) {
			kNorm := strings.ToLower(strings.TrimSpace(key))
			vNorm := strings.ToLower(strings.TrimSpace(val))

			// Official hashed user-data pipelines intentionally pass user fields.
			if isMetaTemplate && (inTable == "userDataList" || strings.Contains(kNorm, "userdata")) {
				return
			}
			if isGoogleAdsEnhanced && (kNorm == "cssprovidedenhancedconversionvalue" || kNorm == "userdata") {
				return
			}

			if piiParamKeyExact[kNorm] || (inTable == "eventSettingsTable" && kNorm == "parameter" && piiParamKeyExact[vNorm]) {
				hits = append(hits, firstNonEmpty(val, key))
				return
			}
			if emailLiteralRe.MatchString(val) || cpfLiteralRe.MatchString(val) {
				hits = append(hits, "literal PII in "+firstNonEmpty(key, "parameter"))
			}
		})

		if len(hits) > 0 {
			out = append(out, Finding{
				RuleID:      "PII_EXPOSED_IN_PARAMS",
				Severity:    SeverityCritical,
				Category:    CategoryPrivacy,
				EntityKind:  "tag",
				EntityID:    tg.TagId,
				EntityName:  tg.Name,
				Title:       "Potential unhashed PII exposed in tag parameters",
				Message:     fmt.Sprintf("Tag %q (%s) appears to send raw PII field(s): %s.", tg.Name, tg.Type, strings.Join(uniqueStrings(hits), ", ")),
				Remediation: "Remove unhashed PII from GA4/Web tags (violates Google terms and LGPD). Use Google Ads User-Provided Data ('awec') or sGTM transformations/hashed CAPI userDataList.",
			})
		}
	}
	return out
}

func checkUnsafeCustomHTML(snap *Snapshot) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused || tg.Type != "html" {
			continue
		}
		htmlCode := paramValue(tg.Parameter, "html")
		lower := strings.ToLower(htmlCode)

		var reasons []string
		if strings.Contains(lower, "document.write") || strings.Contains(lower, "eval(") || strings.Contains(lower, ".innerhtml") {
			reasons = append(reasons, "uses unsafe DOM/eval sink (document.write/eval/innerHTML)")
		}
		if strings.Contains(lower, "http://") {
			reasons = append(reasons, "loads insecure http:// script")
		}
		if strings.Contains(lower, "connect.facebook.net") ||
			strings.Contains(lower, "googletagmanager.com/gtag/js") ||
			strings.Contains(lower, "google-analytics.com/analytics.js") ||
			strings.Contains(lower, "analytics.tiktok.com") ||
			strings.Contains(lower, "snap.licdn.com") {
			reasons = append(reasons, "embeds raw vendor pixel loader replaceable by built-in or sandboxed gallery template")
		}

		if len(reasons) > 0 {
			out = append(out, Finding{
				RuleID:      "UNSAFE_CUSTOM_HTML",
				Severity:    SeverityHigh,
				Category:    CategorySecurity,
				EntityKind:  "tag",
				EntityID:    tg.TagId,
				EntityName:  tg.Name,
				Title:       "Unsafe or replaceable Custom HTML tag",
				Message:     fmt.Sprintf("Custom HTML tag %q %s.", tg.Name, strings.Join(reasons, "; ")),
				Remediation: "Replace raw vendor scripts with native tags ('googtag'/'gaawe') or sandboxed Community Gallery templates (e.g., 'stape-io/fb-tag').",
			})
		}
	}
	return out
}

func checkConsentModeBaseline(snap *Snapshot, tplRepoByType map[string]string) []Finding {
	hasMarketingTags := false
	hasConsentInitTag := false

	for _, tr := range snap.Triggers {
		if tr != nil && tr.Type == "consentInit" {
			hasConsentInitTag = true
			break
		}
	}
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		if tg.Type == "googtag" || tg.Type == "gaawe" || tg.Type == "awct" || tg.Type == "sp" || strings.HasPrefix(tg.Type, "cvt_") || tg.Type == "html" {
			if isConsentBaselineTag(tg, tplRepoByType) {
				hasConsentInitTag = true
				continue
			}
			hasMarketingTags = true
		}
	}

	if hasMarketingTags && !hasConsentInitTag {
		return []Finding{{
			RuleID:      "CONSENT_MODE_MISSING_BASELINE",
			Severity:    SeverityHigh,
			Category:    CategoryPrivacy,
			EntityKind:  "container",
			EntityID:    snap.ContainerID,
			EntityName:  snap.TargetLabel,
			Title:       "No Google Consent Mode v2 baseline detected",
			Message:     "Active analytics/marketing tags exist without a Consent Initialization ('consentInit') trigger or Consent Mode default tag.",
			Remediation: "Deploy the 'consent-mode-v2' blueprint (sets ad_storage, analytics_storage, ad_user_data, ad_personalization defaults on consentInit).",
		}}
	}
	return nil
}

func checkConsentNotSetOnMarketing(snap *Snapshot, tplRepoByType map[string]string) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		if tg.Type != "html" && !strings.HasPrefix(tg.Type, "cvt_") {
			continue
		}
		if isConsentBaselineTag(tg, tplRepoByType) {
			continue
		}
		status := ""
		if tg.ConsentSettings != nil {
			status = tg.ConsentSettings.ConsentStatus
		}
		if status == "" || status == "notSet" {
			out = append(out, Finding{
				RuleID:      "CONSENT_NOT_SET_ON_MARKETING_TAG",
				Severity:    SeverityHigh,
				Category:    CategoryPrivacy,
				EntityKind:  "tag",
				EntityID:    tg.TagId,
				EntityName:  tg.Name,
				Title:       "Non-Google tag missing explicit consent settings",
				Message:     fmt.Sprintf("Tag %q (%s) has consentSettings.consentStatus='notSet' instead of 'needed' or explicit 'notNeeded'.", tg.Name, tg.Type),
				Remediation: "Set consentSettings.consentStatus='needed' with required consent types (e.g. ad_storage, ad_user_data, ad_personalization) or mark 'notNeeded' if strictly essential.",
			})
		}
	}
	return out
}

func checkDeprecatedTagTypes(snap *Snapshot) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		if reason, bad := deprecatedTagTypes[strings.ToLower(strings.TrimSpace(tg.Type))]; bad {
			out = append(out, Finding{
				RuleID:      "DEPRECATED_TAG_TYPE",
				Severity:    SeverityHigh,
				Category:    CategoryReliability,
				EntityKind:  "tag",
				EntityID:    tg.TagId,
				EntityName:  tg.Name,
				Title:       fmt.Sprintf("Deprecated or invalid tag type %q", tg.Type),
				Message:     fmt.Sprintf("Tag %q uses type %q. %s", tg.Name, tg.Type, reason),
				Remediation: "Replace with the current official tag type or blueprint.",
			})
		}
	}
	return out
}

func checkMetaDedupMissingEventID(snap *Snapshot, tplRepoByType map[string]string) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		repo := tplRepoByType[tg.Type]

		switch {
		case repo == "stape-io/fb-tag":
			evID := strings.TrimSpace(paramValue(tg.Parameter, "eventId"))
			if evID == "" {
				out = append(out, Finding{
					RuleID:      "META_DEDUP_MISSING_EVENT_ID",
					Severity:    SeverityHigh,
					Category:    CategoryReliability,
					EntityKind:  "tag",
					EntityID:    tg.TagId,
					EntityName:  tg.Name,
					Title:       "Meta Pixel tag missing eventId for CAPI deduplication",
					Message:     fmt.Sprintf("Meta Pixel tag %q has no 'eventId' configured; browser and server events cannot deduplicate.", tg.Name),
					Remediation: "Bind 'eventId' to a Unique Event ID variable (e.g., {{VAR - Unique Event ID}}) and forward the same event_id to sGTM.",
				})
			}
		case repo == "stape-io/facebook-tag":
			hasEventID := hasServerEventDataKey(tg.Parameter, "event_id") || strings.EqualFold(paramValue(tg.Parameter, "autoMapServerEventData"), "true")
			if !hasEventID {
				out = append(out, Finding{
					RuleID:      "META_DEDUP_MISSING_EVENT_ID",
					Severity:    SeverityHigh,
					Category:    CategoryReliability,
					EntityKind:  "tag",
					EntityID:    tg.TagId,
					EntityName:  tg.Name,
					Title:       "Meta CAPI tag missing event_id mapping for deduplication",
					Message:     fmt.Sprintf("Server-side Meta CAPI tag %q neither maps 'event_id' in serverEventDataList nor enables autoMapServerEventData.", tg.Name),
					Remediation: "Map 'event_id' in serverEventDataList to {{ED - event_id}} and match it with the browser Meta Pixel tag.",
				})
			}
		case tg.Type == "html":
			htmlCode := paramValue(tg.Parameter, "html")
			if strings.Contains(htmlCode, "fbq(") && !strings.Contains(strings.ToLower(htmlCode), "eventid") {
				out = append(out, Finding{
					RuleID:      "META_DEDUP_MISSING_EVENT_ID",
					Severity:    SeverityHigh,
					Category:    CategoryReliability,
					EntityKind:  "tag",
					EntityID:    tg.TagId,
					EntityName:  tg.Name,
					Title:       "Custom HTML Meta Pixel missing eventID deduplication",
					Message:     fmt.Sprintf("Custom HTML Meta Pixel tag %q calls fbq(...) without an eventID parameter.", tg.Name),
					Remediation: "Migrate to the 'meta-pixel-web' blueprint using sandboxed 'stape-io/fb-tag' + Unique Event ID.",
				})
			}
		}
	}
	return out
}

func checkDuplicateGoogleTags(snap *Snapshot) []Finding {
	byTagID := make(map[string][]*tagmanager.Tag)
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused || tg.Type != "googtag" {
			continue
		}
		tagID := strings.TrimSpace(paramValue(tg.Parameter, "tagId"))
		if tagID == "" {
			tagID = "(empty)"
		}
		byTagID[tagID] = append(byTagID[tagID], tg)
	}

	var out []Finding
	for tagID, group := range byTagID {
		if len(group) < 2 {
			continue
		}
		names := make([]string, 0, len(group))
		for _, g := range group {
			names = append(names, fmt.Sprintf("%s (ID %s)", g.Name, g.TagId))
		}
		out = append(out, Finding{
			RuleID:      "DUPLICATE_GOOGLE_TAG",
			Severity:    SeverityHigh,
			Category:    CategoryReliability,
			EntityKind:  "tag",
			EntityID:    group[0].TagId,
			EntityName:  group[0].Name,
			Title:       "Duplicate active Google Tag for the same destination",
			Message:     fmt.Sprintf("%d active Google Tags target tagId %s: %s.", len(group), tagID, strings.Join(names, ", ")),
			Remediation: "Consolidate into a single active 'googtag' tag to avoid double-counted page views and inflated sessions.",
		})
	}
	return out
}

func checkGoogleAdsConversionLinker(snap *Snapshot) []Finding {
	hasAdsTag := false
	hasLinker := false
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		switch tg.Type {
		case "awct", "sp":
			hasAdsTag = true
		case "gclidw":
			hasLinker = true
		}
	}
	if hasAdsTag && !hasLinker {
		return []Finding{{
			RuleID:      "GADS_MISSING_CONVERSION_LINKER",
			Severity:    SeverityHigh,
			Category:    CategoryReliability,
			EntityKind:  "container",
			EntityID:    snap.ContainerID,
			EntityName:  snap.TargetLabel,
			Title:       "Google Ads tags present without active Conversion Linker",
			Message:     "Active Google Ads conversion/remarketing tags exist without an active Conversion Linker ('gclidw') tag.",
			Remediation: "Add a Conversion Linker ('gclidw') tag firing on Initialization - All Pages (included in the 'google-ads-conversions' blueprint).",
		}}
	}
	return nil
}

func checkTestEventCodeActive(snap *Snapshot, tplRepoByType map[string]string) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		if tplRepoByType[tg.Type] == "stape-io/facebook-tag" {
			if testID := strings.TrimSpace(paramValue(tg.Parameter, "testId")); testID != "" && !strings.HasPrefix(testID, "{{") {
				out = append(out, Finding{
					RuleID:      "TEST_EVENT_CODE_ACTIVE",
					Severity:    SeverityMedium,
					Category:    CategoryReliability,
					EntityKind:  "tag",
					EntityID:    tg.TagId,
					EntityName:  tg.Name,
					Title:       "Hardcoded Meta CAPI testId left on active tag",
					Message:     fmt.Sprintf("Meta CAPI tag %q has non-empty literal testId %q.", tg.Name, testID),
					Remediation: "Clear testId before publishing to production so live conversions do not route exclusively into Test Events.",
				})
			}
		}
	}
	for _, v := range snap.Variables {
		if v == nil || v.Type != "c" {
			continue
		}
		if strings.Contains(strings.ToLower(v.Name), "test event code") {
			if val := strings.TrimSpace(paramValue(v.Parameter, "value")); val != "" {
				out = append(out, Finding{
					RuleID:      "TEST_EVENT_CODE_ACTIVE",
					Severity:    SeverityMedium,
					Category:    CategoryReliability,
					EntityKind:  "variable",
					EntityID:    v.VariableId,
					EntityName:  v.Name,
					Title:       "Non-empty Meta Test Event Code constant",
					Message:     fmt.Sprintf("Constant variable %q has non-empty test code %q.", v.Name, val),
					Remediation: "Clear the constant value prior to production release.",
				})
			}
		}
	}
	return out
}

func checkTagWithoutTrigger(snap *Snapshot) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		if len(tg.FiringTriggerId) == 0 {
			out = append(out, Finding{
				RuleID:      "TAG_WITHOUT_TRIGGER",
				Severity:    SeverityMedium,
				Category:    CategoryReliability,
				EntityKind:  "tag",
				EntityID:    tg.TagId,
				EntityName:  tg.Name,
				Title:       "Active tag has no firing trigger",
				Message:     fmt.Sprintf("Tag %q (%s) is not paused but has an empty firingTriggerId list and will never fire.", tg.Name, tg.Type),
				Remediation: "Attach a firing trigger or pause/delete the tag if no longer needed.",
			})
		}
	}
	return out
}

func checkUnusedTriggers(snap *Snapshot) []Finding {
	referenced := make(map[string]bool)
	for _, tg := range snap.Tags {
		if tg == nil {
			continue
		}
		for _, id := range tg.FiringTriggerId {
			referenced[id] = true
		}
		for _, id := range tg.BlockingTriggerId {
			referenced[id] = true
		}
	}
	for _, v := range snap.Variables {
		if v == nil {
			continue
		}
		for _, id := range v.EnablingTriggerId {
			referenced[id] = true
		}
		for _, id := range v.DisablingTriggerId {
			referenced[id] = true
		}
	}

	var out []Finding
	for _, tr := range snap.Triggers {
		if tr == nil || referenced[tr.TriggerId] {
			continue
		}
		out = append(out, Finding{
			RuleID:      "UNUSED_TRIGGER",
			Severity:    SeverityMedium,
			Category:    CategoryHygiene,
			EntityKind:  "trigger",
			EntityID:    tr.TriggerId,
			EntityName:  tr.Name,
			Title:       "Orphaned trigger with no referencing tags",
			Message:     fmt.Sprintf("Trigger %q (%s) is not referenced as a firing or blocking trigger by any tag.", tr.Name, tr.Type),
			Remediation: "Link it to the intended tag or remove unused triggers to keep the container clean.",
		})
	}
	return out
}

func checkUnusedVariables(snap *Snapshot) []Finding {
	refCounts := make(map[string]int)
	addRefs := func(v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			return
		}
		for _, m := range varReferenceRe.FindAllStringSubmatch(string(raw), -1) {
			if len(m) == 2 {
				refCounts[strings.TrimSpace(m[1])]++
			}
		}
	}

	for _, tg := range snap.Tags {
		addRefs(tg)
	}
	for _, tr := range snap.Triggers {
		addRefs(tr)
	}
	for _, cl := range snap.Clients {
		addRefs(cl)
	}
	for _, trf := range snap.Transformations {
		addRefs(trf)
	}
	for _, v := range snap.Variables {
		if v == nil {
			continue
		}
		// Inspect variable references inside other variables while ignoring self-mentions in notes/name.
		clone := *v
		clone.Name = ""
		clone.Notes = ""
		addRefs(&clone)
	}

	var out []Finding
	for _, v := range snap.Variables {
		if v == nil {
			continue
		}
		if refCounts[strings.TrimSpace(v.Name)] == 0 {
			out = append(out, Finding{
				RuleID:      "UNUSED_VARIABLE",
				Severity:    SeverityMedium,
				Category:    CategoryHygiene,
				EntityKind:  "variable",
				EntityID:    v.VariableId,
				EntityName:  v.Name,
				Title:       "Unused user-defined variable",
				Message:     fmt.Sprintf("Variable {{%s}} (%s) is never referenced by any tag, trigger, variable, client, or transformation.", v.Name, v.Type),
				Remediation: "Reference it where needed or delete unused variables to shrink container bundle size.",
			})
		}
	}
	return out
}

func checkHardcodedTrackingIDs(snap *Snapshot) []Finding {
	idKeys := map[string]bool{
		"tagId": true, "measurementId": true, "measurementIdOverride": true,
		"conversionId": true, "pixelId": true, "pixelIds": true, "accessToken": true,
	}

	var out []Finding
	for _, tg := range snap.Tags {
		if tg == nil || tg.Paused {
			continue
		}
		var hardcoded []string
		for _, p := range tg.Parameter {
			if p == nil || !idKeys[p.Key] {
				continue
			}
			val := strings.TrimSpace(p.Value)
			if val == "" || strings.HasPrefix(val, "{{") {
				continue
			}
			if ga4LiteralRe.MatchString(val) || awLiteralRe.MatchString(val) || numericPixelRe.MatchString(val) || p.Key == "accessToken" {
				hardcoded = append(hardcoded, p.Key)
			}
		}
		if len(hardcoded) > 0 {
			out = append(out, Finding{
				RuleID:      "HARDCODED_TRACKING_ID",
				Severity:    SeverityMedium,
				Category:    CategoryHygiene,
				EntityKind:  "tag",
				EntityID:    tg.TagId,
				EntityName:  tg.Name,
				Title:       "Hardcoded tracking ID or token inside tag",
				Message:     fmt.Sprintf("Tag %q hardcodes %s instead of referencing a Constant ('c') variable.", tg.Name, strings.Join(hardcoded, ", ")),
				Remediation: "Extract tracking IDs and tokens into Constant ('c') variables for centralized maintenance.",
			})
		}
	}
	return out
}

func checkPausedTags(snap *Snapshot) []Finding {
	var out []Finding
	for _, tg := range snap.Tags {
		if tg != nil && tg.Paused {
			out = append(out, Finding{
				RuleID:      "PAUSED_TAGS_ACCUMULATION",
				Severity:    SeverityLow,
				Category:    CategoryHygiene,
				EntityKind:  "tag",
				EntityID:    tg.TagId,
				EntityName:  tg.Name,
				Title:       "Paused tag left in container",
				Message:     fmt.Sprintf("Tag %q (%s) is paused.", tg.Name, tg.Type),
				Remediation: "Either unpause when ready or delete stale paused tags before publishing.",
			})
		}
	}
	return out
}

func checkGovernanceHygiene(snap *Snapshot) []Finding {
	var out []Finding

	if len(snap.Tags) >= 5 {
		unfoldered := 0
		for _, tg := range snap.Tags {
			if tg != nil && strings.TrimSpace(tg.ParentFolderId) == "" {
				unfoldered++
			}
		}
		if unfoldered > 0 {
			out = append(out, Finding{
				RuleID:      "GOVERNANCE_HYGIENE",
				Severity:    SeverityLow,
				Category:    CategoryHygiene,
				EntityKind:  "container",
				EntityID:    snap.ContainerID,
				EntityName:  snap.TargetLabel,
				Title:       "Tags not organized in folders",
				Message:     fmt.Sprintf("%d of %d tags have no parentFolderId assigned.", unfoldered, len(snap.Tags)),
				Remediation: "Group tags by platform/purpose into GTM folders (e.g. 'Google - GA4', 'Meta - Pixel Web').",
			})
		}
	}

	if len(snap.Workspaces) >= 3 {
		out = append(out, Finding{
			RuleID:      "GOVERNANCE_HYGIENE",
			Severity:    SeverityLow,
			Category:    CategoryHygiene,
			EntityKind:  "container",
			EntityID:    snap.ContainerID,
			EntityName:  snap.TargetLabel,
			Title:       "High active workspace count",
			Message:     fmt.Sprintf("Container has %d open workspaces (free GTM containers cap at 3 workspaces).", len(snap.Workspaces)),
			Remediation: "Version and clean up stale workspaces to avoid hitting the container workspace limit.",
		})
	}

	unnamedVersions := 0
	for _, vh := range snap.VersionHeaders {
		if vh != nil && !vh.Deleted && strings.TrimSpace(vh.Name) == "" {
			unnamedVersions++
		}
	}
	if unnamedVersions > 0 {
		out = append(out, Finding{
			RuleID:      "GOVERNANCE_HYGIENE",
			Severity:    SeverityLow,
			Category:    CategoryHygiene,
			EntityKind:  "container",
			EntityID:    snap.ContainerID,
			EntityName:  snap.TargetLabel,
			Title:       "Unnamed container versions in history",
			Message:     fmt.Sprintf("%d published/saved version(s) have no display name.", unnamedVersions),
			Remediation: "Always pass a descriptive --name and --notes to 'workspaces create-version' for clean rollback history.",
		})
	}

	return out
}

func isConsentBaselineTag(tg *tagmanager.Tag, tplRepoByType map[string]string) bool {
	if tg.Type == "html" {
		lower := strings.ToLower(paramValue(tg.Parameter, "html"))
		if strings.Contains(lower, "consent") && strings.Contains(lower, "default") {
			return true
		}
	}
	repo := tplRepoByType[tg.Type]
	return strings.Contains(repo, "consent") || strings.Contains(strings.ToLower(tg.Name), "consent mode")
}

func hasServerEventDataKey(params []*tagmanager.Parameter, targetKey string) bool {
	for _, p := range params {
		if p == nil || p.Key != "serverEventDataList" {
			continue
		}
		for _, item := range p.List {
			if item == nil {
				continue
			}
			for _, field := range item.Map {
				if field != nil && field.Key == "name" && strings.TrimSpace(field.Value) == targetKey {
					return true
				}
			}
		}
	}
	return false
}

func paramValue(params []*tagmanager.Parameter, key string) string {
	for _, p := range params {
		if p != nil && p.Key == key {
			return p.Value
		}
	}
	return ""
}

func walkParameters(params []*tagmanager.Parameter, fn func(key, value, tableName string)) {
	var walk func(list []*tagmanager.Parameter, tableName string)
	walk = func(list []*tagmanager.Parameter, tableName string) {
		for _, p := range list {
			if p == nil {
				continue
			}
			if p.Key != "" && p.Value != "" {
				fn(p.Key, p.Value, tableName)
			}
			subTable := tableName
			if p.Type == "LIST" && p.Key != "" {
				subTable = p.Key
			}
			if len(p.List) > 0 {
				walk(p.List, subTable)
			}
			if len(p.Map) > 0 {
				walk(p.Map, tableName)
			}
		}
	}
	walk(params, "")
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
