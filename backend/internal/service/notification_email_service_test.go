package service

import (
	"bufio"
	"context"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsScheduledReportDeliverySourceIDIncludesReportIdentity(t *testing.T) {
	report := &opsScheduledReport{Name: "日报", ReportType: "daily_summary", Schedule: "0 9 * * *"}
	sourceID := opsScheduledReportDeliverySourceID(report)
	require.Contains(t, sourceID, "daily_summary")
	require.Contains(t, sourceID, "日报")
	require.Contains(t, sourceID, "0 9 * * *")
	require.NotEqual(t, sourceID, opsScheduledReportDeliverySourceID(&opsScheduledReport{Name: "周报", ReportType: "weekly_summary", Schedule: "0 9 * * 1"}))
	require.Equal(t, "scheduled_report", opsScheduledReportDeliverySourceID(nil))
}

type notificationEmailMemorySettingRepo struct {
	mu     sync.RWMutex
	values map[string]string
}

func newNotificationEmailMemorySettingRepo() *notificationEmailMemorySettingRepo {
	return &notificationEmailMemorySettingRepo{values: make(map[string]string)}
}
func (r *notificationEmailMemorySettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}
func (r *notificationEmailMemorySettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}
func (r *notificationEmailMemorySettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}
func (r *notificationEmailMemorySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}
func (r *notificationEmailMemorySettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}
func (r *notificationEmailMemorySettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}
func (r *notificationEmailMemorySettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.values[key]; !ok {
		return ErrSettingNotFound
	}
	delete(r.values, key)
	return nil
}

type notificationEmailTestSMTPServer struct {
	listener      net.Listener
	wg            sync.WaitGroup
	messages      atomic.Int64
	messageMu     sync.Mutex
	messageBodies []string
}

func startNotificationEmailTestSMTPServer(t *testing.T) *notificationEmailTestSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &notificationEmailTestSMTPServer{listener: listener}
	server.wg.Add(1)
	go server.serve()
	t.Cleanup(server.close)
	return server
}
func (s *notificationEmailTestSMTPServer) settings() map[string]string {
	host, port, _ := net.SplitHostPort(s.listener.Addr().String())
	return map[string]string{
		SettingKeySMTPHost:     host,
		SettingKeySMTPPort:     port,
		SettingKeySMTPUsername: "user",
		SettingKeySMTPPassword: "password",
		SettingKeySMTPFrom:     "noreply@example.com",
		SettingKeySMTPFromName: "Sub2API",
		SettingKeySMTPUseTLS:   "false",
	}
}
func (s *notificationEmailTestSMTPServer) messageCount() int64 {
	return s.messages.Load()
}
func (s *notificationEmailTestSMTPServer) lastMessage() string {
	s.messageMu.Lock()
	defer s.messageMu.Unlock()
	if len(s.messageBodies) == 0 {
		return ""
	}
	return s.messageBodies[len(s.messageBodies)-1]
}

// lastMessageBody 解析测试服务器收到的最后一封邮件并解码正文。
func (s *notificationEmailTestSMTPServer) lastMessageBody(t *testing.T) string {
	t.Helper()

	message, err := mail.ReadMessage(strings.NewReader(s.lastMessage()))
	require.NoError(t, err)

	bodyReader := io.Reader(message.Body)
	if strings.EqualFold(message.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		bodyReader = quotedprintable.NewReader(message.Body)
	}
	body, err := io.ReadAll(bodyReader)
	require.NoError(t, err)
	return string(body)
}
func (s *notificationEmailTestSMTPServer) close() {
	_ = s.listener.Close()
	s.wg.Wait()
}
func (s *notificationEmailTestSMTPServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.handleConn(conn)
	}
}
func (s *notificationEmailTestSMTPServer) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	writeLine := func(line string) bool {
		if _, err := rw.WriteString(line + "\r\n"); err != nil {
			return false
		}
		return rw.Flush() == nil
	}
	if !writeLine("220 localhost ESMTP") {
		return
	}
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimRight(line, "\r\n"))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if _, err := rw.WriteString("250-localhost\r\n250 AUTH PLAIN\r\n"); err != nil {
				return
			}
			if err := rw.Flush(); err != nil {
				return
			}
		case strings.HasPrefix(cmd, "AUTH"):
			if !writeLine("235 2.7.0 Authentication successful") {
				return
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !writeLine("250 2.1.0 OK") {
				return
			}
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if !writeLine("250 2.1.5 OK") {
				return
			}
		case strings.HasPrefix(cmd, "DATA"):
			if !writeLine("354 End data with <CR><LF>.<CR><LF>") {
				return
			}
			var message strings.Builder
			for {
				dataLine, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				_, _ = message.WriteString(dataLine)
			}
			s.messageMu.Lock()
			s.messageBodies = append(s.messageBodies, message.String())
			s.messageMu.Unlock()
			s.messages.Add(1)
			if !writeLine("250 2.0.0 OK") {
				return
			}
		case strings.HasPrefix(cmd, "QUIT"):
			_ = writeLine("221 2.0.0 Bye")
			return
		default:
			if !writeLine("250 OK") {
				return
			}
		}
	}
}
