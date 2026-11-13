// Package templates holds the embedded files that `gh spruce fix` writes.
package templates

import (
	"bytes"
	"embed"
	"text/template"
)

//go:embed files
var files embed.FS

var tmpl = template.Must(template.New("").ParseFS(files, "files/*.tmpl", "files/ci/*.yml"))

// Render executes the named template (e.g. "node.yml", "README.md.tmpl").
func Render(name string, data any) (string, error) {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// HasCI reports whether a CI template exists for the stack kind.
func HasCI(kind string) bool {
	return tmpl.Lookup(kind+".yml") != nil
}
