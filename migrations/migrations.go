// Package migrations embeds the schema DDL and runs it on service boot. The
// statements are idempotent (IF NOT EXISTS), so this is safe to run repeatedly.
package migrations

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed postgres/*.sql clickhouse/*.sql
var files embed.FS

// Statements returns the ordered SQL statements for a directory ("postgres" or
// "clickhouse"), split on ";" with blanks removed.
func Statements(dir string) ([]string, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var stmts []string
	for _, name := range names {
		data, err := files.ReadFile(dir + "/" + name)
		if err != nil {
			return nil, err
		}
		for _, raw := range strings.Split(string(data), ";") {
			s := strings.TrimSpace(raw)
			if s != "" {
				stmts = append(stmts, s)
			}
		}
	}
	return stmts, nil
}
