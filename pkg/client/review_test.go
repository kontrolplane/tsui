package client

import (
	"strings"
	"testing"
)

func TestReviewConnectErrorsDoNotLeakPasswords(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, url := range []string{
		"nats://user:s3cr3t@127.0.0.1:1",
		"nats://user:s3cr3t%zz@127.0.0.1:4222",
		"nats://user:s3cr3t/x@127.0.0.1:4222",
		"nats://user:s3cr3t@127.0.0.1:notaport",
		"nats://s3cr3t@[::1",
	} {
		_, _, _, err := Connect(Options{URL: url}, "t")
		if err == nil {
			t.Fatalf("%s: expected an error", url)
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%s: error leaks the secret: %v", url, err)
		}
	}
}
