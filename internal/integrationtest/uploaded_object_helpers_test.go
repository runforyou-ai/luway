//go:build server

package integrationtest

import (
	"path"

	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// testImageHeads 是可内嵌展示图片按存储扩展名对应的文件头。
var testImageHeads = map[string]string{
	".png":  "\x89PNG\r\n\x1a\n",
	".jpg":  "\xff\xd8\xff",
	".gif":  "GIF89a",
	".webp": "RIFF\x00\x00\x00\x00WEBPVP",
}

// uploadedTestObject 按文件记录声明的字节数返回上传核验结果，可内嵌展示的图片带上对应格式的文件头。
func uploadedTestObject(record *servermodels.File, etag string) serverfilecontent.UploadedObject {
	return serverfilecontent.UploadedObject{ETag: etag, ByteSize: record.ByteSize, Head: []byte(testImageHeads[path.Ext(record.StorageKey)])}
}
