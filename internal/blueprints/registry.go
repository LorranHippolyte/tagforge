package blueprints

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	blueprintspecs "github.com/LorranHippolyte/tagforge/blueprints"
	"gopkg.in/yaml.v3"
)

// ForbiddenLegacyTypes blocks deprecated or invalid GTM entity types inside blueprints.
var ForbiddenLegacyTypes = map[string]bool{
	"gtagjs":       true,
	"gtagjs_event": true,
	"fbpixel":      true,
	"ua":           true,
	"gaawc":        true,
}

// Registry holds validated, embedded GTM blueprints indexed by ID.
type Registry struct {
	specs map[string]*Spec
	order []string
}

// NewDefaultRegistry loads and validates all official YAML blueprints embedded in the binary.
func NewDefaultRegistry() (*Registry, error) {
	return NewRegistryFromFS(blueprintspecs.FS)
}

// NewRegistryFromFS loads and validates all *.yaml blueprints from fsys.
func NewRegistryFromFS(fsys fs.FS) (*Registry, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read blueprints dir: %w", err)
	}

	reg := &Registry{
		specs: make(map[string]*Spec),
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read blueprint %s: %w", entry.Name(), err)
		}
		var spec Spec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			return nil, fmt.Errorf("parse blueprint %s: %w", entry.Name(), err)
		}
		if err := validateSpec(&spec); err != nil {
			return nil, fmt.Errorf("invalid blueprint %s: %w", entry.Name(), err)
		}
		if _, dup := reg.specs[spec.ID]; dup {
			return nil, fmt.Errorf("duplicate blueprint id %q", spec.ID)
		}
		reg.specs[spec.ID] = &spec
		reg.order = append(reg.order, spec.ID)
	}

	slices.Sort(reg.order)
	return reg, nil
}

// List returns all registered blueprints sorted by ID.
func (r *Registry) List() []*Spec {
	out := make([]*Spec, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.specs[id])
	}
	return out
}

// Get returns a blueprint by ID.
func (r *Registry) Get(id string) (*Spec, bool) {
	spec, ok := r.specs[strings.TrimSpace(id)]
	return spec, ok
}

func validateSpec(spec *Spec) error {
	if strings.TrimSpace(spec.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if spec.Context != "web" && spec.Context != "server" {
		return fmt.Errorf("context must be 'web' or 'server', got %q", spec.Context)
	}

	galleryAliases := make(map[string]bool, len(spec.GalleryTemplates))
	for _, gt := range spec.GalleryTemplates {
		if strings.TrimSpace(gt.Alias) == "" || strings.TrimSpace(gt.Owner) == "" || strings.TrimSpace(gt.Repository) == "" {
			return fmt.Errorf("gallery template must define alias, owner, and repository")
		}
		galleryAliases[gt.Alias] = true
	}

	folderKeys := make(map[string]bool, len(spec.Folders))
	for _, f := range spec.Folders {
		if strings.TrimSpace(f.Key) == "" || strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("folder must define key and name")
		}
		folderKeys[f.Key] = true
	}

	for _, v := range spec.Variables {
		if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Type) == "" {
			return fmt.Errorf("variable must define name and type")
		}
		if err := validateEntityType(v.Type, galleryAliases); err != nil {
			return fmt.Errorf("variable %q: %w", v.Name, err)
		}
		if err := validateFolderRef(v.ParentFolder, folderKeys); err != nil {
			return fmt.Errorf("variable %q: %w", v.Name, err)
		}
	}

	triggerKeys := make(map[string]bool, len(spec.Triggers))
	for _, tr := range spec.Triggers {
		if strings.TrimSpace(tr.Key) == "" || strings.TrimSpace(tr.Name) == "" || strings.TrimSpace(tr.Type) == "" {
			return fmt.Errorf("trigger must define key, name, and type")
		}
		if err := validateFolderRef(tr.ParentFolder, folderKeys); err != nil {
			return fmt.Errorf("trigger %q: %w", tr.Name, err)
		}
		triggerKeys[tr.Key] = true
	}

	for _, tg := range spec.Tags {
		if strings.TrimSpace(tg.Name) == "" || strings.TrimSpace(tg.Type) == "" {
			return fmt.Errorf("tag must define name and type")
		}
		if err := validateEntityType(tg.Type, galleryAliases); err != nil {
			return fmt.Errorf("tag %q: %w", tg.Name, err)
		}
		if err := validateFolderRef(tg.ParentFolder, folderKeys); err != nil {
			return fmt.Errorf("tag %q: %w", tg.Name, err)
		}
		for _, ref := range tg.FiringTriggers {
			if err := validateTriggerRef(ref, triggerKeys); err != nil {
				return fmt.Errorf("tag %q firing trigger: %w", tg.Name, err)
			}
		}
		for _, ref := range tg.BlockingTriggers {
			if err := validateTriggerRef(ref, triggerKeys); err != nil {
				return fmt.Errorf("tag %q blocking trigger: %w", tg.Name, err)
			}
		}
		if tg.Consent != nil {
			switch tg.Consent.Status {
			case "notSet", "notNeeded", "needed":
			default:
				return fmt.Errorf("tag %q invalid consent status %q", tg.Name, tg.Consent.Status)
			}
		}
	}

	return nil
}

func validateEntityType(entityType string, galleryAliases map[string]bool) error {
	if ForbiddenLegacyTypes[strings.ToLower(strings.TrimSpace(entityType))] {
		return fmt.Errorf("forbidden legacy/invalid GTM type %q", entityType)
	}
	if strings.HasPrefix(entityType, "template_ref:") {
		alias := strings.TrimPrefix(entityType, "template_ref:")
		if !galleryAliases[alias] {
			return fmt.Errorf("unknown gallery template alias %q", alias)
		}
	}
	return nil
}

func validateFolderRef(ref string, folderKeys map[string]bool) error {
	if ref == "" || !strings.HasPrefix(ref, "ref:") {
		return nil
	}
	key := strings.TrimPrefix(ref, "ref:")
	if !folderKeys[key] {
		return fmt.Errorf("unknown folder ref %q", ref)
	}
	return nil
}

func validateTriggerRef(ref string, triggerKeys map[string]bool) error {
	if !strings.HasPrefix(ref, "ref:") {
		return nil
	}
	key := strings.TrimPrefix(ref, "ref:")
	if !triggerKeys[key] {
		return fmt.Errorf("unknown trigger ref %q", ref)
	}
	return nil
}
