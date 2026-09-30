package password

import (
	"errors"
	"strings"
	"testing"
)

// TestValidate 验证密码字符数和字节数限制。
func TestValidate(t *testing.T) {
	if err := Validate("short"); !errors.Is(err, ErrTooShort) {
		t.Fatalf("Validate(short) = %v, want ErrTooShort", err)
	}
	if err := Validate(strings.Repeat("中", 25)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("Validate(long) = %v, want ErrTooLong", err)
	}
	if err := Validate("password123"); err != nil {
		t.Fatalf("Validate(valid) = %v, want nil", err)
	}
}

// TestHashAndMatches 验证密码哈希与比对。
func TestHashAndMatches(t *testing.T) {
	hash, err := Hash("password123")
	if err != nil {
		t.Fatal(err)
	}
	if !Matches(hash, "password123") {
		t.Fatal("Matches() = false, want true")
	}
	if Matches(hash, "different-password") {
		t.Fatal("Matches() = true for a different password")
	}
}
