package common

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinAccountPasswordLength = 8
	MaxAccountPasswordLength = 128
	accountPasswordMemory    = 19 * 1024
	accountPasswordTime      = 2
	accountPasswordSaltBytes = 16
	accountPasswordKeyBytes  = 32
)

var (
	ErrAccountPasswordLength  = errors.New("Password must contain between 8 and 128 characters.")
	ErrAccountPasswordComplex = errors.New("Password does not meet the configured complexity requirement.")
	ErrAccountPasswordSame    = errors.New("New password must be different from current password")
	ErrPasswordLegacyLimit    = errors.New("Long passwords are unavailable until the password storage upgrade is complete.")
)

// 可配置密码策略（v1.1.0 M1 · B77）。
//
// PasswordMinLength 只允许上调：MinAccountPasswordLength 是历史兼容下限，
// 已有密码必须继续能登录，所以策略只约束「新设置的密码」。
// PasswordRequireComplex 要求至少一个字母与一个数字（不追求字符种类最大化，
// 而是避免纯数字/纯字母这类真正弱的组合，同时不逼迫用户使用难以输入的符号）。
// LoginFailureLock* 是登录失败锁定的策略：阈值为 0 表示关闭锁定。
var (
	PasswordMinLength      = MinAccountPasswordLength
	PasswordRequireComplex = false

	LoginFailureLockThreshold = 5
	LoginFailureLockMinutes   = 15
)

// passwordPolicyError carries a human-readable message while still matching its
// sentinel through errors.Is, so the API layer keeps its single error branch.
type passwordPolicyError struct {
	sentinel error
	message  string
}

func (e *passwordPolicyError) Error() string { return e.message }
func (e *passwordPolicyError) Unwrap() error { return e.sentinel }

// NormalizePasswordMinLength bounds an operator-supplied minimum to the range
// the hashing layer supports.
func NormalizePasswordMinLength(v int) int {
	if v < MinAccountPasswordLength {
		return MinAccountPasswordLength
	}
	if v > 64 {
		return 64
	}
	return v
}

func passwordHasLetterAndDigit(password string) bool {
	var letter, digit bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		}
		if letter && digit {
			return true
		}
	}
	return false
}

// ValidateNewAccountPassword applies only when a user chooses a new password.
// Authentication must continue to accept historical passwords without applying
// the new policy. Do not normalize passwords, including surrounding whitespace.
func ValidateNewAccountPassword(password string) error {
	length := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || length < MinAccountPasswordLength || length > MaxAccountPasswordLength {
		return ErrAccountPasswordLength
	}
	if length < PasswordMinLength {
		return &passwordPolicyError{
			sentinel: ErrAccountPasswordLength,
			message:  fmt.Sprintf("Password must contain at least %d characters.", PasswordMinLength),
		}
	}
	if PasswordRequireComplex && !passwordHasLetterAndDigit(password) {
		return &passwordPolicyError{
			sentinel: ErrAccountPasswordComplex,
			message:  ErrAccountPasswordComplex.Error(),
		}
	}
	return nil
}

// HashAccountPassword is for account passwords, not MFA backup codes. The
// temporary bcrypt mode permits rolling out dual-format readers to all nodes
// before enabling Argon2id writes. Existing hashes are never rewritten in bulk.
func HashAccountPassword(password string) (string, error) {
	if err := ValidateNewAccountPassword(password); err != nil {
		return "", err
	}
	switch os.Getenv("ACCOUNT_PASSWORD_HASH_ALGORITHM") {
	case "bcrypt":
		if len(password) > 72 {
			return "", ErrPasswordLegacyLimit
		}
		return Password2Hash(password)
	case "", "argon2id":
	default:
		return "", errors.New("Unsupported account password hashing configuration.")
	}
	salt := make([]byte, accountPasswordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate account password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, accountPasswordTime, accountPasswordMemory, 1, accountPasswordKeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func validateArgon2AccountPassword(password, encoded string) bool {
	// Bound both plaintext and parameters before invoking a memory-hard KDF.
	// Only the version/parameters emitted by this application are accepted.
	if len(password) > MaxAccountPasswordLength*utf8.UTFMax || len(encoded) > 256 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != accountPasswordSaltBytes {
		return false
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) != accountPasswordKeyBytes {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, accountPasswordTime, accountPasswordMemory, 1, accountPasswordKeyBytes)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
