// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	bufio "bufio"
	context "context"
	ecdsa "crypto/ecdsa"
	elliptic "crypto/elliptic"
	rand "crypto/rand"
	tls "crypto/tls"
	x509 "crypto/x509"
	pkix "crypto/x509/pkix"
	json "encoding/json"
	pem "encoding/pem"
	errors "errors"
	fmt "fmt"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	tlsfingerprint "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	big "math/big"
	net "net"
	http "net/http"
	strings "strings"
	sync "sync"
	time "time"
)

type TLSFingerprintCollectorStatus = egress.TLSFingerprintCollectorStatus
type TLSFingerprintCollectorSession = egress.TLSFingerprintCollectorSession
type TLSFingerprintCaptureRecord = egress.TLSFingerprintCaptureRecord

// CollectorOptions 由 app 投影配置，不包含完整应用配置或业务服务。
type CollectorOptions struct {
	Host                 string
	Port                 int
	PublicBaseURL        string
	CertFile             string
	KeyFile              string
	SessionTTLSeconds    int
	MaxRecordsPerSession int
	Now                  func() time.Time
	Diagnostics          egress.Diagnostics
}

func NewTLSFingerprintCollectorService(options CollectorOptions) *TLSFingerprintCollectorService {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &TLSFingerprintCollectorService{options: options, now: now, sessions: egress.NewCaptureSessions(now)}
}
func (s *TLSFingerprintCollectorService) collectorConfigLocked() CollectorOptions { return s.options }

const (
	tlsFingerprintCollectorDefaultTTL        = 30 * time.Minute
	tlsFingerprintCollectorDefaultMaxRecords = 20
	// TLS 记录长度字段最大为 65535，Peek 时还需要包含 5 字节记录头。
	tlsFingerprintCollectorClientHelloBufferSize = 64*1024 + 5
)

// TLSFingerprintCollectorService 管理运行时 TLS 指纹收集器。
type TLSFingerprintCollectorService struct {
	closed    bool
	serveDone chan struct{}
	options   CollectorOptions
	now       func() time.Time

	mu            sync.Mutex
	server        *http.Server
	listener      net.Listener
	running       bool
	startedAt     *time.Time
	lastError     string
	caPEM         string
	cert          tls.Certificate
	generatedCert bool
	sessions      *egress.CaptureSessions
}

type tlsFingerprintCaptureContextKey struct{}

type tlsFingerprintCaptureContext struct {
	clientHello    *tlsfingerprint.CapturedClientHello
	negotiatedALPN string
}

// Status 返回收集器状态。
func (s *TLSFingerprintCollectorService) Status() TLSFingerprintCollectorStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

// Start 启动独立 HTTPS 收集器监听。
func (s *TLSFingerprintCollectorService) Start(ctx context.Context) (TLSFingerprintCollectorStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.statusLocked(), errors.New("TLS fingerprint collector is shutting down")
	}
	if s.running {
		return s.statusLocked(), nil
	}

	cert, caPEM, generated, err := s.loadOrGenerateCertificateLocked()
	if err != nil {
		s.lastError = err.Error()
		return s.statusLocked(), err
	}

	listenAddr := s.listenAddressLocked()
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		s.lastError = err.Error()
		return s.statusLocked(), err
	}

	now := s.now()
	s.cert = cert
	s.caPEM = caPEM
	s.generatedCert = generated
	s.startedAt = &now
	s.lastError = ""
	s.sessions = egress.NewCaptureSessions(s.now)
	certificate := s.cert
	s.listener = newTLSFingerprintCaptureListener(ln, &certificate)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleCaptureRequest)
	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if captureConn, ok := conn.(*tlsFingerprintCaptureConn); ok {
				return context.WithValue(ctx, tlsFingerprintCaptureContextKey{}, captureConn)
			}
			return ctx
		},
		TLSConfig: &tls.Config{
			// 收集器只需要采集客户端声明的 ALPN，实际响应固定走 HTTP/1.1，避免 h2 请求解析复杂度。
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"},
		},
	}
	s.running = true

	server := s.server
	listener := s.listener
	done := make(chan struct{})
	s.serveDone = done
	go func() {
		defer close(done)
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.options.Diagnostics.Log("service.tls_fp_collector", "[TLSFPCollector] serve failed: %v", serveErr)
			s.mu.Lock()
			if s.server != server {
				s.mu.Unlock()
				return
			}
			s.running = false
			s.lastError = serveErr.Error()
			s.server = nil
			s.listener = nil
			s.mu.Unlock()
		}
	}()
	return s.statusLocked(), nil
}

