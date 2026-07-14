package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxContentModerationSnapshotBytes      = 20 * 1024 * 1024
	maxContentModerationSnapshotTotalBytes = 256 * 1024 * 1024
	contentModerationSnapshotTimeout       = 15 * time.Second
	contentModerationSnapshotTotalTimeout  = 15 * time.Second
	contentModerationSnapshotConcurrency   = 4
)

func (s *ContentModerationService) snapshotContentModerationMedia(ctx context.Context, media []ContentModerationMedia) []ContentModerationMedia {
	out := append([]ContentModerationMedia(nil), media...)
	if len(out) == 0 {
		return out
	}
	// 整批快照共享同一个截止时间，避免图片数量线性放大网关响应延迟。
	snapshotCtx, cancel := context.WithTimeout(ctx, contentModerationSnapshotTotalTimeout)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var retainedBytes atomic.Int64
	workerCount := min(contentModerationSnapshotConcurrency, len(out))
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				data, mimeType, err := fetchContentModerationImage(snapshotCtx, out[index].OriginalRef)
				if err != nil {
					out[index].SnapshotStatus = "error"
					out[index].SnapshotError = err.Error()
					out[index].Content = nil
					continue
				}
				if !reserveContentModerationSnapshotBytes(&retainedBytes, int64(len(data))) {
					out[index].SnapshotStatus = "error"
					out[index].SnapshotError = "当前记录的图片快照总大小超过 256 MB 限制"
					out[index].Content = nil
					continue
				}
				digest := sha256.Sum256(data)
				out[index].MIMEType = mimeType
				out[index].SHA256 = hex.EncodeToString(digest[:])
				out[index].ByteSize = int64(len(data))
				out[index].SnapshotStatus = "ready"
				out[index].SnapshotError = ""
				out[index].Content = data
			}
		}()
	}
	enqueueStopped := false
	for index := range out {
		if err := snapshotCtx.Err(); err != nil {
			for pending := index; pending < len(out); pending++ {
				out[pending].SnapshotStatus = "error"
				out[pending].SnapshotError = err.Error()
				out[pending].Content = nil
			}
			break
		}
		select {
		case jobs <- index:
		case <-snapshotCtx.Done():
			for pending := index; pending < len(out); pending++ {
				out[pending].SnapshotStatus = "error"
				out[pending].SnapshotError = snapshotCtx.Err().Error()
				out[pending].Content = nil
			}
			enqueueStopped = true
		}
		if enqueueStopped {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

func reserveContentModerationSnapshotBytes(retained *atomic.Int64, size int64) bool {
	if retained == nil || size < 0 || size > maxContentModerationSnapshotTotalBytes {
		return false
	}
	for {
		current := retained.Load()
		if current > maxContentModerationSnapshotTotalBytes-size {
			return false
		}
		if retained.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

func fetchContentModerationImage(ctx context.Context, reference string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	reference = strings.TrimSpace(reference)
	if strings.HasPrefix(reference, "data:") {
		return decodeContentModerationDataImage(reference)
	}
	parsed, err := url.Parse(reference)
	if err != nil {
		return nil, "", fmt.Errorf("图片引用无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", errors.New("仅支持 HTTP(S) 或 data 图片引用")
	}
	if err := validateContentModerationRemoteURL(ctx, parsed); err != nil {
		return nil, "", err
	}
	requestCtx, cancel := context.WithTimeout(ctx, contentModerationSnapshotTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", err
	}
	client := newContentModerationSnapshotClient()
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("下载图片失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("下载图片返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxContentModerationSnapshotBytes {
		return nil, "", errors.New("图片超过 20 MB 限制")
	}
	mimeType, err := normalizeContentModerationImageMIME(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxContentModerationSnapshotBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("读取图片失败: %w", err)
	}
	if len(data) > maxContentModerationSnapshotBytes {
		return nil, "", errors.New("图片超过 20 MB 限制")
	}
	if err := validateContentModerationImageBytes(data, mimeType); err != nil {
		return nil, "", err
	}
	return data, mimeType, nil
}

func decodeContentModerationDataImage(reference string) ([]byte, string, error) {
	header, payload, ok := strings.Cut(strings.TrimPrefix(reference, "data:"), ",")
	if !ok {
		return nil, "", errors.New("data 图片缺少内容")
	}
	parts := strings.Split(header, ";")
	mimeType, err := normalizeContentModerationImageMIME(parts[0])
	if err != nil {
		return nil, "", err
	}
	var data []byte
	if len(parts) > 1 && strings.EqualFold(parts[len(parts)-1], "base64") {
		// 流式解码并在 20 MB 后停止，不能先为攻击者提供的完整载荷分配目标缓冲区。
		decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload))
		data, err = io.ReadAll(io.LimitReader(decoder, maxContentModerationSnapshotBytes+1))
	} else {
		if escapedContentModerationDataLength(payload) > maxContentModerationSnapshotBytes {
			return nil, "", errors.New("图片超过 20 MB 限制")
		}
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil {
		return nil, "", fmt.Errorf("解码 data 图片失败: %w", err)
	}
	if len(data) > maxContentModerationSnapshotBytes {
		return nil, "", errors.New("图片超过 20 MB 限制")
	}
	if err := validateContentModerationImageBytes(data, mimeType); err != nil {
		return nil, "", err
	}
	return data, mimeType, nil
}

func escapedContentModerationDataLength(payload string) int {
	length := 0
	for index := 0; index < len(payload); {
		length++
		if payload[index] == '%' && index+2 < len(payload) {
			index += 3
		} else {
			index++
		}
	}
	return length
}

func normalizeContentModerationImageMIME(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("图片 MIME 无效: %w", err)
	}
	mediaType = strings.ToLower(mediaType)
	if !strings.HasPrefix(mediaType, "image/") {
		return "", errors.New("远程内容不是 image/*")
	}
	return mediaType, nil
}

func validateContentModerationImageBytes(data []byte, declaredMIME string) error {
	if len(data) == 0 {
		return errors.New("图片内容为空")
	}
	detected := strings.ToLower(http.DetectContentType(data))
	if strings.HasPrefix(detected, "image/") {
		return nil
	}
	if declaredMIME == "image/svg+xml" && (strings.Contains(string(data[:min(len(data), 512)]), "<svg") || strings.Contains(string(data[:min(len(data), 512)]), "<?xml")) {
		return nil
	}
	return errors.New("图片内容与 MIME 不匹配")
}

func newContentModerationSnapshotClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// 快照下载必须直连已校验的公网 IP，避免代理重新解析主机后绕过私网限制。
		Proxy: nil,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := resolveContentModerationPublicIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   contentModerationSnapshotTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("图片重定向次数过多")
			}
			return validateContentModerationRemoteURL(req.Context(), req.URL)
		},
	}
}

func validateContentModerationRemoteURL(ctx context.Context, value *url.URL) error {
	if value == nil || (value.Scheme != "http" && value.Scheme != "https") || strings.TrimSpace(value.Hostname()) == "" {
		return errors.New("远程图片 URL 无效")
	}
	_, err := resolveContentModerationPublicIPs(ctx, value.Hostname())
	return err
}

func resolveContentModerationPublicIPs(ctx context.Context, host string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("解析图片主机失败: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("图片主机没有可用地址")
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ip := address.IP
		if !isContentModerationPublicIP(ip) {
			return nil, errors.New("禁止访问私网或本地图片地址")
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

func isContentModerationPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return true
}
