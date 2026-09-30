//go:build server

package filecontent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestLocalMultipart 验证缺片无法合并、错误分片可重传以及完整文件字节一致。
func TestLocalMultipart(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "organizations/org/files/attachment.bin"
	part := bytes.Repeat([]byte("a"), int(domain.FilePartSize))
	if err := store.SavePart(ctx, key, 1, bytes.NewReader(part), int64(len(part))); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteMultipart(ctx, key, domain.FilePartSize+3, domain.FilePartSize); err == nil {
		t.Fatal("missing part accepted")
	}
	if _, err := store.Stat(ctx, key); !os.IsNotExist(err) {
		t.Fatalf("partial file exposed: %v", err)
	}
	if err := store.SavePart(ctx, key, 2, strings.NewReader("bad!"), 3); err == nil {
		t.Fatal("oversized part accepted")
	}
	if err := store.SavePart(ctx, key, 2, strings.NewReader("end"), 3); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteMultipart(ctx, key, domain.FilePartSize+3, domain.FilePartSize); err != nil {
		t.Fatal(err)
	}
	file, _, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(content, append(part, []byte("end")...)) {
		t.Fatal("merged bytes differ")
	}
	if err := store.DeleteParts(key); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteParts("../outside"); err == nil {
		t.Fatal("invalid part directory accepted")
	}
}

// TestS3Multipart 验证预签名直传、服务端读取分片清单和完成请求。
func TestS3Multipart(t *testing.T) {
	ctx := context.Background()
	completed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>test-upload</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut:
			if r.URL.Query().Get("uploadId") != "test-upload" || r.URL.Query().Get("partNumber") != "1" {
				t.Errorf("part URL=%s", r.URL)
			}
			w.Header().Set("ETag", `"part-1"`)
		case r.Method == http.MethodGet:
			_, _ = fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>"part-1"</ETag><Size>%d</Size></Part><Part><PartNumber>2</PartNumber><ETag>"part-2"</ETag><Size>3</Size></Part></ListPartsResult>`, domain.FilePartSize)
		case r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte("part-1")) || !bytes.Contains(body, []byte("part-2")) {
				t.Errorf("complete body=%s", body)
			}
			completed = true
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>"complete"</ETag></CompleteMultipartUploadResult>`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s", r.Method)
		}
	}))
	defer server.Close()
	config := S3Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "test", AccessKeyID: "access", SecretAccessKey: "secret", ForcePathStyle: true}
	id, err := CreateMultipart(ctx, config, "attachment.bin", "application/octet-stream")
	if err != nil || id != "test-upload" {
		t.Fatalf("create=%s %v", id, err)
	}
	request, err := PresignPart(ctx, config, "attachment.bin", id, 1, domain.FilePartSize)
	if err != nil {
		t.Fatal(err)
	}
	put, err := http.NewRequest(request.Method, request.URL, bytes.NewReader(make([]byte, domain.FilePartSize)))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range request.Headers {
		put.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if err := CompleteMultipart(ctx, config, "attachment.bin", id, domain.FilePartSize+3, domain.FilePartSize); err != nil || !completed {
		t.Fatalf("complete=%v err=%v", completed, err)
	}
	completed = false
	if err := CompleteMultipart(ctx, config, "attachment.bin", id, domain.FilePartSize+4, domain.FilePartSize); err == nil || completed {
		t.Fatal("incorrect part size accepted")
	}
	if err := AbortMultipart(ctx, config, "attachment.bin", id); err != nil {
		t.Fatal(err)
	}
}
