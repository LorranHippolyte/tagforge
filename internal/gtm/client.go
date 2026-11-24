package gtm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/safety"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/tagmanager/v2"
)

// ClientConfig configures a safe, rate-limited GTM API v2 Client.
type ClientConfig struct {
	Service       *tagmanager.Service
	TokenSource   oauth2.TokenSource
	ClientOptions []option.ClientOption
	Policy        *safety.Policy
	Scheduler     *Scheduler
	Cache         *SnapshotCache
}

// ListOptions controls read caching behavior.
type ListOptions struct {
	Fresh bool
}

// CreateOptions controls creation safeguards such as duplicate detection.
type CreateOptions struct {
	AllowDuplicate bool
}

// CreateVersionOptions sets metadata when creating a container version from a workspace.
type CreateVersionOptions struct {
	Name  string
	Notes string
}

// Client wraps Google Tag Manager API v2 with safety checks, rate limiting, and snapshot caching.
type Client struct {
	svc       *tagmanager.Service
	policy    *safety.Policy
	scheduler *Scheduler
	cache     *SnapshotCache
}

// NewClient initializes a GTM API v2 Client.
func NewClient(ctx context.Context, cfg ClientConfig) (*Client, error) {
	svc := cfg.Service
	if svc == nil {
		var opts []option.ClientOption
		if cfg.TokenSource != nil {
			opts = append(opts, option.WithTokenSource(cfg.TokenSource))
		}
		opts = append(opts, cfg.ClientOptions...)
		var err error
		svc, err = tagmanager.NewService(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("create tagmanager v2 service: %w", err)
		}
	}

	policy := cfg.Policy
	if policy == nil {
		policy = safety.DefaultPolicy()
	}
	scheduler := cfg.Scheduler
	if scheduler == nil {
		scheduler = NewScheduler(SchedulerOptions{})
	}
	cache := cfg.Cache
	if cache == nil {
		cache = NewSnapshotCache(true, DefaultCacheTTL)
	}

	return &Client{
		svc:       svc,
		policy:    policy,
		scheduler: scheduler,
		cache:     cache,
	}, nil
}

// Policy returns the active safety policy.
func (c *Client) Policy() *safety.Policy {
	return c.policy
}

func (c *Client) check(resource safety.Resource, action safety.Action, confirm bool) error {
	return c.policy.Check(safety.Request{
		Resource: resource,
		Action:   action,
		Confirm:  confirm,
	})
}

func requireNonEmpty(name, val string) error {
	if strings.TrimSpace(val) == "" {
		return fmt.Errorf("%w: %s is required", ErrValidation, name)
	}
	return nil
}

func requireFingerprint(fingerprint string) error {
	if strings.TrimSpace(fingerprint) == "" {
		return fmt.Errorf("%w: fingerprint is required for optimistic concurrency control (fetch the resource with 'get' first)", ErrValidation)
	}
	return nil
}

// --- Accounts ---

