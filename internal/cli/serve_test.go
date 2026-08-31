package cli

import "testing"

// Whether to trust "you can reach this port" as authentication turns on this,
// so a hostname is not resolved: a name that points at loopback today may not
// tomorrow.
func TestOnLoopback(t *testing.T) {
	yes := []string{"127.0.0.1:8080", "[::1]:8080", "127.7.7.7:9000"}
	no := []string{
		"0.0.0.0:8080",      // every interface
		":8080",             // the same, said differently
		"192.168.1.10:8080", // the network
		"localhost:8080",    // a name, which is not a fact
		"board.example.com:80",
		"nonsense",
	}
	for _, addr := range yes {
		if !onLoopback(addr) {
			t.Errorf("%q is loopback and was not recognised", addr)
		}
	}
	for _, addr := range no {
		if onLoopback(addr) {
			t.Errorf("%q is not loopback and was accepted", addr)
		}
	}
}
