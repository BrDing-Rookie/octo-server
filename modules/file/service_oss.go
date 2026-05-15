package file

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"go.uber.org/zap"
)

type ServiceOSS struct {
	log.Log
	ctx *config.Context
}

// NewServiceOSS NewServiceOSS
func NewServiceOSS(ctx *config.Context) *ServiceOSS {

	return &ServiceOSS{
		Log: log.NewTLog("ServiceOSS"),
		ctx: ctx,
	}
}

// newClient builds an OSS client. The Aliyun SDK derives the region from the
// configured Endpoint string (e.g. `oss-cn-hangzhou.aliyuncs.com`), so we do
// not have to set a separate Region option on the client — we just pass the
// SDK its own safe default by leaving the option list empty.
func (s *ServiceOSS) newClient() (*oss.Client, error) {
	ossCfg := s.ctx.GetConfig().OSS
	return oss.New(ossCfg.Endpoint, ossCfg.AccessKeyID, ossCfg.AccessKeySecret)
}

// UploadFile 上传文件
func (s *ServiceOSS) UploadFile(filePath string, contentType string, contentDisposition string, copyFileWriter func(io.Writer) error) (map[string]interface{}, error) {
	client, err := s.newClient()
	if err != nil {
		return nil, err
	}
	bucketName := s.ctx.GetConfig().OSS.BucketName

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, err
	}
	if bucket == nil {
		err = client.CreateBucket(bucketName, oss.ACL(oss.ACLPublicRead))
		if err != nil {
			return nil, err
		}
		bucket, err = client.Bucket(bucketName)
		if err != nil {
			return nil, err
		}
	}
	buff := bytes.NewBuffer(make([]byte, 0))
	err = copyFileWriter(buff)
	if err != nil {
		s.Error("复制文件内容失败！", zap.Error(err))
		return nil, err
	}
	putOptions := []oss.Option{oss.ContentType(contentType), oss.ContentLength(int64(len(buff.Bytes())))}
	if contentDisposition != "" {
		putOptions = append(putOptions, oss.ContentDisposition(contentDisposition))
	}
	err = bucket.PutObject(filePath, buff, putOptions...)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{}, nil
}

func (s *ServiceOSS) GetFile(path string) (io.ReadCloser, string, error) {
	return nil, "", fmt.Errorf("GetFile not supported for OSS, use DownloadURL instead")
}

func (s *ServiceOSS) DownloadURL(path string, filename string) (string, error) {
	ossCfg := s.ctx.GetConfig().OSS

	rpath, _ := url.JoinPath(ossCfg.BucketURL, path)
	return rpath, nil
}

// trimBucketPrefix strips a leading `<bucketName>/` from the object path. OSS
// only takes the object key in SignURL — passing a path that starts with the
// bucket would sign `/<bucket>/<bucket>/<key>` and 404 at the gateway.
func (s *ServiceOSS) trimBucketPrefix(objectPath string) string {
	bucketName := s.ctx.GetConfig().OSS.BucketName
	objectPath = strings.TrimPrefix(objectPath, "/")
	prefix := bucketName + "/"
	return strings.TrimPrefix(objectPath, prefix)
}

// PresignedPutURL signs an OSS PUT URL the browser can use directly.
// Aliyun OSS does NOT accept a separate Content-Disposition signature on PUT
// the way S3 does — disposition has to be embedded as object metadata at
// upload time. We therefore include it in the signed headers so the client
// echoes the same value, and the OSS gateway records it on the resulting
// object.
func (s *ServiceOSS) PresignedPutURL(objectPath string, contentType string, contentDisposition string, expires time.Duration) (uploadURL string, downloadURL string, err error) {
	client, err := s.newClient()
	if err != nil {
		return "", "", err
	}
	ossCfg := s.ctx.GetConfig().OSS
	bucket, err := client.Bucket(ossCfg.BucketName)
	if err != nil {
		return "", "", err
	}

	key := s.trimBucketPrefix(objectPath)
	if key == "" {
		return "", "", fmt.Errorf("空对象路径，无法生成预签名URL")
	}

	opts := []oss.Option{}
	if contentType != "" {
		opts = append(opts, oss.ContentType(contentType))
	}
	if contentDisposition != "" {
		opts = append(opts, oss.ContentDisposition(contentDisposition))
	}

	signed, err := bucket.SignURL(key, oss.HTTPPut, int64(expires.Seconds()), opts...)
	if err != nil {
		return "", "", fmt.Errorf("生成预签名URL失败: %w", err)
	}

	dl, dlErr := s.DownloadURL(objectPath, "")
	if dlErr != nil {
		s.Warn("生成下载URL失败", zap.Error(dlErr))
	}
	return signed, dl, nil
}

// PresignedGetURL signs an OSS GET URL with a `response-content-disposition`
// override so the browser saves the file under the user-facing filename.
func (s *ServiceOSS) PresignedGetURL(objectPath string, filename string, disposition string, expires time.Duration) (string, error) {
	client, err := s.newClient()
	if err != nil {
		return "", err
	}
	ossCfg := s.ctx.GetConfig().OSS
	bucket, err := client.Bucket(ossCfg.BucketName)
	if err != nil {
		return "", err
	}

	key := s.trimBucketPrefix(objectPath)
	if key == "" {
		return "", fmt.Errorf("空对象路径，无法生成预签名URL")
	}

	if disposition != "inline" {
		disposition = "attachment"
	}

	opts := []oss.Option{}
	if filename != "" {
		encoded := "UTF-8''" + rfc5987Encode(filename)
		opts = append(opts, oss.ResponseContentDisposition(fmt.Sprintf("%s; filename*=%s", disposition, encoded)))
	} else {
		opts = append(opts, oss.ResponseContentDisposition(disposition))
	}

	signed, err := bucket.SignURL(key, oss.HTTPGet, int64(expires.Seconds()), opts...)
	if err != nil {
		return "", fmt.Errorf("生成预签名GET URL失败: %w", err)
	}
	return signed, nil
}
