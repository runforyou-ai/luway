//go:build server

package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDiagnosticsOmitsSecrets 验证诊断配置不含密码、密钥和地址中的凭据。
func TestDiagnosticsOmitsSecrets(t *testing.T) {
	config := validTestConfig()
	config.NATS.URL = "nats://nats-user:nats-pass@nats-1:4222, nats-token@nats-2:4222"
	config.Storage.S3 = S3Config{
		Enabled: true, Endpoint: "https://s3-user:s3-pass@s3.example.com", Region: "auto", Bucket: "files",
		AccessKeyID: "access-key-id", SecretAccessKey: "secret-access-key",
	}
	config.Email.SMTP.Host = "smtp.example.com"
	config.Email.SMTP.Username = "smtp-user"
	config.Email.SMTP.Password = "smtp-pass"

	diagnostics := config.Diagnostics()
	encoded, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret", "nats-user", "nats-pass", "nats-token", "s3-user", "s3-pass", "access-key-id", "secret-access-key", "smtp-user", "smtp-pass"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("diagnostics contains %q: %s", secret, encoded)
		}
	}
	if diagnostics.NATS.URL != "nats://nats-1:4222,nats-2:4222" {
		t.Fatalf("NATS URL = %q", diagnostics.NATS.URL)
	}
	if diagnostics.Storage.Endpoint != "https://s3.example.com" || !diagnostics.Storage.S3Enabled || !diagnostics.SMTP.Enabled {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}
