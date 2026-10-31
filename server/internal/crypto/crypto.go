// Package crypto provides AES-256-GCM sealing with key ids, and random token helpers.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Keyring holds named 32-byte keys; Current is used for sealing.
type Keyring struct {
	Current string
	keys    map[string][]byte
}

// NewKeyring builds a keyring. Each key must be 32 bytes.
func NewKeyring(current string, keys map[string][]byte) (*Keyring, error) {
	for id, k := range keys {
		if len(k) != 32 {
			return nil, fmt.Errorf("crypto: key %q must be 32 bytes, got %d", id, len(k))
		}
		if id == "" || len(id) > 255 {
			return nil, errors.New("crypto: invalid key id")
		}
	}
	if _, ok := keys[current]; !ok {
		return nil, fmt.Errorf("crypto: current key %q not in keyring", current)
	}
	return &Keyring{Current: current, keys: keys}, nil
}

func (k *Keyring) aead(id string) (cipher.AEAD, error) {
	key, ok := k.keys[id]
	if !ok {
		return nil, fmt.Errorf("crypto: unknown key id %q", id)
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Seal encrypts plaintext with the current key. Output layout:
// [1 byte key-id length][key id][12-byte nonce][ciphertext+tag].
// aad is authenticated but not stored.
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error) {
	g, err := k.aead(k.Current)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(k.Current)+g.NonceSize()+len(plaintext)+g.Overhead())
	out = append(out, byte(len(k.Current)))
	out = append(out, k.Current...)
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out = append(out, nonce...)
	return g.Seal(out, nonce, plaintext, aad), nil
}

// Open decrypts output of Seal using the key id embedded in the blob.
func (k *Keyring) Open(sealed, aad []byte) ([]byte, error) {
	if len(sealed) < 1 {
		return nil, errors.New("crypto: short ciphertext")
	}
	n := int(sealed[0])
	if len(sealed) < 1+n {
		return nil, errors.New("crypto: short ciphertext")
	}
	g, err := k.aead(string(sealed[1 : 1+n]))
	if err != nil {
		return nil, err
	}
	rest := sealed[1+n:]
	if len(rest) < g.NonceSize()+g.Overhead() {
		return nil, errors.New("crypto: short ciphertext")
	}
	pt, err := g.Open(nil, rest[:g.NonceSize()], rest[g.NonceSize():], aad)
	if err != nil {
		return nil, errors.New("crypto: decryption failed")
	}
	return pt, nil
}

// NewToken returns a random 32-byte token, base64url (no padding).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the hex sha256 of a token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// TokenMatches compares a token against a stored hash in constant time.
func TokenMatches(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(hash)) == 1
}

// KeyringFromBase64 builds a single-key keyring (id "k1") from a base64 32-byte key (TOKEN_ENC_KEY).
func KeyringFromBase64(b64 string) (*Keyring, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		if key, err = base64.RawStdEncoding.DecodeString(b64); err != nil {
			return nil, errors.New("crypto: key is not valid base64")
		}
	}
	return NewKeyring("k1", map[string][]byte{"k1": key})
}

// ParseKeyring builds the keyring from the environment values.
//
// keys is TOKEN_ENC_KEYS: "id1:base64,id2:base64"; the first entry seals new data and every entry opens old
// data. single is the legacy TOKEN_ENC_KEY (id "k1"); it is the whole keyring when keys is empty, and when
// both are set it is added as a non-current "k1" (unless the list already has that id) so data sealed before
// the switch stays readable. Ids are 1-64 characters of letters, digits, '-' and '_'.
func ParseKeyring(keys, single string) (*Keyring, error) {
	m := map[string][]byte{}
	current := ""
	for _, part := range strings.Split(keys, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, b64, ok := strings.Cut(part, ":")
		if !ok || !validKeyID(id) {
			return nil, fmt.Errorf("crypto: bad key entry %q (want id:base64, id of letters, digits, - or _)", redactKey(part))
		}
		if _, dup := m[id]; dup {
			return nil, fmt.Errorf("crypto: duplicate key id %q", id)
		}
		raw, err := decodeKey(b64)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %q: %w", id, err)
		}
		m[id] = raw
		if current == "" {
			current = id
		}
	}
	if single = strings.TrimSpace(single); single != "" {
		if _, taken := m["k1"]; !taken {
			raw, err := decodeKey(single)
			if err != nil {
				return nil, fmt.Errorf("crypto: TOKEN_ENC_KEY: %w", err)
			}
			m["k1"] = raw
			if current == "" {
				current = "k1"
			}
		}
	}
	if current == "" {
		return nil, errors.New("crypto: no encryption key configured")
	}
	return NewKeyring(current, m)
}

func decodeKey(b64 string) ([]byte, error) {
	b64 = strings.TrimSpace(b64)
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(b64); err != nil {
			return nil, errors.New("not valid base64")
		}
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("must decode to 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

func validKeyID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// redactKey keeps only the id of a malformed entry so key material never lands in an error message.
func redactKey(part string) string {
	if id, _, ok := strings.Cut(part, ":"); ok {
		return id + ":…"
	}
	return "…"
}

// CurrentID reports the id used for sealing.
func (k *Keyring) CurrentID() string { return k.Current }

// Has reports whether id is in the keyring.
func (k *Keyring) Has(id string) bool { _, ok := k.keys[id]; return ok }

// SealedKeyID extracts the key id from output of Seal without decrypting.
func SealedKeyID(sealed []byte) (string, bool) {
	if len(sealed) < 1 || len(sealed) < 1+int(sealed[0]) {
		return "", false
	}
	return string(sealed[1 : 1+int(sealed[0])]), true
}
