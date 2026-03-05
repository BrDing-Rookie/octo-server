package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"go.uber.org/zap"
)

// ComputeHMACSHA256 使用 secret key 计算 payload 的 HMAC-SHA256 签名
func ComputeHMACSHA256(payload []byte, secretKey string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write(payload)
	return fmt.Sprintf("sha256=%s", hex.EncodeToString(mac.Sum(nil)))
}

// VerifyHMACSHA256 验证 HMAC-SHA256 签名
func VerifyHMACSHA256(payload []byte, signature string, secretKey string) bool {
	expected := ComputeHMACSHA256(payload, secretKey)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// verifyRequestSignature 验证入站 webhook 请求的 HMAC-SHA256 签名
// 必须配置 TS_WEBHOOK_SECRET_KEY 环境变量，否则拒绝请求
func (w *Webhook) verifyRequestSignature(c *wkhttp.Context) bool {
	if w.secretKey == "" {
		w.Warn("TS_WEBHOOK_SECRET_KEY 未配置，拒绝 webhook 请求。请设置环境变量以启用安全认证。")
		c.ResponseError(fmt.Errorf("服务器未配置 webhook 签名密钥"))
		return false
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		w.Error("读取请求体失败", zap.Error(err))
		c.ResponseError(fmt.Errorf("读取请求体失败"))
		return false
	}
	// 重置 body 供后续 handler 读取
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	signature := c.GetHeader("X-Signature-256")
	if signature == "" {
		w.Warn("Webhook请求缺少X-Signature-256签名头")
		c.ResponseError(fmt.Errorf("缺少签名头X-Signature-256"))
		return false
	}

	if !VerifyHMACSHA256(body, signature, w.secretKey) {
		w.Warn("Webhook签名验证失败")
		c.ResponseError(fmt.Errorf("签名验证失败"))
		return false
	}
	return true
}
