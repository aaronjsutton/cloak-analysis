package multiplex

import (
	"bufio"
	"bytes"
	"github.com/cbeuw/Cloak/internal/util"
	"io/ioutil"
	"math/rand"
	"net"
	"testing"
	"time"
)

func setupSesh() *Session {
	sessionKey := make([]byte, 32)
	rand.Read(sessionKey)
	obfuscator, _ := GenerateObfs(0x00, sessionKey)
	return MakeSession(0, UNLIMITED_VALVE, obfuscator, util.ReadTLS)
}

type blackhole struct {
	hole *bufio.Writer
}

func newBlackHole() *blackhole { return &blackhole{hole: bufio.NewWriter(ioutil.Discard)} }
func (b *blackhole) Read([]byte) (int, error) {
	time.Sleep(1 * time.Hour)
	return 0, nil
}
func (b *blackhole) Write(in []byte) (int, error) { return b.hole.Write(in) }
func (b *blackhole) Close() error                 { return nil }
func (b *blackhole) LocalAddr() net.Addr {
	ret, _ := net.ResolveTCPAddr("tcp", "127.0.0.1")
	return ret
}
func (b *blackhole) RemoteAddr() net.Addr {
	ret, _ := net.ResolveTCPAddr("tcp", "127.0.0.1")
	return ret
}
func (b *blackhole) SetDeadline(t time.Time) error      { return nil }
func (b *blackhole) SetReadDeadline(t time.Time) error  { return nil }
func (b *blackhole) SetWriteDeadline(t time.Time) error { return nil }

func BenchmarkStream_Write(b *testing.B) {
	const PAYLOAD_LEN = 1 << 20 * 100
	hole := newBlackHole()
	sesh := setupSesh()
	sesh.AddConnection(hole)
	testData := make([]byte, PAYLOAD_LEN)
	rand.Read(testData)

	stream, _ := sesh.OpenStream()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := stream.Write(testData)
		if err != nil {
			b.Error(
				"For", "stream write",
				"got", err,
			)
		}
		b.SetBytes(PAYLOAD_LEN)
	}
}

func TestStream_Read(t *testing.T) {
	sesh := setupSesh()
	testPayload := []byte{42, 42, 42}
	const PAYLOAD_LEN = 3

	f := &Frame{
		1,
		0,
		0,
		testPayload,
	}

	ch := make(chan []byte)
	l, _ := net.Listen("tcp", ":0")
	go func() {
		conn, _ := net.Dial("tcp", l.Addr().String())
		for {
			data := <-ch
			_, err := conn.Write(data)
			if err != nil {
				t.Error("cannot write to connection", err)
			}
		}
	}()
	conn, _ := l.Accept()
	sesh.AddConnection(conn)

	var streamID uint32
	buf := make([]byte, 10)
	t.Run("Plain read", func(t *testing.T) {
		f.StreamID = streamID
		obfsed, _ := sesh.Obfs(f)
		streamID++
		ch <- obfsed
		stream, err := sesh.Accept()
		if err != nil {
			t.Error("failed to accept stream", err)
		}
		i, err := stream.Read(buf)
		if err != nil {
			t.Error("failed to read", err)
		}
		if i != PAYLOAD_LEN {
			t.Errorf("expected read %v, got %v", PAYLOAD_LEN, i)
		}
		if !bytes.Equal(buf[:i], testPayload) {
			t.Error("expected", testPayload,
				"got", buf[:i])
		}
	})
	t.Run("Nil buf", func(t *testing.T) {
		f.StreamID = streamID
		obfsed, _ := sesh.Obfs(f)
		streamID++
		ch <- obfsed
		stream, _ := sesh.Accept()
		i, err := stream.Read(nil)
		if i != 0 || err != nil {
			t.Error("expecting", 0, nil,
				"got", i, err)
		}

		stream.Close()
		i, err = stream.Read(nil)
		if i != 0 || err != ErrBrokenStream {
			t.Error("expecting", 0, ErrBrokenStream,
				"got", i, err)
		}

	})
	t.Run("Read after stream close", func(t *testing.T) {
		f.StreamID = streamID
		obfsed, _ := sesh.Obfs(f)
		streamID++
		ch <- obfsed
		stream, _ := sesh.Accept()
		stream.Close()
		i, err := stream.Read(buf)
		if err != nil {
			t.Error("failed to read", err)
		}
		if i != PAYLOAD_LEN {
			t.Errorf("expected read %v, got %v", PAYLOAD_LEN, i)
		}
		if !bytes.Equal(buf[:i], testPayload) {
			t.Error("expected", testPayload,
				"got", buf[:i])
		}
		_, err = stream.Read(buf)
		if err == nil {
			t.Error("expecting error", ErrBrokenStream,
				"got nil error")
		}
	})
	t.Run("Read after session close", func(t *testing.T) {
		f.StreamID = streamID
		obfsed, _ := sesh.Obfs(f)
		streamID++
		ch <- obfsed
		stream, _ := sesh.Accept()
		sesh.Close()
		i, err := stream.Read(buf)
		if err != nil {
			t.Error("failed to read", err)
		}
		if i != PAYLOAD_LEN {
			t.Errorf("expected read %v, got %v", PAYLOAD_LEN, i)
		}
		if !bytes.Equal(buf[:i], testPayload) {
			t.Error("expected", testPayload,
				"got", buf[:i])
		}
		_, err = stream.Read(buf)
		if err == nil {
			t.Error("expecting error", ErrBrokenStream,
				"got nil error")
		}
	})

}