package blueprints

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"google.golang.org/api/tagmanager/v2"
)

// PlanInput specifies the blueprint, target GTM workspace, and parameter overrides.
type PlanInput struct {
	AccountID   string            `json:"accountId"`
	ContainerID string            `json:"containerId"`
	WorkspaceID string            `json:"workspaceId"`
	BlueprintID string            `json:"blueprintId"`
	Params      map[string]string `json:"params,omitempty"`
}

// PlannedAction describes a single step in a blueprint execution plan.
type PlannedAction struct {
	Kind       string `json:"kind"`
	EntityType string `json:"entityType"`
	Name       string `json:"name"`
	TempID     string `json:"tempId,omitempty"`
	Details    string `json:"details,omitempty"`
}

// PlanResult captures the dry-run diff, prerequisites, and dataLayer contract for a blueprint.
type PlanResult struct {
	BlueprintID             string                     `json:"blueprintId"`
	BlueprintName           string                     `json:"blueprintName"`
	Context                 string                     `json:"context"`
	ContainerID             string                     `json:"containerId"`
	WorkspaceID             string                     `json:"workspaceId"`
	ResolvedParams          map[string]string          `json:"resolvedParams,omitempty"`
	Actions                 []PlannedAction            `json:"actions"`
	MissingBuiltIns         []string                   `json:"missingBuiltIns,omitempty"`
	MissingGalleryTemplates []GalleryTemplateSpec      `json:"missingGalleryTemplates,omitempty"`
	ProposedChange          *tagmanager.ProposedChange `json:"proposedChange,omitempty"`
	DataLayerSnippets       []DataLayerSnippet         `json:"dataLayerSnippets,omitempty"`
	Warnings                []string                   `json:"warnings,omitempty"`
	rawChange               *tagmanager.ProposedChange // unredacted changes used strictly by Apply
	skippedExisting         int
}

// ApplyInput configures atomic execution of a blueprint into a GTM workspace.
type ApplyInput struct {
	PlanInput
	CreateWorkspaceName            string `json:"createWorkspaceName,omitempty"`
	ImportGalleryTemplates         bool   `json:"importGalleryTemplates,omitempty"`
	AcknowledgeTemplatePermissions bool   `json:"acknowledgeTemplatePermissions,omitempty"`
}

// ApplyResult summarizes the changes applied to GTM.
type ApplyResult struct {
	BlueprintID       string             `json:"blueprintId"`
	WorkspaceID       string             `json:"workspaceId"`
	CreatedWorkspace  bool               `json:"createdWorkspace"`
	ImportedTemplates []string           `json:"importedTemplates,omitempty"`
	EnabledBuiltIns   []string           `json:"enabledBuiltIns,omitempty"`
	AppliedChanges    int                `json:"appliedChanges"`
	SkippedExisting   int                `json:"skippedExisting"`
	DataLayerSnippets []DataLayerSnippet `json:"dataLayerSnippets,omitempty"`
}