// Stop 停止收集器并清空短期会话。
func (s *TLSFingerprintCollectorService) Stop(ctx context.Context) error {
	s.mu.Lock()
	server := s.server
	done := s.serveDone
	s.serveDone = nil
	s.running = false
	s.server = nil
	s.listener = nil
	s.startedAt = nil
	s.caPEM = ""
	s.cert = tls.Certificate{}
	s.generatedCert = false
	s.sessions = egress.NewCaptureSessions(s.now)
	s.mu.Unlock()

	if server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}

	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Shutdown 封闭按需启动入口，再停止当前监听；管理员普通 Stop 仍可再次启动。
func (s *TLSFingerprintCollectorService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.Stop(ctx)
}

func (s *TLSFingerprintCollectorService) CreateSession() (*TLSFingerprintCollectorSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return nil, errors.New("TLS fingerprint collector is not running")
	}
	return s.sessions.Create(s.publicBaseURLLocked(), s.caPEM, s.sessionTTLLocked())
}

func (s *TLSFingerprintCollectorService) ListCaptures(token string) ([]*TLSFingerprintCaptureRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions.List(token)
}

func (s *TLSFingerprintCollectorService) DeleteSession(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions.Delete(token)
}

func (s *TLSFingerprintCollectorService) handleCaptureRequest(w http.ResponseWriter, r *http.Request) {
	token := captureTokenFromRequest(r)
	if token == "" {
		writeCollectorJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing token"})
		return
	}
	captureConn, _ := r.Context().Value(tlsFingerprintCaptureContextKey{}).(*tlsFingerprintCaptureConn)
	if captureConn == nil {
		writeCollectorJSON(w, http.StatusBadRequest, map[string]any{"error": "missing tls connection"})
		return
	}
	captureCtx := captureConn.captureContext()
	if captureCtx == nil || captureCtx.clientHello == nil {
		writeCollectorJSON(w, http.StatusBadRequest, map[string]any{"error": "missing tls fingerprint"})
		return
	}

	record := s.buildCaptureRecord(r, captureCtx)
	if err := s.appendCapture(token, record); err != nil {
		writeCollectorJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}
	writeCollectorJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"captured_id": record.ID,
		"message":     "TLS fingerprint captured",
	})
}

func (s *TLSFingerprintCollectorService) appendCapture(token string, record *TLSFingerprintCaptureRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions.Append(token, record, s.maxRecordsLocked())
}

func (s *TLSFingerprintCollectorService) buildCaptureRecord(r *http.Request, captureCtx *tlsFingerprintCaptureContext) *TLSFingerprintCaptureRecord {
	now := time.Now()
	ch := captureCtx.clientHello
	clientKind := detectTLSFingerprintClientKind(r)
	profile := &egress.TLSFingerprintProfile{
		Name:                defaultTLSFingerprintProfileName(clientKind, now),
		Description:         tlsFingerprintCollectorStringPtr("由内置 TLS 指纹收集器采集"),
		EnableGREASE:        ch.EnableGREASE,
		CipherSuites:        ch.CipherSuites,
		Curves:              ch.Curves,
		PointFormats:        ch.PointFormats,
		SignatureAlgorithms: ch.SignatureAlgorithms,
		ALPNProtocols:       ch.ALPNProtocols,
		SupportedVersions:   ch.SupportedVersions,
		KeyShareGroups:      ch.KeyShareGroups,
		PSKModes:            ch.PSKModes,
		Extensions:          ch.Extensions,
	}
	record := &TLSFingerprintCaptureRecord{
		ID:                fmt.Sprintf("%d", now.UnixNano()),
		CapturedAt:        now,
		ClientKind:        clientKind,
		RequestPath:       r.URL.Path,
		Method:            r.Method,
		UserAgent:         r.UserAgent(),
		JA3Raw:            ch.JA3Raw,
		JA3Hash:           ch.JA3Hash,
		NegotiatedALPN:    captureCtx.negotiatedALPN,
		HTTPProto:         r.Proto,
		Profile:           profile,
		YAML:              tlsFingerprintProfileToYAML(profile),
		HeadersSummary:    summarizeTLSFingerprintHeaders(r.Header),
		StainlessSummary:  summarizeStainlessHeaders(r.Header),
		RawTLSFingerprint: (*egress.CapturedClientHello)(ch),
	}
	return record
}

