package wecom

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// mediaPaddingBlock 是平台媒体加密使用的 PKCS#7 填充块大小。
const mediaPaddingBlock = 32

// DownloadedMedia 是解密后的媒体内容与平台给出的文件名。
type DownloadedMedia struct {
	FileName string
	Data     []byte
}

// DownloadMedia 下载入站媒体并用回调中的密钥解密，超过 maxSize 字节时报错。
func DownloadMedia(ctx context.Context, client connectiontest.HTTPDoer, media Media, maxSize int64) (DownloadedMedia, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
	if err != nil {
		return DownloadedMedia{}, connectiontest.InvalidConfigError(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return DownloadedMedia{}, connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DownloadedMedia{}, connectiontest.HTTPStatusError(response.StatusCode)
	}
	encrypted, err := io.ReadAll(io.LimitReader(response.Body, maxSize+mediaPaddingBlock+1))
	if err != nil {
		return DownloadedMedia{}, connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	}
	if int64(len(encrypted)) > maxSize+mediaPaddingBlock {
		return DownloadedMedia{}, fmt.Errorf("wecom media exceeds %d bytes", maxSize)
	}
	data, err := decryptMedia(encrypted, media.AESKey)
	if err != nil {
		return DownloadedMedia{}, err
	}
	downloaded := DownloadedMedia{Data: data}
	// 文件名取自 Content-Disposition，平台未给出时为空。
	if _, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition")); err == nil {
		downloaded.FileName = params["filename"]
	}
	return downloaded, nil
}

// decryptMedia 用 Base64 密钥做 AES-256-CBC 解密，IV 为密钥前 16 字节，去除 32 字节块的 PKCS#7 填充。
func decryptMedia(encrypted []byte, aesKey string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(aesKey)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(aesKey)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid wecom media aes key")
	}
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, errors.New("invalid wecom media ciphertext length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, encrypted)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > mediaPaddingBlock || padding > len(plain) {
		return nil, errors.New("invalid wecom media padding")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid wecom media padding")
		}
	}
	return plain[:len(plain)-padding], nil
}
