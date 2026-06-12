package qoder

import (
	"crypto/md5"
	"fmt"
)

const (
	AppCode = "cosy"
	Secret  = "d2FyLCB3YXIgbmV2ZXIgY2hhbmdlcw=="
	Sep     = "&"
)

// SignCenterRequest generates an MD5 signature for center.qoder.sh API requests.
func SignCenterRequest(date string) string {
	return md5Hex(fmt.Sprintf("%s%s%s%s%s", AppCode, Sep, Secret, Sep, date))
}

// SignQoderRequest generates an MD5 signature for api1.qoder.sh API requests.
func SignQoderRequest(payloadB64, cosyKey, cosyDate, body, pathWithoutAlgo string) string {
	return md5Hex(fmt.Sprintf("%s\n%s\n%s\n%s\n%s", payloadB64, cosyKey, cosyDate, body, pathWithoutAlgo))
}

// ComposeBearer composes the Authorization header value.
func ComposeBearer(payloadB64, signature string) string {
	return fmt.Sprintf("Bearer COSY.%s.%s", payloadB64, signature)
}

func md5Hex(value string) string {
	h := md5.Sum([]byte(value))
	return fmt.Sprintf("%x", h[:])
}
