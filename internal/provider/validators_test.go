package provider

import "testing"

func TestValidateDomain(t *testing.T) {
	for _, ok := range []string{"acme.com", "city.victoria.bc.ca", "xn--bcher-kva.example", "a-b.co"} {
		if err := validateDomain(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "localhost", "Acme.com", "acme..com", "-acme.com", "acme-.com", "acme.com/", "https://acme.com", "ac me.com", "10.0.0.1", "a_b.com", ".com"} {
		if err := validateDomain(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}
