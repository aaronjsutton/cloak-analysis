package multiplex

import (
	"log"
	"net"
	"sort"
)

const (
	sentNotifyBacklog = 1024
	dispatchBacklog   = 102400
	newConnBacklog    = 8
)

type switchboard struct {
	session *Session

	ces []*connEnclave

	// For telling dispatcher how many bytes have been sent after Connection.send.
	sentNotifyCh chan *sentNotifier
	dispatCh     chan []byte
	newConnCh    chan net.Conn
	closingCECh  chan *connEnclave
}

// Some data comes from a Stream to be sent through one of the many
// remoteConn, but which remoteConn should we use to send the data?
//
// In this case, we pick the remoteConn that has about the smallest sendQueue.
// Though "smallest" is not guaranteed because it doesn't has to be
type connEnclave struct {
	sb         *switchboard
	remoteConn net.Conn
	sendQueue  int
}

type byQ []*connEnclave

func (a byQ) Len() int {
	return len(a)
}
func (a byQ) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}
func (a byQ) Less(i, j int) bool {
	return a[i].sendQueue < a[j].sendQueue
}

// It takes at least 1 conn to start a switchboard
func makeSwitchboard(conn net.Conn, sesh *Session) *switchboard {
	sb := &switchboard{
		session:      sesh,
		ces:          []*connEnclave{},
		sentNotifyCh: make(chan *sentNotifier, sentNotifyBacklog),
		dispatCh:     make(chan []byte, dispatchBacklog),
		newConnCh:    make(chan net.Conn, newConnBacklog),
		closingCECh:  make(chan *connEnclave, 5),
	}
	ce := &connEnclave{
		sb:         sb,
		remoteConn: conn,
		sendQueue:  0,
	}
	sb.ces = append(sb.ces, ce)
	go sb.deplex(ce)

	go sb.dispatch()
	return sb
}

// Everytime after a remoteConn sends something, it constructs this struct
// Which is sent back to dispatch() through sentNotifyCh to tell dispatch
// how many bytes it has sent
type sentNotifier struct {
	ce   *connEnclave
	sent int
}

func (ce *connEnclave) send(data []byte) {
	// TODO: error handling
	n, _ := ce.remoteConn.Write(data)
	sn := &sentNotifier{
		ce,
		n,
	}
	ce.sb.sentNotifyCh <- sn
}

// Dispatcher sends data coming from a stream to a remote connection
// I used channels here because I didn't want to use mutex
func (sb *switchboard) dispatch() {
	for {
		select {
		// dispatCh receives data from stream.Write
		case data := <-sb.dispatCh:
			go sb.ces[0].send(data)
			sb.ces[0].sendQueue += len(data)
		case notified := <-sb.sentNotifyCh:
			notified.ce.sendQueue -= notified.sent
			sort.Sort(byQ(sb.ces))
		case conn := <-sb.newConnCh:
			newCe := &connEnclave{
				sb:         sb,
				remoteConn: conn,
				sendQueue:  0,
			}
			sb.ces = append(sb.ces, newCe)
			go sb.deplex(newCe)
			sort.Sort(byQ(sb.ces))
		case closing := <-sb.closingCECh:
			for i, ce := range sb.ces {
				if closing == ce {
					sb.ces = append(sb.ces[:i], sb.ces[i+1:]...)
					break
				}
			}
			// TODO: when all connections closed
		}
	}
}

func (sb *switchboard) deplex(ce *connEnclave) {
	buf := make([]byte, 20480)
	for {
		i, err := sb.session.obfsedReader(ce.remoteConn, buf)
		if err != nil {
			log.Println(err)
			go ce.remoteConn.Close()
			sb.closingCECh <- ce
			return
		}
		frame := sb.session.deobfs(buf[:i])
		var stream *Stream
		if stream = sb.session.getStream(frame.StreamID); stream == nil {
			stream = sb.session.addStream(frame.StreamID)
		}
		if closing := sb.session.getStream(frame.ClosingStreamID); closing != nil {
			closing.Close()
		}
		stream.newFrameCh <- frame
	}
}
