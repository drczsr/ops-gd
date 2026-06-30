package gsconfig

// Uploader 把生成的配置字节上传到对象存储(COS)。实现见子包 cos;测试用 stub。
type Uploader interface {
	Upload(key string, data []byte) (string, error)
}
