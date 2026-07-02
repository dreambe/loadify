package apisrv

import "testing"

// TestBlockInternalDial verifies the SSRF guard on the debug fetch: loopback and
// link-local (incl. cloud metadata) are rejected; public and RFC1918 private
// addresses (legitimate internal load-test targets) are allowed.
func TestBlockInternalDial(t *testing.T) {
	blocked := []string{"127.0.0.1:80", "169.254.169.254:80", "[::1]:443", "0.0.0.0:80"}
	for _, a := range blocked {
		if err := blockInternalDial("tcp", a, nil); err == nil {
			t.Errorf("expected %s to be blocked", a)
		}
	}
	allowed := []string{"93.184.216.34:443", "10.0.0.5:8080", "192.168.1.20:80", "172.16.3.4:9000"}
	for _, a := range allowed {
		if err := blockInternalDial("tcp", a, nil); err != nil {
			t.Errorf("expected %s to be allowed, got %v", a, err)
		}
	}
}
