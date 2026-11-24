package gtm

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/googleapi"
)

var (
	ErrNotFound              = errors.New("gtm resource not found")
	ErrUnauthorized          = errors.New("gtm authentication failed")
	ErrPermissionDenied      = errors.New("gtm permission denied")
	ErrConflict              = errors.New("gtm conflict or stale fingerprint")
	ErrRateLimited           = errors.New("gtm api rate limit exceeded")
	ErrValidation            = errors.New("gtm validation error")
	ErrWorkspaceHasConflicts = errors.New("workspace has unresolved merge conflicts")
	ErrWorkspaceNoChanges    = errors.New("workspace has no pending changes to version")
)

// TranslateGoogleError maps Google API HTTP errors into clear, actionable domain errors.
func TranslateGoogleError(err error) error {
	if err == nil {
		return nil
	}
	var gErr *googleapi.Error
	if !errors.As(err, &gErr) {
		return err
	}

	switch gErr.Code {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrValidation, gErr.Message)
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s (re-authenticate with 'tagforge login' or check credentials)", ErrUnauthorized, gErr.Message)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s (verify GTM container permissions and OAuth scopes)", ErrPermissionDenied, gErr.Message)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, gErr.Message)
	case http.StatusConflict, http.StatusPreconditionFailed:
		return fmt.Errorf("%w: %s (re-fetch resource to obtain latest fingerprint)", ErrConflict, gErr.Message)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrRateLimited, gErr.Message)
	default:
		return fmt.Errorf("gtm api error (HTTP %d): %s", gErr.Code, gErr.Message)
	}
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var gErr *googleapi.Error
	if errors.As(err, &gErr) {
		if gErr.Code == http.StatusTooManyRequests {
			return true
		}
		if gErr.Code >= http.StatusInternalServerError && gErr.Code < 600 {
			return true
		}
	}
	return false
}
