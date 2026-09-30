//go:build server

package user

import "testing"

// TestNormalizeProfileInput 验证个人资料输入规范化和字段校验。
func TestNormalizeProfileInput(t *testing.T) {
	normalized, fields := normalizeProfileInput(ProfileInput{
		DisplayName:  "  林晓  ",
		Email:        " LIN@Example.com ",
		AvatarFileID: " file-1 ",
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want empty", fields)
	}
	if normalized.DisplayName != "林晓" || normalized.Email != "lin@example.com" || normalized.AvatarFileID != "file-1" {
		t.Fatalf("normalized input = %#v", normalized)
	}

	_, fields = normalizeProfileInput(ProfileInput{Email: "invalid"})
	if fields["displayName"] != ValidationDisplayNameRequired || fields["email"] != ValidationEmailInvalid {
		t.Fatalf("fields = %#v", fields)
	}
}
