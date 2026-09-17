package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/backup"
)

// PgDumper implements backup.DBDumper using pg_dump/psql
// DatabaseOptions 是装配传入的数据库连接快照。
type DatabaseOptions struct {
	Host                            string
	Port                            int
	User, Password, DBName, SSLMode string
}
type PgDumper struct {
	cfg *DatabaseOptions
}

// NewPgDumper creates a new PgDumper
func NewPgDumper(cfg DatabaseOptions) backup.DBDumper {
	return &PgDumper{cfg: &cfg}
}

// Dump executes pg_dump and returns a streaming reader of the output
func (d *PgDumper) Dump(ctx context.Context, opts backup.BackupDumpOptions) (io.ReadCloser, error) {
	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--no-owner",
		"--no-acl",
		"--clean",
		"--if-exists",
	}
	for _, tablePattern := range opts.ExcludeTableData {
		// 只跳过表数据，保留结构、约束和索引，避免恢复后缺表。
		args = append(args, "--exclude-table-data="+tablePattern)
	}

	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	if d.cfg.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+d.cfg.Password)
	}
	if d.cfg.SSLMode != "" {
		cmd.Env = append(cmd.Environ(), "PGSSLMODE="+d.cfg.SSLMode)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start pg_dump: %w", err)
	}

	// 返回一个 ReadCloser：读 stdout，关闭时等待进程退出
	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd}, nil
}

// Restore executes psql to restore from a streaming reader
// @project-doc docs/operations/deployment_and_migrations.md#maintenance_execution
func (d *PgDumper) Restore(ctx context.Context, data io.Reader) error {
	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--single-transaction",
		"--set=ON_ERROR_STOP=1",
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "psql", args...)
	if d.cfg.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+d.cfg.Password)
	}
	if d.cfg.SSLMode != "" {
		cmd.Env = append(cmd.Environ(), "PGSSLMODE="+d.cfg.SSLMode)
	}

	// 输入只有完整读取后才关闭管道，损坏归档先取消 psql，避免 EOF 触发提交。
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	_, copyErr := io.Copy(stdin, data)
	if copyErr != nil {
		cancel()
	}
	closeErr := stdin.Close()
	waitErr := cmd.Wait()
	if err := errors.Join(copyErr, closeErr, waitErr); err != nil {
		return fmt.Errorf("%w: %s", err, output.String())
	}
	return nil
}

// cmdReadCloser wraps a command stdout pipe and waits for the process on Close
type cmdReadCloser struct {
	io.ReadCloser
	cmd      *exec.Cmd
	once     sync.Once
	closeErr error
}

func (c *cmdReadCloser) Close() error {
	c.once.Do(func() {
		_ = c.ReadCloser.Close()
		if err := c.cmd.Wait(); err != nil {
			c.closeErr = fmt.Errorf("pg_dump exited with error: %w", err)
		}
	})
	return c.closeErr
}