// Plan performs a read-only inspection of the target workspace and builds a deterministic
// bulk-update plan without mutating GTM. Secret parameter values are redacted in PlanResult.
func Plan(ctx context.Context, client *gtm.Client, reg *Registry, input PlanInput) (*PlanResult, error) {
	if client == nil {
		return nil, errors.New("gtm client is required")
	}
	if reg == nil {
		return nil, errors.New("blueprint registry is required")
	}
	spec, ok := reg.Get(input.BlueprintID)
	if !ok {
		return nil, fmt.Errorf("unknown blueprint %q", input.BlueprintID)
	}
	if strings.TrimSpace(input.AccountID) == "" || strings.TrimSpace(input.ContainerID) == "" || strings.TrimSpace(input.WorkspaceID) == "" {
		return nil, fmt.Errorf("%w: accountId, containerId, and workspaceId are required", gtm.ErrValidation)
	}

	rawVals, displayVals, err := resolveParams(spec, input.Params)
	if err != nil {
		return nil, err
	}

	builtIns, err := client.ListBuiltInVariables(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, gtm.ListOptions{Fresh: true})
	if err != nil {
		return nil, err
	}
	templates, err := client.ListTemplates(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, gtm.ListOptions{Fresh: true})
	if err != nil {
		return nil, err
	}
	folders, err := client.ListFolders(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, gtm.ListOptions{Fresh: true})
	if err != nil {
		return nil, err
	}
	variables, err := client.ListVariables(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, gtm.ListOptions{Fresh: true})
	if err != nil {
		return nil, err
	}
	triggers, err := client.ListTriggers(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, gtm.ListOptions{Fresh: true})
	if err != nil {
		return nil, err
	}
	tags, err := client.ListTags(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, gtm.ListOptions{Fresh: true})
	if err != nil {
		return nil, err
	}

	existingBuiltIns := make(map[string]bool, len(builtIns))
	for _, bv := range builtIns {
		existingBuiltIns[strings.ToLower(strings.TrimSpace(bv.Type))] = true
	}

	existingTemplates := make(map[string]*tagmanager.CustomTemplate, len(templates))
	for _, tpl := range templates {
		if tpl.GalleryReference != nil {
			key := galleryKey(tpl.GalleryReference.Owner, tpl.GalleryReference.Repository)
			existingTemplates[key] = tpl
		}
	}

	existingFolders := make(map[string]string, len(folders))
	for _, f := range folders {
		existingFolders[normName(f.Name)] = f.FolderId
	}
	existingVariables := make(map[string]string, len(variables))
	for _, v := range variables {
		existingVariables[normName(v.Name)] = v.VariableId
	}
	existingTriggers := make(map[string]string, len(triggers))
	for _, tr := range triggers {
		existingTriggers[normName(tr.Name)] = tr.TriggerId
	}
	existingTags := make(map[string]string, len(tags))
	for _, tg := range tags {
		existingTags[normName(tg.Name)] = tg.TagId
	}

	result := &PlanResult{
		BlueprintID:       spec.ID,
		BlueprintName:     spec.Name,
		Context:           spec.Context,
		ContainerID:       input.ContainerID,
		WorkspaceID:       input.WorkspaceID,
		ResolvedParams:    displayVals,
		DataLayerSnippets: spec.DataLayerSnippet,
	}

	for _, bvType := range spec.BuiltInVariables {
		if existingBuiltIns[strings.ToLower(strings.TrimSpace(bvType))] {
			result.Actions = append(result.Actions, PlannedAction{
				Kind:       "skip_existing",
				EntityType: "builtin_variable",
				Name:       bvType,
				Details:    "built-in variable already enabled",
			})
			continue
		}
		result.MissingBuiltIns = append(result.MissingBuiltIns, bvType)
		result.Actions = append(result.Actions, PlannedAction{
			Kind:       "enable_builtin",
			EntityType: "builtin_variable",
			Name:       bvType,
		})
	}

	aliasToType := make(map[string]string, len(spec.GalleryTemplates))
	for _, gt := range spec.GalleryTemplates {
		key := galleryKey(gt.Owner, gt.Repository)
		if tpl, found := existingTemplates[key]; found {
			resolvedType := fmt.Sprintf("cvt_%s_%s", input.ContainerID, tpl.TemplateId)
			aliasToType[gt.Alias] = resolvedType
			result.Actions = append(result.Actions, PlannedAction{
				Kind:       "reuse_template",
				EntityType: "custom_template",
				Name:       gt.Owner + "/" + gt.Repository,
				Details:    "resolved type " + resolvedType,
			})
			continue
		}
		pendingType := fmt.Sprintf("cvt_%s_pending_%s", input.ContainerID, gt.Alias)
		aliasToType[gt.Alias] = pendingType
		result.MissingGalleryTemplates = append(result.MissingGalleryTemplates, gt)
		result.Actions = append(result.Actions, PlannedAction{
			Kind:       "import_template",
			EntityType: "custom_template",
			Name:       gt.Owner + "/" + gt.Repository,
			Details:    "requires import_from_gallery before bulk_update",
		})
	}
	if len(result.MissingGalleryTemplates) > 0 {
		result.Warnings = append(result.Warnings,
			"Missing Community Template Gallery template(s): pass --import-templates (or importGalleryTemplates: true) during apply to import them automatically.")
	}

	displayActions, displayChange, skipped, err := buildWorkspacePlan(
		spec, rawVals, displayVals, aliasToType,
		existingFolders, existingVariables, existingTriggers, existingTags,
	)
	if err != nil {
		return nil, err
	}
	_, rawChange, _, err := buildWorkspacePlan(
		spec, rawVals, rawVals, aliasToType,
		existingFolders, existingVariables, existingTriggers, existingTags,
	)
	if err != nil {
		return nil, err
	}

	result.Actions = append(result.Actions, displayActions...)
	result.ProposedChange = displayChange
	result.rawChange = rawChange
	result.skippedExisting = skipped
	return result, nil
}

