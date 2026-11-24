package trackingplan

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/LorranHippolyte/tagforge/internal/auditor"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"google.golang.org/api/tagmanager/v2"
)

var varRefRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// Generate fetches the workspace or live version snapshot and builds a living Tracking Plan.
func Generate(ctx context.Context, client *gtm.Client, input Input) (*Plan, error) {
	target, err := ParseSnippetTarget(string(input.SnippetTarget))
	if err != nil {
		return nil, err
	}
	snap, err := auditor.LoadSnapshot(ctx, client, input.AccountID, input.ContainerID, input.WorkspaceID, input.Live)
	if err != nil {
		return nil, err
	}
	return BuildFromSnapshot(snap, target), nil
}

// BuildFromSnapshot deterministically extracts events, destinations, Data Layer keys,
// redacted constants, and typed code snippets from snap.
func BuildFromSnapshot(snap *auditor.Snapshot, target SnippetTarget) *Plan {
	if target == "" {
		target = TargetTS
	}
	tplDisplay := indexTemplateDisplayNames(snap.ContainerID, snap.Templates)

	// Map variable name -> DataLayer keyPath + usedByTags.
	dlVarByName := make(map[string]*DataLayerVar)
	var constants []ConstantRef
	for _, v := range snap.Variables {
		if v == nil {
			continue
		}
		switch v.Type {
		case "v":
			keyPath := paramVal(v.Parameter, "name")
			if keyPath == "" {
				keyPath = v.Name
			}
			dlVarByName[strings.TrimSpace(v.Name)] = &DataLayerVar{
				VariableID:   v.VariableId,
				VariableName: v.Name,
				KeyPath:      keyPath,
				DefaultValue: paramVal(v.Parameter, "defaultValue"),
			}
		case "c":
			val := paramVal(v.Parameter, "value")
			if isSecretVariableName(v.Name) && strings.TrimSpace(val) != "" {
				val = "[REDACTED]"
			}
			constants = append(constants, ConstantRef{
				VariableName: v.Name,
				Value:        val,
			})
		}
	}

	// Map triggerID -> tags firing on that trigger.
	tagsByTrigger := make(map[string][]*tagmanager.Tag)
	tagVarRefs := make(map[string][]string)
	for _, tg := range snap.Tags {
		if tg == nil {
			continue
		}
		for _, trigID := range tg.FiringTriggerId {
			tagsByTrigger[trigID] = append(tagsByTrigger[trigID], tg)
		}
		refs := extractVarRefs(tg)
		tagVarRefs[tg.TagId] = refs
		for _, r := range refs {
			if dv, ok := dlVarByName[r]; ok {
				dv.UsedByTags = appendUnique(dv.UsedByTags, tg.Name)
			}
		}
	}

	var events []EventEntry
	for _, tr := range snap.Triggers {
		if tr == nil {
			continue
		}
		eventNames, condDesc := inferTriggerEvents(tr)
		firingTags := tagsByTrigger[tr.TriggerId]

		destinations := make([]DestinationEntry, 0, len(firingTags))
		dlKeysSet := make(map[string]bool)
		for _, tg := range firingTags {
			destinations = append(destinations, buildDestination(tg, tplDisplay))
			for _, ref := range tagVarRefs[tg.TagId] {
				if dv, ok := dlVarByName[ref]; ok {
					dlKeysSet[dv.KeyPath] = true
				}
			}
		}
		dlKeys := mapKeysSorted(dlKeysSet)

		for _, evName := range eventNames {
			events = append(events, EventEntry{
				EventName:     evName,
				TriggerID:     tr.TriggerId,
				TriggerName:   tr.Name,
				TriggerType:   tr.Type,
				Condition:     condDesc,
				Destinations:  destinations,
				DataLayerKeys: dlKeys,
			})
		}
	}

	dlVars := make([]DataLayerVar, 0, len(dlVarByName))
	for _, dv := range dlVarByName {
		dlVars = append(dlVars, *dv)
	}
	slices.SortFunc(dlVars, func(a, b DataLayerVar) int {
		return strings.Compare(a.KeyPath, b.KeyPath)
	})
	slices.SortFunc(constants, func(a, b ConstantRef) int {
		return strings.Compare(a.VariableName, b.VariableName)
	})
	slices.SortStableFunc(events, func(a, b EventEntry) int {
		if a.EventName != b.EventName {
			return strings.Compare(a.EventName, b.EventName)
		}
		return strings.Compare(a.TriggerName, b.TriggerName)
	})

	plan := &Plan{
		AccountID:     snap.AccountID,
		ContainerID:   snap.ContainerID,
		WorkspaceID:   snap.WorkspaceID,
		LiveVersionID: snap.LiveVersionID,
		Target:        snap.TargetLabel,
		SnippetTarget: target,
		Events:        events,
		DataLayerVars: dlVars,
		Constants:     constants,
	}
	plan.Snippets = RenderSnippets(plan, target)
	return plan
}

