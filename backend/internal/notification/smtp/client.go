// SMTP 技术发送唯一实现；取消不会触发重发。
package smtp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
)

type Client struct{}
type SMTPConfig = contract.SMTPConfig

func New() *Client { return &Client{} }

const smtpDialTimeout = 10 * time.Second
const smtpIOTimeout = 20 * time.Second

var smtpTestRootCAs *x509.CertPool

func sanitizeEmailHeader(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }

// SendEmailWithConfig 使用指定配置发送邮件
func (s *Client) Send(ctx context.Context, config *SMTPConfig, to, subject, body string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil && ctx.Err() != nil {
			resultErr = errors.Join(resultErr, ctx.Err())
		}
	}()
	message, err := buildSMTPMessage(config, to, subject, body)
	if err != nil {
		return err
	}

	client, err := s.connectSMTPContext(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err = client.Mail(message.envelopeFrom); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err = client.Rcpt(message.envelopeTo); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err = w.Write(message.data); err != nil {
		return fmt.Errorf("write msg: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	// 数据写入成功即视为发送成功，忽略部分 SMTP 服务的非标准 QUIT 响应。
	_ = client.Quit()
	return nil
}
func smtpTLSConfig(host string) *tls.Config {
	return &tls.Config{
		ServerName: host,
		// 强制 TLS 1.2+，避免协议降级导致的弱加密风险。
		MinVersion: tls.VersionTLS12,
		RootCAs:    smtpTestRootCAs,
	}
}

// connectSMTP 建立发送和测试共用的 SMTP 会话。
// UseTLS 为 true 时先尝试隐式 TLS；仅当服务端返回明文 SMTP 问候时改走强制
// STARTTLS。UseTLS 为 false 时保留机会式 STARTTLS 行为。
func (s *Client) connectSMTPContext(ctx context.Context, config *SMTPConfig) (*smtp.Client, error) {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	tlsConfig := smtpTLSConfig(config.Host)

	if config.UseTLS {
		dialCtx, cancel := context.WithTimeout(ctx, smtpDialTimeout)
		raw, err := dialer.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("tls dial: %w", err)
		}
		bound := bindCancellation(ctx, raw)
		conn := tls.Client(bound, tlsConfig)
		err = conn.HandshakeContext(dialCtx)
		cancel()
		if err != nil {
			_ = conn.Close()
		}
		if err == nil {
			return newSMTPClient(conn, config.Host)
		}
		var recordErr tls.RecordHeaderError
		if !errors.As(err, &recordErr) {
			return nil, fmt.Errorf("tls dial: %w", err)
		}
		// SMTP 服务先发问候语，明文问候会使 TLS 握手返回 RecordHeaderError。
		return s.connectSMTPStartTLS(ctx, dialer, addr, config.Host, tlsConfig, true)
	}

	return s.connectSMTPStartTLS(ctx, dialer, addr, config.Host, tlsConfig, false)
}

// connectSMTPStartTLS 建立明文连接并按需升级；mandatory 为 true 时禁止明文继续。
func (s *Client) connectSMTPStartTLS(ctx context.Context, dialer *net.Dialer, addr, host string, tlsConfig *tls.Config, mandatory bool) (*smtp.Client, error) {
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("smtp dial: %w", err)
	}
	client, err := newSMTPClient(bindCancellation(ctx, conn), host)
	if err != nil {
		return nil, err
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		if mandatory {
			_ = client.Close()
			return nil, errors.New("smtp server does not support STARTTLS")
		}
		return client, nil
	}
	if err := client.StartTLS(tlsConfig); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("starttls: %w", err)
	}
	return client, nil
}
func newSMTPClient(conn net.Conn, host string) (*smtp.Client, error) {
	_ = conn.SetDeadline(time.Now().Add(smtpIOTimeout))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("new smtp client: %w", err)
	}
	return client, nil
}

// TestSMTPConnectionWithConfig 使用发送路径相同的建连与 STARTTLS 逻辑测试配置。
func (s *Client) Test(ctx context.Context, config *SMTPConfig) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil && ctx.Err() != nil {
			resultErr = errors.Join(resultErr, ctx.Err())
		}
	}()
	client, err := s.connectSMTPContext(ctx, config)
	if err != nil {
		return fmt.Errorf("smtp connection failed: %w", err)
	}
	defer func() { _ = client.Close() }()

	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp authentication failed: %w", err)
	}

	// 认证成功即可证明配置可用于发送，忽略非标准 QUIT 响应。
	_ = client.Quit()
	return nil
}

// contextConn 让取消关闭当前连接；正常关闭会注销回调，避免残留等待任务。
type contextConn struct {
	net.Conn
	stop     func() bool
	deadline time.Time
}

func (c *contextConn) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.Conn.Close()
}
func bindCancellation(ctx context.Context, conn net.Conn) net.Conn {
	wrapped := &contextConn{Conn: conn}
	wrapped.deadline, _ = ctx.Deadline()
	wrapped.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	return wrapped
}

// SetDeadline 保留请求与原 SMTP I/O 上限中较早的截止时间。
func (c *contextConn) SetDeadline(deadline time.Time) error {
	if !c.deadline.IsZero() && (deadline.IsZero() || c.deadline.Before(deadline)) {
		deadline = c.deadline
	}
	return c.Conn.SetDeadline(deadline)
}
func (s *Client) SendEmailWithConfig(cfg *SMTPConfig, to, subject, body string) error {
	return s.Send(context.Background(), cfg, to, subject, body)
}
func (s *Client) TestSMTPConnectionWithConfig(cfg *SMTPConfig) error {
	return s.Test(context.Background(), cfg)
}