// Apply executes a blueprint atomically in GTM after checking safety policies and resolving
// gallery templates and built-in variables.
func Apply(ctx context.Context, client *gtm.Client, reg *Registry, input ApplyInput) (*ApplyResult, error) {
	if client == nil {
		return nil, errors.New("gtm client is required")
	}
	if reg == nil {
		return nil, errors.New("blueprint registry is required")
	}
	if input.CreateWorkspaceName != "" {
		if err := client.Policy().Check(safety.Request{Resource: safety.ResourceWorkspace, Action: safety.ActionCreate}); err != nil {
			return nil, err
		}
	}
	if err := client.Policy().Check(safety.Request{Resource: safety.ResourceWorkspace, Action: safety.ActionBulkUpdate}); err != nil {
		return nil, err
	}

	spec, ok := reg.Get(input.BlueprintID)
	if !ok {
		return nil, fmt.Errorf("unknown blueprint %q", input.BlueprintID)
	}
	if _, _, err := resolveParams(spec, input.Params); err != nil {
		return nil, err
	}

	res := &ApplyResult{
		BlueprintID:       spec.ID,
		WorkspaceID:       input.WorkspaceID,
		DataLayerSnippets: spec.DataLayerSnippet,
	}

	if strings.TrimSpace(input.CreateWorkspaceName) != "" {
		ws, err := client.CreateWorkspace(ctx, input.AccountID, input.ContainerID, &tagmanager.Workspace{
			Name:        input.CreateWorkspaceName,
			Description: "Created by tagforge blueprint " + spec.ID,
		}, gtm.CreateOptions{})
		if err != nil {
			return nil, err
		}
		input.WorkspaceID = ws.WorkspaceId
		res.WorkspaceID = ws.WorkspaceId
		res.CreatedWorkspace = true
	}

	plan, err := Plan(ctx, client, reg, input.PlanInput)
	if err != nil {
		return nil, err
	}

	if len(plan.MissingGalleryTemplates) > 0 {
		if !input.ImportGalleryTemplates {
			names := make([]string, 0, len(plan.MissingGalleryTemplates))
			for _, gt := range plan.MissingGalleryTemplates {
				names = append(names, gt.Owner+"/"+gt.Repository)
			}
			return nil, fmt.Errorf("blueprint %q requires Community Gallery template(s) [%s]; pass --import-templates (or importGalleryTemplates: true) to import them",
				spec.ID, strings.Join(names, ", "))
		}
		for _, gt := range plan.MissingGalleryTemplates {
			ack := gt.AcknowledgePermissions || input.AcknowledgeTemplatePermissions
			if _, err := client.ImportTemplateFromGallery(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, gtm.ImportGalleryTemplateOptions{
				Owner:                  gt.Owner,
				Repository:             gt.Repository,
				SHA:                    gt.SHA,
				AcknowledgePermissions: ack,
			}); err != nil {
				return nil, fmt.Errorf("import gallery template %s/%s: %w", gt.Owner, gt.Repository, err)
			}
			res.ImportedTemplates = append(res.ImportedTemplates, gt.Owner+"/"+gt.Repository)
		}

		// Re-plan so the newly imported template IDs resolve to real cvt_<container>_<templateId> types.
		plan, err = Plan(ctx, client, reg, input.PlanInput)
		if err != nil {
			return nil, err
		}
	}

	if len(plan.MissingBuiltIns) > 0 {
		if _, err := client.EnableBuiltInVariables(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, plan.MissingBuiltIns); err != nil {
			return nil, err
		}
		res.EnabledBuiltIns = append(res.EnabledBuiltIns, plan.MissingBuiltIns...)
	}

	if plan.rawChange != nil && len(plan.rawChange.Changes) > 0 {
		if _, err := client.BulkUpdateWorkspace(ctx, input.AccountID, input.ContainerID, input.WorkspaceID, plan.rawChange); err != nil {
			return nil, err
		}
		res.AppliedChanges = len(plan.rawChange.Changes)
	}
	res.SkippedExisting = plan.skippedExisting

	return res, nil
}