// RenderMarkdown renders the complete tracking plan as documentation ready for devs and QA.
func (p *Plan) RenderMarkdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# GTM Tracking Plan\n\n")
	fmt.Fprintf(&sb, "- **Account / Container:** `%s` / `%s`\n", p.AccountID, p.ContainerID)
	fmt.Fprintf(&sb, "- **Source:** %s\n", p.Target)
	fmt.Fprintf(&sb, "- **Snippet Target:** `%s`\n\n", p.SnippetTarget)

	if len(p.Constants) > 0 {
		fmt.Fprintf(&sb, "## Configuration Constants\n\n| Variable | Value |\n|---|---|\n")
		for _, c := range p.Constants {
			fmt.Fprintf(&sb, "| `%s` | `%s` |\n", c.VariableName, c.Value)
		}
		fmt.Fprintf(&sb, "\n")
	}

	fmt.Fprintf(&sb, "## Event & Trigger Matrix\n\n")
	if len(p.Events) == 0 {
		fmt.Fprintf(&sb, "_No triggers found._\n\n")
	} else {
		fmt.Fprintf(&sb, "| Event / Lifecycle | Trigger | Type | Destinations | Consent | Data Layer Keys |\n|---|---|---|---|---|---|\n")
		for _, ev := range p.Events {
			var dests, consents []string
			for _, d := range ev.Destinations {
				label := fmt.Sprintf("%s (%s)", d.TagName, d.Platform)
				if d.Paused {
					label += " [PAUSED]"
				}
				dests = append(dests, label)
				if d.ConsentStatus != "" {
					if len(d.ConsentTypes) > 0 {
						consents = appendUnique(consents, fmt.Sprintf("%s: %s", d.ConsentStatus, strings.Join(d.ConsentTypes, ",")))
					} else {
						consents = appendUnique(consents, d.ConsentStatus)
					}
				}
			}
			destCell := "-"
			if len(dests) > 0 {
				destCell = strings.Join(dests, "<br>")
			}
			consentCell := "-"
			if len(consents) > 0 {
				consentCell = strings.Join(consents, "<br>")
			}
			dlCell := "-"
			if len(ev.DataLayerKeys) > 0 {
				dlCell = "`" + strings.Join(ev.DataLayerKeys, "`, `") + "`"
			}
			fmt.Fprintf(&sb, "| `%s` | %s (`%s`) | `%s` | %s | %s | %s |\n",
				ev.EventName, ev.TriggerName, ev.TriggerID, ev.TriggerType, destCell, consentCell, dlCell)
		}
		fmt.Fprintf(&sb, "\n")
	}

	if len(p.DataLayerVars) > 0 {
		fmt.Fprintf(&sb, "## Data Layer Variable Dictionary\n\n| Key Path | GTM Variable | Default | Used By |\n|---|---|---|---|\n")
		for _, dv := range p.DataLayerVars {
			def := "-"
			if dv.DefaultValue != "" {
				def = "`" + dv.DefaultValue + "`"
			}
			used := "-"
			if len(dv.UsedByTags) > 0 {
				used = strings.Join(dv.UsedByTags, ", ")
			}
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s |\n", dv.KeyPath, dv.VariableName, def, used)
		}
		fmt.Fprintf(&sb, "\n")
	}

	fmt.Fprintf(&sb, "## Application Snippets (`%s`)\n\n```%s\n%s\n```\n", p.SnippetTarget, codeFenceLang(p.SnippetTarget), p.Snippets)
	return sb.String()
}