func (s *TLSFingerprintCollectorService) statusLocked() TLSFingerprintCollectorStatus {
	return TLSFingerprintCollectorStatus{
		Running:              s.running,
		ListenAddress:        s.listenAddressLocked(),
		PublicBaseURL:        s.publicBaseURLLocked(),
		UsingGeneratedCert:   s.generatedCert,
		CAPEM:                s.caPEM,
		SessionTTLSeconds:    int(s.sessionTTLLocked().Seconds()),
		MaxRecordsPerSession: s.maxRecordsLocked(),
		StartedAt:            s.startedAt,
		LastError:            s.lastError,
	}
}

func (s *TLSFingerprintCollectorService) loadOrGenerateCertificateLocked() (tls.Certificate, string, bool, error) {
	collectorCfg := s.collectorConfigLocked()
	certFile := strings.TrimSpace(collectorCfg.CertFile)
	keyFile := strings.TrimSpace(collectorCfg.KeyFile)
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return tls.Certificate{}, "", false, errors.New("cert_file and key_file must be configured together")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return tls.Certificate{}, "", false, err
		}
		return cert, "", false, nil
	}
	cert, caPEM, err := generateTLSFingerprintCollectorCertificate(s.publicHostLocked())
	return cert, caPEM, true, err
}

func (s *TLSFingerprintCollectorService) listenAddressLocked() string {
	collectorCfg := s.collectorConfigLocked()
	host := strings.TrimSpace(collectorCfg.Host)
	if host == "" {
		host = "0.0.0.0"
	}
	port := collectorCfg.Port
	if port <= 0 {
		port = 8443
	}
	return net.JoinHostPort(host, strconvItoa(port))
}

func (s *TLSFingerprintCollectorService) publicBaseURLLocked() string {
	collectorCfg := s.collectorConfigLocked()
	if raw := strings.TrimRight(strings.TrimSpace(collectorCfg.PublicBaseURL), "/"); raw != "" {
		return raw
	}
	host := s.publicHostLocked()
	port := collectorCfg.Port
	if port <= 0 {
		port = 8443
	}
	return fmt.Sprintf("https://%s", net.JoinHostPort(host, strconvItoa(port)))
}

func (s *TLSFingerprintCollectorService) publicHostLocked() string {
	collectorCfg := s.collectorConfigLocked()
	if raw := strings.TrimSpace(collectorCfg.PublicBaseURL); raw != "" {
		if host := hostFromURL(raw); host != "" {
			return host
		}
	}
	host := strings.TrimSpace(collectorCfg.Host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "localhost"
	}
	return host
}

func (s *TLSFingerprintCollectorService) sessionTTLLocked() time.Duration {
	seconds := s.collectorConfigLocked().SessionTTLSeconds
	if seconds <= 0 {
		return tlsFingerprintCollectorDefaultTTL
	}
	return time.Duration(seconds) * time.Second
}

func (s *TLSFingerprintCollectorService) maxRecordsLocked() int {
	maxRecords := s.collectorConfigLocked().MaxRecordsPerSession
	if maxRecords <= 0 {
		return tlsFingerprintCollectorDefaultMaxRecords
	}
	return maxRecords
}

type tlsFingerprintCaptureListener struct {
	net.Listener
	cert *tls.Certificate
}

func newTLSFingerprintCaptureListener(inner net.Listener, cert *tls.Certificate) net.Listener {
	return &tlsFingerprintCaptureListener{Listener: inner, cert: cert}
}

func (l *tlsFingerprintCaptureListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return newTLSFingerprintCaptureConn(conn, l.cert), nil
}

type tlsFingerprintCaptureConn struct {
	net.Conn
	cert           *tls.Certificate
	reader         *bufio.Reader
	captured       *tlsfingerprint.CapturedClientHello
	negotiatedALPN string
	handshakeOnce  sync.Once
	handshakeErr   error
	tlsConn        *tls.Conn
}

