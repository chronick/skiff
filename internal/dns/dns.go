package dns

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	mdns "github.com/miekg/dns"
)

// ServiceDNS is an embedded DNS server for service discovery.
type ServiceDNS struct {
	mu       sync.RWMutex
	gateway  net.IP
	records  map[string]net.IP
	upstream string
	domain   string
	ttl      uint32
	server   *mdns.Server
	logger   *slog.Logger
}

// New creates a ServiceDNS server.
func New(gateway net.IP, domain string, ttl uint32, logger *slog.Logger) *ServiceDNS {
	return &ServiceDNS{
		gateway:  gateway,
		records:  make(map[string]net.IP),
		upstream: "8.8.8.8:53",
		domain:   strings.TrimSuffix(domain, "."),
		ttl:      ttl,
		logger:   logger,
	}
}

// SetUpstream sets the upstream DNS server for non-plane queries.
func (d *ServiceDNS) SetUpstream(addr string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.upstream = addr
}

// UpdateRecords replaces the full record set. All names resolve to the gateway IP.
func (d *ServiceDNS) UpdateRecords(names []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = make(map[string]net.IP, len(names))
	for _, name := range names {
		d.records[name] = d.gateway
	}
	d.logger.Debug("dns records updated", "count", len(names))
}

// Start begins serving DNS on the given port.
func (d *ServiceDNS) Start(port int) error {
	mux := mdns.NewServeMux()
	mux.HandleFunc(".", d.handleDNS)

	d.server = &mdns.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Net:     "udp",
		Handler: mux,
	}

	d.logger.Info("starting dns server", "port", port, "domain", d.domain)
	go func() {
		if err := d.server.ListenAndServe(); err != nil {
			d.logger.Error("dns server error", "error", err)
		}
	}()
	return nil
}

// Stop shuts down the DNS server.
func (d *ServiceDNS) Stop() {
	if d.server != nil {
		_ = d.server.Shutdown()
	}
}

func (d *ServiceDNS) handleDNS(w mdns.ResponseWriter, r *mdns.Msg) {
	msg := new(mdns.Msg)
	msg.SetReply(r)
	msg.Authoritative = true

	for _, q := range r.Question {
		if q.Qtype != mdns.TypeA {
			continue
		}

		name := d.extractServiceName(q.Name)
		d.mu.RLock()
		ip, ok := d.records[name]
		d.mu.RUnlock()

		if ok {
			msg.Answer = append(msg.Answer, &mdns.A{
				Hdr: mdns.RR_Header{
					Name:   q.Name,
					Rrtype: mdns.TypeA,
					Class:  mdns.ClassINET,
					Ttl:    d.ttl,
				},
				A: ip,
			})
		}
	}

	if len(msg.Answer) == 0 {
		// Forward to upstream resolver
		d.mu.RLock()
		upstream := d.upstream
		d.mu.RUnlock()

		resp, err := mdns.Exchange(r, upstream)
		if err == nil {
			resp.Id = r.Id
			_ = w.WriteMsg(resp)
			return
		}
		d.logger.Debug("upstream dns failed", "error", err)
	}

	_ = w.WriteMsg(msg)
}

func (d *ServiceDNS) extractServiceName(qname string) string {
	// Remove trailing dot
	name := strings.TrimSuffix(qname, ".")

	// Match "db.plane.local" -> "db"
	suffix := "." + d.domain
	if strings.HasSuffix(name, suffix) {
		return strings.TrimSuffix(name, suffix)
	}

	// Match bare name "db"
	if !strings.Contains(name, ".") {
		return name
	}

	return name
}

// DetectGateway attempts to find the host gateway IP for containers.
func DetectGateway() net.IP {
	// Try to find the default interface IP
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return net.ParseIP("127.0.0.1")
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP
}

// DetectUpstream finds the system DNS resolver.
func DetectUpstream() string {
	// On macOS, the system resolver is typically at this address
	// A more robust implementation would parse /etc/resolv.conf
	return "8.8.8.8:53"
}
