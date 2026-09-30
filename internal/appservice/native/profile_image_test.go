//go:build !server

package native

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// TestReadImageFile 验证原生端只读取扩展名和内容一致的图片。
func TestReadImageFile(t *testing.T) {
	directory := t.TempDir()
	imagePath := filepath.Join(directory, "avatar.png")
	content := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if err := os.WriteFile(imagePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	selected, err := readImageFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Name != "avatar.png" || selected.ContentType != "image/png" || selected.DataBase64 != base64.StdEncoding.EncodeToString(content) {
		t.Fatalf("selected image = %#v", selected)
	}

	dmgPath := filepath.Join(directory, "installer.dmg")
	if err := os.WriteFile(dmgPath, []byte("disk image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readImageFile(dmgPath); err == nil {
		t.Fatal("DMG file was accepted as image")
	}

	fakeImagePath := filepath.Join(directory, "installer.png")
	if err := os.WriteFile(fakeImagePath, []byte("disk image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readImageFile(fakeImagePath); err == nil {
		t.Fatal("non-image content with PNG extension was accepted")
	}
}
