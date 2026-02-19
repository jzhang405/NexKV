package transport

import (
	"bufio"
	"io"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/libp2p/go-libp2p/core/network"
)

// 确保实现 service.Stream 接口
var _ service.Stream = (*Libp2pStream)(nil)

// Libp2pStream 实现 Stream 接口
type Libp2pStream struct {
	stream   network.Stream
	protocol string
}

// NewLibp2pStream 创建新的 Stream
func NewLibp2pStream(stream network.Stream, protocol string) *Libp2pStream {
	return &Libp2pStream{
		stream:   stream,
		protocol: protocol,
	}
}

// ID 返回流 ID
func (s *Libp2pStream) ID() string {
	return s.stream.ID()
}

// Protocol 返回协议名称
func (s *Libp2pStream) Protocol() string {
	return s.protocol
}

// RemotePeer 返回远程节点 ID
func (s *Libp2pStream) RemotePeer() model.PeerID {
	return model.PeerID(s.stream.Conn().RemotePeer().String())
}

// Read 读取数据
func (s *Libp2pStream) Read(p []byte) (n int, err error) {
	return s.stream.Read(p)
}

// Write 写入数据
func (s *Libp2pStream) Write(p []byte) (n int, err error) {
	return s.stream.Write(p)
}

// Close 关闭流
func (s *Libp2pStream) Close() error {
	return s.stream.Close()
}

// SetReadDeadline 设置读超时
func (s *Libp2pStream) SetReadDeadline(t interface{ UnixNano() int64 }) error {
	return s.stream.SetReadDeadline(time.Unix(0, t.UnixNano()))
}

// SetWriteDeadline 设置写超时
func (s *Libp2pStream) SetWriteDeadline(t interface{ UnixNano() int64 }) error {
	return s.stream.SetWriteDeadline(time.Unix(0, t.UnixNano()))
}

// SetDeadline 设置读写超时
func (s *Libp2pStream) SetDeadline(t interface{ UnixNano() int64 }) error {
	return s.stream.SetDeadline(time.Unix(0, t.UnixNano()))
}

// Reset 重置流（发送 RST）
func (s *Libp2pStream) Reset() error {
	return s.stream.Reset()
}

// WriteWithCodec 使用编解码器写入消息
func (s *Libp2pStream) WriteWithCodec(codec *LengthPrefixedCodec, msg []byte) error {
	writer := bufio.NewWriterSize(s.stream, DefaultBufferSize)
	if err := codec.Encode(writer, msg); err != nil {
		return errors.Wrap(err, "encode message")
	}
	return writer.Flush()
}

// ReadWithCodec 使用编解码器读取消息
func (s *Libp2pStream) ReadWithCodec(codec *LengthPrefixedCodec) ([]byte, error) {
	reader := bufio.NewReaderSize(s.stream, DefaultBufferSize)
	data, err := codec.Decode(reader)
	if err != nil {
		if err == io.EOF {
			return nil, errors.ErrChannelClosed
		}
		return nil, errors.Wrap(err, "decode message")
	}
	return data, nil
}