func resolveParams(spec *Spec, provided map[string]string) (raw map[string]string, redacted map[string]string, err error) {
	raw = make(map[string]string, len(spec.Params))
	redacted = make(map[string]string, len(spec.Params))
	known := make(map[string]ParamSpec, len(spec.Params))

	for _, p := range spec.Params {
		known[p.Name] = p
		val := p.Default
		if v, exists := provided[p.Name]; exists {
			val = v
		}
		if p.Required && strings.TrimSpace(val) == "" {
			return nil, nil, fmt.Errorf("%w: blueprint %q requires parameter %q (%s)", gtm.ErrValidation, spec.ID, p.Name, p.Description)
		}
		if p.Pattern != "" && strings.TrimSpace(val) != "" {
			re, compErr := regexp.Compile(p.Pattern)
			if compErr != nil {
				return nil, nil, fmt.Errorf("invalid regex pattern on param %q: %w", p.Name, compErr)
			}
			if !re.MatchString(val) {
				return nil, nil, fmt.Errorf("%w: parameter %q value does not match pattern %s", gtm.ErrValidation, p.Name, p.Pattern)
			}
		}
		raw[p.Name] = val
		if p.Secret && strings.TrimSpace(val) != "" {
			redacted[p.Name] = "[REDACTED]"
		} else {
			redacted[p.Name] = val
		}
	}

	for k := range provided {
		if _, ok := known[k]; !ok {
			return nil, nil, fmt.Errorf("%w: unknown parameter %q for blueprint %q", gtm.ErrValidation, k, spec.ID)
		}
	}

	return raw, redacted, nil
}

