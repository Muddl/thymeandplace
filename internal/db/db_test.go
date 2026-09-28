package db

import "testing"

func TestPoolConfigDefaultsAndURLOverride(t *testing.T) {
	cases := []struct {
		name, url    string
		opts         PoolOptions
		wantMax      int32
		wantMin      int32
		useDefaultPx bool // expect pgx's own default MaxConns
	}{
		{name: "defaults apply", url: "postgres://u:p@db:5432/tp", opts: PoolOptions{MaxConns: 16, MinConns: 2}, wantMax: 16, wantMin: 2},
		{name: "url max wins", url: "postgres://u:p@db:5432/tp?pool_max_conns=7", opts: PoolOptions{MaxConns: 16, MinConns: 2}, wantMax: 7, wantMin: 2},
		{name: "url min wins", url: "postgres://u:p@db:5432/tp?pool_min_conns=0&sslmode=disable", opts: PoolOptions{MaxConns: 16, MinConns: 2}, wantMax: 16, wantMin: 0},
		{name: "keyword form", url: "host=db user=u dbname=tp pool_max_conns=5", opts: PoolOptions{MaxConns: 16}, wantMax: 5},
		{name: "cli pool", url: "postgres://u:p@db:5432/tp", opts: CLIPool, wantMax: 4},
		{name: "min capped at max", url: "postgres://u:p@db:5432/tp?pool_max_conns=1", opts: PoolOptions{MaxConns: 16, MinConns: 2}, wantMax: 1, wantMin: 1},
		{name: "no options", url: "postgres://u:p@db:5432/tp", useDefaultPx: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := poolConfig(c.url, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if c.useDefaultPx {
				if cfg.MaxConns < 4 {
					t.Fatalf("MaxConns = %d, want pgx's default (at least 4)", cfg.MaxConns)
				}
				return
			}
			if cfg.MaxConns != c.wantMax || cfg.MinConns != c.wantMin {
				t.Fatalf("MaxConns=%d MinConns=%d, want %d/%d", cfg.MaxConns, cfg.MinConns, c.wantMax, c.wantMin)
			}
		})
	}
}

func TestPoolConfigBadURL(t *testing.T) {
	if _, err := poolConfig("postgres://u:p@db:5432/tp?pool_max_conns=lots", ServerPool); err == nil {
		t.Fatal("a bad pool_max_conns must be an error")
	}
	if _, err := poolConfig("://nope", ServerPool); err == nil {
		t.Fatal("a malformed URL must be an error")
	}
}
