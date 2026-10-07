package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPutsDatabaseNextToConfiguration(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct{ database, want string }{
		{"", filepath.Join(dir, "winnow.db")},
		{"database: data/w.db\n", filepath.Join(dir, "data/w.db")},
		{"database: /var/lib/w.db\n", "/var/lib/w.db"},
	} {
		path := filepath.Join(dir, "winnow.yaml")
		if err := os.WriteFile(path, []byte("listen: \":8080\"\n"+tt.database), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, errs, _ := load(path)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		if cfg.Database != tt.want {
			t.Errorf("%q: database = %q, want %q", tt.database, cfg.Database, tt.want)
		}
	}
}
