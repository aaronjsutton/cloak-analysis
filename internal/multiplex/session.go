package multiplex

import (
	"log"
	"net"
	"sync"
)

const (
	// Copied from smux
	errBrokenPipe          = "broken pipe"
	errRepeatStreamClosing = "trying to close a closed stream"
	acceptBacklog          = 1024

	closeBacklog = 512
)

type Session struct {
	id int

	// Used in Stream.Write. Add multiplexing headers, encrypt and add TLS header
	obfs func(*Frame) []byte
	// Remove TLS header, decrypt and unmarshall multiplexing headers
	deobfs func([]byte) *Frame
	// This is supposed to read one TLS message, the same as GoQuiet's ReadTillDrain
	obfsedReader func(net.Conn, []byte) (int, error)

	nextStreamIDM sync.Mutex
	nextStreamID  uint32

	streamsM sync.RWMutex
	streams  map[uint32]*Stream

	// Switchboard manages all connections to remote
	sb *switchboard

	// For accepting new streams
	acceptCh chan *Stream
	// Once a stream.Close is called, it sends its streamID to this channel
	// to be read by another stream to send the streamID to notify the remote
	// that this stream is closed
	closeQCh chan uint32
}

// TODO: put this in main maybe?
// 1 conn is needed to make a session
func MakeSession(id int, conn net.Conn, obfs func(*Frame) []byte, deobfs func([]byte) *Frame, obfsedReader func(net.Conn, []byte) (int, error)) *Session {
	sesh := &Session{
		id:           id,
		obfs:         obfs,
		deobfs:       deobfs,
		obfsedReader: obfsedReader,
		nextStreamID: 1,
		streams:      make(map[uint32]*Stream),
		acceptCh:     make(chan *Stream, acceptBacklog),
		closeQCh:     make(chan uint32, closeBacklog),
	}
	sesh.sb = makeSwitchboard(conn, sesh)
	return sesh
}

func (sesh *Session) AddConnection(conn net.Conn) {
	sesh.sb.newConnCh <- conn
}

func (sesh *Session) OpenStream() (*Stream, error) {
	sesh.nextStreamIDM.Lock()
	id := sesh.nextStreamID
	sesh.nextStreamID += 1
	sesh.nextStreamIDM.Unlock()

	stream := makeStream(id, sesh)

	sesh.streamsM.Lock()
	sesh.streams[id] = stream
	sesh.streamsM.Unlock()
	return stream, nil
}

func (sesh *Session) AcceptStream() (*Stream, error) {
	stream := <-sesh.acceptCh
	return stream, nil

}

func (sesh *Session) delStream(id uint32) {
	sesh.streamsM.RLock()
	delete(sesh.streams, id)
	sesh.streamsM.RUnlock()
}

func (sesh *Session) isStream(id uint32) bool {
	sesh.streamsM.Lock()
	_, ok := sesh.streams[id]
	sesh.streamsM.Unlock()
	return ok
}

func (sesh *Session) getStream(id uint32) *Stream {
	sesh.streamsM.Lock()
	defer sesh.streamsM.Unlock()
	return sesh.streams[id]
}

// addStream is used when the remote opened a new stream and we got notified
func (sesh *Session) addStream(id uint32) *Stream {
	log.Printf("Adding stream %v", id)
	stream := makeStream(id, sesh)
	sesh.streamsM.Lock()
	sesh.streams[id] = stream
	sesh.streamsM.Unlock()
	sesh.acceptCh <- stream
	return stream
}