func buildWorkspacePlan(
	spec *Spec,
	condVals map[string]string,
	interpVals map[string]string,
	aliasToType map[string]string,
	existingFolders map[string]string,
	existingVariables map[string]string,
	existingTriggers map[string]string,
	existingTags map[string]string,
) ([]PlannedAction, *tagmanager.ProposedChange, int, error) {
	var actions []PlannedAction
	change := &tagmanager.ProposedChange{}
	skipped := 0
	nextIdx := 1
	nextTempID := func() string {
		id := fmt.Sprintf("new_%d", nextIdx)
		nextIdx++
		return id
	}

	folderRefs := make(map[string]string, len(spec.Folders))
	for _, f := range spec.Folders {
		name := interpolate(f.Name, interpVals)
		if existID, ok := existingFolders[normName(name)]; ok {
			folderRefs["ref:"+f.Key] = existID
			skipped++
			actions = append(actions, PlannedAction{
				Kind:       "skip_existing",
				EntityType: "folder",
				Name:       name,
				Details:    "reusing existing folder ID " + existID,
			})
			continue
		}
		tempID := nextTempID()
		folderRefs["ref:"+f.Key] = tempID
		change.Changes = append(change.Changes, &tagmanager.Entity{
			ChangeStatus: "added",
			Folder: &tagmanager.Folder{
				FolderId: tempID,
				Name:     name,
				Notes:    interpolate(f.Notes, interpVals),
			},
		})
		actions = append(actions, PlannedAction{
			Kind:       "create_folder",
			EntityType: "folder",
			Name:       name,
			TempID:     tempID,
		})
	}

	for _, v := range spec.Variables {
		if !shouldInclude(v.WhenParamNonEmpty, condVals) {
			continue
		}
		name := interpolate(v.Name, interpVals)
		if existID, ok := existingVariables[normName(name)]; ok {
			skipped++
			actions = append(actions, PlannedAction{
				Kind:       "skip_existing",
				EntityType: "variable",
				Name:       name,
				Details:    "reusing existing variable ID " + existID,
			})
			continue
		}
		tempID := nextTempID()
		resolvedType := resolveTemplateType(v.Type, aliasToType)
		change.Changes = append(change.Changes, &tagmanager.Entity{
			ChangeStatus: "added",
			Variable: &tagmanager.Variable{
				VariableId:     tempID,
				Name:           name,
				Type:           resolvedType,
				Notes:          interpolate(v.Notes, interpVals),
				ParentFolderId: resolveRef(v.ParentFolder, folderRefs),
				Parameter:      buildParameters(v.Parameters, condVals, interpVals),
			},
		})
		actions = append(actions, PlannedAction{
			Kind:       "create_variable",
			EntityType: "variable",
			Name:       name,
			TempID:     tempID,
			Details:    "type=" + resolvedType,
		})
	}

	triggerRefs := make(map[string]string, len(spec.Triggers))
	for _, tr := range spec.Triggers {
		if !shouldInclude(tr.WhenParamNonEmpty, condVals) {
			continue
		}
		name := interpolate(tr.Name, interpVals)
		if existID, ok := existingTriggers[normName(name)]; ok {
			triggerRefs["ref:"+tr.Key] = existID
			skipped++
			actions = append(actions, PlannedAction{
				Kind:       "skip_existing",
				EntityType: "trigger",
				Name:       name,
				Details:    "reusing existing trigger ID " + existID,
			})
			continue
		}
		tempID := nextTempID()
		triggerRefs["ref:"+tr.Key] = tempID
		change.Changes = append(change.Changes, &tagmanager.Entity{
			ChangeStatus: "added",
			Trigger: &tagmanager.Trigger{
				TriggerId:         tempID,
				Name:              name,
				Type:              tr.Type,
				Notes:             interpolate(tr.Notes, interpVals),
				ParentFolderId:    resolveRef(tr.ParentFolder, folderRefs),
				Filter:            buildConditions(tr.Filters, condVals, interpVals),
				CustomEventFilter: buildConditions(tr.CustomEventFilters, condVals, interpVals),
				AutoEventFilter:   buildConditions(tr.AutoEventFilters, condVals, interpVals),
				Parameter:         buildParameters(tr.Parameters, condVals, interpVals),
			},
		})
		actions = append(actions, PlannedAction{
			Kind:       "create_trigger",
			EntityType: "trigger",
			Name:       name,
			TempID:     tempID,
			Details:    "type=" + tr.Type,
		})
	}

	for _, tg := range spec.Tags {
		if !shouldInclude(tg.WhenParamNonEmpty, condVals) {
			continue
		}
		name := interpolate(tg.Name, interpVals)
		if existID, ok := existingTags[normName(name)]; ok {
			skipped++
			actions = append(actions, PlannedAction{
				Kind:       "skip_existing",
				EntityType: "tag",
				Name:       name,
				Details:    "existing tag ID " + existID,
			})
			continue
		}
		tempID := nextTempID()
		resolvedType := resolveTemplateType(tg.Type, aliasToType)
		tagObj := &tagmanager.Tag{
			TagId:             tempID,
			Name:              name,
			Type:              resolvedType,
			Notes:             interpolate(tg.Notes, interpVals),
			ParentFolderId:    resolveRef(tg.ParentFolder, folderRefs),
			FiringTriggerId:   resolveRefSlice(tg.FiringTriggers, triggerRefs),
			BlockingTriggerId: resolveRefSlice(tg.BlockingTriggers, triggerRefs),
			TagFiringOption:   tg.TagFiringOption,
			Parameter:         buildParameters(tg.Parameters, condVals, interpVals),
		}
		if tg.Consent != nil {
			consent := &tagmanager.TagConsentSetting{
				ConsentStatus: tg.Consent.Status,
			}
			if len(tg.Consent.Types) > 0 {
				items := make([]*tagmanager.Parameter, 0, len(tg.Consent.Types))
				for _, ct := range tg.Consent.Types {
					items = append(items, &tagmanager.Parameter{
						Type:  "TEMPLATE",
						Value: ct,
					})
				}
				consent.ConsentType = &tagmanager.Parameter{
					Type: "LIST",
					List: items,
				}
			}
			tagObj.ConsentSettings = consent
		}
		change.Changes = append(change.Changes, &tagmanager.Entity{
			ChangeStatus: "added",
			Tag:          tagObj,
		})
		actions = append(actions, PlannedAction{
			Kind:       "create_tag",
			EntityType: "tag",
			Name:       name,
			TempID:     tempID,
			Details:    "type=" + resolvedType,
		})
	}

	return actions, change, skipped, nil
}