func newTLSFingerprintCaptureConn(conn net.Conn, cert *tls.Certificate) *tlsFingerprintCaptureConn {
	return &tlsFingerprintCaptureConn{
		Conn:   conn,
		cert:   cert,
		reader: bufio.NewReaderSize(conn, tlsFingerprintCollectorClientHelloBufferSize),
	}
}

func (c *tlsFingerprintCaptureConn) Read(p []byte) (int, error) {
	if err := c.ensureHandshake(); err != nil {
		return 0, err
	}
	return c.tlsConn.Read(p)
}

func (c *tlsFingerprintCaptureConn) Write(p []byte) (int, error) {
	if err := c.ensureHandshake(); err != nil {
		return 0, err
	}
	return c.tlsConn.Write(p)
}

func (c *tlsFingerprintCaptureConn) Close() error {
	if c.tlsConn != nil {
		return c.tlsConn.Close()
	}
	return c.Conn.Close()
}

func (c *tlsFingerprintCaptureConn) ensureHandshake() error {
	c.handshakeOnce.Do(func() {
		if err := c.captureClientHello(); err != nil {
			c.handshakeErr = err
			return
		}
		tlsConn := tls.Server(&readerConn{Conn: c.Conn, reader: c.reader}, &tls.Config{
			Certificates: []tls.Certificate{*c.cert},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"http/1.1"},
		})
		if err := tlsConn.Handshake(); err != nil {
			c.handshakeErr = err
			return
		}
		c.tlsConn = tlsConn
		c.negotiatedALPN = tlsConn.ConnectionState().NegotiatedProtocol
	})
	return c.handshakeErr
}

func (c *tlsFingerprintCaptureConn) captureClientHello() error {
	header, err := c.reader.Peek(5)
	if err != nil {
		return err
	}
	recordLen := int(header[3])<<8 | int(header[4])
	if recordLen <= 0 {
		return errors.New("invalid TLS record length")
	}
	record, err := c.reader.Peek(5 + recordLen)
	if err != nil {
		return err
	}
	captured, err := tlsfingerprint.ParseCapturedClientHello(record)
	if err != nil {
		return err
	}
	c.captured = captured
	return nil
}

func (c *tlsFingerprintCaptureConn) captureContext() *tlsFingerprintCaptureContext {
	if c.captured == nil {
		return nil
	}
	return &tlsFingerprintCaptureContext{
		clientHello:    c.captured,
		negotiatedALPN: c.negotiatedALPN,
	}
}

type readerConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *readerConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func writeCollectorJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func generateTLSFingerprintCollectorCertificate(host string) (tls.Certificate, string, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "TokenRouter TLS Fingerprint Collector CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano() + 1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	addCertificateHost(serverTemplate, host)
	addCertificateHost(serverTemplate, "localhost")
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	keyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	return cert, caPEM, nil
}

func addCertificateHost(cert *x509.Certificate, host string) {
	host = strings.TrimSpace(host)
	if host == "" {
		return
	}
	if ip := net.ParseIP(host); ip != nil {
		cert.IPAddresses = append(cert.IPAddresses, ip)
		return
	}
	if strings.Contains(host, ":") {
		if splitHost, _, err := net.SplitHostPort(host); err == nil {
			addCertificateHost(cert, splitHost)
			return
		}
	}
	cert.DNSNames = append(cert.DNSNames, host)
}

func detectTLSFingerprintClientKind(r *http.Request) string {
	ua := strings.ToLower(r.UserAgent())
	path := strings.ToLower(r.URL.Path)
	switch {
	case strings.Contains(ua, "claude") || strings.Contains(path, "messages"):
		return "claude_code"
	case strings.Contains(ua, "codex") || strings.Contains(path, "codex") || strings.Contains(path, "responses"):
		return "codex"
	default:
		return "unknown"
	}
}

func captureTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}
	if token := strings.TrimSpace(r.Header.Get("X-TLS-Fingerprint-Token")); token != "" {
		return token
	}
	// 客户端把采集地址配置为 base URL 后会继续追加自身 API 路径，因此从 /capture/{token}/... 中提取 token。
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "capture" {
		return strings.TrimSpace(parts[1])
	}
	// Claude Code 通过 ANTHROPIC_AUTH_TOKEN 发送鉴权信息，采集器复用该值作为会话 token。
	if token := bearerTokenFromAuthorization(r.Header.Get("Authorization")); token != "" {
		return token
	}
	if token := strings.TrimSpace(r.Header.Get("X-Api-Key")); token != "" {
		return token
	}
	return ""
}

