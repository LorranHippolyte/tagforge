# Official Declarative Blueprints

Declarative tracking blueprints live in this directory as YAML specifications embedded into the binary via `//go:embed` (`embed.go`).

## Included Official Blueprints (v1)

| ID | Context | Description |
|---|---|---|
| `google-tag-ga4-base` | `web` | Google Tag (`googtag`) + shared Event Settings (`gtes`) |
| `google-ga4-leadgen` | `web` | GA4 Lead Generation (`generate_lead`, `form_submit`, WhatsApp link clicks) |
| `google-ga4-ecommerce` | `web` | GA4 Recommended Ecommerce (`gaawe` with `sendEcommerceData: true`) |
| `google-ads-conversions` | `web` | Google Ads Conversion Linker (`gclidw`) + Conversion Tracking (`awct`) + Enhanced Conversions (`awec`) |
| `consent-mode-v2` | `web` | Google Consent Mode v2 baseline (`consentInit`) |
| `meta-pixel-web` | `web` | Sandboxed Stape Meta Pixel Web (`stape-io/fb-tag`) + Unique Event ID (`stape-io/unique-event-id-variable`) |
| `meta-capi-sgtm` | `server` | Server-Side Meta Conversions API (`stape-io/facebook-tag`) with `event_id` deduplication |

## Design Goals

1. **Vendor-accurate schemas:** Every tag, trigger, variable, and consent parameter must match official Google Tag Manager API v2, Google Ads, GA4, and Meta documentation (e.g., `googtag`, `gaawe`, `gtes`, `gclidw`, `awct`, `awec`, Community Gallery templates `cvt_*` — never legacy `gtagjs` or invalid `fbpixel` types).
2. **Plan before Apply:** Every blueprint supports a deterministic `plan` step that inspects the target container, skips or warns on existing components, displays the exact diff, and generates the matching `dataLayer.push` contract.
3. **Atomic Workspace Application:** Applying a blueprint runs inside a dedicated workspace using `workspace.bulkUpdate` with linked `firingTriggerId` references, centralized Constant variables, and vendor folders.
4. **Community Editable:** Keeping blueprints declarative in YAML/JSON enables tracking specialists to contribute or customize templates without writing Go.
