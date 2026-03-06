package dns

import (
	"net"
	"testing"

	mdns "github.com/miekg/dns"

	"github.com/chronick/plane/internal/testutil"
)

func newTestDNS() *ServiceDNS {
	gateway := net.ParseIP("192.168.1.1")
	return New(gateway, "plane.local", 30, testutil.NewTestLogger())
}

// --- extractServiceName ---

func TestExtractServiceName_WithDomain(t *testing.T) {
	d := newTestDNS()
	got := d.extractServiceName("db.plane.local.")
	if got != "db" {
		t.Errorf("expected 'db', got %q", got)
	}
}

func TestExtractServiceName_WithDomainNoDot(t *testing.T) {
	d := newTestDNS()
	got := d.extractServiceName("db.plane.local")
	if got != "db" {
		t.Errorf("expected 'db', got %q", got)
	}
}

func TestExtractServiceName_Bare(t *testing.T) {
	d := newTestDNS()
	got := d.extractServiceName("db")
	if got != "db" {
		t.Errorf("expected 'db', got %q", got)
	}
}

func TestExtractServiceName_BareWithDot(t *testing.T) {
	d := newTestDNS()
	got := d.extractServiceName("db.")
	if got != "db" {
		t.Errorf("expected 'db', got %q", got)
	}
}

func TestExtractServiceName_ExternalDomain(t *testing.T) {
	d := newTestDNS()
	got := d.extractServiceName("google.com.")
	if got != "google.com" {
		t.Errorf("expected 'google.com', got %q", got)
	}
}

// --- UpdateRecords ---

func TestUpdateRecords(t *testing.T) {
	d := newTestDNS()
	d.UpdateRecords([]string{"web", "db", "cache"})

	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(d.records))
	}
	for _, name := range []string{"web", "db", "cache"} {
		ip, ok := d.records[name]
		if !ok {
			t.Errorf("expected record for %q", name)
			continue
		}
		if !ip.Equal(net.ParseIP("192.168.1.1")) {
			t.Errorf("expected gateway IP for %q, got %v", name, ip)
		}
	}
}

func TestUpdateRecords_Replaces(t *testing.T) {
	d := newTestDNS()
	d.UpdateRecords([]string{"web", "db"})
	d.UpdateRecords([]string{"cache"})

	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.records) != 1 {
		t.Fatalf("expected 1 record after replacement, got %d", len(d.records))
	}
	if _, ok := d.records["web"]; ok {
		t.Error("expected 'web' to be removed")
	}
}

// --- SetUpstream ---

func TestSetUpstream(t *testing.T) {
	d := newTestDNS()
	d.SetUpstream("1.1.1.1:53")

	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.upstream != "1.1.1.1:53" {
		t.Errorf("expected upstream 1.1.1.1:53, got %q", d.upstream)
	}
}

// --- handleDNS ---

type mockResponseWriter struct {
	msg *mdns.Msg
}

func (w *mockResponseWriter) LocalAddr() net.Addr  { return nil }
func (w *mockResponseWriter) RemoteAddr() net.Addr { return nil }
func (w *mockResponseWriter) WriteMsg(msg *mdns.Msg) error {
	w.msg = msg
	return nil
}
func (w *mockResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (w *mockResponseWriter) Close() error              { return nil }
func (w *mockResponseWriter) TsigStatus() error         { return nil }
func (w *mockResponseWriter) TsigTimersOnly(bool)       {}
func (w *mockResponseWriter) Hijack()                    {}

func TestHandleDNS_KnownRecord(t *testing.T) {
	d := newTestDNS()
	d.UpdateRecords([]string{"db"})

	q := new(mdns.Msg)
	q.SetQuestion("db.plane.local.", mdns.TypeA)

	w := &mockResponseWriter{}
	d.handleDNS(w, q)

	if w.msg == nil {
		t.Fatal("expected response message")
	}
	if len(w.msg.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(w.msg.Answer))
	}

	a, ok := w.msg.Answer[0].(*mdns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", w.msg.Answer[0])
	}
	if !a.A.Equal(net.ParseIP("192.168.1.1")) {
		t.Errorf("expected gateway IP, got %v", a.A)
	}
	if a.Hdr.Ttl != 30 {
		t.Errorf("expected TTL 30, got %d", a.Hdr.Ttl)
	}
}

func TestHandleDNS_UnknownRecord(t *testing.T) {
	d := newTestDNS()
	// Set upstream to something that won't resolve quickly — we'll just
	// verify the handler doesn't panic and returns a message
	d.SetUpstream("127.0.0.1:1") // unreachable

	q := new(mdns.Msg)
	q.SetQuestion("unknown.plane.local.", mdns.TypeA)

	w := &mockResponseWriter{}
	d.handleDNS(w, q)

	if w.msg == nil {
		t.Fatal("expected response message even for unknown record")
	}
	// No answer for unknown records when upstream fails
	if len(w.msg.Answer) != 0 {
		t.Errorf("expected 0 answers for unknown, got %d", len(w.msg.Answer))
	}
}

func TestHandleDNS_NonTypeA(t *testing.T) {
	d := newTestDNS()
	d.UpdateRecords([]string{"db"})
	d.SetUpstream("127.0.0.1:1") // unreachable

	q := new(mdns.Msg)
	q.SetQuestion("db.plane.local.", mdns.TypeAAAA) // IPv6, not A

	w := &mockResponseWriter{}
	d.handleDNS(w, q)

	if w.msg == nil {
		t.Fatal("expected response message")
	}
	// No A record answers for AAAA queries
	if len(w.msg.Answer) != 0 {
		t.Errorf("expected 0 answers for non-A query, got %d", len(w.msg.Answer))
	}
}

// --- Start/Stop ---

func TestStartStop(t *testing.T) {
	d := newTestDNS()

	// Use port 0 to let the OS pick a free port
	err := d.Start(0)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Should not panic
	d.Stop()
}

func TestStop_NilServer(t *testing.T) {
	d := newTestDNS()
	// Should not panic when server was never started
	d.Stop()
}

// --- DetectGateway / DetectUpstream ---

func TestDetectGateway(t *testing.T) {
	ip := DetectGateway()
	if ip == nil {
		t.Fatal("expected non-nil IP")
	}
}

func TestDetectUpstream(t *testing.T) {
	upstream := DetectUpstream()
	if upstream == "" {
		t.Fatal("expected non-empty upstream")
	}
}