func bearerTokenFromAuthorization(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	fields := strings.Fields(value)
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return strings.TrimSpace(fields[1])
	}
	if len(fields) == 1 {
		return strings.TrimSpace(fields[0])
	}
	return ""
}

func defaultTLSFingerprintProfileName(clientKind string, ts time.Time) string {
	switch clientKind {
	case "claude_code":
		return "Claude Code " + ts.Format("2006-01-02 15:04:05")
	case "codex":
		return "Codex CLI " + ts.Format("2006-01-02 15:04:05")
	default:
		return "Captured TLS " + ts.Format("2006-01-02 15:04:05")
	}
}

func tlsFingerprintProfileToYAML(profile *egress.TLSFingerprintProfile) string {
	if profile == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprint(&b, "captured_profile:\n")
	writeYAMLString(&b, "name", profile.Name)
	if profile.Description != nil {
		writeYAMLString(&b, "description", *profile.Description)
	}
	fmt.Fprintf(&b, "  enable_grease: %t\n", profile.EnableGREASE)
	writeYAMLNumberArray(&b, "cipher_suites", profile.CipherSuites)
	writeYAMLNumberArray(&b, "curves", profile.Curves)
	writeYAMLNumberArray(&b, "point_formats", profile.PointFormats)
	writeYAMLNumberArray(&b, "signature_algorithms", profile.SignatureAlgorithms)
	writeYAMLStringArray(&b, "alpn_protocols", profile.ALPNProtocols)
	writeYAMLNumberArray(&b, "supported_versions", profile.SupportedVersions)
	writeYAMLNumberArray(&b, "key_share_groups", profile.KeyShareGroups)
	writeYAMLNumberArray(&b, "psk_modes", profile.PSKModes)
	writeYAMLNumberArray(&b, "extensions", profile.Extensions)
	return b.String()
}

func writeYAMLString(b *strings.Builder, key, value string) {
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	fmt.Fprintf(b, "  %s: \"%s\"\n", key, escaped)
}

func writeYAMLNumberArray[T ~uint16](b *strings.Builder, key string, values []T) {
	fmt.Fprintf(b, "  %s: [", key)
	for i, value := range values {
		if i > 0 {
			fmt.Fprint(b, ", ")
		}
		fmt.Fprintf(b, "%d", value)
	}
	fmt.Fprint(b, "]\n")
}

func writeYAMLStringArray(b *strings.Builder, key string, values []string) {
	fmt.Fprintf(b, "  %s: [", key)
	for i, value := range values {
		if i > 0 {
			fmt.Fprint(b, ", ")
		}
		escaped := strings.ReplaceAll(value, `"`, `\"`)
		fmt.Fprintf(b, "\"%s\"", escaped)
	}
	fmt.Fprint(b, "]\n")
}

func summarizeTLSFingerprintHeaders(headers http.Header) map[string]string {
	allowed := []string{
		"user-agent",
		"x-stainless-os",
		"x-stainless-arch",
		"x-stainless-runtime",
		"x-stainless-runtime-version",
		"x-stainless-lang",
		"x-stainless-package-version",
		"anthropic-version",
		"openai-organization",
	}
	out := make(map[string]string)
	for _, key := range allowed {
		if value := headers.Get(key); value != "" {
			out[key] = value
		}
	}
	return out
}

func summarizeStainlessHeaders(headers http.Header) map[string]string {
	out := make(map[string]string)
	for key, values := range headers {
		lower := strings.ToLower(key)
		if !strings.HasPrefix(lower, "x-stainless-") || len(values) == 0 {
			continue
		}
		out[lower] = values[0]
	}
	return out
}

func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil || req.URL == nil {
		return ""
	}
	host := req.URL.Hostname()
	if host == "" {
		return req.URL.Host
	}
	return host
}

func tlsFingerprintCollectorStringPtr(v string) *string {
	return &v
}

func strconvItoa(v int) string {
	return fmt.Sprintf("%d", v)
}