func (c *Client) ListAccounts(ctx context.Context, opts ListOptions) ([]*tagmanager.Account, error) {
	if err := c.check(safety.ResourceAccount, safety.ActionRead, false); err != nil {
		return nil, err
	}
	cacheKey := "accounts"
	if !opts.Fresh {
		var cached []*tagmanager.Account
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Account
	var pageToken string
	for {
		var resp *tagmanager.ListAccountsResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.List().Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Account...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetAccount(ctx context.Context, accountID string) (*tagmanager.Account, error) {
	if err := c.check(safety.ResourceAccount, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	var out *tagmanager.Account
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Get(accountPath(accountID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

// --- Containers ---

func (c *Client) ListContainers(ctx context.Context, accountID string, opts ListOptions) ([]*tagmanager.Container, error) {
	if err := c.check(safety.ResourceContainer, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	cacheKey := accountPath(accountID) + "/containers"
	if !opts.Fresh {
		var cached []*tagmanager.Container
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Container
	var pageToken string
	for {
		var resp *tagmanager.ListContainersResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.List(accountPath(accountID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Container...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetContainer(ctx context.Context, accountID, containerID string) (*tagmanager.Container, error) {
	if err := c.check(safety.ResourceContainer, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	var out *tagmanager.Container
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Get(containerPath(accountID, containerID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) LookupContainer(ctx context.Context, publicOrTagID string) (*tagmanager.Container, error) {
	if err := c.check(safety.ResourceContainer, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("publicId", publicOrTagID); err != nil {
		return nil, err
	}
	var out *tagmanager.Container
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Lookup().TagId(publicOrTagID).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) GetContainerSnippet(ctx context.Context, accountID, containerID string) (*tagmanager.GetContainerSnippetResponse, error) {
	if err := c.check(safety.ResourceContainer, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	var out *tagmanager.GetContainerSnippetResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Snippet(containerPath(accountID, containerID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

// --- Workspaces ---

func (c *Client) ListWorkspaces(ctx context.Context, accountID, containerID string, opts ListOptions) ([]*tagmanager.Workspace, error) {
	if err := c.check(safety.ResourceWorkspace, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	cacheKey := containerCachePrefix(accountID, containerID) + "workspaces"
	if !opts.Fresh {
		var cached []*tagmanager.Workspace
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Workspace
	var pageToken string
	for {
		var resp *tagmanager.ListWorkspacesResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.List(containerPath(accountID, containerID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Workspace...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetWorkspace(ctx context.Context, accountID, containerID, workspaceID string) (*tagmanager.Workspace, error) {
	if err := c.check(safety.ResourceWorkspace, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	var out *tagmanager.Workspace
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Get(workspacePath(accountID, containerID, workspaceID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) CreateWorkspace(ctx context.Context, accountID, containerID string, ws *tagmanager.Workspace, opts CreateOptions) (*tagmanager.Workspace, error) {
	if err := c.check(safety.ResourceWorkspace, safety.ActionCreate, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	if ws == nil || strings.TrimSpace(ws.Name) == "" {
		return nil, fmt.Errorf("%w: workspace name is required", ErrValidation)
	}

	existing, err := c.ListWorkspaces(ctx, accountID, containerID, ListOptions{Fresh: true})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(existing))
	for _, w := range existing {
		names = append(names, w.Name)
	}
	if err := safety.CheckDuplicateName("workspace", names, ws.Name, opts.AllowDuplicate); err != nil {
		return nil, err
	}

	var out *tagmanager.Workspace
	err = c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Create(containerPath(accountID, containerID), ws).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateContainer(accountID, containerID)
	}
	return out, err
}

func (c *Client) GetWorkspaceStatus(ctx context.Context, accountID, containerID, workspaceID string) (*tagmanager.GetWorkspaceStatusResponse, error) {
	if err := c.check(safety.ResourceWorkspace, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	var out *tagmanager.GetWorkspaceStatusResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.GetStatus(workspacePath(accountID, containerID, workspaceID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) SyncWorkspace(ctx context.Context, accountID, containerID, workspaceID string) (*tagmanager.SyncWorkspaceResponse, error) {
	if err := c.check(safety.ResourceWorkspace, safety.ActionSync, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	var out *tagmanager.SyncWorkspaceResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Sync(workspacePath(accountID, containerID, workspaceID)).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) QuickPreviewWorkspace(ctx context.Context, accountID, containerID, workspaceID string) (*tagmanager.QuickPreviewResponse, error) {
	if err := c.check(safety.ResourceWorkspace, safety.ActionPreview, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	var out *tagmanager.QuickPreviewResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.QuickPreview(workspacePath(accountID, containerID, workspaceID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) BulkUpdateWorkspace(ctx context.Context, accountID, containerID, workspaceID string, change *tagmanager.ProposedChange) (*tagmanager.BulkUpdateWorkspaceResponse, error) {
	if err := c.check(safety.ResourceWorkspace, safety.ActionBulkUpdate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if change == nil || len(change.Changes) == 0 {
		return nil, fmt.Errorf("%w: proposed change set cannot be empty", ErrValidation)
	}
	var out *tagmanager.BulkUpdateWorkspaceResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.BulkUpdate(workspacePath(accountID, containerID, workspaceID), change).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

// CreateVersion creates a container version after verifying the workspace has changes
// and zero unresolved merge conflicts.
func (c *Client) CreateVersion(ctx context.Context, accountID, containerID, workspaceID string, opts CreateVersionOptions) (*tagmanager.CreateContainerVersionResponse, error) {
	if err := c.check(safety.ResourceVersion, safety.ActionCreateVersion, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("version name", opts.Name); err != nil {
		return nil, err
	}

	status, err := c.GetWorkspaceStatus(ctx, accountID, containerID, workspaceID)
	if err != nil {
		return nil, err
	}
	if len(status.MergeConflict) > 0 {
		return nil, fmt.Errorf("%w: %d conflict(s) detected; run sync and resolve conflicts before creating a version", ErrWorkspaceHasConflicts, len(status.MergeConflict))
	}
	if len(status.WorkspaceChange) == 0 {
		return nil, ErrWorkspaceNoChanges
	}

	req := &tagmanager.CreateContainerVersionRequestVersionOptions{
		Name:  opts.Name,
		Notes: opts.Notes,
	}
	var out *tagmanager.CreateContainerVersionResponse
	err = c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.CreateVersion(workspacePath(accountID, containerID, workspaceID), req).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateContainer(accountID, containerID)
	}
	return out, err
}

// --- Tags ---

func (c *Client) ListTags(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.Tag, error) {
	if err := c.check(safety.ResourceTag, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "tags"
	if !opts.Fresh {
		var cached []*tagmanager.Tag
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Tag
	var pageToken string
	for {
		var resp *tagmanager.ListTagsResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Tags.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Tag...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetTag(ctx context.Context, accountID, containerID, workspaceID, tagID string) (*tagmanager.Tag, error) {
	if err := c.check(safety.ResourceTag, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("tagId", tagID); err != nil {
		return nil, err
	}
	var out *tagmanager.Tag
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Tags.Get(tagPath(accountID, containerID, workspaceID, tagID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) CreateTag(ctx context.Context, accountID, containerID, workspaceID string, tag *tagmanager.Tag, opts CreateOptions) (*tagmanager.Tag, error) {
	if err := c.check(safety.ResourceTag, safety.ActionCreate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if tag == nil || strings.TrimSpace(tag.Name) == "" {
		return nil, fmt.Errorf("%w: tag name is required", ErrValidation)
	}

	if !opts.AllowDuplicate {
		existing, err := c.ListTags(ctx, accountID, containerID, workspaceID, ListOptions{Fresh: true})
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(existing))
		for _, t := range existing {
			names = append(names, t.Name)
		}
		if err := safety.CheckDuplicateName("tag", names, tag.Name, false); err != nil {
			return nil, err
		}
	}

	var out *tagmanager.Tag
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Tags.Create(workspacePath(accountID, containerID, workspaceID), tag).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) UpdateTag(ctx context.Context, accountID, containerID, workspaceID, tagID, fingerprint string, tag *tagmanager.Tag) (*tagmanager.Tag, error) {
	if err := c.check(safety.ResourceTag, safety.ActionUpdate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("tagId", tagID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	if tag == nil {
		return nil, fmt.Errorf("%w: tag payload is required", ErrValidation)
	}

	var out *tagmanager.Tag
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Tags.Update(tagPath(accountID, containerID, workspaceID, tagID), tag).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) DeleteTag(ctx context.Context, accountID, containerID, workspaceID, tagID string, confirm bool) error {
	if err := c.check(safety.ResourceTag, safety.ActionDelete, confirm); err != nil {
		return err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return err
	}
	if err := requireNonEmpty("tagId", tagID); err != nil {
		return err
	}
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		return c.svc.Accounts.Containers.Workspaces.Tags.Delete(tagPath(accountID, containerID, workspaceID, tagID)).Context(callCtx).Do()
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return err
}

func (c *Client) RevertTag(ctx context.Context, accountID, containerID, workspaceID, tagID, fingerprint string) (*tagmanager.RevertTagResponse, error) {
	if err := c.check(safety.ResourceTag, safety.ActionRevert, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("tagId", tagID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	var out *tagmanager.RevertTagResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Tags.Revert(tagPath(accountID, containerID, workspaceID, tagID)).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

// --- Triggers ---

func (c *Client) ListTriggers(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.Trigger, error) {
	if err := c.check(safety.ResourceTrigger, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "triggers"
	if !opts.Fresh {
		var cached []*tagmanager.Trigger
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Trigger
	var pageToken string
	for {
		var resp *tagmanager.ListTriggersResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Triggers.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Trigger...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetTrigger(ctx context.Context, accountID, containerID, workspaceID, triggerID string) (*tagmanager.Trigger, error) {
	if err := c.check(safety.ResourceTrigger, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("triggerId", triggerID); err != nil {
		return nil, err
	}
	var out *tagmanager.Trigger
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Triggers.Get(triggerPath(accountID, containerID, workspaceID, triggerID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) CreateTrigger(ctx context.Context, accountID, containerID, workspaceID string, trigger *tagmanager.Trigger, opts CreateOptions) (*tagmanager.Trigger, error) {
	if err := c.check(safety.ResourceTrigger, safety.ActionCreate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if trigger == nil || strings.TrimSpace(trigger.Name) == "" {
		return nil, fmt.Errorf("%w: trigger name is required", ErrValidation)
	}

	if !opts.AllowDuplicate {
		existing, err := c.ListTriggers(ctx, accountID, containerID, workspaceID, ListOptions{Fresh: true})
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(existing))
		for _, item := range existing {
			names = append(names, item.Name)
		}
		if err := safety.CheckDuplicateName("trigger", names, trigger.Name, false); err != nil {
			return nil, err
		}
	}

	var out *tagmanager.Trigger
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Triggers.Create(workspacePath(accountID, containerID, workspaceID), trigger).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) UpdateTrigger(ctx context.Context, accountID, containerID, workspaceID, triggerID, fingerprint string, trigger *tagmanager.Trigger) (*tagmanager.Trigger, error) {
	if err := c.check(safety.ResourceTrigger, safety.ActionUpdate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("triggerId", triggerID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	if trigger == nil {
		return nil, fmt.Errorf("%w: trigger payload is required", ErrValidation)
	}
	var out *tagmanager.Trigger
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Triggers.Update(triggerPath(accountID, containerID, workspaceID, triggerID), trigger).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) DeleteTrigger(ctx context.Context, accountID, containerID, workspaceID, triggerID string, confirm bool) error {
	if err := c.check(safety.ResourceTrigger, safety.ActionDelete, confirm); err != nil {
		return err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return err
	}
	if err := requireNonEmpty("triggerId", triggerID); err != nil {
		return err
	}
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		return c.svc.Accounts.Containers.Workspaces.Triggers.Delete(triggerPath(accountID, containerID, workspaceID, triggerID)).Context(callCtx).Do()
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return err
}

func (c *Client) RevertTrigger(ctx context.Context, accountID, containerID, workspaceID, triggerID, fingerprint string) (*tagmanager.RevertTriggerResponse, error) {
	if err := c.check(safety.ResourceTrigger, safety.ActionRevert, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("triggerId", triggerID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	var out *tagmanager.RevertTriggerResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Triggers.Revert(triggerPath(accountID, containerID, workspaceID, triggerID)).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

// --- Variables ---

func (c *Client) ListVariables(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.Variable, error) {
	if err := c.check(safety.ResourceVariable, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "variables"
	if !opts.Fresh {
		var cached []*tagmanager.Variable
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Variable
	var pageToken string
	for {
		var resp *tagmanager.ListVariablesResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Variables.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Variable...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetVariable(ctx context.Context, accountID, containerID, workspaceID, variableID string) (*tagmanager.Variable, error) {
	if err := c.check(safety.ResourceVariable, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("variableId", variableID); err != nil {
		return nil, err
	}
	var out *tagmanager.Variable
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Variables.Get(variablePath(accountID, containerID, workspaceID, variableID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) CreateVariable(ctx context.Context, accountID, containerID, workspaceID string, variable *tagmanager.Variable, opts CreateOptions) (*tagmanager.Variable, error) {
	if err := c.check(safety.ResourceVariable, safety.ActionCreate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if variable == nil || strings.TrimSpace(variable.Name) == "" {
		return nil, fmt.Errorf("%w: variable name is required", ErrValidation)
	}

	if !opts.AllowDuplicate {
		existing, err := c.ListVariables(ctx, accountID, containerID, workspaceID, ListOptions{Fresh: true})
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(existing))
		for _, item := range existing {
			names = append(names, item.Name)
		}
		if err := safety.CheckDuplicateName("variable", names, variable.Name, false); err != nil {
			return nil, err
		}
	}

	var out *tagmanager.Variable
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Variables.Create(workspacePath(accountID, containerID, workspaceID), variable).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) UpdateVariable(ctx context.Context, accountID, containerID, workspaceID, variableID, fingerprint string, variable *tagmanager.Variable) (*tagmanager.Variable, error) {
	if err := c.check(safety.ResourceVariable, safety.ActionUpdate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("variableId", variableID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	if variable == nil {
		return nil, fmt.Errorf("%w: variable payload is required", ErrValidation)
	}
	var out *tagmanager.Variable
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Variables.Update(variablePath(accountID, containerID, workspaceID, variableID), variable).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) DeleteVariable(ctx context.Context, accountID, containerID, workspaceID, variableID string, confirm bool) error {
	if err := c.check(safety.ResourceVariable, safety.ActionDelete, confirm); err != nil {
		return err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return err
	}
	if err := requireNonEmpty("variableId", variableID); err != nil {
		return err
	}
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		return c.svc.Accounts.Containers.Workspaces.Variables.Delete(variablePath(accountID, containerID, workspaceID, variableID)).Context(callCtx).Do()
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return err
}

func (c *Client) RevertVariable(ctx context.Context, accountID, containerID, workspaceID, variableID, fingerprint string) (*tagmanager.RevertVariableResponse, error) {
	if err := c.check(safety.ResourceVariable, safety.ActionRevert, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("variableId", variableID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	var out *tagmanager.RevertVariableResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Variables.Revert(variablePath(accountID, containerID, workspaceID, variableID)).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

// --- Built-In Variables ---

func (c *Client) ListBuiltInVariables(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.BuiltInVariable, error) {
	if err := c.check(safety.ResourceBuiltInVariable, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "builtin_variables"
	if !opts.Fresh {
		var cached []*tagmanager.BuiltInVariable
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.BuiltInVariable
	var pageToken string
	for {
		var resp *tagmanager.ListEnabledBuiltInVariablesResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.BuiltInVariables.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.BuiltInVariable...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) EnableBuiltInVariables(ctx context.Context, accountID, containerID, workspaceID string, types []string) ([]*tagmanager.BuiltInVariable, error) {
	if err := c.check(safety.ResourceBuiltInVariable, safety.ActionEnable, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("%w: at least one built-in variable type is required", ErrValidation)
	}
	var resp *tagmanager.CreateBuiltInVariableResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.svc.Accounts.Containers.Workspaces.BuiltInVariables.Create(workspacePath(accountID, containerID, workspaceID)).
			Type(types...).
			Context(callCtx).
			Do()
		return callErr
	})
	if err != nil {
		return nil, err
	}
	c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	return resp.BuiltInVariable, nil
}

func (c *Client) DisableBuiltInVariables(ctx context.Context, accountID, containerID, workspaceID string, types []string, confirm bool) error {
	if err := c.check(safety.ResourceBuiltInVariable, safety.ActionDisable, confirm); err != nil {
		return err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return err
	}
	if len(types) == 0 {
		return fmt.Errorf("%w: at least one built-in variable type is required", ErrValidation)
	}
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		return c.svc.Accounts.Containers.Workspaces.BuiltInVariables.Delete(builtInVariablesPath(accountID, containerID, workspaceID)).
			Type(types...).
			Context(callCtx).
			Do()
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return err
}

// --- Folders ---

func (c *Client) ListFolders(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.Folder, error) {
	if err := c.check(safety.ResourceFolder, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "folders"
	if !opts.Fresh {
		var cached []*tagmanager.Folder
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Folder
	var pageToken string
	for {
		var resp *tagmanager.ListFoldersResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Folders.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Folder...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetFolder(ctx context.Context, accountID, containerID, workspaceID, folderID string) (*tagmanager.Folder, error) {
	if err := c.check(safety.ResourceFolder, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("folderId", folderID); err != nil {
		return nil, err
	}
	var out *tagmanager.Folder
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Folders.Get(folderPath(accountID, containerID, workspaceID, folderID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) CreateFolder(ctx context.Context, accountID, containerID, workspaceID string, folder *tagmanager.Folder, opts CreateOptions) (*tagmanager.Folder, error) {
	if err := c.check(safety.ResourceFolder, safety.ActionCreate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if folder == nil || strings.TrimSpace(folder.Name) == "" {
		return nil, fmt.Errorf("%w: folder name is required", ErrValidation)
	}

	if !opts.AllowDuplicate {
		existing, err := c.ListFolders(ctx, accountID, containerID, workspaceID, ListOptions{Fresh: true})
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(existing))
		for _, item := range existing {
			names = append(names, item.Name)
		}
		if err := safety.CheckDuplicateName("folder", names, folder.Name, false); err != nil {
			return nil, err
		}
	}

	var out *tagmanager.Folder
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Folders.Create(workspacePath(accountID, containerID, workspaceID), folder).Context(callCtx).Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) UpdateFolder(ctx context.Context, accountID, containerID, workspaceID, folderID, fingerprint string, folder *tagmanager.Folder) (*tagmanager.Folder, error) {
	if err := c.check(safety.ResourceFolder, safety.ActionUpdate, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("folderId", folderID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, fmt.Errorf("%w: folder payload is required", ErrValidation)
	}
	var out *tagmanager.Folder
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Folders.Update(folderPath(accountID, containerID, workspaceID, folderID), folder).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return out, err
}

func (c *Client) DeleteFolder(ctx context.Context, accountID, containerID, workspaceID, folderID string, confirm bool) error {
	if err := c.check(safety.ResourceFolder, safety.ActionDelete, confirm); err != nil {
		return err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return err
	}
	if err := requireNonEmpty("folderId", folderID); err != nil {
		return err
	}
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		return c.svc.Accounts.Containers.Workspaces.Folders.Delete(folderPath(accountID, containerID, workspaceID, folderID)).Context(callCtx).Do()
	})
	if err == nil {
		c.cache.InvalidateWorkspace(accountID, containerID, workspaceID)
	}
	return err
}

func (c *Client) ListFolderEntities(ctx context.Context, accountID, containerID, workspaceID, folderID string) (*tagmanager.FolderEntities, error) {
	if err := c.check(safety.ResourceFolder, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("folderId", folderID); err != nil {
		return nil, err
	}
	merged := &tagmanager.FolderEntities{}
	var pageToken string
	for {
		var resp *tagmanager.FolderEntities
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Folders.Entities(folderPath(accountID, containerID, workspaceID, folderID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		merged.Tag = append(merged.Tag, resp.Tag...)
		merged.Trigger = append(merged.Trigger, resp.Trigger...)
		merged.Variable = append(merged.Variable, resp.Variable...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return merged, nil
}

// --- Custom Templates, Clients & Transformations (Web + sGTM) ---

func (c *Client) ListTemplates(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.CustomTemplate, error) {
	if err := c.check(safety.ResourceTemplate, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "templates"
	if !opts.Fresh {
		var cached []*tagmanager.CustomTemplate
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.CustomTemplate
	var pageToken string
	for {
		var resp *tagmanager.ListTemplatesResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Templates.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Template...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetTemplate(ctx context.Context, accountID, containerID, workspaceID, templateID string) (*tagmanager.CustomTemplate, error) {
	if err := c.check(safety.ResourceTemplate, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("templateId", templateID); err != nil {
		return nil, err
	}
	var out *tagmanager.CustomTemplate
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Templates.Get(templatePath(accountID, containerID, workspaceID, templateID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) ListClients(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.Client, error) {
	if err := c.check(safety.ResourceClient, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "clients"
	if !opts.Fresh {
		var cached []*tagmanager.Client
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Client
	var pageToken string
	for {
		var resp *tagmanager.ListClientsResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Clients.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Client...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetClient(ctx context.Context, accountID, containerID, workspaceID, clientID string) (*tagmanager.Client, error) {
	if err := c.check(safety.ResourceClient, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("clientId", clientID); err != nil {
		return nil, err
	}
	var out *tagmanager.Client
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Clients.Get(clientPath(accountID, containerID, workspaceID, clientID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) ListTransformations(ctx context.Context, accountID, containerID, workspaceID string, opts ListOptions) ([]*tagmanager.Transformation, error) {
	if err := c.check(safety.ResourceTransformation, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	cacheKey := workspaceCachePrefix(accountID, containerID, workspaceID) + "transformations"
	if !opts.Fresh {
		var cached []*tagmanager.Transformation
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.Transformation
	var pageToken string
	for {
		var resp *tagmanager.ListTransformationsResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.Workspaces.Transformations.List(workspacePath(accountID, containerID, workspaceID)).Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Transformation...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetTransformation(ctx context.Context, accountID, containerID, workspaceID, transformationID string) (*tagmanager.Transformation, error) {
	if err := c.check(safety.ResourceTransformation, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := validateWorkspaceScope(accountID, containerID, workspaceID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("transformationId", transformationID); err != nil {
		return nil, err
	}
	var out *tagmanager.Transformation
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Workspaces.Transformations.Get(transformationPath(accountID, containerID, workspaceID, transformationID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

// --- Versions ---

func (c *Client) ListVersionHeaders(ctx context.Context, accountID, containerID string, includeDeleted bool, opts ListOptions) ([]*tagmanager.ContainerVersionHeader, error) {
	if err := c.check(safety.ResourceVersionHeader, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	cacheKey := fmt.Sprintf("%sversion_headers:%t", containerCachePrefix(accountID, containerID), includeDeleted)
	if !opts.Fresh {
		var cached []*tagmanager.ContainerVersionHeader
		if c.cache.Get(cacheKey, &cached) {
			return cached, nil
		}
	}

	var items []*tagmanager.ContainerVersionHeader
	var pageToken string
	for {
		var resp *tagmanager.ListContainerVersionsResponse
		err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
			call := c.svc.Accounts.Containers.VersionHeaders.List(containerPath(accountID, containerID)).
				IncludeDeleted(includeDeleted).
				Context(callCtx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			var callErr error
			resp, callErr = call.Do()
			return callErr
		})
		if err != nil {
			return nil, err
		}
		items = append(items, resp.ContainerVersionHeader...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	c.cache.Set(cacheKey, items)
	return items, nil
}

func (c *Client) GetLiveVersion(ctx context.Context, accountID, containerID string) (*tagmanager.ContainerVersion, error) {
	if err := c.check(safety.ResourceVersion, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	var out *tagmanager.ContainerVersion
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Versions.Live(containerPath(accountID, containerID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) GetVersion(ctx context.Context, accountID, containerID, versionID string) (*tagmanager.ContainerVersion, error) {
	if err := c.check(safety.ResourceVersion, safety.ActionRead, false); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("versionId", versionID); err != nil {
		return nil, err
	}
	var out *tagmanager.ContainerVersion
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Versions.Get(versionPath(accountID, containerID, versionID)).Context(callCtx).Do()
		return callErr
	})
	return out, err
}

func (c *Client) PublishVersion(ctx context.Context, accountID, containerID, versionID, fingerprint string, confirm bool) (*tagmanager.PublishContainerVersionResponse, error) {
	if err := c.check(safety.ResourceVersion, safety.ActionPublish, confirm); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("versionId", versionID); err != nil {
		return nil, err
	}
	if err := requireFingerprint(fingerprint); err != nil {
		return nil, err
	}
	var out *tagmanager.PublishContainerVersionResponse
	err := c.scheduler.Do(ctx, func(callCtx context.Context) error {
		var callErr error
		out, callErr = c.svc.Accounts.Containers.Versions.Publish(versionPath(accountID, containerID, versionID)).
			Fingerprint(fingerprint).
			Context(callCtx).
			Do()
		return callErr
	})
	if err == nil {
		c.cache.InvalidateContainer(accountID, containerID)
	}
	return out, err
}

func validateWorkspaceScope(accountID, containerID, workspaceID string) error {
	if err := requireNonEmpty("accountId", accountID); err != nil {
		return err
	}
	if err := requireNonEmpty("containerId", containerID); err != nil {
		return err
	}
	if err := requireNonEmpty("workspaceId", workspaceID); err != nil {
		return err
	}
	return nil
}

// IsBlocked returns true if err represents a safety policy denial.
func IsBlocked(err error) bool {
	return errors.Is(err, safety.ErrBlocked)
}
