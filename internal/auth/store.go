package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

const keyringServiceName = "tagforge"

// ErrTokenNotFound is returned when no token exists for the requested profile.
var ErrTokenNotFound = errors.New("stored auth token not found")

// StoredToken wraps an OAuth 2.0 token with metadata needed for safe refresh and audit.
type StoredToken struct {
	Token    oauth2.Token `json:"token"`
	Scopes   []string     `json:"scopes,omitempty"`
	ClientID string       `json:"client_id,omitempty"`
	SavedAt  time.Time    `json:"saved_at"`
}

// Store persists OAuth tokens per profile.
type Store interface {
	Load(profile string) (*StoredToken, error)
	Save(profile string, tok *StoredToken) error
	Delete(profile string) error
	Backend() string
}

// KeyringStore stores tokens in the native OS credential manager.
type KeyringStore struct{}

func keyringAccount(profile string) string {
	return "oauth-token:" + sanitizeProfile(profile)
}

func (s *KeyringStore) Backend() string {
	return "os-keyring"
}

func (s *KeyringStore) Load(profile string) (*StoredToken, error) {
	raw, err := keyring.Get(keyringServiceName, keyringAccount(profile))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}
	var tok StoredToken
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("corrupt keyring token for profile %q: %w", profile, err)
	}
	return &tok, nil
}

func (s *KeyringStore) Save(profile string, tok *StoredToken) error {
	if tok == nil {
		return errors.New("cannot save nil token")
	}
	copyTok := *tok
	if copyTok.SavedAt.IsZero() {
		copyTok.SavedAt = time.Now().UTC()
	}
	data, err := json.Marshal(copyTok)
	if err != nil {
		return err
	}
	return keyring.Set(keyringServiceName, keyringAccount(profile), string(data))
}

func (s *KeyringStore) Delete(profile string) error {
	err := keyring.Delete(keyringServiceName, keyringAccount(profile))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// FileStore stores tokens as 0600 JSON files inside configDir/tokens.
type FileStore struct {
	Dir string
}

// NewFileStore constructs a FileStore rooted under configDir (or XDG/home default if empty).
func NewFileStore(configDir string) (*FileStore, error) {
	if strings.TrimSpace(configDir) == "" {
		base, err := DefaultConfigDir()
		if err != nil {
			return nil, err
		}
		configDir = base
	}
	return &FileStore{Dir: filepath.Join(configDir, "tokens")}, nil
}

// DefaultConfigDir returns $XDG_CONFIG_HOME/tagforge or ~/.config/tagforge.
func DefaultConfigDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "tagforge"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".config", "tagforge"), nil
}

func (s *FileStore) tokenPath(profile string) string {
	return filepath.Join(s.Dir, sanitizeProfile(profile)+".json")
}

func (s *FileStore) Backend() string {
	return "file-0600"
}

func (s *FileStore) Load(profile string) (*StoredToken, error) {
	data, err := os.ReadFile(s.tokenPath(profile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}
	var tok StoredToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("corrupt token file for profile %q: %w", profile, err)
	}
	return &tok, nil
}

func (s *FileStore) Save(profile string, tok *StoredToken) error {
	if tok == nil {
		return errors.New("cannot save nil token")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	copyTok := *tok
	if copyTok.SavedAt.IsZero() {
		copyTok.SavedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(copyTok, "", "  ")
	if err != nil {
		return err
	}
	path := s.tokenPath(profile)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	return nil
}

func (s *FileStore) Delete(profile string) error {
	err := os.Remove(s.tokenPath(profile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// FallbackStore uses primary (KeyringStore) and seamlessly falls back to secondary (FileStore)
// when the OS keyring is unavailable (for example, headless Linux environments).
type FallbackStore struct {
	Primary   Store
	Secondary Store
	ErrOut    io.Writer
	active    string
}

// NewDefaultStore returns a store backed by the OS keyring with 0600 file fallback.
func NewDefaultStore(configDir string, errOut io.Writer) (Store, error) {
	fs, err := NewFileStore(configDir)
	if err != nil {
		return nil, err
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	return &FallbackStore{
		Primary:   &KeyringStore{},
		Secondary: fs,
		ErrOut:    errOut,
		active:    "os-keyring",
	}, nil
}

func (s *FallbackStore) Backend() string {
	if s.active != "" {
		return s.active
	}
	return s.Primary.Backend()
}

func (s *FallbackStore) warnFallback(err error) {
	if s.ErrOut != nil {
		fmt.Fprintf(s.ErrOut, "warning: OS keyring unavailable (%v); falling back to 0600 file token store\n", err)
	}
}

func (s *FallbackStore) Load(profile string) (*StoredToken, error) {
	tok, err := s.Primary.Load(profile)
	if err == nil {
		s.active = s.Primary.Backend()
		return tok, nil
	}
	if errors.Is(err, ErrTokenNotFound) {
		if secTok, secErr := s.Secondary.Load(profile); secErr == nil {
			s.active = s.Secondary.Backend()
			return secTok, nil
		}
		return nil, ErrTokenNotFound
	}
	s.warnFallback(err)
	s.active = s.Secondary.Backend()
	return s.Secondary.Load(profile)
}

func (s *FallbackStore) Save(profile string, tok *StoredToken) error {
	err := s.Primary.Save(profile, tok)
	if err == nil {
		s.active = s.Primary.Backend()
		return nil
	}
	s.warnFallback(err)
	s.active = s.Secondary.Backend()
	return s.Secondary.Save(profile, tok)
}

func (s *FallbackStore) Delete(profile string) error {
	pErr := s.Primary.Delete(profile)
	sErr := s.Secondary.Delete(profile)
	if pErr != nil && sErr != nil {
		return fmt.Errorf("keyring delete: %v; file delete: %w", pErr, sErr)
	}
	if sErr != nil {
		return sErr
	}
	return nil
}

func sanitizeProfile(profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", "..", "_", " ", "_")
	return replacer.Replace(p)
}
