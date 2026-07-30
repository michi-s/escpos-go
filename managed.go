package escpos

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// defaultConnectMaxWait bounds how long ManagedConn will keep retrying a
// connect/reconnect attempt (via ConnectWithRetry) before giving up for
// that attempt. Matches the ceiling a caller doing a one-off dial-per-job
// would reasonably wait for a printer that's still busy with a previous
// job; a printer still unreachable after this long is treated as a real
// failure rather than "just busy."
const defaultConnectMaxWait = 10 * time.Second

// writeDeadline bounds a single Write call once connected, so a printer
// that accepted the TCP connection but then stops draining its receive
// buffer (e.g. wedged mid-job) can't hang a caller indefinitely.
const writeDeadline = 10 * time.Second

// ManagedConn maintains a persistent, auto-reconnecting connection to a
// network ESC/POS printer, with a background goroutine that periodically
// refreshes its status. Unlike Connect (a single connection the caller owns
// and closes itself), a ManagedConn is meant to be created once per
// physical printer and kept for the life of the process: Write reuses the
// already-open connection instead of paying a fresh TCP handshake — and the
// "printer is still busy with the last job" wait that comes with it — on
// every print, and Status reports the latest known condition without
// blocking on I/O.
//
// Safe for concurrent use: Write and the background status poll are
// serialized against each other (and against reconnects) through connMu, so
// two goroutines can never interleave bytes on the same socket. status has
// its OWN separate lock (statusMu) deliberately NOT shared with connMu —
// connMu is held for as long as a connect attempt takes, up to
// defaultConnectMaxWait (10s) for a printer that's down. Status() must
// still return instantly even while that's in progress (e.g. a caller
// polling status every couple of seconds while the very first connect
// attempt is still working through its retry budget) — sharing one lock
// between "read the cached status" and "do the slow I/O that updates it"
// used to make every Status() call queue up behind whatever connect/write
// was in flight, defeating the "never blocks on I/O" promise and making a
// caller's status poll appear to hang for up to 10s after every reconnect.
type ManagedConn struct {
	ip           string
	port         int
	pollInterval time.Duration

	connMu sync.Mutex // guards p and all connect/reconnect/write I/O
	p      *Printer   // nil when disconnected

	statusMu sync.Mutex // guards status ONLY — never held across I/O
	status   PrinterStatus

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewManagedConn creates a managed connection to the printer at ip:port and
// immediately starts its background connect/status-poll loop (an initial
// refresh runs right away, so Status isn't empty for a full pollInterval
// after construction; subsequent refreshes run every pollInterval
// thereafter). The caller must call Close when done to stop the background
// goroutine.
func NewManagedConn(ip string, port int, pollInterval time.Duration) *ManagedConn {
	if pollInterval <= 0 {
		pollInterval = 3 * time.Second
	}
	m := &ManagedConn{
		ip:           ip,
		port:         port,
		pollInterval: pollInterval,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go m.run()
	return m
}

func (m *ManagedConn) run() {
	defer close(m.done)
	m.refresh()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.refresh()
		}
	}
}

// refresh ensures a connection exists (reconnecting if needed) and queries
// status over it, updating the cached status either way. Holds connMu for
// the whole (potentially slow, up to defaultConnectMaxWait) operation, but
// only takes statusMu briefly at the very end to publish the result —
// Status() callers never wait on the slow part.
func (m *ManagedConn) refresh() {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if err := m.ensureConnectedLocked(); err != nil {
		m.setStatus(PrinterStatus{Connected: false, StatusErrors: []string{err.Error()}})
		return
	}
	st := m.p.GetStatus()
	if !st.Connected {
		// GetStatus itself detected the connection is dead (e.g. the
		// printer was unplugged since the last poll) — drop it so the
		// next refresh/Write reconnects from scratch.
		m.p.Close()
		m.p = nil
	}
	m.setStatus(st)
}

// ensureConnectedLocked dials (with retry) if there's no live connection.
// Caller must hold connMu.
func (m *ManagedConn) ensureConnectedLocked() error {
	if m.p != nil {
		return nil
	}
	p, err := ConnectWithRetry(m.ip, m.port, defaultConnectMaxWait)
	if err != nil {
		return err
	}
	m.p = p
	return nil
}

// Write sends data over the managed connection, connecting (or
// reconnecting, with the same busy-printer retry patience as a fresh
// Connect) first if necessary. In the common case — the background poll
// already has a live connection open — this is just a network write, no
// dial involved at all. A write error drops the connection so the next
// Write or background refresh starts clean.
func (m *ManagedConn) Write(data []byte) error {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if err := m.ensureConnectedLocked(); err != nil {
		return fmt.Errorf("connect %s: %w", m.Addr(), err)
	}
	if err := m.p.conn.SetWriteDeadline(time.Now().Add(writeDeadline)); err != nil {
		return err
	}
	_, err := m.p.Write(data)
	m.p.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		m.p.Close()
		m.p = nil
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// Addr returns the host:port this connection was created for, so a caller
// tracking one ManagedConn per logical printer can detect a configuration
// change (e.g. the printer's IP was edited) and replace it.
func (m *ManagedConn) Addr() string {
	return net.JoinHostPort(m.ip, strconv.Itoa(m.port))
}

// setStatus publishes st as the cached status under statusMu. Callers must
// NOT hold statusMu already (it takes the lock itself) but may hold connMu.
func (m *ManagedConn) setStatus(st PrinterStatus) {
	m.statusMu.Lock()
	m.status = st
	m.statusMu.Unlock()
}

// Status returns the most recently known printer status. It never blocks
// on I/O — it's a cached value kept fresh by the background poll (and by
// Write, since a failed write updates the connection state that the next
// poll tick will reflect) — see the ManagedConn doc comment for why this
// is a separate lock from the one guarding the actual connection.
func (m *ManagedConn) Status() PrinterStatus {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	return m.status
}

// Close stops the background poll loop and closes the underlying
// connection, if any. Waits for an in-progress refresh/Write to finish
// first (via connMu) rather than closing out from under it.
func (m *ManagedConn) Close() error {
	m.stopOnce.Do(func() { close(m.stop) })
	<-m.done
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.p != nil {
		err := m.p.Close()
		m.p = nil
		return err
	}
	return nil
}
