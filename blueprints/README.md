# Official Declarative Blueprints

Declarative tracking blueprints live in this directory as YAML specifications embedded into the binary via `//go:embed` (`embed.go`).

## Included Official Blueprints (16)

| ID | Context | Description |
|---|---|---|
| `google-tag-ga4-base` | `web` | Google Tag (`googtag`) + shared Event Settings (`gtes`) |
| `google-ga4-leadgen` | `web` | GA4 Lead Generation (`generate_lead`, `form_submit`, WhatsApp link clicks) |
| `google-ga4-ecommerce` | `web` | GA4 Recommended Ecommerce (`gaawe` with `sendEcommerceData: true`) |
| `google-ads-conversions` | `web` | Google Ads Conversion Linker (`gclidw`) + Conversion Tracking (`awct`) + Enhanced Conversions (`awec`) |
| `consent-mode-v2` | `web` | Google Consent Mode v2 baseline (`consentInit`) |
| `meta-pixel-web` | `web` | Sandboxed Stape Meta Pixel Web (`stape-io/fb-tag`) + Unique Event ID (`stape-io/unique-event-id-variable`) |
| `meta-capi-sgtm` | `server` | Server-Side Meta Conversions API (`stape-io/facebook-tag`) with `event_id` deduplication |
| `tiktok-pixel-web` | `web` | Sandboxed Stape TikTok Pixel Web (`stape-io/tiktok-web-tag`) + Unique Event ID deduplication |
| `tiktok-events-api-sgtm` | `server` | Official Server-Side TikTok Events API (`tiktok/gtm-template-eapi`) with `event_id` deduplication |
| `linkedin-insight-web` | `web` | Official LinkedIn Insight Tag 2.0 (`linkedin/linkedin-gtm-community-template`) + optional Conversion tracking |
| `linkedin-capi-sgtm` | `server` | Official Server-Side LinkedIn CAPI (`linkedin-developers/linkedin-capi-tag-template`) with `eventId` deduplication |
| `microsoft-clarity-web` | `web` | Official Microsoft Clarity (`microsoft/clarity-gtm-template`) with explicit `analytics_storage` consent |
| `microsoft-uet-web` | `web` | Built-in Microsoft Advertising UET (`baut`) for Page Load + Variable Revenue conversions |
| `pinterest-tag-web` | `web` | Official Pinterest Tag (`pinterest/ws-gtm-template`) with `useGA4Ecommerce: true` and `event_id` |
| `shopify-gtm-web` | `web` | Shopify Customer Events Custom Pixel pack (Consent Mode v2 + `gtes` `page_location` override + GA4 Ecommerce + optional Ads) |
| `shopify-sgtm-pack` | `server` | Shopify first-party sGTM GA4 Forwarder (`sgtmgaaw`) + CAPI composition playbook |

## Quick Usage

```bash
# List all 16 embedded blueprints
tagforge blueprints list

# Inspect required/optional parameters and dataLayer contract
tagforge blueprints show --name shopify-gtm-web

# Dry-run diff against a workspace (zero API mutations)
tagforge blueprints plan \
  --name google-ga4-ecommerce \
  --account 123456 --container 789012 --workspace 5 \
  --param measurement_id=G-XXXXXXXXXX

# Apply atomically into a new workspace and import Community Gallery templates
tagforge blueprints apply \
  --name meta-pixel-web \
  --account 123456 --container 789012 \
  --create-workspace "bp-meta-web" \
  --import-templates \
  --param pixel_id=123456789
```

*(Note: `--acknowledge-permissions` defaults to `true` when importing Community Template Gallery templates with `--import-templates`.)*

## Design Goals

1. **Vendor-accurate schemas:** Every tag, trigger, variable, and consent parameter must match official Google Tag Manager API v2, Google Ads, GA4, and Meta documentation (e.g., `googtag`, `gaawe`, `gtes`, `gclidw`, `awct`, `awec`, Community Gallery templates `cvt_*` — never legacy `gtagjs` or invalid `fbpixel` types).
2. **Plan before Apply:** Every blueprint supports a deterministic `plan` step that inspects the target container, skips or warns on existing components, displays the exact diff, and generates the matching `dataLayer.push` contract.
3. **Atomic Workspace Application:** Applying a blueprint runs inside a dedicated workspace using `workspace.bulkUpdate` with linked `firingTriggerId` references, centralized Constant variables, and vendor folders.
4. **Community Editable:** Keeping blueprints declarative in YAML allows tracking specialists to contribute or customize templates without writing Go.
