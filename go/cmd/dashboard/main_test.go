package main

import "testing"

func TestDashboardNeedsPassword(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "localhost"} {
		if dashboardNeedsPassword(host) {
			t.Fatalf("loopback host %q requires password", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "::", "192.0.2.1", "panel.example"} {
		if !dashboardNeedsPassword(host) {
			t.Fatalf("non-loopback host %q did not require password", host)
		}
	}
}
