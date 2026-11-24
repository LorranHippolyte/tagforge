# Blueprints Contract (Planned for Phase 2)

Declarative tracking blueprints will live in this directory as YAML/JSON specifications embedded into the binary via `//go:embed`.

## Design Goals

1. **Vendor-accurate schemas:** Every tag, trigger, variable, and consent parameter must match official Google Tag Manager API v2, Google Ads, GA4, and Meta documentation (e.g., `googtag`, `gaawe`, `gtes`, `gclidw`, `awct`, `awec`, Community Gallery templates `cvt_*` — never legacy `gtagjs` or invalid `fbpixel` types).
2. **Plan before Apply:** Every blueprint supports a deterministic `plan` step that inspects the target container, skips or warns on existing components, displays the exact diff, and generates the matching `dataLayer.push` contract.
3. **Atomic Workspace Application:** Applying a blueprint runs inside a dedicated workspace using `workspace.bulkUpdate` with linked `firingTriggerId` references, centralized Constant variables, and vendor folders.
4. **Community Editable:** Keeping blueprints declarative in YAML/JSON enables tracking specialists to contribute or customize templates without writing Go.
