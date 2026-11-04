package config

import (
	"errors"
	"regexp"
	"strings"

	"github.com/nono/scut/internal/exit"
	"github.com/zalando/go-keyring"
)

// KeyringService is the keyring service name of the scut tokens.
const KeyringService = "scut"

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

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// validSlug tells if s is a workspace slug. The slug names the cache dir, so
// it must not hold a path element such as "..".
func validSlug(s string) bool { return slugRe.MatchString(s) }

// ParseToken splits sct_{scopes}_{workspace}_{secret}. A bad token is an
// auth error (exit 4).
func ParseToken(s string) (scopes, workspace string, err error) {
	p := strings.SplitN(strings.TrimSpace(s), "_", 4)
	if len(p) != 4 || p[0] != "sct" || p[1] == "" || p[3] == "" || !validSlug(p[2]) {
		return "", "", &exit.Error{Code: exit.Auth,
			Msg: "malformed token: the form is sct_<scopes>_<workspace>_<secret>, and the workspace has only a-z, 0-9 and -"}
	}
	return p[1], p[2], nil
}
