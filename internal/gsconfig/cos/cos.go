// Package cos 提供基于腾讯云 COS 的对象读写(与核心 gsconfig 解耦,隔离 SDK 依赖)。
package cos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// ErrNotFound 表示对象在桶内不存在(HTTP 404 / NoSuchKey),供调用方区分"空基准"与真错误。
var ErrNotFound = errors.New("cos: object not found")

// Uploader 封装 COS 对象读写。
type Uploader struct {
	client *cos.Client
}

// New 用 region/bucket/密钥构造。bucketURL 形如 https://<bucket>.cos.<region>.myqcloud.com
func New(region, bucket, secretID, secretKey string) (*Uploader, error) {
	raw := fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("COS bucket URL 非法: %w", err)
	}
	client := cos.NewClient(&cos.BaseURL{BucketURL: u}, &http.Client{
		Transport: &cos.AuthorizationTransport{SecretID: secretID, SecretKey: secretKey},
	})
	return &Uploader{client: client}, nil
}

// Upload 以 key 为对象键上传 data,返回新版本 id(桶开启版本控制时由响应头 x-cos-version-id 给出;
// 未开版本控制则为空串,不报错)。
func (u *Uploader) Upload(key string, data []byte) (string, error) {
	resp, err := u.client.Object.Put(context.Background(), key, bytes.NewReader(data), nil)
	if err != nil {
		return "", err
	}
	return resp.Header.Get("x-cos-version-id"), nil
}

// Download 取对象内容;versionID 省略=当前最新版本。对象不存在返回 ErrNotFound。
func (u *Uploader) Download(key string, versionID ...string) ([]byte, error) {
	resp, err := u.client.Object.Get(context.Background(), key, nil, versionID...)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// HeadVersionID 取对象当前版本 id(不下载内容)。对象不存在返回 ErrNotFound。
func (u *Uploader) HeadVersionID(key string) (string, error) {
	resp, err := u.client.Object.Head(context.Background(), key, nil)
	if err != nil {
		if isNotFound(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	return resp.Header.Get("x-cos-version-id"), nil
}

// isNotFound 判定 COS 错误是否为 404 / NoSuchKey。
func isNotFound(err error) bool {
	var e *cos.ErrorResponse
	if errors.As(err, &e) {
		return e.Response != nil && e.Response.StatusCode == http.StatusNotFound
	}
	return false
}
