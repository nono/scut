package config

import (
	"errors"
	"strings"

	"github.com/nono/scut/internal/exit"
	"github.com/zalando/go-keyring"
)

const keyringService = "scut"

// Keyring stores secrets in a keyring.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
}

// OSKeyring is the keyring of the operating system.
type OSKeyring struct{}

// Get returns the secret, or "" and nil when there is none.
func (OSKeyring) Get(service, user string) (string, error) {
	s, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return s, err
}

// Set stores the secret.
func (OSKeyring) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

// ParseToken splits sct_{scopes}_{workspace}_{secret}.
func ParseToken(s string) (scopes, workspace string, err error) {
	p := strings.SplitN(strings.TrimSpace(s), "_", 4)
	if len(p) != 4 || p[0] != "sct" || p[1] == "" || p[2] == "" || p[3] == "" {
		return "", "", exit.Usagef("token does not have the form sct_<scopes>_<workspace>_<secret>")
	}
	return p[1], p[2], nil
}