func shouldInclude(whenNonEmpty string, condVals map[string]string) bool {
	if strings.TrimSpace(whenNonEmpty) == "" {
		return true
	}
	return strings.TrimSpace(condVals[whenNonEmpty]) != ""
}

func interpolate(s string, vals map[string]string) string {
	out := s
	for k, v := range vals {
		out = strings.ReplaceAll(out, "${"+k+"}", v)
	}
	return out
}

func buildParameters(specs []ParameterSpec, condVals, interpVals map[string]string) []*tagmanager.Parameter {
	if len(specs) == 0 {
		return nil
	}
	var out []*tagmanager.Parameter
	for _, p := range specs {
		if !shouldInclude(p.WhenParamNonEmpty, condVals) {
			continue
		}
		param := &tagmanager.Parameter{
			Key:   p.Key,
			Type:  p.Type,
			Value: interpolate(p.Value, interpVals),
		}
		if len(p.List) > 0 {
			param.List = buildParameters(p.List, condVals, interpVals)
		}
		if len(p.Map) > 0 {
			param.Map = buildParameters(p.Map, condVals, interpVals)
		}
		out = append(out, param)
	}
	return out
}

func buildConditions(specs []ConditionSpec, condVals, interpVals map[string]string) []*tagmanager.Condition {
	if len(specs) == 0 {
		return nil
	}
	out := make([]*tagmanager.Condition, 0, len(specs))
	for _, c := range specs {
		out = append(out, &tagmanager.Condition{
			Type:      c.Type,
			Parameter: buildParameters(c.Parameters, condVals, interpVals),
		})
	}
	return out
}

func resolveTemplateType(raw string, aliasToType map[string]string) string {
	if strings.HasPrefix(raw, "template_ref:") {
		alias := strings.TrimPrefix(raw, "template_ref:")
		if resolved, ok := aliasToType[alias]; ok {
			return resolved
		}
	}
	return raw
}

func resolveRef(ref string, refs map[string]string) string {
	if ref == "" {
		return ""
	}
	if resolved, ok := refs[ref]; ok {
		return resolved
	}
	return ref
}

func resolveRefSlice(in []string, refs map[string]string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, resolveRef(item, refs))
	}
	return out
}

func normName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func galleryKey(owner, repo string) string {
	return strings.ToLower(strings.TrimSpace(owner)) + "/" + strings.ToLower(strings.TrimSpace(repo))
}
