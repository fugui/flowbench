package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/fugui/flowbench/internal/codec"
)

// 连接模式常量定义
const (
	ConnModeShort     = "short"     // 每次请求建立独立短连接（推荐用于 Keploy eBPF 录制/回放）
	ConnModeKeepAlive = "keepalive" // 单条长连接多帧顺序复用
)

// ClientConfig 配置 TCP 二进制协议客户端
type ClientConfig struct {
	Name       string
	TargetAddr string
	ConnMode   string
	Timeout    time.Duration
	DebugFrame bool
	Logger     *slog.Logger
}

// Client 表示支持短连接与长连接复用的自定义二进制协议客户端
type Client struct {
	cfg  ClientConfig
	mu   sync.Mutex
	conn net.Conn
}

// NewClient 创建 TCP 二进制协议客户端实例
func NewClient(cfg ClientConfig) *Client {
	if cfg.ConnMode == "" {
		cfg.ConnMode = ConnModeShort
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Client{cfg: cfg}
}

// Call 发送一个请求二进制帧并同步读取匹配的响应二进制帧
func (c *Client) Call(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
	if c.cfg.ConnMode == ConnModeKeepAlive {
		return c.callKeepAlive(ctx, reqFrame)
	}
	return c.callShortConn(ctx, reqFrame)
}

func (c *Client) callShortConn(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
	dialer := net.Dialer{Timeout: c.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.cfg.TargetAddr)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %s failed: %w", c.cfg.TargetAddr, err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			c.cfg.Logger.Debug("关闭短连接提示", "err", closeErr)
		}
	}()

	return c.exchangeFrame(ctx, conn, reqFrame)
}

func (c *Client) callKeepAlive(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		dialer := net.Dialer{Timeout: c.cfg.Timeout}
		conn, err := dialer.DialContext(ctx, "tcp", c.cfg.TargetAddr)
		if err != nil {
			return nil, fmt.Errorf("transport: dial keepalive %s failed: %w", c.cfg.TargetAddr, err)
		}
		c.conn = conn
	}

	resp, err := c.exchangeFrame(ctx, c.conn, reqFrame)
	if err != nil {
		if closeErr := c.conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			c.cfg.Logger.Debug("重置长连接提示", "err", closeErr)
		}
		c.conn = nil
		return nil, err
	}
	return resp, nil
}

func (c *Client) exchangeFrame(ctx context.Context, conn net.Conn, reqFrame *codec.Frame) (*codec.Frame, error) {
	deadline := time.Now().Add(c.cfg.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("transport: set deadline failed: %w", err)
	}

	if c.cfg.DebugFrame {
		LogFrameDebug(c.cfg.Logger, c.cfg.Name, "SEND", reqFrame)
	}

	if err := codec.WriteFrame(conn, reqFrame); err != nil {
		return nil, fmt.Errorf("transport: write request frame failed: %w", err)
	}

	respFrame, err := codec.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("transport: read response frame failed: %w", err)
	}

	if c.cfg.DebugFrame {
		LogFrameDebug(c.cfg.Logger, c.cfg.Name, "RECV", respFrame)
	}

	if respFrame.Header.RequestID != reqFrame.Header.RequestID {
		return nil, fmt.Errorf("transport: request_id mismatch: sent %d, got %d",
			reqFrame.Header.RequestID, respFrame.Header.RequestID)
	}

	return respFrame, nil
}

// Close 关闭长连接（若存在）
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	return nil
}
