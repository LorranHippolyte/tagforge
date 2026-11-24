package mcpserver

import "sort"

// Guides holds vendor-accurate reference documentation returned by the gtm_guide tool.
var Guides = map[string]string{
	"safe_editing": `# Safe GTM Editing Workflow

1. Never edit the live container directly. Create or select a dedicated workspace (free GTM containers allow up to 3 workspaces).
2. tagforge runs in 'workspace-only' mode by default: reading, workspace edits, bulkUpdate, and create_version are permitted; live publishing and container deletion require '--mode full' + 'confirm: true'.
3. Every 'update' is a FULL REPLACE in GTM API v2. Always 'get' the resource first, preserve all unchanged fields, and pass back the current 'fingerprint'.
4. Prefer 'gtm_workspace' 'bulk_update' (or 'gtm_blueprint_apply') for multi-entity changes using temporary IDs "new_1", "new_2", ... so folders, triggers, and tags link atomically.
5. Before shipping, run 'gtm_workspace' 'status' (verify zero merge conflicts) and 'preview' (verify zero compiler errors).
6. Create a version ('create_version') with clear name and release notes, then get explicit human confirmation before calling 'gtm_version' 'publish'.`,

	"naming": `# GTM Naming Conventions

Use consistent names so containers remain auditable:
- Tags: "<Platform> - <Kind> - <Detail>" (e.g. "Google Tag - GA4", "GA4 - Event - generate_lead", "Meta - Pixel - PageView")
- Triggers: "<Type> - <Detail>" (e.g. "Init - All Pages", "CE - purchase", "Click - WhatsApp Link")
- Variables: "<Prefix> - <Detail>" (e.g. "CONST - GA4 Measurement ID", "DLV - ecommerce.value", "GTES - GA4 Shared Event Settings", "UPD - Enhanced Conversions User Data")
- Folders: Group entities by vendor/concern ("Google - GA4", "Google - Ads", "Privacy - Consent Mode v2", "Meta - Pixel Web").
- Store IDs, tokens, and endpoints in Constant ('c') variables instead of hard-coding across tags.`,

	"consent_mode_v2": `# Google Consent Mode v2

Required signals:
1. ad_storage (advertising cookies)
2. analytics_storage (analytics cookies)
3. ad_user_data (user data sent to Google for advertising)
4. ad_personalization (personalized ads / remarketing)

Implementation rules:
- Set defaults on the 'Consent Initialization - All Pages' ('consentInit') trigger so defaults apply before any Google or marketing tag executes.
- Use 'consentSettings.consentStatus = "needed"' with explicit 'consentType' list on non-Google tags (e.g. Meta Pixel, Custom HTML).
- Trigger 'gtag("consent", "update", {...})' from your CMP as soon as the user grants or denies consent.
- Use the 'consent-mode-v2' blueprint to deploy the official default baseline cleanly.`,

	"ga4_ecommerce": `# GA4 Recommended Ecommerce (GTM Web)

- Use official tag type 'gaawe' (never legacy 'gtagjs_event' or 'gaawc'), linked to a base 'googtag' tag.
- Enable 'sendEcommerceData: true' with 'getEcommerceDataFrom: "dataLayer"'.
- Always push '{ ecommerce: null }' before each new ecommerce event push to prevent item array leakage across events.
- Official event names: view_item_list, select_item, view_item, add_to_cart, remove_from_cart, view_cart, begin_checkout, add_shipping_info, add_payment_info, purchase.
- Use the 'google-ga4-ecommerce' blueprint to deploy this contract.`,

	"google_ads_enhanced": `# Google Ads Conversion Tracking + Enhanced Conversions

1. Deploy Conversion Linker ('gclidw') on Initialization - All Pages ('init') so gclid/wbraid/gbraid are stored in first-party cookies.
2. Create a User-Provided Data variable ('awec') mapping normalized customer email/phone from Data Layer ('user_data.email', 'user_data.phone_number').
3. Configure Conversion Tracking ('awct') with 'enableEnhancedConversion: true' and 'cssProvidedEnhancedConversionValue' pointing to your 'awec' variable, plus 'conversionValue', 'currencyCode', and 'orderId' for deduplication.
4. Use the 'google-ads-conversions' blueprint to deploy all components atomically.`,

	"meta_capi_dedup": `# Meta Pixel Web + Conversions API (sGTM) Deduplication

To deduplicate browser Pixel and Server CAPI events reliably:
1. Both sources MUST send the identical 'event_name' (e.g. Purchase, Lead, ViewContent) and identical 'event_id'.
2. Generate 'event_id' ONCE per browser event (via 'stape-io/unique-event-id-variable') — never generate separate random IDs on browser and server.
3. On Web GTM, use the sandboxed 'stape-io/fb-tag' gallery template (never raw unvetted HTML or non-existent 'fbpixel' type) with 'eventId' set to '{{VAR - Unique Event ID}}', and forward the same 'event_id' to sGTM via your GA4 Event Settings ('gtes').
4. On sGTM, use 'stape-io/facebook-tag', read 'event_id' from Event Data ('ed'), and map it into 'serverEventDataList' alongside '_fbp', '_fbc', and User Data.
5. Verify both Browser and Server events show "Deduplicated" in Meta Events Manager -> Test Events.`,

	"server_side": `# Server-Side Tagging (sGTM)

- Separate Web container (collects events & forwards to custom tagging domain) from Server container (enforces consent, enriches/strips PII via Transformations, and dispatches server-to-server CAPI hits).
- Inspect sGTM components via 'gtm_client' (e.g. GA4 client) and 'gtm_transformation' (allow/exclude parameters).
- Always strip test event codes ('CONST - Meta Test Event Code') before publishing to production.`,
}

// GuideTopics returns sorted available gtm_guide topics.
func GuideTopics() []string {
	topics := make([]string, 0, len(Guides))
	for k := range Guides {
		topics = append(topics, k)
	}
	sort.Strings(topics)
	return topics
}
