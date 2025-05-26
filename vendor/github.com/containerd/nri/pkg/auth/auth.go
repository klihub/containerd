/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package auth

import (
	"errors"
	fmt "fmt"
)

var (
	// ErrKeyConflict indicates multiple conflicting identities for a key.
	ErrKeyConflict = errors.New("conflicting key identities")
	// ErrUnknownKey indicates an unknown key.
	ErrUnknownKey = errors.New("unknown key")
)

// Config contains authentication configuration. It wraps a slice of
// slice identities, providing validation of keys, uniqueness of key
// to idenitity mapping, and fast identity lookup by key.
type Config struct {
	Identities []*Identity `json:"identities" toml:"identities"`
	keyMap     map[string]*Identity
}

// Validate the configuration. Validation checks that all keys are
// syntactically valid and that all keys map to a single identity.
func (c *Config) Validate() error {
	if c.keyMap != nil {
		return nil
	}

	keyMap := make(map[string]*Identity)
	for _, id := range c.Identities {
		for _, key := range id.Keys {
			if o, ok := keyMap[key]; ok {
				return fmt.Errorf("%w: identity conflict (%q, %q) for key %q",
					ErrKeyConflict, id.Identity, o.Identity, key)
			}
			if _, err := DecodePublicKey([]byte(key)); err != nil {
				return err
			}
			keyMap[key] = id
		}
	}
	c.keyMap = keyMap

	return nil
}

// GetIdentityByKey returns the identity and the decoded key for the given key.
func (c *Config) GetIdentityByKey(keyBytes []byte) (*Identity, *PublicKey, error) {
	id, ok := c.keyMap[string(keyBytes)]
	if !ok {
		return nil, nil, ErrUnknownKey
	}

	pub, err := DecodePublicKey(keyBytes)
	if err != nil {
		return nil, nil, err
	}

	return id, pub, nil
}

// Identity describes an authenticated identity. An identity has a one or more
// public keys and an optional set of associated tags. During authentication a
// client gets mapped to an identity by the clients key. Multiple keys can map
// to a single identity, but a single key can only be associated with a single
// identity. IOW, once authenticated, different clients can end up mapped to a
// single identity, but the identity of any client must be unambiguous.
type Identity struct {
	Identity string            `json:"idenitity" toml:"identity"`
	Keys     []string          `json:"keys" toml:"keys"`
	Tags     map[string]string `json:"tags" toml:"tags"`
}

// GetIdentity retuns the name of the identity.
func (id *Identity) GetIdentity() string {
	if id == nil {
		return ""
	}
	return id.Identity
}

// GetTags return the tags associated with the identity. Tags are opaque
// from the authentication point of view. They carry no inherent meaning.
func (id *Identity) GetTags() map[string]string {
	if id == nil {
		return nil
	}
	return id.Tags
}
