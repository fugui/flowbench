// Package transport 提供基于原生 TCP Socket (net.Conn) 的自定义二进制协议服务端与客户端传输实现。
package transport

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
)

// FrameHandler 定义服务端处理单个自定义二进制协议帧的回调函数签名
type FrameHandler func(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error)

// ServerConfig 配置原生 TCP 二进制协议服务端
type ServerConfig struct {
	Name       string
	ListenAddr string
	IOTimeout  time.Duration
	DebugFrame bool
	Logger     *slog.Logger
}

// Server 表示原生 TCP 二进制协议服务端
type Server struct {
	cfg      ServerConfig
	handler  FrameHandler
	mu       sync.Mutex
	listener net.Listener
	wg       sync.WaitGroup
	closed   bool
}

// NewServer 创建 TCP 二进制协议服务端实例
func NewServer(cfg ServerConfig, handler FrameHandler) *Server {
	if cfg.IOTimeout <= 0 {
		cfg.IOTimeout = 5 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{
		cfg:     cfg,
		handler: handler,
	}
}

// Start Bind 并启动 TCP 监听（非阻塞返回实际监听地址，后台启动 Accept 循环）
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return "", fmt.Errorf("transport: listen on %s failed: %w", s.cfg.ListenAddr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	actualAddr := ln.Addr().String()
	s.cfg.Logger.Info("TCP 二进制协议服务端已启动监听",
		"service", s.cfg.Name,
		"addr", actualAddr,
	)

	s.wg.Add(1)
	go s.acceptLoop(ln)
	return actualAddr, nil
}

// Addr 返回当前实际监听地址
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) acceptLoop(ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			s.cfg.Logger.Warn("Accept 连接异常", "service", s.cfg.Name, "err", err)
			continue
		}

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			s.cfg.Logger.Error("连接处理发生 Panic 已恢复", "service", s.cfg.Name, "recover", r)
		}
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.cfg.Logger.Debug("关闭连接提示", "service", s.cfg.Name, "err", err)
		}
	}()

	for {
		if err := conn.SetReadDeadline(time.Now().Add(s.cfg.IOTimeout)); err != nil {
			return
		}

		reqFrame, err := codec.ReadFrame(conn)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			// 若是空闲连接超时关闭，不视为协议错误
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return
			}
			s.cfg.Logger.Warn("读取请求帧失败",
				"service", s.cfg.Name,
				"remote", conn.RemoteAddr().String(),
				"err", err,
			)
			return
		}

		if s.cfg.DebugFrame {
			LogFrameDebug(s.cfg.Logger, s.cfg.Name, "RECV", reqFrame)
		}

		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.IOTimeout)
		respFrame, handleErr := s.handler(ctx, reqFrame)
		cancel()

		if handleErr != nil {
			s.cfg.Logger.Warn("业务处理器返回错误，封装协议异常帧",
				"service", s.cfg.Name,
				"req_id", reqFrame.Header.RequestID,
				"err", handleErr,
			)
			errMsg := &pb.ErrorResponse{
				ErrorCode:    int32(codec.StatusInternalError),
				ErrorMessage: handleErr.Error(),
				RequestId:    reqFrame.Header.RequestID,
			}
			var encErr error
			respFrame, encErr = protocol.EncodeMessageFrame(
				codec.OpErrorResp,
				reqFrame.Header.RequestID,
				reqFrame.Header.Flags,
				codec.StatusInternalError,
				errMsg,
			)
			if encErr != nil {
				return
			}
		}

		if s.cfg.DebugFrame {
			LogFrameDebug(s.cfg.Logger, s.cfg.Name, "SEND", respFrame)
		}

		if err := conn.SetWriteDeadline(time.Now().Add(s.cfg.IOTimeout)); err != nil {
			return
		}
		if err := codec.WriteFrame(conn, respFrame); err != nil {
			s.cfg.Logger.Warn("写入响应帧失败",
				"service", s.cfg.Name,
				"req_id", reqFrame.Header.RequestID,
				"err", err,
			)
			return
		}
	}
}

// Shutdown 优雅关闭 TCP 服务端并等待所有活跃连接排空
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	ln := s.listener
	s.mu.Unlock()

	if ln != nil {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.cfg.Logger.Info("TCP 二进制协议服务端已优雅停机", "service", s.cfg.Name)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// LogFrameDebug 输出二进制帧头与 Payload 的十六进制摘要调试日志
func LogFrameDebug(logger *slog.Logger, component string, direction string, f *codec.Frame) {
	headerBytes := codec.EncodeHeader(f.Header)
	previewLen := len(f.Payload)
	if previewLen > 64 {
		previewLen = 64
	}
	logger.Info("Frame Debug Hex",
		"component", component,
		"dir", direction,
		"op", codec.OpCodeName(f.Header.OpCode),
		"req_id", f.Header.RequestID,
		"flags", fmt.Sprintf("0x%02X", f.Header.Flags),
		"status", fmt.Sprintf("0x%02X", f.Header.Status),
		"payload_len", f.Header.PayloadLen,
		"crc32", fmt.Sprintf("0x%08X", f.Header.CRC32),
		"header_hex", hex.EncodeToString(headerBytes[:]),
		"payload_hex_preview", hex.EncodeToString(f.Payload[:previewLen]),
	)
}
