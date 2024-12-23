package config

import (
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestDSNKeepsSpecialCharactersInSecrets(t *testing.T) {
	c := &Config{}
	c.DB.Host, c.DB.Port, c.DB.Name, c.DB.SSLMode = "postgres", "5432", "kfc", "disable"
	c.DB.User = "kfc"
	c.DB.Password = `p@ss w'o"rd\/?#&=%`
	c.DB.ConnectTimeout = 10 * time.Second

	opts, err := pq.ParseURL(c.DSN())
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	conn, err := pq.NewConnector(c.DSN())
	if err != nil || conn == nil {
		t.Fatalf("NewConnector: %v", err)
	}
	for _, want := range []string{"host='postgres'", "port='5432'", "dbname='kfc'", "sslmode='disable'", "connect_timeout='10'"} {
		if !strings.Contains(opts, want) {
			t.Errorf("%q missing from %q", want, opts)
		}
	}
	if !strings.Contains(opts, `password='p@ss w\'o"rd\\/?#&=%'`) {
		t.Errorf("password not preserved: %q", opts)
	}
}
