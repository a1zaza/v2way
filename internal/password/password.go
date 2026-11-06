package password

import (
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/argon2"
	"strings"
	"unicode/utf8"
	"v2way/internal/store"
)

func Hash(password string) (string, error) {
	if utf8.RuneCountInString(password) < 8 || len(password) > 1024 {
		return "", errors.New("密码至少 8 个字符，且不超过 1024 字节")
	}
	salt := store.Token()[:32]
	digest := argon2.IDKey([]byte(password), []byte(salt), 3, 64*1024, 2, 32)
	return salt + ":" + hex.EncodeToString(digest), nil
}
func Verify(password, encoded string) bool {
	parts := strings.Split(encoded, ":")
	if len(parts) != 2 || len(password) > 1024 {
		return false
	}
	expected, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), []byte(parts[0]), 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
