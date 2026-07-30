package escpos

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePrinter is a minimal in-process stand-in for a network ESC/POS
// printer: it accepts TCP connections, records everything written to it,
// and answers DLE EOT status queries with a single configurable byte (or
// not at all, to simulate an unresponsive/unsupported printer). No real
// hardware is available to test ManagedConn against, so this is the
// practical substitute.
type fakePrinter struct {
	ln net.Listener

	mu        sync.Mutex
	received  []byte
	statusRes byte // status byte returned for every DLE EOT query
	silent    bool // if true, queries get no reply at all (read times out)
	conns     []net.Conn
}

func newFakePrinter(t *testing.T) *fakePrinter {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fp := &fakePrinter{ln: ln}
	go fp.acceptLoop()
	return fp
}

func (fp *fakePrinter) acceptLoop() {
	for {
		conn, err := fp.ln.Accept()
		if err != nil {
			return // listener closed
		}
		fp.mu.Lock()
		fp.conns = append(fp.conns, conn)
		fp.mu.Unlock()
		go fp.handle(conn)
	}
}

func (fp *fakePrinter) handle(conn net.Conn) {
	buf := make([]byte, 256)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		fp.mu.Lock()
		fp.received = append(fp.received, buf[:n]...)
		silent := fp.silent
		res := fp.statusRes
		fp.mu.Unlock()

		// Recognize a trailing DLE EOT n (0x10 0x04 n) query in whatever
		// was just read and answer it with the single configured status
		// byte, unless silent mode is on.
		if !silent && n >= 3 && buf[n-3] == dle && buf[n-2] == 0x04 {
			conn.Write([]byte{res})
		}
	}
}

// addrParts returns the fake printer's host and port for ConnectWithRetry-
// style (ip string, port int) calls.
func (fp *fakePrinter) addrParts(t *testing.T) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(fp.ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, port
}

// closeAllConns closes every connection accepted so far, simulating the
// printer dropping its end (e.g. power-cycled, cable pulled).
func (fp *fakePrinter) closeAllConns() {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, c := range fp.conns {
		c.Close()
	}
	fp.conns = nil
}

func (fp *fakePrinter) receivedString() string {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return string(fp.received)
}

func (fp *fakePrinter) close() {
	fp.ln.Close()
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagedConn_ConnectAndWrite(t *testing.T) {
	fp := newFakePrinter(t)
	defer fp.close()
	host, port := fp.addrParts(t)

	m := NewManagedConn(host, port, 50*time.Millisecond)
	defer m.Close()

	waitFor(t, 2*time.Second, func() bool { return m.Status().Connected })

	if err := m.Write([]byte("hello receipt")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, time.Second, func() bool { return strings.Contains(fp.receivedString(), "hello receipt") })
}

func TestManagedConn_ReconnectsAfterServerDrop(t *testing.T) {
	fp := newFakePrinter(t)
	defer fp.close()
	host, port := fp.addrParts(t)

	m := NewManagedConn(host, port, 50*time.Millisecond)
	defer m.Close()

	waitFor(t, 2*time.Second, func() bool { return m.Status().Connected })

	// Simulate the printer dropping the connection (power cycle, cable
	// pull) — the next status poll should notice via EOF on its query
	// read and mark the connection dead.
	fp.closeAllConns()
	waitFor(t, 2*time.Second, func() bool { return !m.Status().Connected })

	// The fake printer's listener is still up (only the individual
	// connection was dropped), so the next poll tick — or an explicit
	// Write — should transparently reconnect.
	if err := m.Write([]byte("after reconnect")); err != nil {
		t.Fatalf("Write after drop: %v", err)
	}
	waitFor(t, time.Second, func() bool { return strings.Contains(fp.receivedString(), "after reconnect") })
	waitFor(t, 2*time.Second, func() bool { return m.Status().Connected })
}

func TestManagedConn_StatusReflectsUnresponsivePrinter(t *testing.T) {
	fp := newFakePrinter(t)
	defer fp.close()
	fp.mu.Lock()
	fp.silent = true
	fp.mu.Unlock()
	host, port := fp.addrParts(t)

	m := NewManagedConn(host, port, 50*time.Millisecond)
	defer m.Close()

	// A silent printer still accepts the TCP connection and the query
	// write, it just never answers — GetStatus's DLE EOT read times out
	// (not EOF), so this should settle as connected but without detailed
	// status support, not as "not reachable". The first refresh alone takes
	// ~4.1s for a silent printer (2s DLE EOT timeout + 2s GS r fallback
	// timeout), so the budget here has to comfortably exceed that — unlike
	// most of this file's waitFor calls, Status() being fast (see
	// TestManagedConn_StatusNeverBlocksDuringSlowRefresh) means this test
	// can no longer rely on Status() itself blocking until the right value
	// is ready.
	waitFor(t, 6*time.Second, func() bool {
		st := m.Status()
		return st.Connected && !st.StatusSupported
	})
}

// TestManagedConn_StatusNeverBlocksDuringSlowRefresh guards against a real
// regression: Status() and the background refresh used to share one lock,
// so a caller polling Status() while a refresh was deep in a slow status
// query (or a slow reconnect to a genuinely down printer, which retries for
// up to defaultConnectMaxWait/10s) would hang for however long that I/O
// took — defeating the documented "never blocks on I/O" guarantee and, in
// practice, making a frontend polling this every couple of seconds appear
// stuck showing a stale/loading state for many seconds after every
// (re)connect.
func TestManagedConn_StatusNeverBlocksDuringSlowRefresh(t *testing.T) {
	fp := newFakePrinter(t)
	defer fp.close()
	fp.mu.Lock()
	fp.silent = true // accepts the connection but never answers any status query
	fp.mu.Unlock()
	host, port := fp.addrParts(t)

	// Long poll interval — only the initial background refresh matters here.
	m := NewManagedConn(host, port, time.Hour)
	defer m.Close()

	// Give the background goroutine time to connect and start querying —
	// each status query against a silent printer has its own multi-second
	// read timeout, so by now it should be deep inside that slow I/O.
	time.Sleep(150 * time.Millisecond)

	start := time.Now()
	_ = m.Status()
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("Status() took %s while a refresh was in flight — it must read the "+
			"cached value instantly, not block on the same lock as the slow connect/query I/O", elapsed)
	}
}

func TestManagedConn_Close(t *testing.T) {
	fp := newFakePrinter(t)
	defer fp.close()
	host, port := fp.addrParts(t)

	m := NewManagedConn(host, port, 20*time.Millisecond)
	waitFor(t, 2*time.Second, func() bool { return m.Status().Connected })

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A second Close should be a harmless no-op, not a panic/deadlock.
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
