//go:build server

package filecontent

// Store 使用部署当前的 S3 配置读写和清理文件。
type Store struct{ s3 S3Settings }

// NewStore 创建 S3 文件存储。
func NewStore(s3 S3Settings) *Store { return &Store{s3: s3} }