// RenderSnippets renders application-side dataLayer integration code for target.
func RenderSnippets(plan *Plan, target SnippetTarget) string {
	customEvents := collectCustomEventNames(plan.Events)

	switch target {
	case TargetJS:
		var sb strings.Builder
		sb.WriteString("window.dataLayer = window.dataLayer || [];\n\n")
		sb.WriteString("function pushDataLayerEvent(event, payload = {}) {\n")
		sb.WriteString("  if (payload.ecommerce) window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({ event, ...payload });\n")
		sb.WriteString("}\n\n")
		for _, ev := range customEvents {
			fmt.Fprintf(&sb, "// Triggered event: %s\npushDataLayerEvent(%q, {});\n", ev, ev)
		}
		return strings.TrimSpace(sb.String())

	case TargetNextJS:
		var sb strings.Builder
		sb.WriteString("'use client';\n\n")
		sb.WriteString("export type TrackingEventName =\n")
		if len(customEvents) == 0 {
			sb.WriteString("  | string;\n\n")
		} else {
			for i, ev := range customEvents {
				sep := ""
				if i == len(customEvents)-1 {
					sep = ";"
				}
				fmt.Fprintf(&sb, "  | %q%s\n", ev, sep)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("declare global {\n  interface Window {\n    dataLayer?: Record<string, unknown>[];\n  }\n}\n\n")
		sb.WriteString("export function trackEvent(event: TrackingEventName, payload: Record<string, unknown> = {}) {\n")
		sb.WriteString("  if (typeof window === 'undefined') return;\n")
		sb.WriteString("  window.dataLayer = window.dataLayer || [];\n")
		sb.WriteString("  if ('ecommerce' in payload) window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({ event, ...payload });\n")
		sb.WriteString("}\n")
		return strings.TrimSpace(sb.String())

	case TargetShopify:
		var sb strings.Builder
		sb.WriteString("// Shopify Customer Events (Custom Pixel) -> Consent Mode v2 + GTM dataLayer bridge\n")
		sb.WriteString("window.dataLayer = window.dataLayer || [];\n")
		sb.WriteString("function gtag() { window.dataLayer.push(arguments); }\n\n")
		sb.WriteString("function applyCustomerPrivacyConsent(cp, command) {\n")
		sb.WriteString("  if (!cp) return;\n")
		sb.WriteString("  gtag('consent', command, {\n")
		sb.WriteString("    analytics_storage: cp.analyticsProcessingAllowed ? 'granted' : 'denied',\n")
		sb.WriteString("    ad_storage: cp.marketingAllowed ? 'granted' : 'denied',\n")
		sb.WriteString("    ad_user_data: cp.marketingAllowed ? 'granted' : 'denied',\n")
		sb.WriteString("    ad_personalization: cp.marketingAllowed ? 'granted' : 'denied'\n")
		sb.WriteString("  });\n}\n\n")
		sb.WriteString("applyCustomerPrivacyConsent(init && init.customerPrivacy, 'default');\n")
		sb.WriteString("if (customerPrivacy && typeof customerPrivacy.subscribe === 'function') {\n")
		sb.WriteString("  customerPrivacy.subscribe('visitorConsentCollected', (event) => {\n")
		sb.WriteString("    applyCustomerPrivacyConsent(event.customerPrivacy, 'update');\n")
		sb.WriteString("  });\n}\n\n")
		sb.WriteString("function pageContext(event) {\n")
		sb.WriteString("  const doc = event?.context?.document || {};\n")
		sb.WriteString("  return { page_location: doc.location?.href, page_title: doc.title, page_referrer: doc.referrer };\n")
		sb.WriteString("}\n\n")
		sb.WriteString("analytics.subscribe('page_viewed', (event) => {\n")
		sb.WriteString("  window.dataLayer.push({ event: 'shopify_page_view', ...pageContext(event) });\n});\n\n")
		sb.WriteString("analytics.subscribe('product_viewed', (event) => {\n")
		sb.WriteString("  const pv = event.data?.productVariant;\n")
		sb.WriteString("  window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({\n    event: 'view_item',\n    ...pageContext(event),\n    ecommerce: { currency: pv?.price?.currencyCode, value: pv?.price?.amount, items: [{ item_id: pv?.sku || pv?.id, item_name: pv?.product?.title, price: pv?.price?.amount, quantity: 1 }] }\n  });\n});\n\n")
		sb.WriteString("analytics.subscribe('product_added_to_cart', (event) => {\n")
		sb.WriteString("  const line = event.data?.cartLine;\n")
		sb.WriteString("  window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({\n    event: 'add_to_cart',\n    ...pageContext(event),\n    ecommerce: { currency: line?.cost?.totalAmount?.currencyCode, value: line?.cost?.totalAmount?.amount, items: [{ item_id: line?.merchandise?.sku || line?.merchandise?.id, item_name: line?.merchandise?.product?.title, price: line?.merchandise?.price?.amount, quantity: line?.quantity }] }\n  });\n});\n\n")
		sb.WriteString("analytics.subscribe('checkout_started', (event) => {\n")
		sb.WriteString("  const checkout = event.data?.checkout;\n")
		sb.WriteString("  window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({\n    event: 'begin_checkout',\n    ...pageContext(event),\n    ecommerce: { currency: checkout?.totalPrice?.currencyCode, value: checkout?.totalPrice?.amount, items: (checkout?.lineItems || []).map((li) => ({ item_id: li.variant?.sku || li.variant?.id, item_name: li.title, price: li.variant?.price?.amount, quantity: li.quantity })) }\n  });\n});\n\n")
		sb.WriteString("analytics.subscribe('payment_info_submitted', (event) => {\n")
		sb.WriteString("  const checkout = event.data?.checkout;\n")
		sb.WriteString("  window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({\n    event: 'add_payment_info',\n    ...pageContext(event),\n    ecommerce: { currency: checkout?.totalPrice?.currencyCode, value: checkout?.totalPrice?.amount, items: (checkout?.lineItems || []).map((li) => ({ item_id: li.variant?.sku || li.variant?.id, item_name: li.title, price: li.variant?.price?.amount, quantity: li.quantity })) }\n  });\n});\n\n")
		sb.WriteString("analytics.subscribe('checkout_completed', (event) => {\n")
		sb.WriteString("  const checkout = event.data?.checkout;\n")
		sb.WriteString("  window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({\n    event: 'purchase',\n    ...pageContext(event),\n    user_data: { email: checkout?.email, phone_number: checkout?.phone },\n    ecommerce: {\n      transaction_id: checkout?.order?.id,\n      value: checkout?.totalPrice?.amount,\n      currency: checkout?.totalPrice?.currencyCode,\n      items: (checkout?.lineItems || []).map((li) => ({ item_id: li.variant?.sku || li.variant?.id, item_name: li.title, price: li.variant?.price?.amount, quantity: li.quantity }))\n    }\n  });\n});\n")
		return strings.TrimSpace(sb.String())

	default: // TargetTS
		var sb strings.Builder
		sb.WriteString("declare global {\n  interface Window {\n    dataLayer?: Record<string, unknown>[];\n  }\n}\n\n")
		sb.WriteString("export type GTMCustomEvent =\n")
		if len(customEvents) == 0 {
			sb.WriteString("  | string;\n\n")
		} else {
			for i, ev := range customEvents {
				sep := ""
				if i == len(customEvents)-1 {
					sep = ";"
				}
				fmt.Fprintf(&sb, "  | %q%s\n", ev, sep)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("export interface DataLayerPayload {\n")
		for _, dv := range plan.DataLayerVars {
			fieldName := sanitizeTSIdentifier(dv.KeyPath)
			if fieldName != "" {
				fmt.Fprintf(&sb, "  %q?: string | number;\n", dv.KeyPath)
			}
		}
		sb.WriteString("  ecommerce?: Record<string, unknown> | null;\n")
		sb.WriteString("  [key: string]: unknown;\n")
		sb.WriteString("}\n\n")
		sb.WriteString("export function pushDataLayerEvent(event: GTMCustomEvent, payload: DataLayerPayload = {}): void {\n")
		sb.WriteString("  if (typeof window === 'undefined') return;\n")
		sb.WriteString("  window.dataLayer = window.dataLayer || [];\n")
		sb.WriteString("  if (payload.ecommerce) window.dataLayer.push({ ecommerce: null });\n")
		sb.WriteString("  window.dataLayer.push({ event, ...payload });\n")
		sb.WriteString("}\n")
		return strings.TrimSpace(sb.String())
	}
}

func indexTemplateDisplayNames(containerID string, templates []*tagmanager.CustomTemplate) map[string]string {
	out := make(map[string]string, len(templates))
	for _, tpl := range templates {
		if tpl == nil {
			continue
		}
		label := tpl.Name
		if tpl.GalleryReference != nil && tpl.GalleryReference.Owner != "" {
			label = fmt.Sprintf("%s (%s/%s)", tpl.Name, tpl.GalleryReference.Owner, tpl.GalleryReference.Repository)
		}
		if tpl.TemplateId != "" && containerID != "" {
			out["cvt_"+containerID+"_"+tpl.TemplateId] = label
		}
		if tpl.GalleryReference != nil && tpl.GalleryReference.GalleryTemplateId != "" {
			out["cvt_temp_"+tpl.GalleryReference.GalleryTemplateId] = label
		}
	}
	return out
}

func buildDestination(tg *tagmanager.Tag, tplDisplay map[string]string) DestinationEntry {
	dest := DestinationEntry{
		Platform: inferPlatform(tg, tplDisplay),
		TagID:    tg.TagId,
		TagName:  tg.Name,
		TagType:  tg.Type,
		Paused:   tg.Paused,
	}
	if tg.ConsentSettings != nil {
		dest.ConsentStatus = tg.ConsentSettings.ConsentStatus
		if tg.ConsentSettings.ConsentType != nil {
			for _, item := range tg.ConsentSettings.ConsentType.List {
				if item != nil && item.Value != "" {
					dest.ConsentTypes = append(dest.ConsentTypes, item.Value)
				}
			}
		}
	}
	for _, p := range tg.Parameter {
		if p != nil && p.Key != "" {
			dest.Parameters = append(dest.Parameters, p.Key)
		}
	}
	return dest
}

func inferPlatform(tg *tagmanager.Tag, tplDisplay map[string]string) string {
	switch tg.Type {
	case "googtag":
		return "Google Tag"
	case "gaawe":
		return "GA4"
	case "sgtmgaaw":
		return "GA4 (sGTM)"
	case "awct", "gclidw", "sp":
		return "Google Ads"
	case "baut":
		return "Microsoft Advertising UET"
	case "pntr":
		return "Pinterest Tag"
	case "html":
		return "Custom HTML"
	default:
		if label, ok := tplDisplay[tg.Type]; ok {
			lower := strings.ToLower(label)
			switch {
			case strings.Contains(lower, "stape-io/fb-tag"):
				return "Meta Pixel"
			case strings.Contains(lower, "stape-io/facebook-tag"):
				return "Meta CAPI (sGTM)"
			case strings.Contains(lower, "stape-io/tiktok-web-tag"), strings.Contains(lower, "tiktok/gtm-template-pixel"):
				return "TikTok Pixel"
			case strings.Contains(lower, "tiktok/gtm-template-eapi"):
				return "TikTok Events API (sGTM)"
			case strings.Contains(lower, "linkedin/linkedin-gtm-community-template"):
				return "LinkedIn Insight Tag"
			case strings.Contains(lower, "linkedin-developers/linkedin-capi-tag-template"):
				return "LinkedIn CAPI (sGTM)"
			case strings.Contains(lower, "microsoft/clarity-gtm-template"):
				return "Microsoft Clarity"
			case strings.Contains(lower, "pinterest/ws-gtm-template"):
				return "Pinterest Tag"
			default:
				return label
			}
		}
		return tg.Type
	}
}

func inferTriggerEvents(tr *tagmanager.Trigger) ([]string, string) {
	if tr.Type == "customEvent" && len(tr.CustomEventFilter) > 0 {
		for _, cond := range tr.CustomEventFilter {
			arg1 := paramVal(cond.Parameter, "arg1")
			if arg1 == "" {
				continue
			}
			if cond.Type == "matchRegex" {
				expanded := expandSimpleRegexList(arg1)
				if len(expanded) > 0 {
					return expanded, cond.Type + ": " + arg1
				}
			}
			return []string{arg1}, cond.Type + ": " + arg1
		}
	}
	switch tr.Type {
	case "pageview":
		return []string{"gtm.js (Page View)"}, ""
	case "domReady":
		return []string{"gtm.dom (DOM Ready)"}, ""
	case "windowLoaded":
		return []string{"gtm.load (Window Loaded)"}, ""
	case "init":
		return []string{"gtm.init (Initialization)"}, ""
	case "consentInit":
		return []string{"gtm.init_consent (Consent Initialization)"}, ""
	case "linkClick":
		return []string{"gtm.linkClick"}, ""
	case "click":
		return []string{"gtm.click"}, ""
	case "formSubmission":
		return []string{"gtm.formSubmit"}, ""
	default:
		return []string{tr.Type}, ""
	}
}

func expandSimpleRegexList(pattern string) []string {
	trimmed := strings.TrimSpace(pattern)
	trimmed = strings.TrimPrefix(trimmed, "^")
	trimmed = strings.TrimSuffix(trimmed, "$")
	trimmed = strings.TrimPrefix(trimmed, "(")
	trimmed = strings.TrimSuffix(trimmed, ")")
	if !strings.Contains(trimmed, "|") {
		return nil
	}
	parts := strings.Split(trimmed, "|")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || strings.ContainsAny(p, ".*+?[]{}()\\") {
			return nil
		}
		out = append(out, p)
	}
	return out
}

func collectCustomEventNames(events []EventEntry) []string {
	seen := make(map[string]bool)
	var out []string
	for _, ev := range events {
		if ev.TriggerType == "customEvent" && !strings.ContainsAny(ev.EventName, ".*+?[]{}()\\") && !seen[ev.EventName] {
			seen[ev.EventName] = true
			out = append(out, ev.EventName)
		}
	}
	return out
}

func extractVarRefs(v any) []string {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range varRefRe.FindAllStringSubmatch(string(raw), -1) {
		if len(m) == 2 {
			out = appendUnique(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

func isSecretVariableName(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range []string{"token", "secret", "password", "senha", "api_key", "apikey"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func paramVal(params []*tagmanager.Parameter, key string) string {
	for _, p := range params {
		if p != nil && p.Key == key {
			return p.Value
		}
	}
	return ""
}

func appendUnique(slice []string, item string) []string {
	if item == "" || slices.Contains(slice, item) {
		return slice
	}
	return append(slice, item)
}

func mapKeysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sanitizeTSIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func codeFenceLang(t SnippetTarget) string {
	switch t {
	case TargetJS, TargetShopify:
		return "javascript"
	default:
		return "typescript"
	}
}
